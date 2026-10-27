package subscriptions

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/planfeature"
	"github.com/bengobox/subscription-service/internal/ent/predicate"
	"github.com/bengobox/subscription-service/internal/ent/productsubscription"
	"github.com/bengobox/subscription-service/internal/ent/subscriptionplan"
	"github.com/bengobox/subscription-service/internal/ent/tenantsubscription"
)

// Plan changes that wait for a date, kept in tenant_subscriptions.metadata.
//
// An upgrade trial gives the tenant a higher plan's features and limits for a number of days
// while plan_id stays on the plan they pay for, so every invoice, renewal and revenue figure keeps
// billing that plan. A scheduled plan change moves plan_id at the next renewal date: an upgrade
// made before the current period ends, a plan paid for early at checkout, and the upgrade that
// follows a trial all wait for it. The renewal invoice for that period already bills the new plan.
const (
	MetaUpgradeTrial        = "upgrade_trial"
	MetaUpgradeTrialsUsed   = "upgrade_trials_used"
	MetaUpgradeTrialHistory = "upgrade_trial_history"
	MetaScheduledPlanChange = "scheduled_plan_change"
	// MetaTrialLapsedAt marks a signup trial that ended unpaid (set by the expiry job), so a
	// one-time licence plan tried on a trial is not treated as a paid perpetual licence.
	MetaTrialLapsedAt = "trial_lapsed_at"

	// MetaPlanChangePaymentRefs lists the invoice, intent and pay-reference ids of plan-change
	// invoices (the price difference of a change scheduled after the renewal invoice went out).
	// Paying one settles that invoice only; it must not renew the subscription.
	MetaPlanChangePaymentRefs = "plan_change_payment_refs"

	upgradeTrialHistoryKept   = 10
	planChangePaymentRefsKept = 24
	// UpgradeTrialEndingNoticeDays is how long before a trial ends the tenant is reminded.
	UpgradeTrialEndingNoticeDays = 3
)

// Sources of an upgrade trial or scheduled change.
const (
	ChangeSourceTenant       = "tenant"
	ChangeSourcePlatform     = "platform"
	ChangeSourceUpgradeTrial = "upgrade_trial"
	ChangeSourceCheckout     = "checkout"
)

// Outcomes recorded in the upgrade trial history.
const (
	TrialOutcomeCompleted       = "completed"
	TrialOutcomeEndedByTenant   = "ended_by_tenant"
	TrialOutcomeEndedByPlatform = "ended_by_platform"
	TrialOutcomePaid            = "paid"
	TrialOutcomePlanChanged     = "plan_changed"
	TrialOutcomeCancelled       = "cancelled"
)

// UpgradeTrial is the running upgrade trial stored under MetaUpgradeTrial.
type UpgradeTrial struct {
	PlanID           uuid.UUID `json:"plan_id"`
	PlanCode         string    `json:"plan_code"`
	PlanName         string    `json:"plan_name"`
	TierOrder        int       `json:"tier_order"`
	BasePlanID       uuid.UUID `json:"base_plan_id"`
	BasePlanCode     string    `json:"base_plan_code"`
	BasePlanName     string    `json:"base_plan_name"`
	Days             int       `json:"days"`
	StartedAt        time.Time `json:"started_at"`
	EndsAt           time.Time `json:"ends_at"`
	Source           string    `json:"source"`
	StartedBy        string    `json:"started_by,omitempty"`
	EndingNoticeSent bool      `json:"ending_notice_sent,omitempty"`
}

// ScheduledPlanChange is a plan switch waiting for EffectiveAt, stored under MetaScheduledPlanChange.
type ScheduledPlanChange struct {
	PlanID       uuid.UUID `json:"plan_id"`
	PlanCode     string    `json:"plan_code"`
	PlanName     string    `json:"plan_name"`
	FromPlanID   uuid.UUID `json:"from_plan_id"`
	FromPlanCode string    `json:"from_plan_code"`
	FromPlanName string    `json:"from_plan_name"`
	EffectiveAt  time.Time `json:"effective_at"`
	Source       string    `json:"source"`
	RequestedAt  time.Time `json:"requested_at"`
	RequestedBy  string    `json:"requested_by,omitempty"`
}

// UpgradeTrialRecord is one finished trial in MetaUpgradeTrialHistory.
type UpgradeTrialRecord struct {
	PlanCode     string    `json:"plan_code"`
	BasePlanCode string    `json:"base_plan_code"`
	StartedAt    time.Time `json:"started_at"`
	EndedAt      time.Time `json:"ended_at"`
	Outcome      string    `json:"outcome"`
}

