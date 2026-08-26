package subscriptions

import (
	"context"
	"fmt"

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

// defaultTokenPriceKes is used when a tenant has no active plan carrying token_price_kes for
// ServiceTag (e.g. topping up before ever subscribing) — set to the richest published rate
// (ETIMS_API_SCALE's KES 0.40/token) so an unconfigured top-up never OVER-charges relative to
// any real plan; a tenant who later subscribes gets the plan's own rate on subsequent top-ups.
const defaultTokenPriceKes = 0.40

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

	headers := map[string]string{}
	if s.treasuryAPIKey != "" {
		headers["X-API-Key"] = s.treasuryAPIKey
	}

	req := map[string]any{
		"amount":         in.AmountKes,
		"currency":       "KES",
		"payment_method": "pending",
		"reference_id":   fmt.Sprintf("TOKTOP-%s-%s", in.TenantID.String()[:8], in.ServiceTag),
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

	resp, err := s.treasuryClient.Post(ctx, fmt.Sprintf("/api/v1/%s/payments/intents", in.TenantID), req, headers)
	if err != nil || !resp.IsSuccess() {
		return nil, fmt.Errorf("payment initiation failed: %w", err)
	}

	var treasuryResp map[string]any
	_ = resp.DecodeJSON(&treasuryResp)
	return treasuryResp, nil
}

// GetTokenTopUpIntentMetadata reads back a Treasury payment intent's stored metadata to recover
// service_tag/tokens/unit_cost_kes at fulfillment time.
func (s *Service) GetTokenTopUpIntentMetadata(ctx context.Context, tenantID, intentID uuid.UUID) (serviceTag string, tokens int64, unitCostKes float64, err error) {
	if s.treasuryClient == nil {
		return "", 0, 0, fmt.Errorf("payment service unavailable")
	}
	headers := map[string]string{}
	if s.treasuryAPIKey != "" {
		headers["X-API-Key"] = s.treasuryAPIKey
	}

	resp, err := s.treasuryClient.Get(ctx, fmt.Sprintf("/api/v1/%s/payments/intents/%s", tenantID, intentID), headers)
	if err != nil || !resp.IsSuccess() {
		return "", 0, 0, fmt.Errorf("fetch payment intent %s: %w", intentID, err)
	}

	var intent struct {
		Metadata map[string]any `json:"metadata"`
	}
	if err := resp.DecodeJSON(&intent); err != nil {
		return "", 0, 0, fmt.Errorf("decode payment intent: %w", err)
	}

	serviceTag, _ = intent.Metadata["service_tag"].(string)
	switch t := intent.Metadata["tokens"].(type) {
	case float64:
		tokens = int64(t)
	case int64:
		tokens = t
	case int:
		tokens = int64(t)
	}
	switch u := intent.Metadata["unit_cost_kes"].(type) {
	case float64:
		unitCostKes = u
	}
	if serviceTag == "" || tokens <= 0 {
		return "", 0, 0, fmt.Errorf("payment intent %s has no valid service_tag/tokens in metadata", intentID)
	}
	return serviceTag, tokens, unitCostKes, nil
}

// resolveTokenPriceKes looks up the tenant's active plan's token_price_kes for serviceTag
// (mirrors the rate-limit resolver's tenant-subscription-plan lookup in
// internal/http/handlers/rate_limit.go), falling back to defaultTokenPriceKes when the tenant
// has no subscription yet, or their plan doesn't carry this key (a non-token-metered plan).
func (s *Service) resolveTokenPriceKes(ctx context.Context, tenantID uuid.UUID, serviceTag string) float64 {
	sub, err := s.client.TenantSubscription.Query().
		Where(tenantsubscription.TenantIDEQ(tenantID)).
		WithPlan().
		Only(ctx)
	if err != nil || sub.Edges.Plan == nil || sub.Edges.Plan.ServiceTag == nil || *sub.Edges.Plan.ServiceTag != serviceTag {
		return defaultTokenPriceKes
	}
	raw, ok := sub.Edges.Plan.TierLimitsJSON["token_price_kes"]
	if !ok {
		return defaultTokenPriceKes
	}
	if f, ok := raw.(float64); ok && f > 0 {
		return f
	}
	return defaultTokenPriceKes
}
