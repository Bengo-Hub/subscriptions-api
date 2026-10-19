package subscriptions

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// CreatePaymentIntent creates a Treasury payment intent for tenantID and returns Treasury's
// response body. It is the one place subscriptions-api builds the S2S intent call, shared by
// every purchase flow (API token and credit top-ups, bundles, tiers, overage payment).
func (s *Service) CreatePaymentIntent(ctx context.Context, tenantID uuid.UUID, req map[string]any) (map[string]any, error) {
	if s.treasuryClient == nil {
		return nil, fmt.Errorf("payment service unavailable")
	}
	resp, err := s.treasuryClient.Post(ctx, fmt.Sprintf("/api/v1/%s/payments/intents", tenantID), req, s.treasuryHeaders())
	if err != nil || !resp.IsSuccess() {
		return nil, fmt.Errorf("payment initiation failed: %w", err)
	}
	var out map[string]any
	_ = resp.DecodeJSON(&out)
	return out, nil
}

// GetPaymentIntentMetadata reads back the metadata stored on a Treasury payment intent. Purchase
// flows resolve what was bought at checkout time and store it on the intent, because the
// payment.succeeded event does not carry intent metadata.
func (s *Service) GetPaymentIntentMetadata(ctx context.Context, tenantID, intentID uuid.UUID) (map[string]any, error) {
	if s.treasuryClient == nil {
		return nil, fmt.Errorf("payment service unavailable")
	}
	resp, err := s.treasuryClient.Get(ctx, fmt.Sprintf("/api/v1/%s/payments/intents/%s", tenantID, intentID), s.treasuryHeaders())
	if err != nil || !resp.IsSuccess() {
		return nil, fmt.Errorf("fetch payment intent %s: %w", intentID, err)
	}
	var intent struct {
		Metadata map[string]any `json:"metadata"`
	}
	if err := resp.DecodeJSON(&intent); err != nil {
		return nil, fmt.Errorf("decode payment intent: %w", err)
	}
	if intent.Metadata == nil {
		intent.Metadata = map[string]any{}
	}
	return intent.Metadata, nil
}

func (s *Service) treasuryHeaders() map[string]string {
	headers := map[string]string{}
	if s.treasuryAPIKey != "" {
		headers["X-API-Key"] = s.treasuryAPIKey
	}
	return headers
}
