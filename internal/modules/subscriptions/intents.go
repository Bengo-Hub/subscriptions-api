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

// SavedCardCharge is the outcome of an automatic charge to a tenant's saved card.
type SavedCardCharge struct {
	// Status is Treasury's intent status when the call went through ("succeeded", "pending", ...).
	Status string
	// Failure is the reason when the card was declined or Treasury refused the request.
	Failure string
}

// ChargeSavedCard asks Treasury to charge the tenant's saved card for the intent request. Treasury
// intents are idempotent per reference_id, so a caller that derives the reference from the day and
// the purchase can never charge twice for it. A transport error means the outcome is unknown.
func (s *Service) ChargeSavedCard(ctx context.Context, tenantID uuid.UUID, authCode string, req map[string]any) (SavedCardCharge, error) {
	if s.treasuryClient == nil {
		return SavedCardCharge{}, fmt.Errorf("payment service unavailable")
	}
	if authCode == "" {
		return SavedCardCharge{Failure: "no saved card"}, nil
	}
	body := make(map[string]any, len(req)+1)
	for k, v := range req {
		body[k] = v
	}
	body["authorization_code"] = authCode
	resp, err := s.treasuryClient.Post(ctx, fmt.Sprintf("/api/v1/s2s/%s/payments/intents/charge-saved-card", tenantID), body, s.treasuryHeaders())
	if err != nil {
		return SavedCardCharge{}, fmt.Errorf("charge saved card: %w", err)
	}
	var out struct {
		Status string `json:"status"`
		Error  string `json:"error"`
	}
	_ = resp.DecodeJSON(&out)
	if resp.StatusCode >= 500 {
		// Treasury failed on its side: the charge may or may not have happened. Unknown, not declined.
		return SavedCardCharge{}, fmt.Errorf("charge saved card: payment service answered %d", resp.StatusCode)
	}
	if !resp.IsSuccess() {
		reason := out.Error
		if reason == "" {
			reason = fmt.Sprintf("payment service answered %d", resp.StatusCode)
		}
		return SavedCardCharge{Status: "failed", Failure: reason}, nil
	}
	if out.Status == "failed" {
		return SavedCardCharge{Status: "failed", Failure: "the card was declined"}, nil
	}
	return SavedCardCharge{Status: out.Status}, nil
}

func (s *Service) treasuryHeaders() map[string]string {
	headers := map[string]string{}
	if s.treasuryAPIKey != "" {
		headers["X-API-Key"] = s.treasuryAPIKey
	}
	return headers
}
