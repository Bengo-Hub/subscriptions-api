package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	authclient "github.com/Bengo-Hub/shared-auth-client"
	serviceclient "github.com/Bengo-Hub/shared-service-client"
	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/tenantsubscription"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// BillingHandler handles billing-related endpoints.
type BillingHandler struct {
	log            *zap.Logger
	client         *ent.Client
	treasuryClient *serviceclient.Client
	treasuryAPIKey string
}

// NewBillingHandler creates a new billing handler.
func NewBillingHandler(log *zap.Logger, client *ent.Client, treasuryClient *serviceclient.Client, treasuryAPIKey string) *BillingHandler {
	return &BillingHandler{
		log:            log.Named("billing.handler"),
		client:         client,
		treasuryClient: treasuryClient,
		treasuryAPIKey: treasuryAPIKey,
	}
}

// GetBilling godoc
// @Summary Get billing information
// @Description Returns billing details and payment history for the tenant's subscription
// @Tags Billing
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{}
// @Router /billing [get]
func (h *BillingHandler) GetBilling(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tenantIDStr := resolveTenantID(r)
	if tenantIDStr == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "tenant_id required"})
		return
	}
	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid tenant_id"})
		return
	}

	sub, err := h.client.TenantSubscription.Query().
		Where(tenantsubscription.TenantIDEQ(tenantID)).
		WithPlan().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			writeJSON(w, http.StatusOK, map[string]any{
				"hasSubscription": false,
				"invoices":        []any{},
			})
			return
		}
		h.log.Error("failed to get subscription for billing", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	billing := map[string]any{
		"hasSubscription":    true,
		"subscriptionId":     sub.ID.String(),
		"status":             string(sub.Status),
		"billingCycle":       sub.BillingCycle,
		"currentPeriodStart": sub.CurrentPeriodStart.Format("2006-01-02T15:04:05Z"),
		"currentPeriodEnd":   sub.CurrentPeriodEnd.Format("2006-01-02T15:04:05Z"),
		"nextRenewalDate":    sub.CurrentPeriodEnd.Format("2006-01-02T15:04:05Z"),
	}

	if sub.Edges.Plan != nil {
		billing["planCode"] = sub.Edges.Plan.PlanCode
		billing["planName"] = sub.Edges.Plan.Name
		billing["amount"] = sub.Edges.Plan.BasePrice
		billing["nextAmount"] = sub.Edges.Plan.BasePrice // alias for UI compatibility
		billing["currency"] = sub.Edges.Plan.Currency
	}

	// Extract payment method from metadata if stored
	if sub.Metadata != nil {
		if pm, ok := sub.Metadata["payment_method"]; ok {
			billing["paymentMethod"] = pm
		}
	}

	// Fetch invoices from Treasury-API (non-fatal on failure)
	billing["invoices"] = h.fetchInvoices(ctx, tenantID)

	writeJSON(w, http.StatusOK, billing)
}

type invoiceRow struct {
	ID          string  `json:"id"`
	Date        string  `json:"date"`
	Amount      float64 `json:"amount"`
	Currency    string  `json:"currency"`
	Status      string  `json:"status"`
	Description string  `json:"description"`
	PdfURL      string  `json:"pdfUrl,omitempty"`
}

func (h *BillingHandler) fetchInvoices(ctx context.Context, tenantID uuid.UUID) []invoiceRow {
	if h.treasuryClient == nil {
		return []invoiceRow{}
	}

	headers := map[string]string{}
	if h.treasuryAPIKey != "" {
		headers["X-API-Key"] = h.treasuryAPIKey
	}

	resp, err := h.treasuryClient.Get(ctx, fmt.Sprintf("/api/v1/tenants/%s/invoices", tenantID), headers)
	if err != nil {
		h.log.Warn("failed to fetch invoices from treasury", zap.String("tenant_id", tenantID.String()), zap.Error(err))
		return []invoiceRow{}
	}
	if !resp.IsSuccess() {
		h.log.Warn("treasury invoices returned non-2xx", zap.Int("status", resp.StatusCode), zap.String("tenant_id", tenantID.String()))
		return []invoiceRow{}
	}

	var raw []struct {
		ID          string  `json:"id"`
		CreatedAt   string  `json:"created_at"`
		Amount      float64 `json:"amount"`
		Currency    string  `json:"currency"`
		Status      string  `json:"status"`
		Description string  `json:"description"`
		PdfURL      string  `json:"pdf_url"`
	}
	if err := resp.DecodeJSON(&raw); err != nil {
		h.log.Warn("failed to decode treasury invoices", zap.Error(err))
		return []invoiceRow{}
	}

	rows := make([]invoiceRow, 0, len(raw))
	for _, inv := range raw {
		date := inv.CreatedAt
		if t, err := time.Parse(time.RFC3339, inv.CreatedAt); err == nil {
			date = t.Format("2006-01-02T15:04:05Z")
		}
		rows = append(rows, invoiceRow{
			ID:          inv.ID,
			Date:        date,
			Amount:      inv.Amount,
			Currency:    inv.Currency,
			Status:      inv.Status,
			Description: inv.Description,
			PdfURL:      inv.PdfURL,
		})
	}
	return rows
}

