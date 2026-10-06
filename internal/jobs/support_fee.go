package jobs

import (
	"context"
	"time"

	sharedcache "github.com/Bengo-Hub/cache"
	"go.uber.org/zap"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/supportfeecycle"
	"github.com/bengobox/subscription-service/internal/modules/billing"
	"github.com/bengobox/subscription-service/internal/modules/subscriptions"
)

// supportFeeInvoiceBatch caps how many cycles one invoice pass handles. Each needs several
// treasury calls; the rest are picked up on the next hourly pass.
const supportFeeInvoiceBatch = 200

// StartSupportFeeJob drives the support-agreement lifecycle (standard hosting and support fee of
// one-time-license tenants plus any special support agreements). Each pass is claimed once per
// period across replicas and offset so they don't all hit the DB on the same tick:
//   - Cycles (hourly, immediately): creates missing STANDARD agreements, then bills every agreement
//     whose next period is inside the invoice lead window (subscriptions.GenerateDueSupportCycles).
//   - Invoicing (hourly, +15m): invoices PENDING cycles due within SupportFeeInvoiceLeadDays.
//   - Overdue (hourly, +45m): flips unpaid cycles past their due date to OVERDUE and starts grace.
//     Mutations are blocked fleet-wide once grace ends (RequireSupportFeeCurrentForMutations).
//   - Grace reminders (every 6h, immediately): one reminder per cycle per day while in grace.
func StartSupportFeeJob(ctx context.Context, log *zap.Logger, orm *ent.Client, svc *subscriptions.Service, invSvc *billing.InvoiceService) {
	log = log.Named("support_fee.job")

	go runEvery(ctx, 0, time.Hour, func() { runSupportCycles(ctx, log, svc) })
	if invSvc != nil {
		go runEvery(ctx, 15*time.Minute, time.Hour, func() { generateUpcomingSupportFeeInvoices(ctx, log, orm, invSvc) })
	} else {
		log.Warn("support fee invoice job: invoice service not configured, skipping")
	}
	go runEvery(ctx, 45*time.Minute, time.Hour, func() { transitionOverdueSupportFees(ctx, log, orm, svc) })
	go runEvery(ctx, 0, 6*time.Hour, func() { sendSupportFeeGraceReminders(ctx, log, orm, svc) })

	log.Info("support fee jobs started")
}

// runEvery runs fn after delay and then on every tick until ctx ends.
func runEvery(ctx context.Context, delay, every time.Duration, fn func()) {
	if delay > 0 {
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
	}
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	fn()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			fn()
		}
	}
}

func runSupportCycles(ctx context.Context, log *zap.Logger, svc *subscriptions.Service) {
	if !sharedcache.ClaimPeriod(ctx, "subscriptions:support-fee-cycles", time.Hour) {
		return
	}
	now := time.Now().UTC()
	if n, err := svc.EnsureStandardSupportAgreements(ctx, now); err != nil {
		log.Error("support agreements: enrollment failed", zap.Error(err))
	} else if n > 0 {
		log.Info("support agreements: standard agreements created", zap.Int("count", n))
	}
	if n, err := svc.GenerateDueSupportCycles(ctx, now); err != nil {
		log.Error("support cycles: generation failed", zap.Error(err))
	} else if n > 0 {
		log.Info("support cycles: generated", zap.Int("count", n))
	}
}

