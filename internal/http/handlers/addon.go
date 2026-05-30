package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/planfeature"
	"github.com/bengobox/subscription-service/internal/ent/tenantsubscription"
	"github.com/bengobox/subscription-service/internal/modules/subscriptions"
	serviceclient "github.com/Bengo-Hub/shared-service-client"
)

// AddonHandler manages optional add-on features for tenant subscriptions.
// Addons are PlanFeature records with is_included=false that tenants can purchase individually.
type AddonHandler struct {
	log            *zap.Logger
	client         *ent.Client
	svc            *subscriptions.Service
	treasuryClient *serviceclient.Client
	treasuryAPIKey string
	featureHandler *FeatureHandler
}

func NewAddonHandler(log *zap.Logger, client *ent.Client, svc *subscriptions.Service, treasuryClient *serviceclient.Client, treasuryAPIKey string, featureHandler *FeatureHandler) *AddonHandler {
	return &AddonHandler{
		log:            log.Named("addon.handler"),
		client:         client,
		svc:            svc,
		treasuryClient: treasuryClient,
		treasuryAPIKey: treasuryAPIKey,
		featureHandler: featureHandler,
	}
}

type addonItem struct {
	FeatureCode     string  `json:"feature_code"`
	PlanID          string  `json:"plan_id"`
	LimitValue      *int    `json:"limit_value,omitempty"`
	OverageUnitPrice float64 `json:"overage_unit_price"`
	Purchased       bool    `json:"purchased"`
}

