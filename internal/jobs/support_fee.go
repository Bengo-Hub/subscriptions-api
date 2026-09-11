package jobs

import (
	"context"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/predicate"
	"github.com/bengobox/subscription-service/internal/ent/subscriptionplan"
	"github.com/bengobox/subscription-service/internal/ent/supportfeecycle"
	"github.com/bengobox/subscription-service/internal/ent/tenantsubscription"
	"github.com/bengobox/subscription-service/internal/modules/billing"
	"github.com/bengobox/subscription-service/internal/modules/subscriptions"
)

// SupportFeeGraceDays mirrors GraceDays (renewal.go) — the same 7-day grace window,
// deliberately reusing the platform's existing grace-period value rather than a new number.
const SupportFeeGraceDays = 7

// SupportFeeInvoiceLeadDays mirrors InvoiceLeadDays (invoicing.go) exactly — a support-fee
// invoice is generated and emailed 7 days before its due date.
const SupportFeeInvoiceLeadDays = 7

// StartSupportFeeJob runs the three background goroutines that drive the annual support-fee
// lifecycle for perpetual/one-time-license tenants, offset like renewal.go/invoicing.go so they
// don't all hammer the DB on the same tick:
//   - Enrollment (ensureSupportFeeCycles): hourly, runs immediately. Creates the current
//     SupportFeeCycle row for any ACTIVE one-time-license tenant that doesn't have one yet —
//     this single generic job is both the ongoing enrollment mechanism for future one-time
//     tenants AND the backfill for tenants already onboarded when support billing shipped
//     (anchor_date = their real tenant_subscriptions.created_at, never "today").
//   - Invoicing (generateUpcomingSupportFeeInvoices): hourly, offset 15m. Generates + emails an
//     invoice SupportFeeInvoiceLeadDays before due_date.
//   - Overdue transition (transitionOverdueSupportFees): hourly, offset 45m. Flips a cycle
//     OVERDUE once due_date passes unpaid and starts its grace window.
//   - Grace reminders (sendSupportFeeGraceReminders): every 6h, runs immediately. One reminder
//     per day per cycle while still within grace, mirroring grace.go's dedupe-by-date pattern.
func StartSupportFeeJob(ctx context.Context, log *zap.Logger, orm *ent.Client, svc *subscriptions.Service, invSvc *billing.InvoiceService) {
	log = log.Named("support_fee.job")

	go runSupportFeeEnrollmentJob(ctx, log, orm)

	go func() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(15 * time.Minute):
		}
		runSupportFeeInvoiceJob(ctx, log, orm, invSvc)
	}()

	go func() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(45 * time.Minute):
		}
		runSupportFeeOverdueJob(ctx, log, orm, svc)
	}()

	go runSupportFeeGraceReminderJob(ctx, log, orm, svc)

	log.Info("support fee jobs started")
}

// isPerpetualLicense is the inverse of notPerpetual (renewal.go) — subs whose PLAN billing
// cycle is ONE_TIME. Support fees only ever apply to these; a recurring subscriber already pays
// via the normal renewal cycle and has no support-fee obligation at all.
func isPerpetualLicense() predicate.TenantSubscription {
	return tenantsubscription.HasPlanWith(subscriptionplan.BillingCycleEQ("ONE_TIME"))
}

// supportPlanCodeFor derives the SUPPORT_* catalog code from a ONE_TIME license plan code.
// Verified against every currently-seeded one-time family:
//
//	POWERSUITE_DUKA_GOLD_ONE_TIME  -> SUPPORT_DUKA_GOLD        (strip _ONE_TIME, drop POWERSUITE_)
//	LIBRARY_PROFESSIONAL_ONE_TIME  -> SUPPORT_LIBRARY_PROFESSIONAL
//	ERP_STARTER_ONE_TIME           -> SUPPORT_ERP_STARTER
//	AFYA_CLINIC_ONE_TIME           -> SUPPORT_AFYA_CLINIC
//
// Returns ok=false for a plan code that isn't a _ONE_TIME license at all (caller should skip).
func supportPlanCodeFor(licensePlanCode string) (code string, ok bool) {
	if !strings.HasSuffix(licensePlanCode, "_ONE_TIME") {
		return "", false
	}
	base := strings.TrimSuffix(licensePlanCode, "_ONE_TIME")
	base = strings.TrimPrefix(base, "POWERSUITE_")
	return "SUPPORT_" + base, true
}

