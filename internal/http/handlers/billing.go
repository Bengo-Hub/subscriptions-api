package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	authclient "github.com/Bengo-Hub/shared-auth-client"
	serviceclient "github.com/Bengo-Hub/shared-service-client"
	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/tenantsubscription"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// s2sHTTPClient is the HTTP client used for service-to-service calls (e.g. CRM
// upsert to marketflow). It enforces a timeout so calls cannot hang indefinitely.
var s2sHTTPClient = &http.Client{Timeout: 15 * time.Second}

// BillingHandler handles billing-related endpoints.
type BillingHandler struct {
	log              *zap.Logger
	client           *ent.Client
	treasuryClient   *serviceclient.Client
	treasuryAPIKey   string
	marketflowURL    string
	platformTenantID string // issuer tenant for subscription invoices (owns the invoice doc in treasury)
}

// NewBillingHandler creates a new billing handler.
func NewBillingHandler(log *zap.Logger, client *ent.Client, treasuryClient *serviceclient.Client, treasuryAPIKey, marketflowURL, platformTenantID string) *BillingHandler {
	return &BillingHandler{
		log:              log.Named("billing.handler"),
		client:           client,
		treasuryClient:   treasuryClient,
		treasuryAPIKey:   treasuryAPIKey,
		marketflowURL:    marketflowURL,
		platformTenantID: platformTenantID,
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
		billing["planType"] = string(sub.Edges.Plan.PlanType)
		// Resolve billing scenario so the UI can hide renewal/auto-renew for
		// perpetual one-time licences and service-charge tenants.
		isPerpetual := sub.Edges.Plan.BillingCycle == "ONE_TIME"
		billing["isPerpetual"] = isPerpetual
		switch {
		case isPerpetual:
			billing["billingMode"] = "one_time"
			billing["nextRenewalDate"] = nil // perpetual licences never renew
		default:
			billing["billingMode"] = "recurring"
		}
	}

	// Extract payment method from metadata if stored
	if sub.Metadata != nil {
		// Return payment_methods array (preferred) OR fall back to legacy payment_method singular.
		if pms, ok := sub.Metadata["payment_methods"]; ok {
			billing["paymentMethods"] = pms
			// Also expose the default as paymentMethod for backwards compatibility.
			if arr, ok := pms.([]any); ok && len(arr) > 0 {
				billing["paymentMethod"] = arr[0]
			}
		} else if pm, ok := sub.Metadata["payment_method"]; ok {
			billing["paymentMethod"] = pm
			// Normalise legacy single entry into array form for UI.
			billing["paymentMethods"] = []any{pm}
		}
		if cc, ok := sub.Metadata["cancel_at_period_end"].(bool); ok && cc {
			billing["cancelAtPeriodEnd"] = true
		}
	}

	// Invoice history: subscription invoices are owned by the platform tenant (not this
	// tenant), so they won't appear in the tenant's own treasury list — surface the latest
	// one from the subscription's stored markers, then any direct treasury invoices.
	invoices := h.fetchInvoices(ctx, tenantID)
	if row, ok := subscriptionInvoiceRow(sub); ok {
		// Prefer treasury's authoritative invoice (status + amount + currency) over the local
		// sub-status heuristic / stored markers, so a manually-recorded payment is reflected
		// immediately, the row can't show a stale "pending"/false "paid", and the amount always
		// matches the treasury invoice document. Falls back to the local values on error.
		if invID, _ := sub.Metadata["last_invoice_id"].(string); invID != "" {
			if auth, ok := h.fetchSubscriptionInvoice(ctx, invID); ok {
				row.Status = auth.Status
				if auth.Amount > 0 {
					row.Amount = auth.Amount
				}
				if auth.Currency != "" {
					row.Currency = auth.Currency
				}
				row.AmountPaid = auth.AmountPaid
			}
		}
		invoices = append([]invoiceRow{row}, invoices...)
	}
	billing["invoices"] = invoices

	writeJSON(w, http.StatusOK, billing)
}

type invoiceRow struct {
	ID          string   `json:"id"`
	Date        string   `json:"date"`
	Amount      float64  `json:"amount"`
	AmountPaid  *float64 `json:"amountPaid,omitempty"`
	Currency    string   `json:"currency"`
	Status      string   `json:"status"`
	Description string   `json:"description"`
	PdfURL      string   `json:"pdfUrl,omitempty"`
	PayURL      string   `json:"payUrl,omitempty"`
}

