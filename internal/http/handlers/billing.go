package handlers

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/tenantsubscription"
	"github.com/google/uuid"
	serviceclient "github.com/Bengo-Hub/shared-service-client"
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

