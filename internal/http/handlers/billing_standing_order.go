package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"go.uber.org/zap"

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
	cycle := string(sub.BillingCycle)
	freq, ok := ratibaFrequencyForCycle[cycle]
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "this billing cycle cannot be paid by standing order"})
		return
	}
	amount := subscriptions.EffectivePrice(sub, sub.Edges.Plan) * float64(subscriptions.BillingCycleMonths(cycle))
	if amount <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "nothing to pay on this plan"})
		return
	}
	name := ""
	if sub.Edges.Tenant != nil {
		name = sub.Edges.Tenant.Name
	}
	body := map[string]any{
		"phone":           req.Phone,
		"amount":          fmt.Sprintf("%.2f", amount),
		"frequency":       freq,
		"start_date":      sub.CurrentPeriodEnd.UTC().Format("2006-01-02"),
		"subscription_id": sub.ID.String(),
		"tenant_name":     name,
	}
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
	meta["payment_method"] = "mpesa_standing_order"
	if _, err := h.client.TenantSubscription.UpdateOneID(sub.ID).SetMetadata(meta).Save(r.Context()); err != nil {
		h.log.Warn("standing order: metadata not saved", zap.Error(err))
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_, _ = w.Write(resp.Body)
}

// s2sHeaders carries the internal service key on treasury S2S calls.
func (h *BillingHandler) s2sHeaders() map[string]string {
	headers := map[string]string{}
	if h.treasuryAPIKey != "" {
		headers["X-API-Key"] = h.treasuryAPIKey
	}
	return headers
}
