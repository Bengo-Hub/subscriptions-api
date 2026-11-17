package subscriptions

import (
	"context"
	"fmt"

	"github.com/bengobox/subscription-service/internal/ent/subscriptionplan"
	"github.com/bengobox/subscription-service/internal/ent/tenantsubscription"
	"github.com/google/uuid"
)

// TokenTopUpInput carries the tenant/service/amount for a self-serve API-token top-up. Amount is
// KES, converted to tokens at CreateTokenTopUpIntent time using the tenant's current plan's
// token_price_kes (falling back to a platform-default rate for a tenant with no matching plan
// yet, e.g. a brand-new integrator who hasn't subscribed at all) — the resolved token count is
// then stored on the intent's own metadata (same read-back pattern as
// GetEmailLicensePurchaseIntentMetadata) so fulfillment never has to re-derive pricing.
type TokenTopUpInput struct {
	TenantID   uuid.UUID
	ServiceTag string
	AmountKes  float64
	ReturnURL  string
}

// fallbackTokenPriceKes is the last-resort rate, used only when no active public plan for the
// service tag carries token_price_kes at all (a misconfigured catalog). Normal pricing always
// comes from the plan rows, which platform admins edit without a deploy.
const fallbackTokenPriceKes = 0.60

// CreateTokenTopUpIntent creates a Treasury payment intent for a self-serve token purchase.
// Mirrors CreateEmailLicensePurchaseIntent's pattern: tokens are only credited once
// TopUpTokens runs on confirmed payment (see the api_token_topup branch in
// consumers/payment_consumer.go), so a purchase that's never actually paid for never grants
// tokens.
func (s *Service) CreateTokenTopUpIntent(ctx context.Context, in TokenTopUpInput) (map[string]any, error) {
	if s.treasuryClient == nil {
		return nil, fmt.Errorf("payment service unavailable")
	}
	if in.AmountKes <= 0 {
		return nil, fmt.Errorf("amount_kes must be positive")
	}

	tokenPriceKes := s.resolveTokenPriceKes(ctx, in.TenantID, in.ServiceTag)
	tokens := int64(in.AmountKes / tokenPriceKes)
	if tokens <= 0 {
		return nil, fmt.Errorf("amount_kes too small to purchase any tokens at KES %.2f/token", tokenPriceKes)
	}

	req := map[string]any{
		"amount":         in.AmountKes,
		"currency":       "KES",
		"payment_method": "pending",
		"reference_id":   NewPurchaseReference("TOKTOP", in.TenantID, in.ServiceTag),
		"reference_type": "api_token_topup",
		"source_service": "subscriptions",
		"description":    fmt.Sprintf("API token top-up: %d tokens (%s)", tokens, in.ServiceTag),
		"callback_url":   in.ReturnURL,
		// Read back at fulfillment time via GetIntent — the token count and unit price are
		// resolved HERE (checkout time), not re-derived from the payment webhook/NATS event,
		// which carries neither reliably.
		"metadata": map[string]any{
			"tenant_id":     in.TenantID.String(),
			"service_tag":   in.ServiceTag,
			"tokens":        tokens,
			"unit_cost_kes": tokenPriceKes,
		},
	}

	return s.CreatePaymentIntent(ctx, in.TenantID, req)
}

// GetTokenTopUpIntentMetadata reads back a Treasury payment intent's stored metadata to recover
// service_tag/tokens/unit_cost_kes at fulfillment time.
func (s *Service) GetTokenTopUpIntentMetadata(ctx context.Context, tenantID, intentID uuid.UUID) (serviceTag string, tokens int64, unitCostKes float64, err error) {
	paid, err := s.GetSettledPurchase(ctx, tenantID, intentID, "api_token_topup")
	if err != nil {
		return "", 0, 0, err
	}
	meta := paid.Metadata

	serviceTag, _ = meta["service_tag"].(string)
	switch t := meta["tokens"].(type) {
	case float64:
		tokens = int64(t)
	case int64:
		tokens = t
	case int:
		tokens = int64(t)
	}
	switch u := meta["unit_cost_kes"].(type) {
	case float64:
		unitCostKes = u
	}
	if serviceTag == "" || tokens <= 0 {
		return "", 0, 0, fmt.Errorf("payment intent %s has no valid service_tag/tokens in metadata", intentID)
	}
	return serviceTag, tokens, unitCostKes, nil
}

// resolveTokenPriceKes returns the KES-per-token rate for a tenant's top-up on serviceTag.
//
// Order of resolution:
//  1. The tenant's main plan, when its service_tag matches (a standalone API subscriber).
//  2. An active product add-on whose override plan matches (for example ETIMS_API_BUNDLED
//     attached to a POS or PowerSuite tenant through AssignProductPlan).
//  3. No matching plan (only an exempt tenant gets this far, since the top-up handler gates on
//     HasActiveServiceTagSubscription): the highest token_price_kes among the active public
//     plans for the tag, so topping up without a plan is never cheaper than subscribing.
func (s *Service) resolveTokenPriceKes(ctx context.Context, tenantID uuid.UUID, serviceTag string) float64 {
	sub, err := s.client.TenantSubscription.Query().
		Where(tenantsubscription.TenantIDEQ(tenantID)).
		WithPlan().
		Only(ctx)
	if err == nil {
		if p := sub.Edges.Plan; p != nil && p.ServiceTag != nil && *p.ServiceTag == serviceTag {
			if f, ok := tokenPriceFromLimits(p.TierLimitsJSON); ok {
				return f
			}
		}
		if overlays, perr := s.resolveProductPlans(ctx, sub.ID); perr == nil {
			for _, p := range overlays {
				if p.ServiceTag != nil && *p.ServiceTag == serviceTag {
					if f, ok := tokenPriceFromLimits(p.TierLimitsJSON); ok {
						return f
					}
				}
			}
		}
	}
	return s.noPlanTokenPriceKes(ctx, serviceTag)
}

// noPlanTokenPriceKes is the highest published token_price_kes for serviceTag across active
// public plans, read from the catalog so a price change needs only a plan edit.
func (s *Service) noPlanTokenPriceKes(ctx context.Context, serviceTag string) float64 {
	plans, err := s.client.SubscriptionPlan.Query().
		Where(
			subscriptionplan.ServiceTagEQ(serviceTag),
			subscriptionplan.IsActiveEQ(true),
			subscriptionplan.IsPublicEQ(true),
		).
		All(ctx)
	if err != nil {
		return fallbackTokenPriceKes
	}
	highest := 0.0
	for _, p := range plans {
		if f, ok := tokenPriceFromLimits(p.TierLimitsJSON); ok && f > highest {
			highest = f
		}
	}
	if highest <= 0 {
		return fallbackTokenPriceKes
	}
	return highest
}

func tokenPriceFromLimits(limits map[string]any) (float64, bool) {
	switch v := limits["token_price_kes"].(type) {
	case float64:
		return v, v > 0
	case int:
		return float64(v), v > 0
	case int64:
		return float64(v), v > 0
	}
	return 0, false
}
