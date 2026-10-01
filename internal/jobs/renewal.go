package jobs

import (
	"context"
	sharedcache "github.com/Bengo-Hub/cache"
	"time"

	"go.uber.org/zap"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/predicate"
	"github.com/bengobox/subscription-service/internal/ent/subscriptionplan"
	"github.com/bengobox/subscription-service/internal/ent/tenantsubscription"
	"github.com/bengobox/subscription-service/internal/modules/billing"
	"github.com/bengobox/subscription-service/internal/modules/subscriptions"
)

// notPerpetual excludes ONE_TIME (perpetual license) subscriptions from the expiry/renewal
// lifecycle: they never expire and never auto-renew. Filter on the PLAN's billing cycle (the
// sub row may carry a stale MONTHLY default even for a one-time plan — see buildResult).
func notPerpetual() predicate.TenantSubscription {
	return tenantsubscription.Not(tenantsubscription.HasPlanWith(subscriptionplan.BillingCycleEQ("ONE_TIME")))
}

// StartRenewalJob runs two background goroutines:
//   - Expiry: marks ACTIVE subscriptions past current_period_end as EXPIRED (runs every hour)
//   - Renewal: extends free plans and charges a saved card for paid ones expiring within 24h
//     (runs every hour, offset 30m)
func StartRenewalJob(ctx context.Context, log *zap.Logger, orm *ent.Client, svc *subscriptions.Service, invSvc *billing.InvoiceService) {
	log = log.Named("renewal.job")

	go runExpiryJob(ctx, log, orm, svc)
	go func() {
		// Offset renewal job by 30 minutes so they don't both hammer DB at the same time
		select {
		case <-ctx.Done():
			return
		case <-time.After(30 * time.Minute):
		}
		runRenewalJob(ctx, log, orm, svc, invSvc)
	}()

	log.Info("renewal and expiry jobs started")
}

func runExpiryJob(ctx context.Context, log *zap.Logger, orm *ent.Client, svc *subscriptions.Service) {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	// Run immediately on startup
	expireSubscriptions(ctx, log, orm, svc)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			expireSubscriptions(ctx, log, orm, svc)
		}
	}
}

func runRenewalJob(ctx context.Context, log *zap.Logger, orm *ent.Client, svc *subscriptions.Service, invSvc *billing.InvoiceService) {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	// Run immediately on startup
	initiateRenewals(ctx, log, orm, svc, invSvc)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			initiateRenewals(ctx, log, orm, svc, invSvc)
		}
	}
}

// GraceDays is the post-expiry grace window during which services stay accessible
// (status remains ACTIVE) while the tenant is reminded daily to pay. After it elapses
// unpaid, the subscription is EXPIRED (total block).
const GraceDays = 7

