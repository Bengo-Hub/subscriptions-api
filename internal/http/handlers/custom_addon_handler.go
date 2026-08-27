package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	authclient "github.com/Bengo-Hub/shared-auth-client"
	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/customaddon"
	"github.com/bengobox/subscription-service/internal/modules/subscriptions"
)

// notificationsFulfilledAddonTypes are the service_addon_type values under service_code
// "notifications" that need fulfillment inside notifications-api (a DIFFERENT service) when
// activated — SMS credit grants and WhatsApp plan activation. Email hosting is deliberately NOT
// here: it's a separate first-class product in this service with its own purchase/fulfillment
// flow (EmailLicense/EmailPlan, see email_handler.go), not CustomAddon-shaped.
var notificationsFulfilledAddonTypes = map[string]bool{
	"sms_bundle":    true,
	"whatsapp_plan": true,
}

// CustomAddonHandler manages platform-admin-configured recurring or one-time charges
// attached to a specific tenant. Tenants have read-only access to their own addons.
type CustomAddonHandler struct {
	log    *zap.Logger
	client *ent.Client
	subSvc *subscriptions.Service
}

// subSvc is used to emit the custom_addon.activated outbox event (on the same "subscription"
// aggregate stream notifications-api's cmd/worker/subscription_consumer.go already subscribes
// to) when an SMS/WhatsApp addon needs fulfillment in that other service — see
// notificationsFulfilledAddonTypes.
func NewCustomAddonHandler(log *zap.Logger, client *ent.Client, subSvc *subscriptions.Service) *CustomAddonHandler {
	return &CustomAddonHandler{
		log:    log.Named("custom_addon.handler"),
		client: client,
		subSvc: subSvc,
	}
}

type customAddonInput struct {
	Name             string  `json:"name"`
	Description      string  `json:"description"`
	ServiceCode      *string `json:"service_code"`
	ServiceAddonType *string `json:"service_addon_type"`
	BillingCycle     string  `json:"billing_cycle"`
	UnitPriceKes     int     `json:"unit_price_kes"`
	Quantity         int     `json:"quantity"`
	Notes            *string `json:"notes"`
	// Metadata carries addon-specific provisioning parameters — e.g. {"sms_credits": 5000} for a
	// sms_bundle addon, or {"whatsapp_plan_id": "<uuid>"} for a whatsapp_plan addon. Passed through
	// verbatim to the fulfillment event; notifications-api's consumer interprets it.
	Metadata map[string]any `json:"metadata"`
}

// needsNotificationsFulfillment reports whether this addon requires an activation event to
// notifications-api once it's active.
func needsNotificationsFulfillment(serviceCode, serviceAddonType *string) bool {
	if serviceCode == nil || *serviceCode != "notifications" || serviceAddonType == nil {
		return false
	}
	return notificationsFulfilledAddonTypes[*serviceAddonType]
}

