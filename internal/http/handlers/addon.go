package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/product"
	"github.com/bengobox/subscription-service/internal/ent/productsubscription"
	"github.com/bengobox/subscription-service/internal/ent/tenantsubscription"
)

// AddonHandler handles add-on product management endpoints.
type AddonHandler struct {
	log    *zap.Logger
	client *ent.Client
}

// NewAddonHandler creates a new AddonHandler.
func NewAddonHandler(log *zap.Logger, client *ent.Client) *AddonHandler {
	return &AddonHandler{
		log:    log.Named("addon.handler"),
		client: client,
	}
}

type addonResponse struct {
	ID           uuid.UUID `json:"id"`
	Code         string    `json:"code"`
	Name         string    `json:"name"`
	Description  string    `json:"description"`
	MonthlyPrice float64   `json:"monthly_price"`
	Currency     string    `json:"currency"`
}

type activeAddonResponse struct {
	Code        string     `json:"code"`
	Name        string     `json:"name"`
	Status      string     `json:"status"`
	ActivatedAt *time.Time `json:"activated_at,omitempty"`
}

// ListAvailable returns all add-on products available for purchase.
// @Summary List available add-ons
// @Tags addons
// @Produce json
// @Param tenant_id path string true "Tenant ID (UUID)"
// @Success 200 {object} map[string]any
// @Router /api/v1/tenants/{tenant_id}/addons/available [get]
func (h *AddonHandler) ListAvailable(w http.ResponseWriter, r *http.Request) {
	addons, err := h.client.Product.Query().
		Where(
			product.CategoryEQ(product.CategoryAddOn),
			product.StatusEQ(product.StatusActive),
		).
		Order(ent.Asc(product.FieldSortOrder)).
		All(r.Context())
	if err != nil {
		h.log.Error("failed to list addons", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to list add-ons"})
		return
	}

	result := make([]addonResponse, 0, len(addons))
	for _, a := range addons {
		result = append(result, addonResponse{
			ID:           a.ID,
			Code:         a.Code,
			Name:         a.Name,
			Description:  a.Description,
			MonthlyPrice: a.MonthlyPrice,
			Currency:     "KES",
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{"addons": result, "count": len(result)})
}

// Subscribe activates an add-on for a tenant's subscription.
// @Summary Subscribe to add-on
// @Tags addons
// @Accept json
// @Produce json
// @Param tenant_id path string true "Tenant ID (UUID)"
// @Success 201 {object} map[string]string
// @Router /api/v1/tenants/{tenant_id}/addons/subscribe [post]
func (h *AddonHandler) Subscribe(w http.ResponseWriter, r *http.Request) {
	tenantID, err := parseTenantID(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid tenant ID"})
		return
	}

	var req struct {
		AddonCode string `json:"addon_code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return
	}
	if req.AddonCode == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "addon_code is required"})
		return
	}

	ctx := r.Context()

	// Verify product exists and is an add-on
	addonProduct, err := h.client.Product.Query().
		Where(
			product.CodeEQ(req.AddonCode),
			product.CategoryEQ(product.CategoryAddOn),
			product.StatusEQ(product.StatusActive),
		).
		Only(ctx)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "add-on product not found or not available"})
		return
	}

	// Get tenant subscription
	sub, err := h.client.TenantSubscription.Query().
		Where(tenantsubscription.TenantIDEQ(tenantID)).
		Only(ctx)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "tenant subscription not found"})
		return
	}

	// Check not already subscribed
	exists, _ := h.client.ProductSubscription.Query().
		Where(
			productsubscription.TenantSubscriptionIDEQ(sub.ID),
			productsubscription.ProductCodeEQ(req.AddonCode),
			productsubscription.StatusEQ(productsubscription.StatusActive),
		).
		Exist(ctx)
	if exists {
		writeJSON(w, http.StatusConflict, errorResponse{Error: "already subscribed to this add-on"})
		return
	}

	now := time.Now()
	_, err = h.client.ProductSubscription.Create().
		SetTenantSubscriptionID(sub.ID).
		SetProductCode(req.AddonCode).
		SetProductID(addonProduct.ID).
		SetStatus(productsubscription.StatusActive).
		SetActivatedAt(now).
		SetCreatedAt(now).
		SetUpdatedAt(now).
		Save(ctx)
	if err != nil {
		h.log.Error("failed to subscribe to addon", zap.String("addon", req.AddonCode), zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to subscribe to add-on"})
		return
	}

	h.log.Info("addon subscribed",
		zap.String("tenant_id", tenantID.String()),
		zap.String("addon", req.AddonCode),
		zap.Float64("monthly_price", addonProduct.MonthlyPrice),
	)

	writeJSON(w, http.StatusCreated, map[string]any{
		"message":       "add-on activated",
		"addon_code":    req.AddonCode,
		"monthly_price": addonProduct.MonthlyPrice,
		"currency":      "KES",
	})
}

// Unsubscribe cancels an add-on for a tenant.
// @Summary Cancel add-on
// @Tags addons
// @Produce json
// @Param tenant_id path string true "Tenant ID (UUID)"
// @Param addonCode path string true "Add-on product code"
// @Success 200 {object} map[string]string
// @Router /api/v1/tenants/{tenant_id}/addons/{addonCode} [delete]
func (h *AddonHandler) Unsubscribe(w http.ResponseWriter, r *http.Request) {
	tenantID, err := parseTenantID(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid tenant ID"})
		return
	}

	addonCode := chi.URLParam(r, "addonCode")
	if addonCode == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "addon code is required"})
		return
	}

	ctx := r.Context()

	// Get tenant subscription
	sub, err := h.client.TenantSubscription.Query().
		Where(tenantsubscription.TenantIDEQ(tenantID)).
		Only(ctx)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "tenant subscription not found"})
		return
	}

	// Find the active product subscription
	ps, err := h.client.ProductSubscription.Query().
		Where(
			productsubscription.TenantSubscriptionIDEQ(sub.ID),
			productsubscription.ProductCodeEQ(addonCode),
			productsubscription.StatusEQ(productsubscription.StatusActive),
		).
		Only(ctx)
	if err != nil {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "add-on subscription not found"})
		return
	}

	now := time.Now()
	_, err = ps.Update().
		SetStatus(productsubscription.StatusInactive).
		SetDeactivatedAt(now).
		SetUpdatedAt(now).
		Save(ctx)
	if err != nil {
		h.log.Error("failed to unsubscribe addon", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to cancel add-on"})
		return
	}

	h.log.Info("addon cancelled",
		zap.String("tenant_id", tenantID.String()),
		zap.String("addon", addonCode),
	)

	writeJSON(w, http.StatusOK, map[string]string{
		"message":    "add-on cancelled",
		"addon_code": addonCode,
	})
}

// ListActive returns the tenant's currently active add-ons.
// @Summary List active add-ons
// @Tags addons
// @Produce json
// @Param tenant_id path string true "Tenant ID (UUID)"
// @Success 200 {object} map[string]any
// @Router /api/v1/tenants/{tenant_id}/addons/active [get]
func (h *AddonHandler) ListActive(w http.ResponseWriter, r *http.Request) {
	tenantID, err := parseTenantID(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid tenant ID"})
		return
	}

	ctx := r.Context()

	sub, err := h.client.TenantSubscription.Query().
		Where(tenantsubscription.TenantIDEQ(tenantID)).
		Only(ctx)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "tenant subscription not found"})
		return
	}

	// Get active product subscriptions that are add-ons
	productSubs, err := h.client.ProductSubscription.Query().
		Where(
			productsubscription.TenantSubscriptionIDEQ(sub.ID),
			productsubscription.StatusEQ(productsubscription.StatusActive),
		).
		WithProduct().
		All(ctx)
	if err != nil {
		h.log.Error("failed to list active addons", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to list active add-ons"})
		return
	}

	// Filter to add-on category only
	result := make([]activeAddonResponse, 0)
	for _, ps := range productSubs {
		if ps.Edges.Product != nil && ps.Edges.Product.Category == product.CategoryAddOn {
			resp := activeAddonResponse{
				Code:   ps.ProductCode,
				Name:   ps.Edges.Product.Name,
				Status: string(ps.Status),
			}
			if ps.ActivatedAt != nil {
				resp.ActivatedAt = ps.ActivatedAt
			}
			result = append(result, resp)
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{"addons": result, "count": len(result)})
}

// RegisterAddonRoutes registers add-on routes on a chi router.
func (h *AddonHandler) RegisterAddonRoutes(r chi.Router) {
	r.Get("/addons/available", h.ListAvailable)
	r.Post("/addons/subscribe", h.Subscribe)
	r.Delete("/addons/{addonCode}", h.Unsubscribe)
	r.Get("/addons/active", h.ListActive)
}