// UpgradeTrialInfo is the client view of a running trial on SubscriptionResult.
type UpgradeTrialInfo struct {
	PlanCode      string    `json:"plan_code"`
	PlanName      string    `json:"plan_name"`
	TierOrder     int       `json:"tier_order"`
	BasePlanCode  string    `json:"base_plan_code"`
	BasePlanName  string    `json:"base_plan_name"`
	StartedAt     time.Time `json:"started_at"`
	EndsAt        time.Time `json:"ends_at"`
	DaysRemaining int       `json:"days_remaining"`
	Source        string    `json:"source"`
}

// ScheduledChangeInfo is the client view of a scheduled plan change on SubscriptionResult.
type ScheduledChangeInfo struct {
	PlanCode     string    `json:"plan_code"`
	PlanName     string    `json:"plan_name"`
	FromPlanCode string    `json:"from_plan_code"`
	FromPlanName string    `json:"from_plan_name"`
	EffectiveAt  time.Time `json:"effective_at"`
	Source       string    `json:"source"`
}

func (t *UpgradeTrial) info(now time.Time) *UpgradeTrialInfo {
	return &UpgradeTrialInfo{
		PlanCode: t.PlanCode, PlanName: t.PlanName, TierOrder: t.TierOrder,
		BasePlanCode: t.BasePlanCode, BasePlanName: t.BasePlanName,
		StartedAt: t.StartedAt, EndsAt: t.EndsAt, DaysRemaining: daysUntil(now, t.EndsAt), Source: t.Source,
	}
}

func (c *ScheduledPlanChange) info() *ScheduledChangeInfo {
	return &ScheduledChangeInfo{
		PlanCode: c.PlanCode, PlanName: c.PlanName, FromPlanCode: c.FromPlanCode, FromPlanName: c.FromPlanName,
		EffectiveAt: c.EffectiveAt, Source: c.Source,
	}
}

// daysUntil returns whole days left until t, rounded up, never negative.
func daysUntil(now, t time.Time) int {
	d := t.Sub(now)
	if d <= 0 {
		return 0
	}
	return int((d + 24*time.Hour - 1) / (24 * time.Hour))
}

// decodeMeta reads one metadata entry into out through a JSON round trip (metadata arrives as
// map[string]any from the jsonb column).
func decodeMeta(meta map[string]any, key string, out any) bool {
	raw, ok := meta[key]
	if !ok || raw == nil {
		return false
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return false
	}
	return json.Unmarshal(b, out) == nil
}

// encodeMeta turns a value into the map form stored in metadata.
func encodeMeta(v any) map[string]any {
	b, _ := json.Marshal(v)
	out := map[string]any{}
	_ = json.Unmarshal(b, &out)
	return out
}

func cloneMetadata(m map[string]any) map[string]any {
	out := make(map[string]any, len(m)+3)
	for k, v := range m {
		out[k] = v
	}
	return out
}

// upgradeTrialOf returns the stored upgrade trial, in force or not.
func upgradeTrialOf(sub *ent.TenantSubscription) (*UpgradeTrial, bool) {
	var t UpgradeTrial
	if !decodeMeta(sub.Metadata, MetaUpgradeTrial, &t) || t.PlanID == uuid.Nil || t.EndsAt.IsZero() {
		return nil, false
	}
	return &t, true
}

// scheduledChangeOf returns the stored scheduled plan change, current or stale.
func scheduledChangeOf(sub *ent.TenantSubscription) (*ScheduledPlanChange, bool) {
	var c ScheduledPlanChange
	if !decodeMeta(sub.Metadata, MetaScheduledPlanChange, &c) || c.PlanID == uuid.Nil || c.EffectiveAt.IsZero() {
		return nil, false
	}
	return &c, true
}

// grantsAccess reports whether a status keeps the tenant's plan usable.
func grantsAccess(st tenantsubscription.Status) bool {
	return st == tenantsubscription.StatusACTIVE || st == tenantsubscription.StatusTRIAL
}

