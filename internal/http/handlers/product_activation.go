package handlers

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/bengobox/subscription-service/internal/modules/subscriptions"
)

// ProductActivationHandler serves the tenant-self-service app-activation endpoints — the
// tenant-admin-facing counterpart to PlatformHandler's admin-only product endpoints
// (/admin/tenants/{tenant_id}/products). Unlike the admin endpoints, these never take a
// tenant_id path param: the acting tenant is always resolved from the caller's own auth
// context via resolveTenantID, so a tenant admin can only ever act on their own tenant.
type ProductActivationHandler struct {
	log    *zap.Logger
	subSvc *subscriptions.Service
}

// NewProductActivationHandler creates the handler.
func NewProductActivationHandler(log *zap.Logger, subSvc *subscriptions.Service) *ProductActivationHandler {
	return &ProductActivationHandler{log: log.Named("product-activation.handler"), subSvc: subSvc}
}

func (h *ProductActivationHandler) tenantID(r *http.Request) (uuid.UUID, bool) {
	idStr := resolveTenantID(r)
	if idStr == "" {
		return uuid.Nil, false
	}
	id, err := uuid.Parse(idStr)
	return id, err == nil
}

// CheckProductEntitlement godoc
// @Summary Check whether the caller's tenant may self-activate a product
// @Tags Products
// @Produce json
// @Security BearerAuth
// @Param product_code path string true "Product code"
// @Success 200 {object} subscriptions.EntitlementCheck
// @Router /products/{product_code}/entitlement [get]
func (h *ProductActivationHandler) CheckProductEntitlement(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := h.tenantID(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "tenant context required"})
		return
	}
	productCode := chi.URLParam(r, "product_code")
	check, err := h.subSvc.IsEntitledToProduct(r.Context(), tenantID, productCode)
	if err != nil {
		h.log.Error("check product entitlement failed", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, check)
}

// ActivateMyProduct godoc
// @Summary Self-activate a product for the caller's own tenant
// @Description Turns on a product line within the tenant's current plan. Never grants a new
// @Description entitlement — 403s with the recommended upgrade plan when not already entitled.
// @Tags Products
// @Produce json
// @Security BearerAuth
// @Param product_code path string true "Product code"
// @Success 200 {object} map[string]interface{}
// @Failure 403 {object} subscriptions.EntitlementCheck
// @Router /products/{product_code}/activate [post]
func (h *ProductActivationHandler) ActivateMyProduct(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := h.tenantID(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "tenant context required"})
		return
	}
	productCode := chi.URLParam(r, "product_code")
	if productCode == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "product_code is required"})
		return
	}

	check, err := h.subSvc.ActivateMyProduct(r.Context(), tenantID, productCode)
	if err != nil {
		if subscriptions.IsExemptErr(err) {
			writeJSON(w, http.StatusOK, map[string]any{"is_bypass": true, "message": "tenant is exempt from subscriptions"})
			return
		}
		h.log.Error("self-service product activation failed", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if !check.Entitled {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"code":          "feature_not_available",
			"product_code":  productCode,
			"min_plan_code": check.MinPlanCode,
			"min_plan_name": check.MinPlanName,
			"upgrade_url":   "/plans?product=" + productCode,
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "product_code": productCode})
}

// DeactivateMyProduct godoc
// @Summary Self-deactivate a previously self-activated product
// @Tags Products
// @Produce json
// @Security BearerAuth
// @Param product_code path string true "Product code"
// @Success 200 {object} map[string]interface{}
// @Router /products/{product_code}/deactivate [post]
func (h *ProductActivationHandler) DeactivateMyProduct(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := h.tenantID(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "tenant context required"})
		return
	}
	productCode := chi.URLParam(r, "product_code")
	if productCode == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "product_code is required"})
		return
	}
	if err := h.subSvc.DeactivateProduct(r.Context(), tenantID, productCode); err != nil {
		h.log.Error("self-service product deactivation failed", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "product_code": productCode})
}

// ListMyProducts godoc
// @Summary List the caller's tenant's product-activation lines
// @Tags Products
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{}
// @Router /products [get]
func (h *ProductActivationHandler) ListMyProducts(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := h.tenantID(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "tenant context required"})
		return
	}
	prods, err := h.subSvc.ListProducts(r.Context(), tenantID)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"products": []any{}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"products": prods})
}
