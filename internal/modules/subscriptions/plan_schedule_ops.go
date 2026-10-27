package subscriptions

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	entsql "entgo.io/ent/dialect/sql"
	"entgo.io/ent/dialect/sql/sqljson"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/predicate"
	"github.com/bengobox/subscription-service/internal/ent/tenantsubscription"
)

// ErrConcurrentUpdate means the subscription row changed between read and write.
var ErrConcurrentUpdate = errors.New("the subscription changed at the same time, please try again")

// SetUIBaseURL sets the subscriptions UI origin used for links in tenant emails.
func (s *Service) SetUIBaseURL(u string) { s.uiBaseURL = strings.TrimRight(u, "/") }

// SetRenewalInvoiceReissuer registers the billing hook that voids and reissues a tenant's open
// renewal invoice. Called when the plan a renewal invoice bills changes after it was raised.
// Registered by the app at startup so this package does not depend on billing.
func (s *Service) SetRenewalInvoiceReissuer(fn func(ctx context.Context, tenantID uuid.UUID)) {
	s.reissueRenewalInvoice = fn
}

func (s *Service) uiURL(path string) string {
	if s.uiBaseURL == "" {
		return ""
	}
	return s.uiBaseURL + path
}

// billingPlanIDWith is BillingPlanIDFor evaluated against metadata about to be written.
func billingPlanIDWith(sub *ent.TenantSubscription, meta map[string]any) uuid.UUID {
	cp := *sub
	cp.Metadata = meta
	return BillingPlanIDFor(&cp)
}

// afterBillingPlanChange reissues the open renewal invoice when the plan it bills changed. It
// runs detached from the request: the change is already committed and a treasury hiccup must not
// fail it; the reissuer logs its own failures and the next invoice run raises a fresh invoice.
func (s *Service) afterBillingPlanChange(tenantID, before, after uuid.UUID) {
	if before == after || s.reissueRenewalInvoice == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		s.reissueRenewalInvoice(ctx, tenantID)
	}()
}

// updateMetadataLocked writes meta only if the row is unchanged since sub was read (updated_at
// moves on every write), then lets events add outbox rows in the same transaction.
func (s *Service) updateMetadataLocked(ctx context.Context, sub *ent.TenantSubscription, meta map[string]any, events func(tx *ent.Tx)) error {
	return s.updateLocked(ctx, sub, func(u *ent.TenantSubscriptionUpdateOne) { u.SetMetadata(meta) }, events)
}