func generateUpcomingSupportFeeInvoices(ctx context.Context, log *zap.Logger, orm *ent.Client, invSvc *billing.InvoiceService) {
	if !sharedcache.ClaimPeriod(ctx, "subscriptions:support-fee-invoices", time.Hour) {
		return
	}
	windowEnd := time.Now().UTC().AddDate(0, 0, subscriptions.SupportFeeInvoiceLeadDays)

	// Only PENDING cycles: an INVOICED one already has its invoice (re-issue is an explicit
	// admin action), so the scan stays proportional to new work. (status, due_date) index.
	cycles, err := orm.SupportFeeCycle.Query().
		Where(
			supportfeecycle.StatusEQ(supportfeecycle.StatusPENDING),
			supportfeecycle.DueDateLTE(windowEnd),
		).
		Order(ent.Asc(supportfeecycle.FieldDueDate)).
		Limit(supportFeeInvoiceBatch).
		WithTenantSubscription(func(q *ent.TenantSubscriptionQuery) { q.WithTenant() }).
		WithSupportPlan().
		WithAgreement().
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

// transitionOverdueSupportFees flips a cycle to OVERDUE once its due date passes unpaid and starts
// its grace window. There is no status past OVERDUE: mutations stay blocked (reads always pass)
// until it is paid or waived. The status filter makes this fire once per cycle.
func transitionOverdueSupportFees(ctx context.Context, log *zap.Logger, orm *ent.Client, svc *subscriptions.Service) {
	if !sharedcache.ClaimPeriod(ctx, "subscriptions:support-fee-overdue", time.Hour) {
		return
	}
	now := time.Now().UTC()

	pastDue, err := orm.SupportFeeCycle.Query().
		Where(
			supportfeecycle.StatusIn(supportfeecycle.StatusPENDING, supportfeecycle.StatusINVOICED),
			supportfeecycle.DueDateLT(now),
		).
		WithAgreement().
		All(ctx)
	if err != nil {
		log.Error("support fee overdue job: query failed", zap.Error(err))
		return
	}

	overdueCount := 0
	for _, cycle := range pastDue {
		graceUntil := cycle.DueDate.UTC().AddDate(0, 0, subscriptions.SupportFeeGraceDays)

		tx, err := orm.Tx(ctx)
		if err != nil {
			log.Error("support fee overdue job: tx start failed", zap.String("cycle_id", cycle.ID.String()), zap.Error(err))
			continue
		}
		// Conditional on the status read above, so a payment landing in between is never
		// overwritten back to OVERDUE.
		n, err := tx.SupportFeeCycle.Update().
			Where(
				supportfeecycle.IDEQ(cycle.ID),
				supportfeecycle.StatusIn(supportfeecycle.StatusPENDING, supportfeecycle.StatusINVOICED),
			).
			SetStatus(supportfeecycle.StatusOVERDUE).
			SetGraceUntil(graceUntil).
			Save(ctx)
		if err != nil || n == 0 {
			_ = tx.Rollback()
			if err != nil {
				log.Error("support fee overdue job: update failed", zap.String("cycle_id", cycle.ID.String()), zap.Error(err))
			}
			continue
		}
		payload := supportCyclePayload(cycle)
		payload["grace_ends_at"] = graceUntil.Format("02 Jan 2006")
		payload["days_remaining"] = daysRemaining(now, graceUntil)
		svc.WriteOutboxEventPublic(ctx, tx, cycle.TenantID, "subscription", cycle.ID, "support_fee_overdue", payload)
		if err := tx.Commit(); err != nil {
			log.Error("support fee overdue job: commit failed", zap.String("cycle_id", cycle.ID.String()), zap.Error(err))
			continue
		}
		overdueCount++
		log.Info("support fee overdue: grace started",
			zap.String("tenant_id", cycle.TenantID.String()), zap.Time("grace_until", graceUntil))
	}
	if overdueCount > 0 {
		log.Info("support fee overdue job: completed", zap.Int("overdue", overdueCount))
	}
}

// sendSupportFeeGraceReminders emits one support_fee_grace_reminder per cycle per day while the
// cycle is still inside its grace window (filtered in SQL). After grace ends no further reminders
// are sent; the cycle simply stays OVERDUE and blocking.
func sendSupportFeeGraceReminders(ctx context.Context, log *zap.Logger, orm *ent.Client, svc *subscriptions.Service) {
	if !sharedcache.ClaimPeriod(ctx, "subscriptions:support-fee-grace", 6*time.Hour) {
		return
	}
	now := time.Now().UTC()
	today := now.Format("2006-01-02")

	cycles, err := orm.SupportFeeCycle.Query().
		Where(
			supportfeecycle.StatusEQ(supportfeecycle.StatusOVERDUE),
			supportfeecycle.GraceUntilGT(now),
		).
		WithAgreement().
		All(ctx)
	if err != nil {
		log.Error("support fee grace reminder: query failed", zap.Error(err))
		return
	}

	sent := 0
	for _, cycle := range cycles {
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
		payload := supportCyclePayload(cycle)
		payload["days_remaining"] = daysRemaining(now, *cycle.GraceUntil)
		payload["grace_ends_at"] = cycle.GraceUntil.Format("02 Jan 2006")
		payload["reminder_date"] = today
		svc.WriteOutboxEventPublic(ctx, tx, cycle.TenantID, "subscription", cycle.ID, "support_fee_grace_reminder", payload)
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

// supportCyclePayload is the shared notification payload for support-fee events. The aggregate
// type must be "subscription" so the outbox subject (subscription.support_fee_*) lands in the
// subscription stream that notifications-api consumes.
func supportCyclePayload(cycle *ent.SupportFeeCycle) map[string]any {
	name := "Support"
	email := stringFromMeta(cycle.Metadata, "billing_email")
	if a := cycle.Edges.Agreement; a != nil {
		name = a.Name
		if email == "" {
			email = stringFromMeta(a.Metadata, "billing_email")
		}
	}
	return map[string]any{
		"tenant_id":      cycle.TenantID.String(),
		"agreement_name": name,
		"due_date":       cycle.DueDate.UTC().Format(time.RFC3339),
		"amount":         cycle.Metadata["last_invoice_total"],
		"currency":       stringFromMeta(cycle.Metadata, "last_invoice_currency"),
		"invoice_number": stringFromMeta(cycle.Metadata, "last_invoice_number"),
		"pay_link":       stringFromMeta(cycle.Metadata, "last_invoice_pay_url"),
		"invoice_url":    stringFromMeta(cycle.Metadata, billing.MetaLastInvoiceURL),
		"notification": map[string]any{
			"target":          "tenant_admin",
			"recipient_email": email,
		},
	}
}
