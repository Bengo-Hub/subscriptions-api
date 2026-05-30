package jobs

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/tenantsubscription"
	"github.com/bengobox/subscription-service/internal/modules/subscriptions"
	serviceclient "github.com/Bengo-Hub/shared-service-client"
)

// StartRenewalJob runs two background goroutines:
//   - Expiry: marks ACTIVE subscriptions past current_period_end as EXPIRED (runs every hour)
//   - Renewal: initiates payment for subscriptions expiring within 24h (runs every hour, offset 30m)
func StartRenewalJob(ctx context.Context, log *zap.Logger, orm *ent.Client, svc *subscriptions.Service, treasuryClient *serviceclient.Client, treasuryAPIKey string) {
	log = log.Named("renewal.job")

	go runExpiryJob(ctx, log, orm, svc)
	go func() {
		// Offset renewal job by 30 minutes so they don't both hammer DB at the same time
		select {
		case <-ctx.Done():
			return
		case <-time.After(30 * time.Minute):
		}
		runRenewalJob(ctx, log, orm, svc, treasuryClient, treasuryAPIKey)
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

func runRenewalJob(ctx context.Context, log *zap.Logger, orm *ent.Client, svc *subscriptions.Service, treasuryClient *serviceclient.Client, treasuryAPIKey string) {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	// Run immediately on startup
	initiateRenewals(ctx, log, orm, svc, treasuryClient, treasuryAPIKey)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			initiateRenewals(ctx, log, orm, svc, treasuryClient, treasuryAPIKey)
		}
	}
}

// expireSubscriptions marks all ACTIVE subscriptions past their period end as EXPIRED.
func expireSubscriptions(ctx context.Context, log *zap.Logger, orm *ent.Client, svc *subscriptions.Service) {
	now := time.Now().UTC()

	expired, err := orm.TenantSubscription.Query().
		Where(
			tenantsubscription.StatusEQ(tenantsubscription.StatusACTIVE),
			tenantsubscription.CurrentPeriodEndLT(now),
		).
		All(ctx)
	if err != nil {
		log.Error("expiry job: failed to query subscriptions", zap.Error(err))
		return
	}

	for _, sub := range expired {
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

		log.Info("expiry job: subscription expired", zap.String("tenant_id", updated.TenantID.String()), zap.String("sub_id", updated.ID.String()))
	}

	if len(expired) > 0 {
		log.Info("expiry job: completed", zap.Int("expired_count", len(expired)))
	}
}

// initiateRenewals handles subscriptions expiring within 24h.
// For free plans (base_price == 0): directly extends the period.
// For paid plans: POST a new payment intent to Treasury and publish renewal_initiated event.
func initiateRenewals(ctx context.Context, log *zap.Logger, orm *ent.Client, svc *subscriptions.Service, treasuryClient *serviceclient.Client, treasuryAPIKey string) {
	now := time.Now().UTC()
	horizon := now.Add(24 * time.Hour)

	upcoming, err := orm.TenantSubscription.Query().
		Where(
			tenantsubscription.StatusEQ(tenantsubscription.StatusACTIVE),
			tenantsubscription.CurrentPeriodEndGTE(now),
			tenantsubscription.CurrentPeriodEndLTE(horizon),
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

		if plan.BasePrice == 0 {
			// Free plan: extend period directly
			extendFreePlan(ctx, log, orm, svc, sub)
		} else {
			// Paid plan: initiate Treasury payment intent
			initiatePaymentRenewal(ctx, log, orm, svc, sub, plan, treasuryClient, treasuryAPIKey)
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
		"tenant_id": updated.TenantID.String(),
		"free_plan": true,
		"new_period_end": newEnd.Format(time.RFC3339),
	})

	if err := tx.Commit(); err != nil {
		log.Error("renewal job: commit failed for free plan", zap.String("tenant_id", sub.TenantID.String()), zap.Error(err))
		return
	}

	log.Info("renewal job: free plan extended", zap.String("tenant_id", updated.TenantID.String()))
}

func initiatePaymentRenewal(ctx context.Context, log *zap.Logger, orm *ent.Client, svc *subscriptions.Service, sub *ent.TenantSubscription, plan *ent.SubscriptionPlan, treasuryClient *serviceclient.Client, treasuryAPIKey string) {
	if treasuryClient == nil {
		log.Warn("renewal job: treasury client not configured, skipping paid renewal", zap.String("tenant_id", sub.TenantID.String()))
		return
	}

	headers := map[string]string{}
	if treasuryAPIKey != "" {
		headers["X-API-Key"] = treasuryAPIKey
	}

	req := map[string]any{
		"amount":         plan.BasePrice,
		"currency":       plan.Currency,
		"payment_method": "auto",
		"reference_id":   sub.ID.String(),
		"reference_type": "subscription_renewal",
		"source_service": "subscription-service",
		"description":    "Auto-renewal: " + plan.Name,
		"metadata": map[string]any{
			"tenant_id":   sub.TenantID.String(),
			"plan_code":   plan.PlanCode,
			"renewal":     true,
		},
	}

	resp, err := treasuryClient.Post(ctx, "/api/v1/payments/intents", req, headers)
	if err != nil || !resp.IsSuccess() {
		log.Error("renewal job: treasury payment intent failed", zap.String("tenant_id", sub.TenantID.String()), zap.Error(err))
		return
	}

	// Publish renewal_initiated event via outbox
	tx, err := orm.Tx(ctx)
	if err != nil {
		log.Error("renewal job: tx start failed", zap.String("tenant_id", sub.TenantID.String()), zap.Error(err))
		return
	}

	svc.WriteOutboxEventPublic(ctx, tx, sub.TenantID, "subscription", sub.ID, "renewal_initiated", map[string]any{
		"tenant_id": sub.TenantID.String(),
		"plan_code": plan.PlanCode,
		"amount":    plan.BasePrice,
		"currency":  plan.Currency,
	})

	if err := tx.Commit(); err != nil {
		log.Error("renewal job: outbox commit failed", zap.String("tenant_id", sub.TenantID.String()), zap.Error(err))
	}

	log.Info("renewal job: payment initiated", zap.String("tenant_id", sub.TenantID.String()), zap.String("plan_code", plan.PlanCode))
}