// ActiveUpgradeTrial returns the upgrade trial in force at now: the subscription is usable, the
// trial has not ended, and the plan it was started from is still the paid plan (a plan changed
// by an admin since leaves the trial inert until the schedule job clears it).
func ActiveUpgradeTrial(sub *ent.TenantSubscription, now time.Time) (*UpgradeTrial, bool) {
	t, ok := upgradeTrialOf(sub)
	if !ok || !grantsAccess(sub.Status) || !now.Before(t.EndsAt) || t.BasePlanID != sub.PlanID || t.PlanID == sub.PlanID {
		return nil, false
	}
	return t, true
}

// PendingPlanChange returns the scheduled change still waiting to be applied to sub.
func PendingPlanChange(sub *ent.TenantSubscription) (*ScheduledPlanChange, bool) {
	c, ok := scheduledChangeOf(sub)
	if !ok || c.FromPlanID != sub.PlanID || c.PlanID == sub.PlanID {
		return nil, false
	}
	return c, true
}

// PlanIDInForce is the plan whose features and limits apply at now: a running upgrade trial's
// plan, else a scheduled change whose date has passed but the schedule job has not applied yet,
// else plan_id.
func PlanIDInForce(sub *ent.TenantSubscription, now time.Time) uuid.UUID {
	if t, ok := ActiveUpgradeTrial(sub, now); ok {
		return t.PlanID
	}
	if c, ok := PendingPlanChange(sub); ok && !now.Before(c.EffectiveAt) {
		return c.PlanID
	}
	return sub.PlanID
}

// BillingPlanIDFor is the plan the renewal invoice for the period starting at current_period_end
// bills: the scheduled plan when it takes effect by then, else plan_id.
func BillingPlanIDFor(sub *ent.TenantSubscription) uuid.UUID {
	if c, ok := PendingPlanChange(sub); ok && !sub.CurrentPeriodEnd.Before(c.EffectiveAt) {
		return c.PlanID
	}
	return sub.PlanID
}

// BillingPlanFor loads the plan BillingPlanIDFor names, reusing sub.Edges.Plan when it matches.
func BillingPlanFor(ctx context.Context, client *ent.Client, sub *ent.TenantSubscription) (*ent.SubscriptionPlan, error) {
	id := BillingPlanIDFor(sub)
	if sub.Edges.Plan != nil && sub.Edges.Plan.ID == id {
		return sub.Edges.Plan, nil
	}
	p, err := client.SubscriptionPlan.Get(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("load billing plan: %w", err)
	}
	return p, nil
}

// nextRenewalAtOrAfter returns the first renewal date (current_period_end stepped by whole
// billing periods) that is not before t.
func nextRenewalAtOrAfter(sub *ent.TenantSubscription, t time.Time) time.Time {
	months := BillingCycleMonths(string(sub.BillingCycle))
	if months <= 0 {
		months = 1
	}
	b := sub.CurrentPeriodEnd.UTC()
	for i := 0; b.Before(t) && i < 1200; i++ {
		b = b.AddDate(0, months, 0)
	}
	return b
}

// maxLimit merges two values of one tier limit: -1 (unlimited) always wins, else the larger.
func maxLimit(cur, next int) int {
	if cur == -1 || next == -1 {
		return -1
	}
	if next > cur {
		return next
	}
	return cur
}

func limitValue(v any) (int, bool) {
	switch t := v.(type) {
	case float64:
		return int(t), true
	case int:
		return t, true
	case int64:
		return int(t), true
	}
	return 0, false
}

