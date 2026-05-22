package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/tenantsubscription"
	"github.com/bengobox/subscription-service/internal/modules/subscriptions"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"
	httpware "github.com/Bengo-Hub/httpware"
)

type SubscriptionHandler struct {
	log            *zap.Logger
	client         *ent.Client
	service        *subscriptions.Service
	featureHandler *FeatureHandler
}

func NewSubscriptionHandler(log *zap.Logger, client *ent.Client, svc *subscriptions.Service, featureHandler *FeatureHandler) *SubscriptionHandler {
	return &SubscriptionHandler{
		log:            log.Named("subscription.handler"),
		client:         client,
		service:        svc,
		featureHandler: featureHandler,
	}
}

// Get godoc
// @Summary Get current subscription
// @Description Returns the current tenant's subscription with plan details
// @Tags Subscriptions
// @Produce json
// @Security BearerAuth
// @Param X-Tenant-ID header string false "Tenant UUID"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Router /subscription [get]
func (h *SubscriptionHandler) Get(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tenantIDStr := resolveTenantID(r)
	if tenantIDStr == "" {
		h.respondWithError(w, http.StatusBadRequest, "tenant_id required")
		return
	}

	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		h.respondWithError(w, http.StatusBadRequest, "invalid tenant id")
		return
	}

	sub, err := h.service.GetSubscriptionResult(ctx, tenantID)
	if err != nil {
		h.log.Error("failed to get subscription", zap.Error(err))
		h.respondWithError(w, http.StatusNotFound, "subscription not found")
		return
	}

	h.respondWithJSON(w, http.StatusOK, sub)
}

// Create godoc
// @Summary Create subscription
// @Description Provisions a new subscription for the tenant (starts trial if applicable)
// @Tags Subscriptions
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 201 {object} map[string]interface{}
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /subscription [post]
func (h *SubscriptionHandler) Create(w http.ResponseWriter, r *http.Request) {
	var in subscriptions.CreateInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		h.respondWithError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	ctx := r.Context()
	tenantIDStr := httpware.GetTenantID(ctx)
	if tenantIDStr != "" && !httpware.IsPlatformOwner(ctx) {
		// Non-platform owners are locked to their own tenant
		tid, _ := uuid.Parse(tenantIDStr)
		in.TenantID = tid
	}

	if in.TenantID == uuid.Nil {
		h.respondWithError(w, http.StatusBadRequest, "tenant_id required")
		return
	}

	sub, err := h.service.CreateSubscription(ctx, in)
	if err != nil {
		h.log.Error("failed to create subscription", zap.Error(err))
		h.respondWithError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if h.featureHandler != nil {
		h.featureHandler.InvalidateCache(ctx, sub.TenantID)
	}

	h.respondWithJSON(w, http.StatusCreated, sub)
}

// ChangePlan upgrades or downgrades the subscription.
// ChangePlan godoc
// @Summary Change subscription plan
// @Description Upgrades or downgrades the tenant's subscription plan
// @Tags Subscriptions
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]string
// @Router /subscription/plan [put]
func (h *SubscriptionHandler) ChangePlan(w http.ResponseWriter, r *http.Request) {
	var in subscriptions.ChangePlanInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		h.respondWithError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	ctx := r.Context()
	tenantIDStr := httpware.GetTenantID(ctx)
	if tenantIDStr != "" && !httpware.IsPlatformOwner(ctx) {
		tid, _ := uuid.Parse(tenantIDStr)
		in.TenantID = tid
	}

	if in.TenantID == uuid.Nil {
		h.respondWithError(w, http.StatusBadRequest, "tenant_id required")
		return
	}

	sub, err := h.service.ChangePlan(ctx, in)
	if err != nil {
		h.log.Error("failed to change plan", zap.Error(err))
		h.respondWithError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if h.featureHandler != nil {
		h.featureHandler.InvalidateCache(ctx, in.TenantID)
	}

	h.respondWithJSON(w, http.StatusOK, sub)
}

// Initiate starts the checkout flow for a subscription.
// Initiate godoc
// @Summary Initiate subscription
// @Description Starts the subscription provisioning process for a new tenant
// @Tags Subscriptions
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 201 {object} map[string]interface{}
// @Failure 400 {object} map[string]string
// @Router /subscription/initiate [post]
func (h *SubscriptionHandler) Initiate(w http.ResponseWriter, r *http.Request) {
	var in subscriptions.InitiateSubscriptionInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		h.respondWithError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	ctx := r.Context()
	tenantIDStr := httpware.GetTenantID(ctx)
	if tenantIDStr != "" && !httpware.IsPlatformOwner(ctx) {
		tid, _ := uuid.Parse(tenantIDStr)
		in.TenantID = tid
	}

	if in.TenantID == uuid.Nil {
		h.respondWithError(w, http.StatusBadRequest, "tenant_id required")
		return
	}

	result, err := h.service.InitiateSubscription(ctx, in)
	if err != nil {
		h.log.Error("failed to initiate subscription", zap.Error(err))
		h.respondWithError(w, http.StatusInternalServerError, err.Error())
		return
	}

	h.respondWithJSON(w, http.StatusOK, result)
}

// GetByTenantID returns subscription data for a specific tenant by ID.
// This is a S2S endpoint — intended for service-to-service calls (e.g. auth-api JWT enrichment).
// Requires API key auth (platform service key).
// GetByTenantID godoc
// @Summary Get subscription by tenant ID (S2S)
// @Description Returns a tenant's subscription by tenant UUID. Used by auth-api for JWT enrichment.
// @Tags Subscriptions
// @Produce json
// @Security BearerAuth
// @Security ApiKeyAuth
// @Param tenant_id path string true "Tenant UUID"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Router /tenants/{tenant_id}/subscription [get]
func (h *SubscriptionHandler) GetByTenantID(w http.ResponseWriter, r *http.Request) {
	tenantIDStr := chi.URLParam(r, "tenant_id")
	if tenantIDStr == "" {
		h.respondWithError(w, http.StatusBadRequest, "tenant_id required")
		return
	}

	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		h.respondWithError(w, http.StatusBadRequest, "invalid tenant_id")
		return
	}

	sub, err := h.service.GetSubscriptionResult(r.Context(), tenantID)
	if err != nil {
		h.log.Debug("subscription not found for tenant", zap.String("tenant_id", tenantIDStr), zap.Error(err))
		h.respondWithError(w, http.StatusNotFound, "subscription not found")
		return
	}

	h.respondWithJSON(w, http.StatusOK, sub)
}