// subscriptionInvoiceRow builds an invoice-history row from the latest subscription invoice
// recorded on the subscription metadata by the invoice generation flow.
func subscriptionInvoiceRow(sub *ent.TenantSubscription) (invoiceRow, bool) {
	m := sub.Metadata
	if m == nil {
		return invoiceRow{}, false
	}
	num, _ := m["last_invoice_number"].(string)
	if num == "" {
		return invoiceRow{}, false
	}
	total, _ := m["last_invoice_total"].(float64)
	cur, _ := m["last_invoice_currency"].(string)
	pdf, _ := m["last_invoice_pdf_url"].(string)
	pay, _ := m["last_invoice_pay_url"].(string)
	date, _ := m["last_invoice_period_end"].(string)
	status := "pending"
	if string(sub.Status) == "ACTIVE" {
		if _, inGrace := m["grace_until"]; !inGrace {
			// Active and not in grace → the latest invoice has effectively been settled.
			status = "paid"
		}
	}
	return invoiceRow{
		ID:          num,
		Date:        date,
		Amount:      total,
		Currency:    cur,
		Status:      status,
		Description: "Subscription invoice " + num,
		PdfURL:      pdf,
		PayURL:      pay,
	}, true
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

// authoritativeInvoice is the subset of a treasury invoice the billing page trusts as the
// source of truth for the subscription invoice row.
type authoritativeInvoice struct {
	Status     string
	Amount     float64
	Currency   string
	AmountPaid *float64
}

// fetchSubscriptionInvoice returns the authoritative invoice (status + amount + currency)
// for a platform-owned subscription invoice, fetched by id from treasury S2S. Subscription
// invoices are issued by the platform tenant, so they aren't in the billed tenant's own list —
// this resolves them directly. Returns (_, false) when it can't be determined so callers fall
// back to the local sub-status heuristic / stored markers.
func (h *BillingHandler) fetchSubscriptionInvoice(ctx context.Context, invoiceID string) (authoritativeInvoice, bool) {
	if h.treasuryClient == nil || h.platformTenantID == "" || invoiceID == "" {
		return authoritativeInvoice{}, false
	}
	headers := map[string]string{}
	if h.treasuryAPIKey != "" {
		headers["X-API-Key"] = h.treasuryAPIKey
	}
	resp, err := h.treasuryClient.Get(ctx, fmt.Sprintf("/api/v1/s2s/%s/invoices/%s", h.platformTenantID, invoiceID), headers)
	if err != nil || !resp.IsSuccess() {
		return authoritativeInvoice{}, false
	}
	// total_amount is a decimal serialized as a JSON string (e.g. "2500") or number — json.Number handles both.
	var inv struct {
		Status        string       `json:"status"`
		PaymentStatus string       `json:"payment_status"`
		TotalAmount   json.Number  `json:"total_amount"`
		AmountPaid    *json.Number `json:"amount_paid"`
		Currency      string       `json:"currency"`
	}
	if err := resp.DecodeJSON(&inv); err != nil {
		return authoritativeInvoice{}, false
	}
	amount, _ := inv.TotalAmount.Float64()
	out := authoritativeInvoice{Amount: amount, Currency: inv.Currency}
	if inv.AmountPaid != nil {
		if paid, err := inv.AmountPaid.Float64(); err == nil {
			out.AmountPaid = &paid
		}
	}
	switch {
	case inv.Status == "paid" || inv.PaymentStatus == "paid":
		out.Status = "paid"
	case inv.PaymentStatus == "partial":
		out.Status = "partial"
	case inv.Status == "void" || inv.Status == "cancelled":
		out.Status = "void"
	default:
		out.Status = "pending"
	}
	return out, true
}

// ConfirmPaymentMethod godoc
// @Summary Confirm payment method after card setup
// @Description Called after successful card_setup payment. Reads the authorization_code from the
// treasury intent metadata and persists it to the subscription for auto-renewal.
// @Tags Billing
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]string
// @Router /subscription/payment-method/confirm [post]
func (h *BillingHandler) ConfirmPaymentMethod(w http.ResponseWriter, r *http.Request) {
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

	var body struct {
		IntentID string `json:"intent_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.IntentID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "intent_id required"})
		return
	}

	if h.treasuryClient == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "treasury client not configured"})
		return
	}

	headers := map[string]string{}
	if h.treasuryAPIKey != "" {
		headers["X-API-Key"] = h.treasuryAPIKey
	}

	// Fetch the intent from treasury to extract the auth code saved during verify.
	resp, err := h.treasuryClient.Get(r.Context(),
		fmt.Sprintf("/api/v1/s2s/%s/payments/intents/%s", tenantIDStr, body.IntentID), headers)
	if err != nil || !resp.IsSuccess() {
		h.log.Warn("failed to fetch intent from treasury for card confirm",
			zap.String("tenant_id", tenantIDStr), zap.String("intent_id", body.IntentID))
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "could not fetch intent from treasury"})
		return
	}

	var intentResp struct {
		Metadata map[string]any `json:"metadata"`
	}
	if err := resp.DecodeJSON(&intentResp); err != nil || intentResp.Metadata == nil {
		writeJSON(w, http.StatusOK, map[string]string{"status": "no_card_data"})
		return
	}

	authCode, _ := intentResp.Metadata["paystack_auth_code"].(string)
	if authCode == "" {
		writeJSON(w, http.StatusOK, map[string]string{"status": "no_card_data"})
		return
	}

	cardType, _ := intentResp.Metadata["card_type"].(string)
	last4, _ := intentResp.Metadata["card_last4"].(string)
	expMonth, _ := intentResp.Metadata["card_exp_month"].(string)
	expYear, _ := intentResp.Metadata["card_exp_year"].(string)

	sub, err := h.client.TenantSubscription.Query().
		Where(tenantsubscription.TenantIDEQ(tenantID)).
		Only(r.Context())
	if err != nil {
		h.log.Warn("no subscription found for card confirm", zap.String("tenant_id", tenantIDStr))
		writeJSON(w, http.StatusOK, map[string]string{"status": "no_subscription"})
		return
	}

	meta := make(map[string]any)
	for k, v := range sub.Metadata {
		meta[k] = v
	}
	meta["paystack_auth_code"] = authCode
	meta["payment_method"] = map[string]any{
		"type":        "card",
		"brand":       cardType,
		"last4":       last4,
		"expiryMonth": expMonth,
		"expiryYear":  expYear,
	}

	if _, err := h.client.TenantSubscription.UpdateOneID(sub.ID).SetMetadata(meta).Save(r.Context()); err != nil {
		h.log.Error("failed to save card authorization", zap.String("tenant_id", tenantIDStr), zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save payment method"})
		return
	}

	h.log.Info("payment method confirmed and saved", zap.String("tenant_id", tenantIDStr), zap.String("last4", last4))
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "saved",
		"payment_method": map[string]any{
			"type":        "card",
			"brand":       cardType,
			"last4":       last4,
			"expiryMonth": expMonth,
			"expiryYear":  expYear,
		},
	})
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

	// Upsert CRM contact for this tenant admin so the card-setup intent is linked.
	// Non-fatal: if marketflow is unavailable the intent is still created without a CRM link.
	var crmContactID string
	if h.marketflowURL != "" && customerEmail != "" && !strings.HasSuffix(customerEmail, "@subscriptions.internal") {
		crmContactID = h.upsertCRMContactByEmail(r.Context(), tenantIDStr, customerEmail)
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
		"amount":         5, // KES 5 verification charge (Paystack refund minimum); refunded automatically after card is tokenised
		"source_service": "subscriptions",
		"description":    "Card verification (KES 5 charged and refunded automatically after card is saved)",
		"customer_email": customerEmail,
		"metadata": map[string]any{
			"tenant_id": tenantIDStr,
			"plan_code": planCode,
			"purpose":   "card_setup",
		},
	}
	if crmContactID != "" {
		intentReq["crm_contact_id"] = crmContactID
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

// upsertCRMContactByEmail calls marketflow S2S to create-or-return a contact by email.
// Returns the contact UUID string on success; returns "" on any error (non-fatal).
func (h *BillingHandler) upsertCRMContactByEmail(ctx context.Context, tenantID, email string) string {
	if h.marketflowURL == "" || tenantID == "" || email == "" {
		return ""
	}
	body, _ := json.Marshal(map[string]any{
		"tenant_id": tenantID,
		"email":     email,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		h.marketflowURL+"/api/v1/internal/contacts/upsert",
		bytes.NewReader(body),
	)
	if err != nil {
		h.log.Warn("crm upsert request build failed", zap.Error(err))
		return ""
	}
	req.Header.Set("Content-Type", "application/json")
	if h.treasuryAPIKey != "" {
		req.Header.Set("X-API-Key", h.treasuryAPIKey)
	}
	resp, err := s2sHTTPClient.Do(req)
	if err != nil {
		h.log.Warn("crm upsert call failed", zap.String("email", email), zap.Error(err))
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		h.log.Warn("crm upsert non-200", zap.Int("status", resp.StatusCode))
		return ""
	}
	var result struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		h.log.Warn("crm upsert decode failed", zap.Error(err))
		return ""
	}
	return result.ID
}
