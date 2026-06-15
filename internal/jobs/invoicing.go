package jobs

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/tenantsubscription"
	"github.com/bengobox/subscription-service/internal/modules/billing"
)

// InvoiceLeadDays is how many days before period end a subscription invoice is generated
// and emailed, so tenants can pay early (the next cycle then stacks onto the current end).
const InvoiceLeadDays = 7

// StartInvoiceJob generates subscription invoices ~InvoiceLeadDays before expiry for paid
// plans. Runs hourly (offset 45m to avoid contending with the expiry/renewal jobs).
// Idempotent: GenerateAndSend skips a period that already has an invoice.
func StartInvoiceJob(ctx context.Context, log *zap.Logger, orm *ent.Client, invSvc *billing.InvoiceService) {
	log = log.Named("invoice.job")
	if invSvc == nil {
		log.Warn("invoice job: invoice service not configured, skipping")
		return
	}

	select {
	case <-ctx.Done():
		return
	case <-time.After(45 * time.Minute):
	}

	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	generateUpcomingInvoices(ctx, log, orm, invSvc)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			generateUpcomingInvoices(ctx, log, orm, invSvc)
		}
	}
}

func generateUpcomingInvoices(ctx context.Context, log *zap.Logger, orm *ent.Client, invSvc *billing.InvoiceService) {
	now := time.Now().UTC()
	windowStart := now.AddDate(0, 0, InvoiceLeadDays-1)
	windowEnd := now.AddDate(0, 0, InvoiceLeadDays)

	upcoming, err := orm.TenantSubscription.Query().
		Where(
			tenantsubscription.StatusEQ(tenantsubscription.StatusACTIVE),
			tenantsubscription.CurrentPeriodEndGTE(windowStart),
			tenantsubscription.CurrentPeriodEndLTE(windowEnd),
		).
		WithPlan().
		WithTenant().
		All(ctx)
	if err != nil {
		log.Error("invoice job: query failed", zap.Error(err))
		return
	}

	generated := 0
	for _, sub := range upcoming {
		// Defensive: demo + platform-owner tenants are exempt and should never be invoiced.
		// They normally carry no subscription row at all, but skip by slug just in case.
		if sub.Edges.Tenant != nil && exemptTenantSlug(sub.Edges.Tenant.Slug) {
			continue
		}
		if sub.Edges.Plan == nil || sub.Edges.Plan.BasePrice == 0 {
			continue // free plans are extended directly by the renewal job
		}
		res, err := invSvc.GenerateAndSend(ctx, sub, false)
		if err != nil {
			log.Error("invoice job: generation failed", zap.String("tenant_id", sub.TenantID.String()), zap.Error(err))
			continue
		}
		if res != nil && !res.Skipped {
			generated++
		}
	}

	if generated > 0 {
		log.Info("invoice job: generated", zap.Int("count", generated))
	}
}