// currentCycleNumber returns which annual cycle (1-based) covers `now`, given the tenant's
// anchor_date. Cycle 1 = [anchor, anchor+1y), cycle 2 = [anchor+1y, anchor+2y), etc.
func currentCycleNumber(anchor, now time.Time) int {
	years := now.Year() - anchor.Year()
	if anniversary := anchor.AddDate(years, 0, 0); anniversary.After(now) {
		years--
	}
	if years < 0 {
		years = 0
	}
	return years + 1
}

func runSupportFeeEnrollmentJob(ctx context.Context, log *zap.Logger, orm *ent.Client) {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	ensureSupportFeeCycles(ctx, log, orm)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			ensureSupportFeeCycles(ctx, log, orm)
		}
	}
}

// ensureSupportFeeCycles creates the current SupportFeeCycle row for every ACTIVE one-time-
// license tenant that doesn't already have one. Idempotent via the
// (tenant_subscription_id, cycle_number) unique index — a concurrent/duplicate run is a no-op.
func ensureSupportFeeCycles(ctx context.Context, log *zap.Logger, orm *ent.Client) {
	subs, err := orm.TenantSubscription.Query().
		Where(
			tenantsubscription.StatusEQ(tenantsubscription.StatusACTIVE),
			isPerpetualLicense(),
		).
		WithPlan().
		All(ctx)
	if err != nil {
		log.Error("support fee enrollment: query failed", zap.Error(err))
		return
	}
	if len(subs) == 0 {
		return
	}

	supportPlans, err := orm.SubscriptionPlan.Query().
		Where(subscriptionplan.PlanCodeHasPrefix("SUPPORT_")).
		All(ctx)
	if err != nil {
		log.Error("support fee enrollment: load support plans failed", zap.Error(err))
		return
	}
	byCode := make(map[string]*ent.SubscriptionPlan, len(supportPlans))
	for _, p := range supportPlans {
		byCode[p.PlanCode] = p
	}

	now := time.Now().UTC()
	created := 0
	for _, sub := range subs {
		if sub.Edges.Plan == nil {
			continue
		}
		supportCode, ok := supportPlanCodeFor(sub.Edges.Plan.PlanCode)
		if !ok {
			continue
		}
		supportPlan, ok := byCode[supportCode]
		if !ok {
			log.Warn("support fee enrollment: no catalog row for derived support plan code",
				zap.String("license_plan", sub.Edges.Plan.PlanCode),
				zap.String("derived_support_code", supportCode))
			continue
		}

		// anchor_date = the tenant's real onboarding date. TenantSubscription.CreatedAt is
		// Immutable() in the schema, so it reflects the ORIGINAL subscribe time even if the
		// plan was later changed via PlatformHandler.UpdateSubscription — never "today".
		anchorDate := sub.CreatedAt.UTC()
		cycleNumber := currentCycleNumber(anchorDate, now)

		exists, err := orm.SupportFeeCycle.Query().
			Where(
				supportfeecycle.TenantSubscriptionIDEQ(sub.ID),
				supportfeecycle.CycleNumberEQ(cycleNumber),
			).
			Exist(ctx)
		if err != nil {
			log.Error("support fee enrollment: existence check failed", zap.String("tenant_id", sub.TenantID.String()), zap.Error(err))
			continue
		}
		if exists {
			continue
		}

		periodStart := anchorDate.AddDate(cycleNumber-1, 0, 0)
		dueDate := anchorDate.AddDate(cycleNumber, 0, 0)

		if _, err := orm.SupportFeeCycle.Create().
			SetTenantID(sub.TenantID).
			SetTenantSubscriptionID(sub.ID).
			SetSupportPlanID(supportPlan.ID).
			SetAnchorDate(anchorDate).
			SetCycleNumber(cycleNumber).
			SetPeriodStart(periodStart).
			SetDueDate(dueDate).
			SetBasePrice(supportPlan.BasePrice).
			Save(ctx); err != nil {
			log.Error("support fee enrollment: create cycle failed", zap.String("tenant_id", sub.TenantID.String()), zap.Error(err))
			continue
		}
		created++
		log.Info("support fee enrollment: cycle created",
			zap.String("tenant_id", sub.TenantID.String()),
			zap.String("support_plan", supportCode),
			zap.Int("cycle_number", cycleNumber),
			zap.Time("due_date", dueDate),
		)
	}

	if created > 0 {
		log.Info("support fee enrollment: completed", zap.Int("created", created))
	}
}

