package handlers

import (
	"net/http"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/tenantsubscription"
	"github.com/google/uuid"
	"go.uber.org/zap"

)

// BillingHandler handles billing-related endpoints.
type BillingHandler struct {
	log    *zap.Logger
	client *ent.Client
}

// NewBillingHandler creates a new billing handler.
func NewBillingHandler(log *zap.Logger, client *ent.Client) *BillingHandler {
	return &BillingHandler{
		log:    log.Named("billing.handler"),
		client: client,
	}
}

// GetBilling returns billing information for the tenant.
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
		billing["currency"] = sub.Edges.Plan.Currency
	}

	// Extract payment method from metadata if stored
	if sub.Metadata != nil {
		if pm, ok := sub.Metadata["payment_method"]; ok {
			billing["paymentMethod"] = pm
		}
	}

	// Invoices — future: query from treasury-api. For now return empty array.
	billing["invoices"] = []any{}

	writeJSON(w, http.StatusOK, billing)
}