// expireSubscriptions runs the grace lifecycle for ACTIVE subscriptions past their period
// end. First pass: enter a GraceDays grace window (keep ACTIVE, set metadata.grace_until,
// notify). Second pass: once grace_until elapses, mark EXPIRED (total block). Paying clears
// grace via RenewSubscription.
func expireSubscriptions(ctx context.Context, log *zap.Logger, orm *ent.Client, svc *subscriptions.Service) {
	// Runs on every replica's ticker; only the first replica in each period does the work.
	if !sharedcache.ClaimPeriod(ctx, "subscriptions:expiry", time.Hour) {
		return
	}
	now := time.Now().UTC()

	pastDue, err := orm.TenantSubscription.Query().
		Where(
			tenantsubscription.StatusEQ(tenantsubscription.StatusACTIVE),
			tenantsubscription.CurrentPeriodEndLT(now),
			notPerpetual(), // never expire a paid one-time/perpetual license
		).
		All(ctx)
	if err != nil {
		log.Error("expiry job: failed to query subscriptions", zap.Error(err))
		return
	}

	graceStarted, expiredCount := 0, 0
	for _, sub := range pastDue {
		graceUntil, inGrace := graceUntilOf(sub)

		if !inGrace {
			// Enter grace: keep ACTIVE, set grace_until, notify tenant.
			gu := sub.CurrentPeriodEnd.UTC().AddDate(0, 0, GraceDays)
			meta := mergeMeta(sub.Metadata, map[string]any{"grace_until": gu.Format(time.RFC3339)})
			tx, err := orm.Tx(ctx)
			if err != nil {
				log.Error("expiry job: tx start failed", zap.String("sub_id", sub.ID.String()), zap.Error(err))
				continue
			}
			if _, err := tx.TenantSubscription.UpdateOneID(sub.ID).SetMetadata(meta).Save(ctx); err != nil {
				_ = tx.Rollback()
				log.Error("expiry job: failed to enter grace", zap.String("sub_id", sub.ID.String()), zap.Error(err))
				continue
			}
			svc.WriteOutboxEventPublic(ctx, tx, sub.TenantID, "subscription", sub.ID, "grace_started", graceEventPayload(sub, gu, now))
			if err := tx.Commit(); err != nil {
				log.Error("expiry job: grace commit failed", zap.String("sub_id", sub.ID.String()), zap.Error(err))
				continue
			}
			graceStarted++
			continue
		}

		if now.Before(graceUntil) {
			// Still within grace — daily reminders are handled by the grace reminder job.
			continue
		}

		// Grace elapsed unpaid → total block.
		tx, err := orm.Tx(ctx)
		if err != nil {
			log.Error("expiry job: tx start failed", zap.String("sub_id", sub.ID.String()), zap.Error(err))
			continue
		}
		updated, err := tx.TenantSubscription.UpdateOneID(sub.ID).
			SetStatus(tenantsubscription.StatusEXPIRED).
			Save(ctx)
		if err != nil {
			_ = tx.Rollback()
			log.Error("expiry job: failed to expire subscription", zap.String("sub_id", sub.ID.String()), zap.Error(err))
			continue
		}
		svc.WriteOutboxEventPublic(ctx, tx, updated.TenantID, "subscription", updated.ID, "expired", map[string]any{
			"tenant_id": updated.TenantID.String(),
			"status":    "EXPIRED",
			"notification": map[string]any{
				"target": "tenant_admin",
			},
		})
		if err := tx.Commit(); err != nil {
			log.Error("expiry job: commit failed", zap.String("sub_id", sub.ID.String()), zap.Error(err))
			continue
		}
		expiredCount++
		log.Info("expiry job: subscription blocked after grace", zap.String("tenant_id", updated.TenantID.String()), zap.String("sub_id", updated.ID.String()))
	}

	if graceStarted > 0 || expiredCount > 0 {
		log.Info("expiry job: completed", zap.Int("grace_started", graceStarted), zap.Int("expired", expiredCount))
	}
}

// initiateRenewals handles subscriptions expiring within 24h.
// For free plans (base_price == 0): directly extends the period.
// For paid plans with a saved card: charges the period's invoice to it (see chargeSavedCard).
func initiateRenewals(ctx context.Context, log *zap.Logger, orm *ent.Client, svc *subscriptions.Service, invSvc *billing.InvoiceService) {
	// Runs on every replica's ticker; only the first replica in each period does the work.
	if !sharedcache.ClaimPeriod(ctx, "subscriptions:renewals", time.Hour) {
		return
	}
	now := time.Now().UTC()
	horizon := now.Add(24 * time.Hour)

	upcoming, err := orm.TenantSubscription.Query().
		Where(
			tenantsubscription.StatusEQ(tenantsubscription.StatusACTIVE),
			tenantsubscription.CurrentPeriodEndGTE(now),
			tenantsubscription.CurrentPeriodEndLTE(horizon),
			notPerpetual(), // never auto-renew (re-charge) a paid one-time/perpetual license
		).
		WithPlan().
		All(ctx)
	if err != nil {
		log.Error("renewal job: failed to query upcoming renewals", zap.Error(err))
		return
	}

	for _, sub := range upcoming {
		if sub.Edges.Plan == nil {
			continue
		}
		plan := sub.Edges.Plan

		// Respect auto_renew preference: missing key or true → renew; explicit false → skip.
		if autoRenew, ok := sub.Metadata["auto_renew"].(bool); ok && !autoRenew {
			log.Info("renewal job: auto_renew disabled, skipping", zap.String("tenant_id", sub.TenantID.String()))
			continue
		}

		if subscriptions.EffectivePrice(sub, plan) == 0 {
			// Free plan (or a custom price waived to 0): extend period directly
			extendFreePlan(ctx, log, orm, svc, sub)
		} else {
			// Paid plan: collect the period's invoice from a saved card when there is one.
			chargeSavedCard(ctx, log, orm, svc, invSvc, sub, plan)
		}
	}

	if len(upcoming) > 0 {
		log.Info("renewal job: processed", zap.Int("count", len(upcoming)))
	}
}

