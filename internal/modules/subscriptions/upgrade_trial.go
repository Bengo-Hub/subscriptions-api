package subscriptions

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/subscriptionplan"
)

// Reasons an upgrade trial cannot start, returned to clients as machine codes.
const (
	TrialReasonDisabled       = "disabled"
	TrialReasonExempt         = "exempt"
	TrialReasonNoSubscription = "no_subscription"
	TrialReasonStatus         = "status"
	TrialReasonPastDue        = "past_due"
	TrialReasonTrialActive    = "trial_active"
	TrialReasonChangeExists   = "change_scheduled"
	TrialReasonPlanNotFound   = "plan_not_found"
	TrialReasonOneTime        = "one_time_plan"
	TrialReasonOtherFamily    = "different_family"
	TrialReasonNotUpgrade     = "not_an_upgrade"
	TrialReasonFreePlan       = "free_plan"
	TrialReasonAlreadyUsed    = "already_used"
	TrialReasonNoTrial        = "no_trial"
	TrialReasonNoChange       = "no_change"
)

// UpgradeTrialError explains why an upgrade trial request was refused.
type UpgradeTrialError struct {
	Reason  string
	Message string
}

func (e *UpgradeTrialError) Error() string { return e.Message }

// AsUpgradeTrialError unwraps an UpgradeTrialError.
func AsUpgradeTrialError(err error) (*UpgradeTrialError, bool) {
	var te *UpgradeTrialError
	if errors.As(err, &te) {
		return te, true
	}
	return nil, false
}

func trialErr(reason, msg string) *UpgradeTrialError {
	return &UpgradeTrialError{Reason: reason, Message: msg}
}

// upgradeTrialCheck holds everything checkUpgradeTrial needs, so the rules stay a pure function.
type upgradeTrialCheck struct {
	sub        *ent.TenantSubscription
	paid       *ent.SubscriptionPlan
	target     *ent.SubscriptionPlan
	policy     UpgradeTrialPolicy
	byPlatform bool
	now        time.Time
}

// checkUpgradeTrial applies the eligibility rules. The platform owner skips the policy switch,
// the public-plan rule and the once-per-plan rule; every other rule protects billing and holds
// for everyone.
func checkUpgradeTrial(c upgradeTrialCheck) *UpgradeTrialError {
	if !c.byPlatform && !c.policy.Enabled {
		return trialErr(TrialReasonDisabled, "Free upgrade trials are not available on this account.")
	}
	if !grantsAccess(c.sub.Status) {
		return trialErr(TrialReasonStatus, fmt.Sprintf("A trial needs an active subscription; this one is %s.", c.sub.Status))
	}
	if c.sub.CurrentPeriodEnd.Before(c.now) {
		return trialErr(TrialReasonPastDue, "Pay the outstanding subscription invoice before starting a trial.")
	}
	if t, ok := upgradeTrialOf(c.sub); ok && c.now.Before(t.EndsAt) && t.BasePlanID == c.sub.PlanID {
		return trialErr(TrialReasonTrialActive, fmt.Sprintf("A trial of %s is already running.", t.PlanName))
	}
	if pc, ok := PendingPlanChange(c.sub); ok {
		return trialErr(TrialReasonChangeExists, fmt.Sprintf("A change to %s is already scheduled for %s. Cancel it first.",
			pc.PlanName, pc.EffectiveAt.Format("02 Jan 2006")))
	}
	if c.target == nil || !c.target.IsActive || (!c.byPlatform && !c.target.IsPublic) {
		return trialErr(TrialReasonPlanNotFound, "That plan is not available.")
	}
	if c.paid.BillingCycle == "ONE_TIME" || c.target.BillingCycle == "ONE_TIME" {
		return trialErr(TrialReasonOneTime, "Trials are not offered on one-time licences.")
	}
	if PlanFamily(c.target.PlanCode) != PlanFamily(c.paid.PlanCode) {
		return trialErr(TrialReasonOtherFamily, "A trial can only move you to a higher tier of your current product.")
	}
	if c.target.TierOrder <= c.paid.TierOrder {
		return trialErr(TrialReasonNotUpgrade, "A trial is only offered for a higher tier than your current plan.")
	}
	if c.target.BasePrice <= 0 {
		return trialErr(TrialReasonFreePlan, "That plan is free, so it needs no trial.")
	}
	if !c.byPlatform {
		for _, code := range usedUpgradeTrials(c.sub.Metadata) {
			if code == c.target.PlanCode {
				return trialErr(TrialReasonAlreadyUsed, fmt.Sprintf("You have already had a free trial of %s.", c.target.Name))
			}
		}
	}
	return nil
}