// ListAddons godoc
// @Summary List available add-ons
// @Description Returns purchasable add-on features (is_included=false) for the tenant's current plan, annotated with purchase status.
// @Tags Addons
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /addons [get]
func (h *AddonHandler) ListAddons(w http.ResponseWriter, r *http.Request) {
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

	// Get tenant's current subscription to find plan ID
	sub, err := h.client.TenantSubscription.Query().
		Where(tenantsubscription.TenantIDEQ(tenantID)).
		Only(ctx)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no active subscription"})
		return
	}

	// Fetch optional features for this plan
	features, err := h.client.PlanFeature.Query().
		Where(
			planfeature.PlanIDEQ(sub.PlanID),
			planfeature.IsIncludedEQ(false),
		).
		All(ctx)
	if err != nil {
		h.log.Error("failed to query addons", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	// Get already-purchased addons from metadata
	purchased := map[string]bool{}
	if sub.Metadata != nil {
		if addons, ok := sub.Metadata["purchased_addons"]; ok {
			if list, ok := addons.([]any); ok {
				for _, a := range list {
					if code, ok := a.(string); ok {
						purchased[code] = true
					}
				}
			}
		}
	}

	items := make([]addonItem, 0, len(features))
	for _, f := range features {
		item := addonItem{
			FeatureCode:     f.FeatureCode,
			PlanID:          f.PlanID.String(),
			OverageUnitPrice: f.OverageUnitPrice,
			Purchased:       purchased[f.FeatureCode],
		}
		if f.LimitValue != 0 {
			v := f.LimitValue
			item.LimitValue = &v
		}
		items = append(items, item)
	}

	writeJSON(w, http.StatusOK, map[string]any{"addons": items})
}

// PurchaseAddon godoc
// @Summary Purchase an add-on
// @Description Initiates purchase of a specific add-on feature. Free add-ons are activated immediately; paid add-ons create a Treasury payment intent.
// @Tags Addons
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param feature_code path string true "Feature code of the add-on"
// @Param body body object false "Optional return URL for payment redirect"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Failure 409 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Failure 503 {object} map[string]string
// @Router /addons/{feature_code}/purchase [post]
func (h *AddonHandler) PurchaseAddon(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	featureCode := chi.URLParam(r, "feature_code")
	if featureCode == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "feature_code required"})
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

	var body struct {
		ReturnURL string `json:"return_url"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	// Get subscription
	sub, err := h.client.TenantSubscription.Query().
		Where(tenantsubscription.TenantIDEQ(tenantID)).
		Only(ctx)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no active subscription"})
		return
	}

	// Validate addon exists for this plan
	feature, err := h.client.PlanFeature.Query().
		Where(
			planfeature.PlanIDEQ(sub.PlanID),
			planfeature.FeatureCodeEQ(featureCode),
			planfeature.IsIncludedEQ(false),
		).
		Only(ctx)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "addon not found for current plan"})
		return
	}

	// Check if already purchased
	if sub.Metadata != nil {
		if addons, ok := sub.Metadata["purchased_addons"]; ok {
			if list, ok := addons.([]any); ok {
				for _, a := range list {
					if code, ok := a.(string); ok && code == featureCode {
						writeJSON(w, http.StatusConflict, map[string]string{"error": "addon already purchased"})
						return
					}
				}
			}
		}
	}

	// If free addon (overage_unit_price == 0), activate directly without Treasury
	if feature.OverageUnitPrice == 0 {
		if err := h.activateAddon(ctx, sub, featureCode); err != nil {
			h.log.Error("failed to activate free addon", zap.Error(err))
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to activate addon"})
			return
		}
		if h.featureHandler != nil {
			h.featureHandler.InvalidateCache(ctx, tenantID)
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "activated", "feature_code": featureCode})
		return
	}

	// Paid addon: initiate Treasury payment intent
	if h.treasuryClient == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "payment service unavailable"})
		return
	}

	headers := map[string]string{}
	if h.treasuryAPIKey != "" {
		headers["X-API-Key"] = h.treasuryAPIKey
	}

	req := map[string]any{
		"amount":         feature.OverageUnitPrice,
		"currency":       "KES",
		"payment_method": "pending",
		"reference_id":   fmt.Sprintf("ADDON-%s-%s", tenantID.String()[:8], featureCode),
		"reference_type": "addon_purchase",
		"source_service": "subscription-service",
		"description":    "Add-on: " + featureCode,
		"callback_url":   body.ReturnURL,
		"metadata": map[string]any{
			"tenant_id":    tenantID.String(),
			"feature_code": featureCode,
			"addon":        true,
		},
	}

	resp, err := h.treasuryClient.Post(ctx, fmt.Sprintf("/api/v1/%s/payments/intents", tenantID), req, headers)
	if err != nil || !resp.IsSuccess() {
		h.log.Error("treasury intent failed for addon", zap.String("tenant_id", tenantIDStr), zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "payment initiation failed"})
		return
	}

	var treasuryResp map[string]any
	_ = resp.DecodeJSON(&treasuryResp)

	writeJSON(w, http.StatusOK, map[string]any{
		"status":       "payment_required",
		"feature_code": featureCode,
		"intent":       treasuryResp,
	})
}

// RemoveAddon godoc
// @Summary Remove an add-on
// @Description Deactivates a previously purchased add-on feature for the tenant.
// @Tags Addons
// @Produce json
// @Security BearerAuth
// @Param feature_code path string true "Feature code of the add-on to remove"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /addons/{feature_code} [delete]
func (h *AddonHandler) RemoveAddon(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	featureCode := chi.URLParam(r, "feature_code")
	if featureCode == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "feature_code required"})
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

	sub, err := h.client.TenantSubscription.Query().
		Where(tenantsubscription.TenantIDEQ(tenantID)).
		Only(ctx)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no active subscription"})
		return
	}

	metadata := sub.Metadata
	if metadata == nil {
		metadata = map[string]any{}
	}

	addons := []string{}
	if existing, ok := metadata["purchased_addons"]; ok {
		if list, ok := existing.([]any); ok {
			for _, a := range list {
				if code, ok := a.(string); ok && code != featureCode {
					addons = append(addons, code)
				}
			}
		}
	}
	metadata["purchased_addons"] = addons

	_, err = h.client.TenantSubscription.UpdateOneID(sub.ID).
		SetMetadata(metadata).
		Save(ctx)
	if err != nil {
		h.log.Error("failed to remove addon", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to remove addon"})
		return
	}

	if h.featureHandler != nil {
		h.featureHandler.InvalidateCache(ctx, tenantID)
	}

	writeJSON(w, http.StatusOK, map[string]any{"status": "removed", "feature_code": featureCode})
}

func (h *AddonHandler) activateAddon(ctx context.Context, sub *ent.TenantSubscription, featureCode string) error {
	metadata := sub.Metadata
	if metadata == nil {
		metadata = map[string]any{}
	}

	addons := []string{featureCode}
	if existing, ok := metadata["purchased_addons"]; ok {
		if list, ok := existing.([]any); ok {
			for _, a := range list {
				if code, ok := a.(string); ok {
					addons = append(addons, code)
				}
			}
		}
	}
	metadata["purchased_addons"] = addons
	metadata["addon_activated_at_"+featureCode] = time.Now().UTC().Format(time.RFC3339)

	_, err := h.client.TenantSubscription.UpdateOneID(sub.ID).
		SetMetadata(metadata).
		Save(ctx)
	return err
}