// GetServiceSubscriptions returns a per-service-tag subscription view for a tenant.
// S2S endpoint for auth-ui billing tab. Requires API key or platform owner JWT.
// GET /api/v1/tenants/{tenant_id}/subscriptions
func (h *SubscriptionHandler) GetServiceSubscriptions(w http.ResponseWriter, r *http.Request) {
	tenantIDStr := chi.URLParam(r, "tenant_id")
	if tenantIDStr == "" {
		h.respondWithError(w, http.StatusBadRequest, "tenant_id required")
		return
	}
	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		h.respondWithError(w, http.StatusBadRequest, "invalid tenant_id")
		return
	}

	result, err := h.service.GetServiceSubscriptions(r.Context(), tenantID)
	if err != nil {
		h.log.Error("failed to get service subscriptions", zap.Error(err))
		h.respondWithError(w, http.StatusInternalServerError, "failed to get subscriptions")
		return
	}

	h.respondWithJSON(w, http.StatusOK, result)
}

// SwitchPlan changes the plan for a subscription identified by its ID.
// PUT /api/v1/subscriptions/{id}/switch-plan
func (h *SubscriptionHandler) SwitchPlan(w http.ResponseWriter, r *http.Request) {
	subIDStr := chi.URLParam(r, "id")
	if subIDStr == "" {
		h.respondWithError(w, http.StatusBadRequest, "subscription id required")
		return
	}
	subID, err := uuid.Parse(subIDStr)
	if err != nil {
		h.respondWithError(w, http.StatusBadRequest, "invalid subscription id")
		return
	}

	var body struct {
		PlanCode string `json:"plan_code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.PlanCode == "" {
		h.respondWithError(w, http.StatusBadRequest, "plan_code required")
		return
	}

	ctx := r.Context()
	sub, err := h.service.SwitchPlanByID(ctx, subID, body.PlanCode)
	if err != nil {
		h.log.Error("failed to switch plan", zap.String("sub_id", subIDStr), zap.Error(err))
		h.respondWithError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if h.featureHandler != nil {
		h.featureHandler.InvalidateCache(ctx, sub.TenantID)
	}

	h.respondWithJSON(w, http.StatusOK, sub)
}

// GetSettings returns subscription settings (auto-renew, notification preferences).
// GetSettings godoc
// @Summary Get subscription settings
// @Description Returns the current tenant's subscription settings (auto-renew, payment method, etc.)
// @Tags Subscriptions
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{}
// @Router /subscription/settings [get]
func (h *SubscriptionHandler) GetSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tenantIDStr := httpware.GetTenantID(ctx)
	if tenantIDStr == "" {
		h.respondWithError(w, http.StatusBadRequest, "tenant_id required")
		return
	}
	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		h.respondWithError(w, http.StatusBadRequest, "invalid tenant_id")
		return
	}

	sub, err := h.client.TenantSubscription.Query().
		Where(tenantsubscription.TenantIDEQ(tenantID)).
		Only(ctx)
	if err != nil {
		h.respondWithError(w, http.StatusNotFound, "subscription not found")
		return
	}

	// Extract settings from metadata
	metadata := sub.Metadata
	if metadata == nil {
		metadata = map[string]any{}
	}

	settings := map[string]any{
		"autoRenew":          metadata["auto_renew"] != false,
		"emailNotifications": metadata["email_notifications"] != false,
		"usageAlerts":        metadata["usage_alerts"] != false,
		"usageThreshold":     metadata["usage_threshold"],
		"billingEmail":       metadata["billing_email"],
	}

	h.respondWithJSON(w, http.StatusOK, settings)
}

// UpdateSettings updates subscription settings.
// UpdateSettings godoc
// @Summary Update subscription settings
// @Description Updates the current tenant's subscription settings
// @Tags Subscriptions
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]string
// @Router /subscription/settings [put]
func (h *SubscriptionHandler) UpdateSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tenantIDStr := httpware.GetTenantID(ctx)
	if tenantIDStr == "" {
		h.respondWithError(w, http.StatusBadRequest, "tenant_id required")
		return
	}
	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		h.respondWithError(w, http.StatusBadRequest, "invalid tenant_id")
		return
	}

	var req map[string]any
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.respondWithError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Get existing subscription
	sub, err := h.client.TenantSubscription.Query().
		Where(tenantsubscription.TenantIDEQ(tenantID)).
		Only(ctx)
	if err != nil {
		h.respondWithError(w, http.StatusNotFound, "subscription not found")
		return
	}

	// Merge settings into metadata
	metadata := sub.Metadata
	if metadata == nil {
		metadata = map[string]any{}
	}
	for k, v := range req {
		metadata[k] = v
	}

	_, err = h.client.TenantSubscription.UpdateOneID(sub.ID).
		SetMetadata(metadata).
		Save(ctx)
	if err != nil {
		h.log.Error("failed to update settings", zap.Error(err))
		h.respondWithError(w, http.StatusInternalServerError, "failed to update settings")
		return
	}

	h.respondWithJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

// ListExpiring returns active subscriptions expiring within the given number of days.
// S2S endpoint for notifications-api scheduled expiry warnings.
// GET /api/v1/subscriptions/expiring?days=7
// ListExpiring godoc
// @Summary List expiring subscriptions (S2S)
// @Description Returns subscriptions expiring within the specified number of days. Used by notifications-api.
// @Tags Subscriptions
// @Produce json
// @Security BearerAuth
// @Security ApiKeyAuth
// @Param days query int false "Days until expiry (default 7)"
// @Success 200 {object} map[string]interface{}
// @Router /subscriptions/expiring [get]
func (h *SubscriptionHandler) ListExpiring(w http.ResponseWriter, r *http.Request) {
	daysStr := r.URL.Query().Get("days")
	days := 7 // default
	if daysStr != "" {
		if d, err := strconv.Atoi(daysStr); err == nil && d > 0 {
			days = d
		}
	}

	results, err := h.service.ListExpiring(r.Context(), days)
	if err != nil {
		h.log.Error("failed to list expiring subscriptions", zap.Error(err))
		h.respondWithError(w, http.StatusInternalServerError, "failed to list expiring subscriptions")
		return
	}

	h.respondWithJSON(w, http.StatusOK, results)
}

func (h *SubscriptionHandler) respondWithError(w http.ResponseWriter, code int, message string) {
	h.respondWithJSON(w, code, map[string]string{"error": message})
}

func (h *SubscriptionHandler) respondWithJSON(w http.ResponseWriter, code int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(payload)
}