func (s *Service) updateLocked(ctx context.Context, sub *ent.TenantSubscription, set func(u *ent.TenantSubscriptionUpdateOne), events func(tx *ent.Tx)) error {
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("start transaction: %w", err)
	}
	upd := tx.TenantSubscription.UpdateOneID(sub.ID).Where(tenantsubscription.UpdatedAtEQ(sub.UpdatedAt))
	set(upd)
	if _, err := upd.Save(ctx); err != nil {
		_ = tx.Rollback()
		if ent.IsNotFound(err) {
			return ErrConcurrentUpdate
		}
		return fmt.Errorf("update subscription: %w", err)
	}
	if events != nil {
		events(tx)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// planChangeDirection names a move between plans for events and notifications.
func planChangeDirection(oldPlan, newPlan *ent.SubscriptionPlan) string {
	if oldPlan == nil || newPlan == nil || newPlan.TierOrder <= 0 {
		return "changed"
	}
	switch {
	case newPlan.TierOrder > oldPlan.TierOrder:
		return "upgraded"
	case newPlan.TierOrder < oldPlan.TierOrder:
		return "downgraded"
	}
	return "changed"
}

// publishPlanInForce tells other services which plan's entitlements apply now. auth-api stores
// new_plan_code on its tenant row and drops the shared tenant cache key; services that cache
// entitlements refresh on the same event.
func (s *Service) publishPlanInForce(ctx context.Context, tx *ent.Tx, sub *ent.TenantSubscription, plan *ent.SubscriptionPlan, direction string) {
	s.writeOutboxEvent(ctx, tx, sub.TenantID, "tenant", sub.TenantID, "subscription.updated",
		planEventPayload(ctx, tx, sub, nil, plan, direction))
}

// emitPlanChanged records a plan_id move: the directional subscription event (tenant email) and
// tenant.subscription.updated for other services, with one payload.
func (s *Service) emitPlanChanged(ctx context.Context, tx *ent.Tx, sub *ent.TenantSubscription, oldPlan, newPlan *ent.SubscriptionPlan) {
	direction := planChangeDirection(oldPlan, newPlan)
	payload := planEventPayload(ctx, tx, sub, oldPlan, newPlan, direction)
	payload["notification"] = map[string]any{"target": "tenant_admin"}
	s.writeOutboxEvent(ctx, tx, sub.TenantID, "subscription", sub.ID, direction, payload)
	s.writeOutboxEvent(ctx, tx, sub.TenantID, "tenant", sub.TenantID, "subscription.updated", payload)
}

func planEventPayload(ctx context.Context, tx *ent.Tx, sub *ent.TenantSubscription, oldPlan, newPlan *ent.SubscriptionPlan, direction string) map[string]any {
	tenantSlug := ""
	if t, err := tx.Tenant.Get(ctx, sub.TenantID); err == nil {
		tenantSlug = t.Slug
	}
	serviceTag := ""
	if newPlan.ServiceTag != nil {
		serviceTag = *newPlan.ServiceTag
	}
	payload := map[string]any{
		"tenant_id":     sub.TenantID.String(),
		"tenant_slug":   tenantSlug,
		"new_plan_code": newPlan.PlanCode,
		"new_plan_name": newPlan.Name,
		"service_tag":   serviceTag,
		"direction":     direction,
	}
	if oldPlan != nil {
		payload["old_plan_id"] = oldPlan.ID.String()
		payload["old_plan_code"] = oldPlan.PlanCode
		payload["old_plan_name"] = oldPlan.Name
	}
	return payload
}

// schedulePlanChange stores a plan switch for effectiveAt, replacing any earlier schedule.
func (s *Service) schedulePlanChange(ctx context.Context, sub *ent.TenantSubscription, paid, target *ent.SubscriptionPlan, effectiveAt time.Time, source, by string) error {
	now := time.Now().UTC()
	change := &ScheduledPlanChange{
		PlanID: target.ID, PlanCode: target.PlanCode, PlanName: target.Name,
		FromPlanID: paid.ID, FromPlanCode: paid.PlanCode, FromPlanName: paid.Name,
		EffectiveAt: effectiveAt, Source: source, RequestedAt: now, RequestedBy: by,
	}
	meta := cloneMetadata(sub.Metadata)
	meta[MetaScheduledPlanChange] = encodeMeta(change)
	before, after := BillingPlanIDFor(sub), billingPlanIDWith(sub, meta)
	if err := s.updateMetadataLocked(ctx, sub, meta, func(tx *ent.Tx) {
		s.writeOutboxEvent(ctx, tx, sub.TenantID, "subscription", sub.ID, "plan_change_scheduled", map[string]any{
			"tenant_id":      sub.TenantID.String(),
			"plan_code":      target.PlanCode,
			"plan_name":      target.Name,
			"from_plan_code": paid.PlanCode,
			"from_plan_name": paid.Name,
			"effective_at":   effectiveAt.Format("02 Jan 2006"),
			"source":         source,
			"manage_url":     s.uiURL("/billing"),
			"notification":   map[string]any{"target": "tenant_admin"},
		})
	}); err != nil {
		return err
	}
	s.log.Info("plan change scheduled", zap.String("tenant_id", sub.TenantID.String()),
		zap.String("plan_code", target.PlanCode), zap.Time("effective_at", effectiveAt), zap.String("source", source))
	s.afterBillingPlanChange(sub.TenantID, before, after)
	return nil
}

// CancelScheduledPlanChange drops the plan change waiting for the next renewal. A running trial
// carries on to its end; the tenant then stays on the plan they pay for.
func (s *Service) CancelScheduledPlanChange(ctx context.Context, tenantID uuid.UUID) (*SubscriptionResult, error) {
	sub, err := s.getSubscription(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	pc, ok := PendingPlanChange(sub)
	if !ok {
		return nil, trialErr(TrialReasonNoChange, "No plan change is scheduled.")
	}
	meta := cloneMetadata(sub.Metadata)
	delete(meta, MetaScheduledPlanChange)
	before, after := BillingPlanIDFor(sub), billingPlanIDWith(sub, meta)
	if err := s.updateMetadataLocked(ctx, sub, meta, nil); err != nil {
		return nil, err
	}
	s.log.Info("scheduled plan change cancelled", zap.String("tenant_id", tenantID.String()), zap.String("plan_code", pc.PlanCode))
	s.afterBillingPlanChange(tenantID, before, after)
	return s.GetSubscriptionResult(ctx, tenantID)
}

// ApplyScheduledPlanChangeNow moves the tenant to the scheduled plan today (platform override).
func (s *Service) ApplyScheduledPlanChangeNow(ctx context.Context, tenantID uuid.UUID) (*SubscriptionResult, error) {
	sub, err := s.getSubscription(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	pc, ok := PendingPlanChange(sub)
	if !ok {
		return nil, trialErr(TrialReasonNoChange, "No plan change is scheduled.")
	}
	if err := s.applyScheduledChange(ctx, sub, pc, time.Now().UTC()); err != nil {
		return nil, err
	}
	return s.GetSubscriptionResult(ctx, tenantID)
}

// applyScheduledChange moves plan_id to the scheduled plan and clears the schedule (and a trial
// of that same plan, which the move makes redundant).
func (s *Service) applyScheduledChange(ctx context.Context, sub *ent.TenantSubscription, pc *ScheduledPlanChange, now time.Time) error {
	oldPlan, err := s.client.SubscriptionPlan.Get(ctx, sub.PlanID)
	if err != nil {
		return fmt.Errorf("lookup plan: %w", err)
	}
	newPlan, err := s.client.SubscriptionPlan.Get(ctx, pc.PlanID)
	if err != nil {
		return fmt.Errorf("lookup scheduled plan: %w", err)
	}
	meta := cloneMetadata(sub.Metadata)
	delete(meta, MetaScheduledPlanChange)
	if t, ok := upgradeTrialOf(sub); ok {
		meta = withTrialClosed(meta, t, TrialOutcomeCompleted, now)
	}
	before := BillingPlanIDFor(sub)
	if err := s.updateLocked(ctx, sub, func(u *ent.TenantSubscriptionUpdateOne) {
		u.SetPlanID(newPlan.ID).SetMetadata(meta)
	}, func(tx *ent.Tx) {
		s.emitPlanChanged(ctx, tx, sub, oldPlan, newPlan)
	}); err != nil {
		return err
	}
	s.log.Info("scheduled plan change applied", zap.String("tenant_id", sub.TenantID.String()),
		zap.String("from", oldPlan.PlanCode), zap.String("to", newPlan.PlanCode))
	s.afterBillingPlanChange(sub.TenantID, before, newPlan.ID)
	return nil
}

// ScheduleRunStats counts what one ProcessPlanSchedules pass did.
type ScheduleRunStats struct {
	TrialsEnded    int
	EndingNotices  int
	ChangesApplied int
	Cleared        int
	Failed         int
}

// hasPendingSchedule matches subscriptions holding an upgrade trial or a scheduled change.
func hasPendingSchedule() predicate.TenantSubscription {
	return func(sel *entsql.Selector) {
		col := sel.C(tenantsubscription.FieldMetadata)
		sel.Where(entsql.Or(
			sqljson.HasKey(col, sqljson.Path(MetaUpgradeTrial)),
			sqljson.HasKey(col, sqljson.Path(MetaScheduledPlanChange)),
		))
	}
}

// ProcessPlanSchedules sends trial ending reminders, closes finished trials and applies scheduled
// plan changes whose date has passed. It walks only rows carrying one of the two keys, in id
// pages, so the cost tracks the number of open trials and schedules rather than all tenants.
func (s *Service) ProcessPlanSchedules(ctx context.Context, now time.Time) (ScheduleRunStats, error) {
	var st ScheduleRunStats
	const page = 200
	last := uuid.Nil
	for {
		subs, err := s.client.TenantSubscription.Query().
			Where(hasPendingSchedule(), tenantsubscription.IDGT(last)).
			Order(ent.Asc(tenantsubscription.FieldID)).
			Limit(page).
			All(ctx)
		if err != nil {
			return st, fmt.Errorf("list plan schedules: %w", err)
		}
		for _, sub := range subs {
			last = sub.ID
			if err := s.processPlanSchedule(ctx, sub, now, &st); err != nil {
				st.Failed++
				s.log.Warn("plan schedule step failed", zap.String("tenant_id", sub.TenantID.String()), zap.Error(err))
			}
		}
		if len(subs) < page {
			return st, nil
		}
	}
}

func (s *Service) processPlanSchedule(ctx context.Context, sub *ent.TenantSubscription, now time.Time, st *ScheduleRunStats) error {
	if t, ok := upgradeTrialOf(sub); ok {
		stale := t.BasePlanID != sub.PlanID || t.PlanID == sub.PlanID
		var err error
		switch {
		case stale:
			outcome := TrialOutcomePlanChanged
			if t.PlanID == sub.PlanID {
				outcome = TrialOutcomePaid
			}
			err = s.closeUpgradeTrial(ctx, sub, t, outcome, false, now)
			st.Cleared++
		case !now.Before(t.EndsAt):
			err = s.closeUpgradeTrial(ctx, sub, t, TrialOutcomeCompleted, grantsAccess(sub.Status), now)
			st.TrialsEnded++
		case !t.EndingNoticeSent && grantsAccess(sub.Status) && t.EndsAt.Sub(now) <= UpgradeTrialEndingNoticeDays*24*time.Hour:
			err = s.sendTrialEndingNotice(ctx, sub, t, now)
			st.EndingNotices++
		default:
			return nil
		}
		if err != nil {
			return err
		}
		if sub, err = s.client.TenantSubscription.Get(ctx, sub.ID); err != nil {
			return fmt.Errorf("reload subscription: %w", err)
		}
	}
	pc, ok := scheduledChangeOf(sub)
	if !ok {
		return nil
	}
	if pc.FromPlanID != sub.PlanID || pc.PlanID == sub.PlanID {
		meta := cloneMetadata(sub.Metadata)
		delete(meta, MetaScheduledPlanChange)
		st.Cleared++
		return s.updateMetadataLocked(ctx, sub, meta, nil)
	}
	if now.Before(pc.EffectiveAt) || sub.Status == tenantsubscription.StatusCANCELLED {
		return nil
	}
	st.ChangesApplied++
	return s.applyScheduledChange(ctx, sub, pc, now)
}

func (s *Service) sendTrialEndingNotice(ctx context.Context, sub *ent.TenantSubscription, t *UpgradeTrial, now time.Time) error {
	t.EndingNoticeSent = true
	meta := cloneMetadata(sub.Metadata)
	meta[MetaUpgradeTrial] = encodeMeta(t)
	payload := map[string]any{
		"tenant_id":      sub.TenantID.String(),
		"plan_code":      t.PlanCode,
		"plan_name":      t.PlanName,
		"base_plan_name": t.BasePlanName,
		"days_remaining": daysUntil(now, t.EndsAt),
		"ends_at":        t.EndsAt.Format("02 Jan 2006"),
		"manage_url":     s.uiURL("/billing"),
		"notification":   map[string]any{"target": "tenant_admin"},
	}
	if pc, ok := PendingPlanChange(sub); ok && pc.PlanID == t.PlanID {
		payload["upgrade_at"] = pc.EffectiveAt.Format("02 Jan 2006")
	}
	return s.updateMetadataLocked(ctx, sub, meta, func(tx *ent.Tx) {
		s.writeOutboxEvent(ctx, tx, sub.TenantID, "subscription", sub.ID, "upgrade_trial_ending", payload)
	})
}