// CreateCustomAddon handles POST /admin/tenants/{tenant_id}/custom-addons
func (h *CustomAddonHandler) CreateCustomAddon(w http.ResponseWriter, r *http.Request) {
	tenantID, err := uuid.Parse(chi.URLParam(r, "tenant_id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid tenant_id"})
		return
	}

	adminUserID := resolveUserID(r)

	var inp customAddonInput
	if err := json.NewDecoder(r.Body).Decode(&inp); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if inp.Name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name is required"})
		return
	}
	if inp.UnitPriceKes < 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unit_price_kes must be >= 0"})
		return
	}

	cycle := customaddon.BillingCycleMonthly
	if inp.BillingCycle != "" {
		cycle = customaddon.BillingCycle(inp.BillingCycle)
	}

	qty := inp.Quantity
	if qty <= 0 {
		qty = 1
	}

	ctx := r.Context()
	tx, err := h.client.Tx(ctx)
	if err != nil {
		h.log.Error("create custom addon: begin tx failed", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create addon"})
		return
	}

	b := tx.CustomAddon.Create().
		SetTenantID(tenantID).
		SetName(inp.Name).
		SetDescription(inp.Description).
		SetBillingCycle(cycle).
		SetUnitPriceKes(inp.UnitPriceKes).
		SetQuantity(qty).
		SetStatus(customaddon.StatusActive).
		SetCreatedByUserID(adminUserID).
		SetUpdatedAt(time.Now())

	if inp.ServiceCode != nil {
		b.SetServiceCode(*inp.ServiceCode)
	}
	if inp.ServiceAddonType != nil {
		b.SetServiceAddonType(*inp.ServiceAddonType)
	}
	if inp.Notes != nil {
		b.SetNotes(*inp.Notes)
	}
	if inp.Metadata != nil {
		b.SetMetadata(inp.Metadata)
	}

	addon, err := b.Save(ctx)
	if err != nil {
		_ = tx.Rollback()
		h.log.Error("create custom addon failed", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create addon"})
		return
	}

	if h.subSvc != nil && needsNotificationsFulfillment(inp.ServiceCode, inp.ServiceAddonType) {
		h.subSvc.WriteOutboxEventPublic(ctx, tx, tenantID, "custom_addon", addon.ID, "custom_addon.activated", map[string]any{
			"service_code":       *inp.ServiceCode,
			"service_addon_type": *inp.ServiceAddonType,
			"quantity":           qty,
			"metadata":           inp.Metadata,
		})
	}

	if err := tx.Commit(); err != nil {
		h.log.Error("create custom addon: commit failed", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create addon"})
		return
	}

	writeJSON(w, http.StatusCreated, addon)
}

// ListCustomAddonsByTenant handles GET /admin/tenants/{tenant_id}/custom-addons
func (h *CustomAddonHandler) ListCustomAddonsByTenant(w http.ResponseWriter, r *http.Request) {
	tenantID, err := uuid.Parse(chi.URLParam(r, "tenant_id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid tenant_id"})
		return
	}

	addons, err := h.client.CustomAddon.Query().
		Where(customaddon.TenantIDEQ(tenantID)).
		Order(ent.Desc("created_at")).
		All(r.Context())
	if err != nil {
		h.log.Error("list custom addons failed", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"data": addons, "total": len(addons)})
}

// UpdateCustomAddon handles PATCH /admin/tenants/{tenant_id}/custom-addons/{id}
func (h *CustomAddonHandler) UpdateCustomAddon(w http.ResponseWriter, r *http.Request) {
	tenantID, err := uuid.Parse(chi.URLParam(r, "tenant_id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid tenant_id"})
		return
	}
	addonID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid addon id"})
		return
	}

	existing, err := h.client.CustomAddon.Query().
		Where(customaddon.ID(addonID), customaddon.TenantIDEQ(tenantID)).
		Only(r.Context())
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "addon not found"})
		return
	}

	var inp map[string]any
	if err := json.NewDecoder(r.Body).Decode(&inp); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	ctx := r.Context()
	tx, err := h.client.Tx(ctx)
	if err != nil {
		h.log.Error("update custom addon: begin tx failed", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "update failed"})
		return
	}
	upd := tx.CustomAddon.UpdateOneID(existing.ID).SetUpdatedAt(time.Now())

	wasActive := existing.Status == customaddon.StatusActive
	newStatus := existing.Status

	if v, ok := inp["name"].(string); ok && v != "" {
		upd.SetName(v)
	}
	if v, ok := inp["description"].(string); ok {
		upd.SetDescription(v)
	}
	if v, ok := inp["unit_price_kes"].(float64); ok {
		upd.SetUnitPriceKes(int(v))
	}
	if v, ok := inp["quantity"].(float64); ok && int(v) > 0 {
		upd.SetQuantity(int(v))
	}
	if v, ok := inp["status"].(string); ok {
		newStatus = customaddon.Status(v)
		upd.SetStatus(newStatus)
	}
	if v, ok := inp["notes"].(string); ok {
		upd.SetNotes(v)
	}
	if v, ok := inp["metadata"].(map[string]any); ok {
		upd.SetMetadata(v)
	}

	updated, err := upd.Save(ctx)
	if err != nil {
		_ = tx.Rollback()
		h.log.Error("update custom addon failed", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "update failed"})
		return
	}

	// Reactivation (paused/cancelled -> active) re-fires fulfillment — e.g. an admin un-pausing an
	// SMS addon should re-grant the credits, matching CreateCustomAddon's "active = fulfill" rule.
	// A plain edit that leaves status active (or leaves it non-active) does NOT re-fire.
	if h.subSvc != nil && !wasActive && newStatus == customaddon.StatusActive &&
		needsNotificationsFulfillment(updated.ServiceCode, updated.ServiceAddonType) {
		h.subSvc.WriteOutboxEventPublic(ctx, tx, tenantID, "custom_addon", updated.ID, "custom_addon.activated", map[string]any{
			"service_code":       *updated.ServiceCode,
			"service_addon_type": *updated.ServiceAddonType,
			"quantity":           updated.Quantity,
			"metadata":           updated.Metadata,
		})
	}

	if err := tx.Commit(); err != nil {
		h.log.Error("update custom addon: commit failed", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "update failed"})
		return
	}

	writeJSON(w, http.StatusOK, updated)
}

// DeleteCustomAddon handles DELETE /admin/tenants/{tenant_id}/custom-addons/{id}.
// This permanently removes the add-on. To cancel/reactivate (soft state change),
// use PATCH with {"status": "cancelled"|"active"|"paused"} instead.
func (h *CustomAddonHandler) DeleteCustomAddon(w http.ResponseWriter, r *http.Request) {
	tenantID, err := uuid.Parse(chi.URLParam(r, "tenant_id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid tenant_id"})
		return
	}
	addonID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid addon id"})
		return
	}

	n, err := h.client.CustomAddon.Delete().
		Where(customaddon.ID(addonID), customaddon.TenantIDEQ(tenantID)).
		Exec(r.Context())
	if err != nil {
		h.log.Error("delete custom addon failed", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "delete failed"})
		return
	}
	if n == 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "addon not found"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// ListMyCustomAddons handles GET /custom-addons (tenant read-only view)
func (h *CustomAddonHandler) ListMyCustomAddons(w http.ResponseWriter, r *http.Request) {
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

	addons, err := h.client.CustomAddon.Query().
		Where(
			customaddon.TenantIDEQ(tenantID),
			customaddon.StatusEQ(customaddon.StatusActive),
		).
		Order(ent.Asc("created_at")).
		All(r.Context())
	if err != nil {
		h.log.Error("list my custom addons failed", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"data": addons, "total": len(addons)})
}

// resolveUserID extracts the user UUID from the JWT claims context.
func resolveUserID(r *http.Request) uuid.UUID {
	if claims, ok := authclient.ClaimsFromContext(r.Context()); ok && claims.Subject != "" {
		if id, err := uuid.Parse(claims.Subject); err == nil {
			return id
		}
	}
	return uuid.Nil
}