func usedUpgradeTrials(meta map[string]any) []string {
	var out []string
	_ = decodeMeta(meta, MetaUpgradeTrialsUsed, &out)
	return out
}

func upgradeTrialHistory(meta map[string]any) []UpgradeTrialRecord {
	var out []UpgradeTrialRecord
	_ = decodeMeta(meta, MetaUpgradeTrialHistory, &out)
	return out
}

// withTrialClosed returns a copy of meta without the running trial and with it appended to the
// bounded history.
func withTrialClosed(meta map[string]any, t *UpgradeTrial, outcome string, now time.Time) map[string]any {
	out := cloneMetadata(meta)
	delete(out, MetaUpgradeTrial)
	hist := append(upgradeTrialHistory(meta), UpgradeTrialRecord{
		PlanCode: t.PlanCode, BasePlanCode: t.BasePlanCode, StartedAt: t.StartedAt, EndedAt: now, Outcome: outcome,
	})
	if len(hist) > upgradeTrialHistoryKept {
		hist = hist[len(hist)-upgradeTrialHistoryKept:]
	}
	out[MetaUpgradeTrialHistory] = encodeHistory(hist)
	return out
}

func encodeHistory(hist []UpgradeTrialRecord) []any {
	out := make([]any, 0, len(hist))
	for _, h := range hist {
		out = append(out, encodeMeta(h))
	}
	return out
}

// UpgradeTrialEligibility answers "can this tenant start a trial of this plan".
type UpgradeTrialEligibility struct {
	PlanCode string     `json:"plan_code"`
	PlanName string     `json:"plan_name,omitempty"`
	Eligible bool       `json:"eligible"`
	Days     int        `json:"days"`
	EndsAt   *time.Time `json:"ends_at,omitempty"`
	// UpgradeAt is the renewal date the upgrade would take effect after the trial.
	UpgradeAt *time.Time `json:"upgrade_at,omitempty"`
	Reason    string     `json:"reason,omitempty"`
	Message   string     `json:"message,omitempty"`
}

// UpgradeTrialStatus is the tenant's whole trial picture.
type UpgradeTrialStatus struct {
	Policy          UpgradeTrialPolicy       `json:"policy"`
	Active          *UpgradeTrialInfo        `json:"active,omitempty"`
	ScheduledChange *ScheduledChangeInfo     `json:"scheduled_change,omitempty"`
	Used            []string                 `json:"used"`
	History         []UpgradeTrialRecord     `json:"history"`
	Eligibility     *UpgradeTrialEligibility `json:"eligibility,omitempty"`
}

