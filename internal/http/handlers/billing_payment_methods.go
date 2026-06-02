package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/bengobox/subscription-service/internal/ent/tenantsubscription"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// DeletePaymentMethod removes a payment method from the subscription metadata.
// The last payment method cannot be removed. Deletion is only allowed when the
// subscription is cancelled (cancel_at_period_end=true) or expired/inactive.
//
// @Summary Delete a saved payment method
// @Description Removes a payment method by last4 from the subscription metadata.
// @Tags Billing
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]string
// @Router /subscription/payment-method/{last4} [delete]
func (h *BillingHandler) DeletePaymentMethod(w http.ResponseWriter, r *http.Request) {
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
		Last4 string `json:"last4"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Last4 == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "last4 required"})
		return
	}

	sub, err := h.client.TenantSubscription.Query().
		Where(tenantsubscription.TenantIDEQ(tenantID)).
		Only(r.Context())
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "subscription not found"})
		return
	}

	meta := make(map[string]any)
	for k, v := range sub.Metadata {
		meta[k] = v
	}

	// Resolve the payment_methods array.
	var methods []any
	if pms, ok := meta["payment_methods"]; ok {
		if arr, ok := pms.([]any); ok {
			methods = arr
		}
	} else if pm, ok := meta["payment_method"]; ok {
		methods = []any{pm}
	}

	if len(methods) <= 1 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "cannot remove the only payment method"})
		return
	}

	// Check subscription is cancelled or expired before allowing delete.
	status := string(sub.Status)
	cancelAtPeriodEnd, _ := meta["cancel_at_period_end"].(bool)
	if status == "ACTIVE" && !cancelAtPeriodEnd {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "payment method can only be removed after subscription is cancelled",
		})
		return
	}

	// Filter out the method with the given last4.
	filtered := make([]any, 0, len(methods))
	for _, m := range methods {
		if mm, ok := m.(map[string]any); ok {
			if l4, _ := mm["last4"].(string); l4 == body.Last4 {
				continue
			}
		}
		filtered = append(filtered, m)
	}

	if len(filtered) == len(methods) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "payment method not found"})
		return
	}

	meta["payment_methods"] = filtered
	if len(filtered) > 0 {
		meta["payment_method"] = filtered[0]
	} else {
		delete(meta, "payment_method")
	}

	if _, err := h.client.TenantSubscription.UpdateOneID(sub.ID).SetMetadata(meta).Save(r.Context()); err != nil {
		h.log.Error("failed to delete payment method", zap.String("tenant_id", tenantIDStr), zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to update subscription"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"status": "deleted", "remaining": len(filtered)})
}

// SetDefaultPaymentMethod sets a payment method (by last4) as the default.
//
// @Summary Set default payment method
// @Tags Billing
// @Accept json
// @Produce json
// @Security BearerAuth
// @Router /subscription/payment-method/default [put]
func (h *BillingHandler) SetDefaultPaymentMethod(w http.ResponseWriter, r *http.Request) {
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
		Last4 string `json:"last4"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Last4 == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "last4 required"})
		return
	}

	sub, err := h.client.TenantSubscription.Query().
		Where(tenantsubscription.TenantIDEQ(tenantID)).
		Only(r.Context())
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "subscription not found"})
		return
	}

	meta := make(map[string]any)
	for k, v := range sub.Metadata {
		meta[k] = v
	}

	var methods []any
	if pms, ok := meta["payment_methods"]; ok {
		if arr, ok := pms.([]any); ok {
			methods = arr
		}
	}

	// Move the selected method to the front (default = index 0).
	reordered := make([]any, 0, len(methods))
	for _, m := range methods {
		if mm, ok := m.(map[string]any); ok {
			if l4, _ := mm["last4"].(string); l4 == body.Last4 {
				reordered = append([]any{m}, reordered...)
				continue
			}
		}
		reordered = append(reordered, m)
	}
	meta["payment_methods"] = reordered
	if len(reordered) > 0 {
		meta["payment_method"] = reordered[0]
	}

	if _, err := h.client.TenantSubscription.UpdateOneID(sub.ID).SetMetadata(meta).Save(r.Context()); err != nil {
		h.log.Error("failed to set default payment method", zap.String("tenant_id", tenantIDStr), zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to update subscription"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"status": "updated"})
}

// CancelSubscription marks the subscription for cancellation at the end of the current billing period.
// The subscription remains ACTIVE and usable until period end; it just won't auto-renew.
//
// @Summary Cancel subscription at period end
// @Tags Billing
// @Produce json
// @Security BearerAuth
// @Router /subscription/cancel [post]
func (h *BillingHandler) CancelSubscription(w http.ResponseWriter, r *http.Request) {
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
		Only(r.Context())
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "subscription not found"})
		return
	}

	meta := make(map[string]any)
	for k, v := range sub.Metadata {
		meta[k] = v
	}
	meta["cancel_at_period_end"] = true
	meta["cancelled_at"] = time.Now().UTC().Format(time.RFC3339)

	if _, err := h.client.TenantSubscription.UpdateOneID(sub.ID).SetMetadata(meta).Save(r.Context()); err != nil {
		h.log.Error("failed to cancel subscription", zap.String("tenant_id", tenantIDStr), zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to cancel subscription"})
		return
	}

	h.log.Info("subscription marked for cancellation at period end", zap.String("tenant_id", tenantIDStr))
	writeJSON(w, http.StatusOK, map[string]any{
		"status":             "cancel_scheduled",
		"effective_date":     sub.CurrentPeriodEnd.Format(time.RFC3339),
		"cancel_at_period_end": true,
	})
}

// UndoCancelSubscription removes the cancel_at_period_end flag (reactivates auto-renewal).
//
// @Summary Undo subscription cancellation
// @Tags Billing
// @Produce json
// @Security BearerAuth
// @Router /subscription/cancel [delete]
func (h *BillingHandler) UndoCancelSubscription(w http.ResponseWriter, r *http.Request) {
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
		Only(r.Context())
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "subscription not found"})
		return
	}

	meta := make(map[string]any)
	for k, v := range sub.Metadata {
		meta[k] = v
	}
	delete(meta, "cancel_at_period_end")
	delete(meta, "cancelled_at")

	if _, err := h.client.TenantSubscription.UpdateOneID(sub.ID).SetMetadata(meta).Save(r.Context()); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to update subscription"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"status": "reactivated"})
}