func runSupportFeeInvoiceJob(ctx context.Context, log *zap.Logger, orm *ent.Client, invSvc *billing.InvoiceService) {
	if invSvc == nil {
		log.Warn("support fee invoice job: invoice service not configured, skipping")
		return
	}
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	generateUpcomingSupportFeeInvoices(ctx, log, orm, invSvc)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			generateUpcomingSupportFeeInvoices(ctx, log, orm, invSvc)
		}
	}
}

func generateUpcomingSupportFeeInvoices(ctx context.Context, log *zap.Logger, orm *ent.Client, invSvc *billing.InvoiceService) {
	now := time.Now().UTC()
	windowEnd := now.AddDate(0, 0, SupportFeeInvoiceLeadDays)

	cycles, err := orm.SupportFeeCycle.Query().
		Where(
			supportfeecycle.StatusIn(supportfeecycle.StatusPENDING, supportfeecycle.StatusINVOICED),
			supportfeecycle.DueDateLTE(windowEnd),
		).
		WithTenantSubscription(func(q *ent.TenantSubscriptionQuery) { q.WithTenant() }).
		WithSupportPlan().
		All(ctx)
	if err != nil {
		log.Error("support fee invoice job: query failed", zap.Error(err))
		return
	}

	generated := 0
	for _, cycle := range cycles {
		res, err := invSvc.GenerateAndSendSupportFeeInvoice(ctx, cycle, false)
		if err != nil {
			log.Error("support fee invoice job: generation failed", zap.String("tenant_id", cycle.TenantID.String()), zap.Error(err))
			continue
		}
		if res != nil && !res.Skipped {
			generated++
		}
	}

	if generated > 0 {
		log.Info("support fee invoice job: generated", zap.Int("count", generated))
	}
}

func runSupportFeeOverdueJob(ctx context.Context, log *zap.Logger, orm *ent.Client, svc *subscriptions.Service) {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	transitionOverdueSupportFees(ctx, log, orm, svc)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			transitionOverdueSupportFees(ctx, log, orm, svc)
		}
	}
}

// transitionOverdueSupportFees flips a cycle to OVERDUE the moment its due_date passes unpaid
// and starts its grace window. Confirmed product decision: there is no further status past
// OVERDUE — it stays blocked (mutations only; reads always pass, enforced by
// RequireSupportFeeCurrentForMutations) indefinitely until paid or a platform admin waives it.
// The StatusIn(PENDING, INVOICED) filter means this only ever fires once per cycle.
func transitionOverdueSupportFees(ctx context.Context, log *zap.Logger, orm *ent.Client, svc *subscriptions.Service) {
	now := time.Now().UTC()

	pastDue, err := orm.SupportFeeCycle.Query().
		Where(
			supportfeecycle.StatusIn(supportfeecycle.StatusPENDING, supportfeecycle.StatusINVOICED),
			supportfeecycle.DueDateLT(now),
		).
		All(ctx)
	if err != nil {
		log.Error("support fee overdue job: query failed", zap.Error(err))
		return
	}

	overdueCount := 0
	for _, cycle := range pastDue {
		graceUntil := cycle.DueDate.UTC().AddDate(0, 0, SupportFeeGraceDays)

		tx, err := orm.Tx(ctx)
		if err != nil {
			log.Error("support fee overdue job: tx start failed", zap.String("cycle_id", cycle.ID.String()), zap.Error(err))
			continue
		}
		updated, err := tx.SupportFeeCycle.UpdateOneID(cycle.ID).
			SetStatus(supportfeecycle.StatusOVERDUE).
			SetGraceUntil(graceUntil).
			Save(ctx)
		if err != nil {
			_ = tx.Rollback()
			log.Error("support fee overdue job: update failed", zap.String("cycle_id", cycle.ID.String()), zap.Error(err))
			continue
		}
		svc.WriteOutboxEventPublic(ctx, tx, updated.TenantID, "support_fee_cycle", updated.ID, "support_fee_overdue", map[string]any{
			"tenant_id":     updated.TenantID.String(),
			"due_date":      updated.DueDate.UTC().Format(time.RFC3339),
			"grace_ends_at": graceUntil.Format("02 Jan 2006"),
			"notification": map[string]any{
				"target":          "tenant_admin",
				"recipient_email": stringFromMeta(updated.Metadata, "billing_email"),
			},
		})
		if err := tx.Commit(); err != nil {
			log.Error("support fee overdue job: commit failed", zap.String("cycle_id", cycle.ID.String()), zap.Error(err))
			continue
		}
		overdueCount++
		log.Info("support fee overdue job: cycle overdue, grace started",
			zap.String("tenant_id", updated.TenantID.String()), zap.Time("grace_until", graceUntil))
	}

	if overdueCount > 0 {
		log.Info("support fee overdue job: completed", zap.Int("overdue", overdueCount))
	}
}

