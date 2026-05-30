package billing

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"

	serviceclient "github.com/Bengo-Hub/shared-service-client"
	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/tenantsubscription"
	"github.com/bengobox/subscription-service/internal/modules/subscriptions"
)

// dunningSchedule lists how many days after suspension to attempt retry.
var dunningSchedule = []int{1, 3, 7}

// StartDunningJob runs a daily background job that retries payment for SUSPENDED subscriptions.
// After the last retry day (day 7), the subscription is cancelled.
func StartDunningJob(ctx context.Context, log *zap.Logger, orm *ent.Client, svc *subscriptions.Service, treasuryClient *serviceclient.Client, treasuryAPIKey string) {
	log = log.Named("dunning.job")

	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()

	runDunning(ctx, log, orm, svc, treasuryClient, treasuryAPIKey)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runDunning(ctx, log, orm, svc, treasuryClient, treasuryAPIKey)
		}
	}
}

func runDunning(ctx context.Context, log *zap.Logger, orm *ent.Client, svc *subscriptions.Service, treasuryClient *serviceclient.Client, treasuryAPIKey string) {
	// Find subscriptions that have been SUSPENDED for at least 1 day
	cutoff := time.Now().UTC().Add(-24 * time.Hour)

	suspended, err := orm.TenantSubscription.Query().
		Where(
			tenantsubscription.StatusEQ(tenantsubscription.StatusSUSPENDED),
			tenantsubscription.UpdatedAtLTE(cutoff),
		).
		WithPlan().
		All(ctx)
	if err != nil {
		log.Error("dunning job: failed to query suspended subscriptions", zap.Error(err))
		return
	}

	for _, sub := range suspended {
		processDunning(ctx, log, orm, svc, sub, treasuryClient, treasuryAPIKey)
	}

	if len(suspended) > 0 {
		log.Info("dunning job: processed suspended subscriptions", zap.Int("count", len(suspended)))
	}
}

func processDunning(ctx context.Context, log *zap.Logger, orm *ent.Client, svc *subscriptions.Service, sub *ent.TenantSubscription, treasuryClient *serviceclient.Client, treasuryAPIKey string) {
	daysSuspended := int(time.Since(sub.UpdatedAt).Hours() / 24)

	// Determine current attempt from metadata
	meta := make(map[string]any)
	for k, v := range sub.Metadata {
		meta[k] = v
	}
	attemptRaw, _ := meta["dunning_attempt"].(float64)
	attempt := int(attemptRaw)

	// Check if today is a retry day
	shouldRetry := false
	for _, d := range dunningSchedule {
		if daysSuspended >= d && attempt < d {
			shouldRetry = true
			break
		}
	}

	// Cancel after last retry day (7 days) with no success
	maxDay := dunningSchedule[len(dunningSchedule)-1]
	if daysSuspended > maxDay && attempt >= maxDay {
		if _, err := svc.CancelSubscription(ctx, subscriptions.CancelInput{
			TenantID: sub.TenantID,
			Reason:   "dunning_exhausted",
		}); err != nil {
			log.Error("dunning job: failed to cancel subscription", zap.String("tenant_id", sub.TenantID.String()), zap.Error(err))
		} else {
			log.Info("dunning job: subscription cancelled after exhausted retries", zap.String("tenant_id", sub.TenantID.String()))
		}
		return
	}

	if !shouldRetry {
		return
	}

	// Increment attempt counter
	meta["dunning_attempt"] = attempt + 1
	_, _ = orm.TenantSubscription.UpdateOneID(sub.ID).
		SetMetadata(meta).
		Save(ctx)

	// Attempt charge if auth code is available
	authCode, _ := meta["paystack_auth_code"].(string)
	if authCode == "" || treasuryClient == nil || sub.Edges.Plan == nil {
		log.Info("dunning job: no auth code or treasury client, skipping charge attempt",
			zap.String("tenant_id", sub.TenantID.String()),
			zap.Int("attempt", attempt+1),
		)
		return
	}

	if err := initiateAutoCharge(ctx, log, sub, authCode, treasuryClient, treasuryAPIKey); err != nil {
		log.Warn("dunning job: auto-charge attempt failed",
			zap.String("tenant_id", sub.TenantID.String()),
			zap.Int("attempt", attempt+1),
			zap.Error(err),
		)
	} else {
		log.Info("dunning job: auto-charge initiated",
			zap.String("tenant_id", sub.TenantID.String()),
			zap.Int("attempt", attempt+1),
		)
	}
}

func initiateAutoCharge(ctx context.Context, log *zap.Logger, sub *ent.TenantSubscription, authCode string, treasuryClient *serviceclient.Client, treasuryAPIKey string) error {
	plan := sub.Edges.Plan
	if plan == nil {
		return fmt.Errorf("plan not loaded")
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
		"description":    fmt.Sprintf("Subscription renewal (dunning) — %s", plan.Name),
		"metadata": map[string]any{
			"tenant_id":          sub.TenantID.String(),
			"plan_code":          plan.PlanCode,
			"dunning":            true,
			"paystack_auth_code": authCode,
		},
	}

	resp, err := treasuryClient.Post(ctx, "/api/v1/payments/intents", req, headers)
	if err != nil || !resp.IsSuccess() {
		if err == nil {
			return fmt.Errorf("treasury charge returned non-success")
		}
		return err
	}

	log.Info("dunning job: charge request sent",
		zap.String("tenant_id", sub.TenantID.String()),
		zap.String("plan_code", plan.PlanCode),
	)
	return nil
}
