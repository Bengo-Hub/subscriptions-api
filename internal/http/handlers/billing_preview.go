package handlers

import (
	"net/http"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/customaddon"
	"github.com/bengobox/subscription-service/internal/ent/overagecharge"
	"github.com/bengobox/subscription-service/internal/ent/subscriptioncredit"
	"github.com/bengobox/subscription-service/internal/ent/tenantsubscription"
)

type overageLineItem struct {
	MetricType   string  `json:"metric_type"`
	UnitsUsed    float64 `json:"units_used"`
	PlanLimit    int     `json:"plan_limit"`
	UnitsOver    float64 `json:"units_over"`
	UnitPriceKes float64 `json:"unit_price_kes"`
	TotalKes     float64 `json:"total_kes"`
}

type addonLineItem struct {
	Name         string `json:"name"`
	ServiceCode  string `json:"service_code,omitempty"`
	BillingCycle string `json:"billing_cycle"`
	UnitPriceKes int    `json:"unit_price_kes"`
	Quantity     int    `json:"quantity"`
	TotalKes     int    `json:"total_kes"`
}

type invoicePreviewResponse struct {
	BasePlanPriceKes float64           `json:"base_plan_price_kes"`
	Currency         string            `json:"currency"`
	OverageCharges   []overageLineItem `json:"overage_charges"`
	OverageTotalKes  float64           `json:"overage_total_kes"`
	CustomAddons     []addonLineItem   `json:"custom_addons"`
	AddonsTotalKes   int               `json:"addons_total_kes"`
	CreditsAvailable int               `json:"credits_available_kes"`
	CreditsToApply   int               `json:"credits_to_apply_kes"`
	EstimatedTotalKes float64          `json:"estimated_total_kes"`
}

// InvoicePreview returns the projected next billing amount breakdown.
//
// GET /api/v1/billing/invoice-preview
func (h *BillingHandler) InvoicePreview(w http.ResponseWriter, r *http.Request) {
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

	// Load subscription + plan
	sub, err := h.client.TenantSubscription.Query().
		Where(tenantsubscription.TenantIDEQ(tenantID)).
		WithPlan().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "no subscription found"})
			return
		}
		h.log.Error("invoice preview: subscription query failed", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	preview := invoicePreviewResponse{Currency: "KES"}

	if sub.Edges.Plan != nil {
		preview.BasePlanPriceKes = sub.Edges.Plan.BasePrice
		preview.Currency = sub.Edges.Plan.Currency
	}

	// Overage charges (pending)
	overages, err := h.client.OverageCharge.Query().
		Where(
			overagecharge.TenantSubscriptionIDEQ(sub.ID),
			overagecharge.StatusEQ(overagecharge.StatusPending),
		).
		All(ctx)
	if err != nil {
		h.log.Warn("invoice preview: overage query failed", zap.Error(err))
	}
	var overageTotal float64
	lines := make([]overageLineItem, 0, len(overages))
	for _, o := range overages {
		lines = append(lines, overageLineItem{
			MetricType:   o.MetricType,
			UnitsUsed:    o.UnitsUsed,
			PlanLimit:    o.PlanLimit,
			UnitsOver:    o.UnitsOver,
			UnitPriceKes: o.UnitPriceKes,
			TotalKes:     o.TotalChargeKes,
		})
		overageTotal += o.TotalChargeKes
	}
	preview.OverageCharges = lines
	preview.OverageTotalKes = overageTotal

	// Active custom addons
	addons, err := h.client.CustomAddon.Query().
		Where(
			customaddon.TenantIDEQ(tenantID),
			customaddon.StatusEQ(customaddon.StatusActive),
		).
		All(ctx)
	if err != nil {
		h.log.Warn("invoice preview: custom addons query failed", zap.Error(err))
	}
	var addonsTotal int
	addonLines := make([]addonLineItem, 0, len(addons))
	for _, a := range addons {
		lineTotal := a.UnitPriceKes * a.Quantity
		sc := ""
		if a.ServiceCode != nil {
			sc = *a.ServiceCode
		}
		addonLines = append(addonLines, addonLineItem{
			Name:         a.Name,
			ServiceCode:  sc,
			BillingCycle: string(a.BillingCycle),
			UnitPriceKes: a.UnitPriceKes,
			Quantity:     a.Quantity,
			TotalKes:     lineTotal,
		})
		addonsTotal += lineTotal
	}
	preview.CustomAddons = addonLines
	preview.AddonsTotalKes = addonsTotal

	// Credit wallet balance
	credit, err := h.client.SubscriptionCredit.Query().
		Where(subscriptioncredit.TenantIDEQ(tenantID)).
		Only(ctx)
	if err == nil {
		preview.CreditsAvailable = credit.BalanceKes
	}

	// Estimated gross total before credits
	gross := preview.BasePlanPriceKes + preview.OverageTotalKes + float64(preview.AddonsTotalKes)
	creditsToApply := preview.CreditsAvailable
	if float64(creditsToApply) > gross {
		creditsToApply = int(gross)
	}
	preview.CreditsToApply = creditsToApply
	preview.EstimatedTotalKes = gross - float64(creditsToApply)

	writeJSON(w, http.StatusOK, preview)
}