func runSupportFeeGraceReminderJob(ctx context.Context, log *zap.Logger, orm *ent.Client, svc *subscriptions.Service) {
	ticker := time.NewTicker(6 * time.Hour)
	defer ticker.Stop()

	sendSupportFeeGraceReminders(ctx, log, orm, svc)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sendSupportFeeGraceReminders(ctx, log, orm, svc)
		}
	}
}

// sendSupportFeeGraceReminders emits one support_fee_grace_reminder per day (mirrors
// grace.go's sendGraceReminders dedupe-by-date pattern) for every cycle still within its grace
// window. Once grace_until elapses, no further reminders are sent — the cycle just stays
// OVERDUE/blocked, matching the confirmed no-further-escalation product decision.
func sendSupportFeeGraceReminders(ctx context.Context, log *zap.Logger, orm *ent.Client, svc *subscriptions.Service) {
	now := time.Now().UTC()
	today := now.Format("2006-01-02")

	cycles, err := orm.SupportFeeCycle.Query().
		Where(supportfeecycle.StatusEQ(supportfeecycle.StatusOVERDUE)).
		All(ctx)
	if err != nil {
		log.Error("support fee grace reminder: query failed", zap.Error(err))
		return
	}

	sent := 0
	for _, cycle := range cycles {
		if cycle.GraceUntil == nil || !now.Before(*cycle.GraceUntil) {
			continue // grace already elapsed — stays blocked, no further reminders
		}
		if last, _ := cycle.Metadata["last_grace_reminder_date"].(string); last == today {
			continue
		}

		tx, err := orm.Tx(ctx)
		if err != nil {
			log.Error("support fee grace reminder: tx start failed", zap.String("cycle_id", cycle.ID.String()), zap.Error(err))
			continue
		}
		meta := mergeMeta(cycle.Metadata, map[string]any{"last_grace_reminder_date": today})
		if _, err := tx.SupportFeeCycle.UpdateOneID(cycle.ID).SetMetadata(meta).Save(ctx); err != nil {
			_ = tx.Rollback()
			log.Error("support fee grace reminder: update failed", zap.String("cycle_id", cycle.ID.String()), zap.Error(err))
			continue
		}
		svc.WriteOutboxEventPublic(ctx, tx, cycle.TenantID, "support_fee_cycle", cycle.ID, "support_fee_grace_reminder", map[string]any{
			"tenant_id":      cycle.TenantID.String(),
			"days_remaining": daysRemaining(now, *cycle.GraceUntil),
			"grace_ends_at":  cycle.GraceUntil.Format("02 Jan 2006"),
			"amount":         stringFromMeta(cycle.Metadata, "last_invoice_total"),
			"invoice_number": stringFromMeta(cycle.Metadata, "last_invoice_number"),
			"pay_link":       stringFromMeta(cycle.Metadata, "last_invoice_pay_url"),
			"notification": map[string]any{
				"target":          "tenant_admin",
				"recipient_email": stringFromMeta(cycle.Metadata, "billing_email"),
			},
		})
		if err := tx.Commit(); err != nil {
			log.Error("support fee grace reminder: commit failed", zap.String("cycle_id", cycle.ID.String()), zap.Error(err))
			continue
		}
		sent++
	}

	if sent > 0 {
		log.Info("support fee grace reminder: dispatched", zap.Int("count", sent))
	}
}