// SetupPaymentMethod godoc
// @Summary Setup payment method for auto-renewal
// @Description Creates a Paystack payment intent so the tenant can register a card for subscription auto-renewal
// @Tags Billing
// @Produce json
// @Security BearerAuth
// @Success 201 {object} map[string]interface{}
// @Failure 400 {object} map[string]string
// @Failure 503 {object} map[string]string
// @Router /subscription/payment-method/setup [post]
func (h *BillingHandler) SetupPaymentMethod(w http.ResponseWriter, r *http.Request) {
	if h.treasuryClient == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "treasury client not configured"})
		return
	}

	tenantIDStr := resolveTenantID(r)
	if tenantIDStr == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "tenant_id required"})
		return
	}
	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid tenant_id"})
		return
	}

	// Read optional billing_email from request body (takes highest priority).
	var body struct {
		BillingEmail string `json:"billing_email"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	// Look up subscription to get billing details
	sub, err := h.client.TenantSubscription.Query().
		Where(tenantsubscription.TenantIDEQ(tenantID)).
		WithPlan().
		Only(r.Context())
	if err != nil && !ent.IsNotFound(err) {
		h.log.Error("failed to fetch subscription for card setup", zap.String("tenant_id", tenantIDStr), zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	currency := "KES"
	planCode := "unknown"

	// Email resolution priority:
	// 1. billing_email from request body (explicitly submitted by frontend)
	// 2. billing_email stored in subscription metadata
	// 3. JWT claims email
	customerEmail := body.BillingEmail

	if sub != nil {
		if sub.Edges.Plan != nil {
			currency = sub.Edges.Plan.Currency
			planCode = sub.Edges.Plan.PlanCode
		}
		if customerEmail == "" && sub.Metadata != nil {
			if be, ok := sub.Metadata["billing_email"].(string); ok && be != "" {
				customerEmail = be
			}
		}
	}
	if customerEmail == "" {
		if claims, ok := authclient.ClaimsFromContext(r.Context()); ok && claims.Email != "" {
			customerEmail = claims.Email
		}
	}
	if customerEmail == "" {
		customerEmail = tenantIDStr + "@subscriptions.internal"
	}

	headers := map[string]string{}
	if h.treasuryAPIKey != "" {
		headers["X-API-Key"] = h.treasuryAPIKey
	}

	// Create a pending card-tokenisation intent via treasury S2S.
	// payment_method="pending" so treasury returns an initiate_url in treasury format
	// ({publicBase}/api/v1/pay/{tenantID}/intents/{intentID}/initiate). TreasuryPaymentModal
	// loads treasury-ui with this URL; treasury-ui then initiates Paystack and handles the
	// checkout UI. Treasury charges 1 KES to verify the card; it is automatically refunded
	// after authorization_code is captured — the same pattern used by Stripe, Anthropic, Cursor.
	intentReq := map[string]any{
		"reference_id":   fmt.Sprintf("card-setup-%s-%d", tenantIDStr[:8], time.Now().UnixMilli()),
		"reference_type": "card_setup",
		"payment_method": "pending",
		"currency":       currency,
		"amount":         1, // 1 KES verification charge; refunded automatically after authorization_code is captured
		"source_service": "subscriptions",
		"description":    "Card verification (KES 1 charged and refunded automatically)",
		"customer_email": customerEmail,
		"metadata": map[string]any{
			"tenant_id": tenantIDStr,
			"plan_code": planCode,
			"purpose":   "card_setup",
		},
	}

	resp, err := h.treasuryClient.Post(r.Context(), fmt.Sprintf("/api/v1/s2s/%s/payments/intents", tenantIDStr), intentReq, headers)
	if err != nil || !resp.IsSuccess() {
		h.log.Error("failed to create card setup intent", zap.String("tenant_id", tenantIDStr), zap.Error(err))
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "failed to create payment intent"})
		return
	}

	var result struct {
		IntentID         string  `json:"intent_id"`
		InitiateURL      string  `json:"initiate_url"`
		AuthorizationURL *string `json:"authorization_url"`
		Status           string  `json:"status"`
	}
	if err := json.Unmarshal(resp.Body, &result); err != nil {
		h.log.Error("failed to decode treasury response", zap.String("tenant_id", tenantIDStr), zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "invalid treasury response"})
		return
	}

	// Card intents return authorization_url (Paystack checkout); pending intents return initiate_url.
	// Always expose a single initiate_url to the frontend.
	initiateURL := result.InitiateURL
	if initiateURL == "" && result.AuthorizationURL != nil && *result.AuthorizationURL != "" {
		initiateURL = *result.AuthorizationURL
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"payment_intent_id": result.IntentID,
		"initiate_url":      initiateURL,
		"status":            result.Status,
	})
}
