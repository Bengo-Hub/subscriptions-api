package handlers

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	authclient "github.com/Bengo-Hub/shared-auth-client"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/tenantsubscription"
	"github.com/bengobox/subscription-service/internal/modules/subscriptions"
)

// ratibaFrequencyForCycle maps a billing cycle to the treasury standing-order frequency.
var ratibaFrequencyForCycle = map[string]string{
	"MONTHLY": "monthly", "QUARTERLY": "quarterly", "SEMI_ANNUAL": "semi_annual", "ANNUAL": "annual",
}

// GetStandingOrder returns the tenant's M-Pesa standing order (from treasury), or null.
//
// @Summary Get the M-Pesa standing order for the subscription
// @Tags Billing
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]any
// @Router /subscription/standing-order [get]
func (h *BillingHandler) GetStandingOrder(w http.ResponseWriter, r *http.Request) {
	tenantID, err := uuid.Parse(resolveTenantID(r))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "tenant_id required"})
		return
	}
	if h.treasuryClient == nil {
		writeJSON(w, http.StatusOK, map[string]any{"standing_order": nil})
		return
	}
	resp, err := h.treasuryClient.Get(r.Context(), fmt.Sprintf("/api/v1/s2s/%s/payments/standing-order", tenantID), h.s2sHeaders())
	if err != nil || !resp.IsSuccess() {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "could not read standing order"})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(resp.Body)
}

