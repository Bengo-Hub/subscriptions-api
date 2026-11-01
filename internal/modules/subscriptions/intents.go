package subscriptions

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	serviceclient "github.com/Bengo-Hub/shared-service-client"
	"github.com/google/uuid"
)

// TreasuryIntentsPath is Treasury's S2S route for a tenant's payment intents. Every intent this
// service creates is a platform-billed payment (plan, credits, licences, tokens), and Treasury
// refuses those on the tenant-user route /api/v1/{tenant}/payments/intents with a 403, so a
// tenant user can never create or settle a platform bill themselves.
func TreasuryIntentsPath(tenantID uuid.UUID) string {
	return fmt.Sprintf("/api/v1/s2s/%s/payments/intents", tenantID)
}

// NewPurchaseReference builds the reference_id of a one-off purchase intent. Treasury returns the
// existing intent for a repeated reference, so a reference without a unique part would hand a
// second purchase the first one's intent (already paid, or at the first purchase's amount) and
// grant nothing. Recurring charges that must collapse to one intent per period build their own.
func NewPurchaseReference(prefix string, tenantID uuid.UUID, what string) string {
	return fmt.Sprintf("%s-%s-%s-%s-%s", prefix, tenantID.String()[:8], what,
		time.Now().UTC().Format("060102150405"), uuid.NewString()[:8])
}

// TreasuryFailure turns a failed Treasury call into an error that says why: the transport error,
// or Treasury's status and its "error" message. A failed status carries no Go error, so wrapping
// err alone printed "%!w(<nil>)" and hid the real reason.
func TreasuryFailure(action string, resp *serviceclient.Response, err error) error {
	if err != nil {
		return fmt.Errorf("%s: %w", action, err)
	}
	if resp == nil {
		return fmt.Errorf("%s: no answer from the payment service", action)
	}
	var body struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	_ = resp.DecodeJSON(&body)
	reason := strings.TrimSpace(body.Error)
	if reason == "" {
		reason = strings.TrimSpace(body.Message)
	}
	if reason == "" {
		return fmt.Errorf("%s: payment service answered %d", action, resp.StatusCode)
	}
	return fmt.Errorf("%s: payment service answered %d: %s", action, resp.StatusCode, reason)
}

// CreatePaymentIntent creates a Treasury payment intent for tenantID and returns Treasury's
// response body. It is the one place subscriptions-api builds the S2S intent call, shared by
// every purchase flow (API token and credit top-ups, bundles, tiers, email licences).
func (s *Service) CreatePaymentIntent(ctx context.Context, tenantID uuid.UUID, req map[string]any) (map[string]any, error) {
	if s.treasuryClient == nil {
		return nil, fmt.Errorf("payment service unavailable")
	}
	resp, err := s.treasuryClient.Post(ctx, TreasuryIntentsPath(tenantID), req, s.treasuryHeaders())
	if err != nil || !resp.IsSuccess() {
		return nil, TreasuryFailure("payment initiation failed", resp, err)
	}
	var out map[string]any
	if err := resp.DecodeJSON(&out); err != nil {
		return nil, fmt.Errorf("decode payment intent: %w", err)
	}
	return out, nil
}

// SettledPurchase is a Treasury payment intent that paid for a platform purchase.
type SettledPurchase struct {
	Amount   float64
	Metadata map[string]any
}

// GetSettledPurchase reads back a Treasury payment intent for fulfilment and refuses one that is
// not a settled payment of refType. Purchase flows resolve what was bought at checkout time and
// store it on the intent, because the payment.succeeded event does not carry intent metadata.
// Checking the type and status here means an intent of another kind (a tenant's own sale) or one
// that never settled can never be fulfilled as a platform purchase.
func (s *Service) GetSettledPurchase(ctx context.Context, tenantID, intentID uuid.UUID, refType string) (SettledPurchase, error) {
	if s.treasuryClient == nil {
		return SettledPurchase{}, fmt.Errorf("payment service unavailable")
	}
	resp, err := s.treasuryClient.Get(ctx, TreasuryIntentsPath(tenantID)+"/"+intentID.String(), s.treasuryHeaders())
	if err != nil || !resp.IsSuccess() {
		return SettledPurchase{}, TreasuryFailure(fmt.Sprintf("fetch payment intent %s", intentID), resp, err)
	}
	var intent struct {
		ReferenceType string         `json:"reference_type"`
		Status        string         `json:"status"`
		Amount        json.Number    `json:"amount"`
		Metadata      map[string]any `json:"metadata"`
	}
	if err := resp.DecodeJSON(&intent); err != nil {
		return SettledPurchase{}, fmt.Errorf("decode payment intent: %w", err)
	}
	if intent.ReferenceType != refType {
		return SettledPurchase{}, fmt.Errorf("payment intent %s is a %q payment, not %q", intentID, intent.ReferenceType, refType)
	}
	if intent.Status != "succeeded" {
		return SettledPurchase{}, fmt.Errorf("payment intent %s is %q, not paid", intentID, intent.Status)
	}
	// Treasury sends the amount as a decimal string ("500"), which json.Number accepts.
	amount, err := strconv.ParseFloat(intent.Amount.String(), 64)
	if err != nil {
		return SettledPurchase{}, fmt.Errorf("payment intent %s amount %q: %w", intentID, intent.Amount, err)
	}
	if intent.Metadata == nil {
		intent.Metadata = map[string]any{}
	}
	return SettledPurchase{Amount: amount, Metadata: intent.Metadata}, nil
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
	resp, err := s.treasuryClient.Post(ctx, TreasuryIntentsPath(tenantID)+"/charge-saved-card", body, s.treasuryHeaders())
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