// UpgradeTrialStatusFor returns the policy, any running trial or scheduled change, the trial
// history and, when planCode is set, whether a trial of that plan can start now.
func (s *Service) UpgradeTrialStatusFor(ctx context.Context, tenantID uuid.UUID, planCode string, byPlatform bool) (*UpgradeTrialStatus, error) {
	policy, err := s.UpgradeTrialPolicyFor(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	out := &UpgradeTrialStatus{Policy: policy, Used: []string{}, History: []UpgradeTrialRecord{}}
	if s.IsExemptTenant(ctx, tenantID) {
		if planCode != "" {
			out.Eligibility = &UpgradeTrialEligibility{PlanCode: planCode, Reason: TrialReasonExempt, Message: "This account has every feature already."}
		}
		return out, nil
	}
	sub, err := s.getSubscription(ctx, tenantID)
	if err != nil {
		if planCode != "" {
			out.Eligibility = &UpgradeTrialEligibility{PlanCode: planCode, Reason: TrialReasonNoSubscription, Message: "Subscribe to a plan before starting a trial."}
		}
		return out, nil //nolint:nilerr // no subscription is a normal state here
	}
	now := time.Now().UTC()
	if t, ok := ActiveUpgradeTrial(sub, now); ok {
		out.Active = t.info(now)
	}
	if pc, ok := PendingPlanChange(sub); ok {
		out.ScheduledChange = pc.info()
	}
	if used := usedUpgradeTrials(sub.Metadata); used != nil {
		out.Used = used
	}
	if hist := upgradeTrialHistory(sub.Metadata); hist != nil {
		out.History = hist
	}
	if planCode == "" {
		return out, nil
	}
	el := &UpgradeTrialEligibility{PlanCode: planCode, Days: policy.Days}
	paid, err := s.client.SubscriptionPlan.Get(ctx, sub.PlanID)
	if err != nil {
		return nil, fmt.Errorf("lookup plan: %w", err)
	}
	target, err := s.client.SubscriptionPlan.Query().Where(subscriptionplan.PlanCodeEQ(planCode)).Only(ctx)
	if err != nil && !ent.IsNotFound(err) {
		return nil, fmt.Errorf("lookup plan: %w", err)
	}
	if target != nil {
		el.PlanName = target.Name
	}
	if terr := checkUpgradeTrial(upgradeTrialCheck{sub: sub, paid: paid, target: target, policy: policy, byPlatform: byPlatform, now: now}); terr != nil {
		el.Reason, el.Message = terr.Reason, terr.Message
	} else {
		el.Eligible = true
		ends := now.AddDate(0, 0, policy.Days)
		at := nextRenewalAtOrAfter(sub, ends)
		el.EndsAt, el.UpgradeAt = &ends, &at
	}
	out.Eligibility = el
	return out, nil
}

// StartUpgradeTrialInput starts a trial of a higher tier.
type StartUpgradeTrialInput struct {
	TenantID uuid.UUID
	PlanCode string
	// Days overrides the policy length; honoured for the platform owner only.
	Days       int
	ByPlatform bool
	StartedBy  string
}

// StartUpgradeTrial gives the tenant the higher plan's features and limits for the trial days
// and schedules the upgrade for the first renewal date on or after the trial ends. plan_id keeps
// the paid plan until then. Cancelling the trial (EndUpgradeTrial) cancels the upgrade too.
func (s *Service) StartUpgradeTrial(ctx context.Context, in StartUpgradeTrialInput) (*SubscriptionResult, error) {
	if err := s.guardExempt(ctx, in.TenantID); err != nil {
		return nil, err
	}
	sub, err := s.getSubscription(ctx, in.TenantID)
	if err != nil {
		return nil, trialErr(TrialReasonNoSubscription, "Subscribe to a plan before starting a trial.")
	}
	paid, err := s.client.SubscriptionPlan.Get(ctx, sub.PlanID)
	if err != nil {
		return nil, fmt.Errorf("lookup plan: %w", err)
	}
	target, err := s.client.SubscriptionPlan.Query().Where(subscriptionplan.PlanCodeEQ(in.PlanCode)).Only(ctx)
	if err != nil && !ent.IsNotFound(err) {
		return nil, fmt.Errorf("lookup plan: %w", err)
	}
	policy, err := s.UpgradeTrialPolicyFor(ctx, in.TenantID)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	if terr := checkUpgradeTrial(upgradeTrialCheck{sub: sub, paid: paid, target: target, policy: policy, byPlatform: in.ByPlatform, now: now}); terr != nil {
		return nil, terr
	}
	days := policy.Days
	if in.ByPlatform && in.Days > 0 {
		if err := ValidateUpgradeTrialDays(in.Days); err != nil {
			return nil, err
		}
		days = in.Days
	}
	source := ChangeSourceTenant
	if in.ByPlatform {
		source = ChangeSourcePlatform
	}
	trial := &UpgradeTrial{
		PlanID: target.ID, PlanCode: target.PlanCode, PlanName: target.Name, TierOrder: target.TierOrder,
		BasePlanID: paid.ID, BasePlanCode: paid.PlanCode, BasePlanName: paid.Name,
		Days: days, StartedAt: now, EndsAt: now.AddDate(0, 0, days), Source: source, StartedBy: in.StartedBy,
	}
	change := &ScheduledPlanChange{
		PlanID: target.ID, PlanCode: target.PlanCode, PlanName: target.Name,
		FromPlanID: paid.ID, FromPlanCode: paid.PlanCode, FromPlanName: paid.Name,
		EffectiveAt: nextRenewalAtOrAfter(sub, trial.EndsAt), Source: ChangeSourceUpgradeTrial,
		RequestedAt: now, RequestedBy: in.StartedBy,
	}

	meta := cloneMetadata(sub.Metadata)
	if old, ok := upgradeTrialOf(sub); ok {
		// A finished or stale trial the schedule job has not cleared yet goes to the history.
		meta = withTrialClosed(meta, old, TrialOutcomeCompleted, now)
	}
	meta[MetaUpgradeTrial] = encodeMeta(trial)
	meta[MetaScheduledPlanChange] = encodeMeta(change)
	used := usedUpgradeTrials(sub.Metadata)
	if !containsString(used, target.PlanCode) {
		used = append(used, target.PlanCode)
	}
	meta[MetaUpgradeTrialsUsed] = used

	if err := s.updateMetadataLocked(ctx, sub, meta, func(tx *ent.Tx) {
		s.writeOutboxEvent(ctx, tx, sub.TenantID, "subscription", sub.ID, "upgrade_trial_started", map[string]any{
			"tenant_id":      sub.TenantID.String(),
			"plan_code":      target.PlanCode,
			"plan_name":      target.Name,
			"base_plan_code": paid.PlanCode,
			"base_plan_name": paid.Name,
			"days":           days,
			"ends_at":        trial.EndsAt.Format("02 Jan 2006"),
			"upgrade_at":     change.EffectiveAt.Format("02 Jan 2006"),
			"source":         source,
			"manage_url":     s.uiURL("/billing"),
			"notification":   map[string]any{"target": "tenant_admin"},
		})
		s.publishPlanInForce(ctx, tx, sub, target, "upgrade_trial_started")
	}); err != nil {
		return nil, err
	}
	s.log.Info("upgrade trial started", zap.String("tenant_id", sub.TenantID.String()),
		zap.String("plan_code", target.PlanCode), zap.Int("days", days), zap.String("source", source))
	s.afterBillingPlanChange(sub.TenantID, BillingPlanIDFor(sub), billingPlanIDWith(sub, meta))
	return s.GetSubscriptionResult(ctx, in.TenantID)
}

// EndUpgradeTrial ends a running trial now. The tenant keeps the plan they pay for: the upgrade
// the trial scheduled is cancelled with it.
func (s *Service) EndUpgradeTrial(ctx context.Context, tenantID uuid.UUID, byPlatform bool) (*SubscriptionResult, error) {
	sub, err := s.getSubscription(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	t, ok := ActiveUpgradeTrial(sub, now)
	if !ok {
		return nil, trialErr(TrialReasonNoTrial, "No upgrade trial is running.")
	}
	outcome := TrialOutcomeEndedByTenant
	if byPlatform {
		outcome = TrialOutcomeEndedByPlatform
	}
	if err := s.closeUpgradeTrial(ctx, sub, t, outcome, true, now); err != nil {
		return nil, err
	}
	return s.GetSubscriptionResult(ctx, tenantID)
}

// closeUpgradeTrial removes the trial. A trial cut short (ended by the tenant or the platform)
// also drops the upgrade it scheduled; a completed one leaves it for the renewal date. notify
// sends the tenant-facing ended event and moves the plan in force back to the paid plan.
func (s *Service) closeUpgradeTrial(ctx context.Context, sub *ent.TenantSubscription, t *UpgradeTrial, outcome string, notify bool, now time.Time) error {
	meta := withTrialClosed(sub.Metadata, t, outcome, now)
	cutShort := outcome == TrialOutcomeEndedByTenant || outcome == TrialOutcomeEndedByPlatform
	var keptChange *ScheduledPlanChange
	if pc, ok := PendingPlanChange(sub); ok {
		if cutShort && pc.Source == ChangeSourceUpgradeTrial && pc.PlanID == t.PlanID {
			delete(meta, MetaScheduledPlanChange)
		} else {
			keptChange = pc
		}
	}
	var paid *ent.SubscriptionPlan
	if notify {
		p, err := s.client.SubscriptionPlan.Get(ctx, sub.PlanID)
		if err != nil {
			return fmt.Errorf("lookup plan: %w", err)
		}
		paid = p
	}
	billingBefore, billingAfter := BillingPlanIDFor(sub), billingPlanIDWith(sub, meta)
	if err := s.updateMetadataLocked(ctx, sub, meta, func(tx *ent.Tx) {
		if !notify {
			return
		}
		payload := map[string]any{
			"tenant_id":      sub.TenantID.String(),
			"plan_code":      t.PlanCode,
			"plan_name":      t.PlanName,
			"base_plan_code": paid.PlanCode,
			"base_plan_name": paid.Name,
			"outcome":        outcome,
			"manage_url":     s.uiURL("/billing"),
			"notification":   map[string]any{"target": "tenant_admin"},
		}
		if keptChange != nil {
			payload["upgrade_plan_name"] = keptChange.PlanName
			payload["upgrade_at"] = keptChange.EffectiveAt.Format("02 Jan 2006")
		}
		s.writeOutboxEvent(ctx, tx, sub.TenantID, "subscription", sub.ID, "upgrade_trial_ended", payload)
		s.publishPlanInForce(ctx, tx, sub, paid, "upgrade_trial_ended")
	}); err != nil {
		return err
	}
	s.log.Info("upgrade trial closed", zap.String("tenant_id", sub.TenantID.String()),
		zap.String("plan_code", t.PlanCode), zap.String("outcome", outcome))
	s.afterBillingPlanChange(sub.TenantID, billingBefore, billingAfter)
	return nil
}

func containsString(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}