// RegisterStandingOrder sets up an M-Pesa Ratiba standing order that pays the subscription from
// the given phone every billing period, starting at the next renewal. Amount and frequency come
// from the tenant's plan and billing cycle; the customer approves the order on their phone.
//
// @Summary Pay the subscription automatically by M-Pesa standing order
// @Tags Billing
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 201 {object} map[string]any
// @Router /subscription/standing-order [post]
func (h *BillingHandler) RegisterStandingOrder(w http.ResponseWriter, r *http.Request) {
	tenantID, err := uuid.Parse(resolveTenantID(r))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "tenant_id required"})
		return
	}
	var req struct {
		Phone string `json:"phone"`
		// Authorised is the customer's explicit tick on the authorisation text (payee, amount,
		// frequency, until cancelled). Required.
		Authorised bool `json:"authorised"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Phone) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "phone is required"})
		return
	}
	if h.treasuryClient == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "payments are not configured"})
		return
	}
	sub, err := h.client.TenantSubscription.Query().
		Where(tenantsubscription.TenantIDEQ(tenantID)).WithPlan().WithTenant().Only(r.Context())
	if err != nil || sub.Edges.Plan == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "subscription not found"})
		return
	}
	terms, termsErr := standingOrderTermsFor(sub)
	if termsErr != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": termsErr})
		return
	}
	// The customer must tick the authorisation that states payee, amount and frequency. Without
	// it there is no consent to a recurring debit, so nothing is registered.
	if !req.Authorised {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "please confirm you authorise the recurring M-Pesa payment"})
		return
	}
	name := ""
	if sub.Edges.Tenant != nil {
		name = sub.Edges.Tenant.Name
	}
	body := map[string]any{
		"phone":           req.Phone,
		"amount":          fmt.Sprintf("%.2f", terms.Amount),
		"frequency":       terms.Frequency,
		"start_date":      terms.StartDate,
		"subscription_id": sub.ID.String(),
		"tenant_name":     name,
	}
	freq := terms.Frequency
	resp, err := h.treasuryClient.Post(r.Context(), fmt.Sprintf("/api/v1/s2s/%s/payments/standing-order", tenantID), body, h.s2sHeaders())
	if err != nil {
		h.log.Error("standing order registration failed", zap.String("tenant_id", tenantID.String()), zap.Error(err))
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "could not register the standing order"})
		return
	}
	if !resp.IsSuccess() {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(resp.Body, &e)
		writeJSON(w, resp.StatusCode, map[string]string{"error": e.Error})
		return
	}
	meta := sub.Metadata
	if meta == nil {
		meta = map[string]any{}
	}
	// Its own key: payment_method holds a saved payment method object (card or mobile money).
	// Writing the string "mpesa_standing_order" there broke the billing page for that tenant
	// (the UI read it as a payment method card and crashed on its missing phone).
	// The authorisation record: what was agreed, when, and from where (proof of consent).
	now := time.Now().UTC().Format(time.RFC3339)
	meta["standing_order"] = map[string]any{
		"phone":              req.Phone,
		"frequency":          freq,
		"amount":             terms.Amount,
		"start_date":         terms.StartDate,
		"status":             "pending_approval",
		"registered_at":      now,
		"authorised_at":      now,
		"authorisation_text": standingOrderAuthorisationText(terms),
		"authorised_ip":      requestIP(r),
		"authorised_by":      requestUserID(r),
	}
	if _, err := h.client.TenantSubscription.UpdateOneID(sub.ID).SetMetadata(meta).Save(r.Context()); err != nil {
		h.log.Warn("standing order: metadata not saved", zap.Error(err))
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_, _ = w.Write(resp.Body)
}

// standingOrderTerms is what a standing order would debit, shown to the customer before they
// authorise it and sent to treasury when they do.
type standingOrderTerms struct {
	Payee     string  `json:"payee"`
	Amount    float64 `json:"amount"`
	Currency  string  `json:"currency"`
	Frequency string  `json:"frequency"`
	StartDate string  `json:"start_date"`
	PlanName  string  `json:"plan_name"`
}

// standingOrderPayee is the registered business the customer authorises to debit them.
const standingOrderPayee = "Codevertex Africa Limited"

// standingOrderTermsFor derives the terms from the subscription; the string is a customer-facing
// reason when a standing order is not possible.
func standingOrderTermsFor(sub *ent.TenantSubscription) (standingOrderTerms, string) {
	if sub == nil || sub.Edges.Plan == nil {
		return standingOrderTerms{}, "subscription not found"
	}
	cycle := string(sub.BillingCycle)
	freq, ok := ratibaFrequencyForCycle[cycle]
	if !ok {
		return standingOrderTerms{}, "this billing cycle cannot be paid by standing order"
	}
	amount := subscriptions.EffectivePrice(sub, sub.Edges.Plan) * float64(subscriptions.BillingCycleMonths(cycle))
	if amount <= 0 {
		return standingOrderTerms{}, "nothing to pay on this plan"
	}
	currency := sub.Edges.Plan.Currency
	if currency == "" {
		currency = "KES"
	}
	return standingOrderTerms{
		Payee:     standingOrderPayee,
		Amount:    amount,
		Currency:  currency,
		Frequency: freq,
		StartDate: sub.CurrentPeriodEnd.UTC().Format("2006-01-02"),
		PlanName:  sub.Edges.Plan.Name,
	}, ""
}

var frequencyWords = map[string]string{
	"monthly": "every month", "quarterly": "every three months", "semi_annual": "every six months", "annual": "every year",
}

// standingOrderAuthorisationText is the exact sentence the customer agrees to. The UI shows the
// same wording (built from the quote), and it is stored with the authorisation as evidence.
func standingOrderAuthorisationText(t standingOrderTerms) string {
	words := frequencyWords[t.Frequency]
	if words == "" {
		words = t.Frequency
	}
	return fmt.Sprintf("I authorise %s to debit %s %.0f %s from my M-Pesa number for the %s subscription, starting %s, until I cancel.",
		t.Payee, t.Currency, t.Amount, words, t.PlanName, t.StartDate)
}

// GetStandingOrderQuote returns what a standing order would debit, before the customer agrees.
//
// @Summary Standing order terms (amount, frequency, start date, payee) before authorising
// @Tags Billing
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]any
// @Router /subscription/standing-order/quote [get]
func (h *BillingHandler) GetStandingOrderQuote(w http.ResponseWriter, r *http.Request) {
	tenantID, err := uuid.Parse(resolveTenantID(r))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "tenant_id required"})
		return
	}
	sub, err := h.client.TenantSubscription.Query().
		Where(tenantsubscription.TenantIDEQ(tenantID)).WithPlan().Only(r.Context())
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "subscription not found"})
		return
	}
	terms, reason := standingOrderTermsFor(sub)
	if reason != "" {
		writeJSON(w, http.StatusOK, map[string]any{"available": false, "reason": reason})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"available":          true,
		"terms":              terms,
		"authorisation_text": standingOrderAuthorisationText(terms),
	})
}

// CancelStandingOrder stops the tenant's M-Pesa standing order being used for the subscription.
// Daraja cannot cancel a Ratiba order for us, so the response tells the customer to end it in
// M-Pesa as well; later renewals come as invoices to pay.
//
// @Summary Cancel the M-Pesa standing order
// @Tags Billing
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]any
// @Router /subscription/standing-order [delete]
func (h *BillingHandler) CancelStandingOrder(w http.ResponseWriter, r *http.Request) {
	tenantID, err := uuid.Parse(resolveTenantID(r))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "tenant_id required"})
		return
	}
	if h.treasuryClient == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "payments are not configured"})
		return
	}
	resp, err := h.treasuryClient.Delete(r.Context(), fmt.Sprintf("/api/v1/s2s/%s/payments/standing-order", tenantID), h.s2sHeaders())
	if err != nil {
		h.log.Error("standing order cancel failed", zap.String("tenant_id", tenantID.String()), zap.Error(err))
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "could not cancel the standing order"})
		return
	}
	if !resp.IsSuccess() {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(resp.Body, &e)
		writeJSON(w, resp.StatusCode, map[string]string{"error": e.Error})
		return
	}
	if sub, err := h.client.TenantSubscription.Query().Where(tenantsubscription.TenantIDEQ(tenantID)).Only(r.Context()); err == nil {
		meta := make(map[string]any, len(sub.Metadata))
		for k, v := range sub.Metadata {
			meta[k] = v
		}
		if so, ok := meta["standing_order"].(map[string]any); ok {
			so["status"] = "cancelled"
			so["cancelled_at"] = time.Now().UTC().Format(time.RFC3339)
			so["cancelled_by"] = requestUserID(r)
			meta["standing_order"] = so
			if _, err := h.client.TenantSubscription.UpdateOneID(sub.ID).SetMetadata(meta).Save(r.Context()); err != nil {
				h.log.Warn("standing order cancel: metadata not saved", zap.Error(err))
			}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(resp.Body)
}

// requestIP is the caller's address (the router's RealIP middleware has already resolved it).
func requestIP(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// requestUserID is the signed-in user's id, or "" for service calls.
func requestUserID(r *http.Request) string {
	if claims, ok := authclient.ClaimsFromContext(r.Context()); ok && claims != nil {
		if id, err := claims.UserID(); err == nil {
			return id.String()
		}
		return claims.Subject
	}
	return ""
}

// s2sHeaders carries the internal service key on treasury S2S calls.
func (h *BillingHandler) s2sHeaders() map[string]string {
	headers := map[string]string{}
	if h.treasuryAPIKey != "" {
		headers["X-API-Key"] = h.treasuryAPIKey
	}
	return headers
}