// EntitlementLimits returns the tier limits that apply to sub now, the same composite
// GetSubscriptionResult mints into tokens: the plan in force (PlanIDInForce) raised by every
// active product overlay plan. Server-side usage, overage and rate-limit checks read limits here
// so they never disagree with the token. One query loads every plan involved. On a query error it
// returns the paid plan's limits (when loaded) with the error, so callers can fail open.
func EntitlementLimits(ctx context.Context, client *ent.Client, sub *ent.TenantSubscription) (map[string]any, error) {
	inForce := PlanIDInForce(sub, time.Now().UTC())
	preds := []predicate.SubscriptionPlan{
		subscriptionplan.HasOverrideProductSubscriptionsWith(
			productsubscription.TenantSubscriptionIDEQ(sub.ID),
			productsubscription.StatusIn(activeProductStatuses...),
		),
	}
	var inForcePlan *ent.SubscriptionPlan
	if sub.Edges.Plan != nil && sub.Edges.Plan.ID == inForce {
		inForcePlan = sub.Edges.Plan
	} else {
		preds = append(preds, subscriptionplan.IDEQ(inForce))
	}
	plans, err := client.SubscriptionPlan.Query().
		Where(subscriptionplan.Or(preds...)).
		Select(subscriptionplan.FieldID, subscriptionplan.FieldTierLimitsJSON).
		All(ctx)
	if err != nil {
		if sub.Edges.Plan != nil {
			return copyLimits(sub.Edges.Plan.TierLimitsJSON), fmt.Errorf("load entitlement plans: %w", err)
		}
		return map[string]any{}, fmt.Errorf("load entitlement plans: %w", err)
	}
	for _, p := range plans {
		if inForcePlan == nil && p.ID == inForce {
			inForcePlan = p
		}
	}
	out := map[string]any{}
	if inForcePlan != nil {
		out = copyLimits(inForcePlan.TierLimitsJSON)
	}
	for _, p := range plans {
		if p.ID == inForce {
			continue
		}
		for k, v := range p.TierLimitsJSON {
			iv, ok := limitValue(v)
			if !ok {
				continue
			}
			if cur, ok := limitValue(out[k]); ok {
				out[k] = float64(maxLimit(cur, iv))
			} else {
				out[k] = float64(iv)
			}
		}
	}
	return out, nil
}

func copyLimits(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// loadPlanInForce returns the paid plan and the plan in force at now (both with included
// features). They are the same pointer when nothing is overlaid.
func (s *Service) loadPlanInForce(ctx context.Context, sub *ent.TenantSubscription, now time.Time) (paid, inForce *ent.SubscriptionPlan, err error) {
	ids := []uuid.UUID{sub.PlanID}
	forceID := PlanIDInForce(sub, now)
	if forceID != sub.PlanID {
		ids = append(ids, forceID)
	}
	plans, err := s.client.SubscriptionPlan.Query().
		Where(subscriptionplan.IDIn(ids...)).
		WithFeatures(func(q *ent.PlanFeatureQuery) { q.Where(planfeature.IsIncludedEQ(true)) }).
		All(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("lookup plan: %w", err)
	}
	for _, p := range plans {
		if p.ID == sub.PlanID {
			paid = p
		}
		if p.ID == forceID {
			inForce = p
		}
	}
	if paid == nil {
		return nil, nil, fmt.Errorf("lookup plan: %w", &ent.NotFoundError{})
	}
	if inForce == nil {
		inForce = paid
	}
	return paid, inForce, nil
}

// AppendPlanChangePaymentRefs returns MetaPlanChangePaymentRefs with refs added, newest kept.
func AppendPlanChangePaymentRefs(meta map[string]any, refs ...string) []any {
	var list []string
	_ = decodeMeta(meta, MetaPlanChangePaymentRefs, &list)
	for _, r := range refs {
		if r != "" && !containsString(list, r) {
			list = append(list, r)
		}
	}
	if len(list) > planChangePaymentRefsKept {
		list = list[len(list)-planChangePaymentRefsKept:]
	}
	out := make([]any, 0, len(list))
	for _, r := range list {
		out = append(out, r)
	}
	return out
}

// IsPlanChangePayment reports whether a payment (by intent id, invoice id or pay reference)
// settles a plan-change invoice rather than a renewal.
func (s *Service) IsPlanChangePayment(ctx context.Context, tenantID uuid.UUID, ids ...string) bool {
	sub, err := s.getSubscription(ctx, tenantID)
	if err != nil {
		return false
	}
	var list []string
	if !decodeMeta(sub.Metadata, MetaPlanChangePaymentRefs, &list) {
		return false
	}
	for _, id := range ids {
		if id != "" && containsString(list, id) {
			return true
		}
	}
	return false
}

// WithScheduledChangeMoved returns sub's metadata with a scheduled change that was due on the
// old renewal date moved to newPeriodEnd, for an admin edit of current_period_end. moved is false
// when there is nothing to move.
func WithScheduledChangeMoved(sub *ent.TenantSubscription, newPeriodEnd time.Time) (map[string]any, bool) {
	pc, ok := PendingPlanChange(sub)
	if !ok || !pc.EffectiveAt.Equal(sub.CurrentPeriodEnd) || pc.EffectiveAt.Equal(newPeriodEnd) {
		return nil, false
	}
	pc.EffectiveAt = newPeriodEnd.UTC()
	meta := cloneMetadata(sub.Metadata)
	meta[MetaScheduledPlanChange] = encodeMeta(pc)
	return meta, true
}