func extendFreePlan(ctx context.Context, log *zap.Logger, orm *ent.Client, svc *subscriptions.Service, sub *ent.TenantSubscription) {
	newStart := sub.CurrentPeriodEnd
	newEnd := newStart.AddDate(0, 1, 0)

	tx, err := orm.Tx(ctx)
	if err != nil {
		log.Error("renewal job: tx start failed for free plan", zap.String("tenant_id", sub.TenantID.String()), zap.Error(err))
		return
	}

	updated, err := tx.TenantSubscription.UpdateOneID(sub.ID).
		SetCurrentPeriodStart(newStart).
		SetCurrentPeriodEnd(newEnd).
		Save(ctx)
	if err != nil {
		_ = tx.Rollback()
		log.Error("renewal job: failed to extend free plan", zap.String("tenant_id", sub.TenantID.String()), zap.Error(err))
		return
	}

	svc.WriteOutboxEventPublic(ctx, tx, updated.TenantID, "subscription", updated.ID, "renewed", map[string]any{
		"tenant_id":      updated.TenantID.String(),
		"free_plan":      true,
		"new_period_end": newEnd.Format(time.RFC3339),
	})

	if err := tx.Commit(); err != nil {
		log.Error("renewal job: commit failed for free plan", zap.String("tenant_id", sub.TenantID.String()), zap.Error(err))
		return
	}

	log.Info("renewal job: free plan extended", zap.String("tenant_id", updated.TenantID.String()))
}

// chargeSavedCard collects a paid renewal from the tenant's saved card through the period's
// treasury invoice, so card, M-Pesa and manual payments all settle the same invoice. It replaces a
// direct payment-intent call to a treasury route that never existed (every paid renewal failed
// silently); tenants without a saved card pay from the emailed invoice link as before.
func chargeSavedCard(ctx context.Context, log *zap.Logger, orm *ent.Client, svc *subscriptions.Service, invSvc *billing.InvoiceService, sub *ent.TenantSubscription, plan *ent.SubscriptionPlan) {
	if invSvc == nil {
		log.Warn("renewal job: invoice service not configured, skipping paid renewal", zap.String("tenant_id", sub.TenantID.String()))
		return
	}
	res, err := invSvc.ChargeSavedCard(ctx, sub)
	if err != nil {
		log.Error("renewal job: saved-card charge failed", zap.String("tenant_id", sub.TenantID.String()), zap.Error(err))
		return
	}
	if !res.Attempted {
		return
	}
	tx, err := orm.Tx(ctx)
	if err != nil {
		log.Error("renewal job: tx start failed", zap.String("tenant_id", sub.TenantID.String()), zap.Error(err))
		return
	}
	svc.WriteOutboxEventPublic(ctx, tx, sub.TenantID, "subscription", sub.ID, "renewal_initiated", map[string]any{
		"tenant_id":      sub.TenantID.String(),
		"plan_code":      plan.PlanCode,
		"invoice_number": res.InvoiceNumber,
		"collection":     "saved_card",
		"status":         res.Status,
	})
	if err := tx.Commit(); err != nil {
		log.Error("renewal job: outbox commit failed", zap.String("tenant_id", sub.TenantID.String()), zap.Error(err))
	}
}
