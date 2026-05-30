package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/bengobox/subscription-service/internal/ent"
	enttenant "github.com/bengobox/subscription-service/internal/ent/tenant"
	"github.com/bengobox/subscription-service/internal/ent/tenantsubscription"
	"github.com/bengobox/subscription-service/internal/modules/subscriptions"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
	httpware "github.com/Bengo-Hub/httpware"
)

type SubscriptionHandler struct {
	log            *zap.Logger
	client         *ent.Client
	db             *pgxpool.Pool
	service        *subscriptions.Service
	featureHandler *FeatureHandler
}

func NewSubscriptionHandler(log *zap.Logger, client *ent.Client, db *pgxpool.Pool, svc *subscriptions.Service, featureHandler *FeatureHandler) *SubscriptionHandler {
	return &SubscriptionHandler{
		log:            log.Named("subscription.handler"),
		client:         client,
		db:             db,
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
		// Check if this is the demo tenant — bypass subscription gating entirely
		if h.isDemoTenant(ctx, tenantID) {
			h.respondWithJSON(w, http.StatusOK, demoBypasResponse(tenantIDStr))
			return
		}
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
	if in.TenantID == uuid.Nil {
		if tenantIDStr := resolveTenantID(r); tenantIDStr != "" {
			in.TenantID, _ = uuid.Parse(tenantIDStr)
		}
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
	if in.TenantID == uuid.Nil {
		if tenantIDStr := resolveTenantID(r); tenantIDStr != "" {
			in.TenantID, _ = uuid.Parse(tenantIDStr)
		}
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
	if in.TenantID == uuid.Nil {
		if tenantIDStr := resolveTenantID(r); tenantIDStr != "" {
			in.TenantID, _ = uuid.Parse(tenantIDStr)
		}
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
		if h.isDemoTenant(r.Context(), tenantID) {
			resp := demoBypasResponse(tenantIDStr)
			resp["usage_limits"] = map[string]any{}
			h.respondWithJSON(w, http.StatusOK, resp)
			return
		}
		h.log.Debug("subscription not found for tenant", zap.String("tenant_id", tenantIDStr), zap.Error(err))
		h.respondWithError(w, http.StatusNotFound, "subscription not found")
		return
	}

	// Build the response: embed the subscription result then append current-month usage_limits
	// so frontend services can cache it in IndexedDB for offline enforcement.
	raw, _ := json.Marshal(sub)
	var resp map[string]any
	_ = json.Unmarshal(raw, &resp)
	resp["usage_limits"] = h.buildUsageLimits(r.Context(), tenantID, sub)

	h.respondWithJSON(w, http.StatusOK, resp)
}

// buildUsageLimits queries current-month usage and pairs it with plan limits.
// Returns an empty map on any error so the subscription response is never blocked.
func (h *SubscriptionHandler) buildUsageLimits(ctx context.Context, tenantID uuid.UUID, sub *subscriptions.SubscriptionResult) map[string]any {
	result := map[string]any{}
	if h.db == nil || sub == nil {
		return result
	}

	now := time.Now().UTC()
	periodStart := now.AddDate(0, -1, 0)

	rows, err := h.db.Query(ctx, `
		SELECT metric_type, SUM(value) as total
		FROM usage_events
		WHERE tenant_id = $1 AND created_at >= $2 AND created_at <= $3
		GROUP BY metric_type
	`, tenantID, periodStart, now)
	if err != nil {
		return result
	}
	defer rows.Close()

	usedByType := map[string]int{}
	for rows.Next() {
		var metricType string
		var total float64
		if err := rows.Scan(&metricType, &total); err == nil {
			usedByType[metricType] = int(total)
		}
	}

	for metricType, limit := range sub.Limits {
		result[metricType] = map[string]int{
			"used":  usedByType[metricType],
			"limit": limit,
		}
	}
	// Also include metrics that have usage but no plan limit
	for metricType, used := range usedByType {
		if _, exists := result[metricType]; !exists {
			result[metricType] = map[string]int{"used": used, "limit": 0}
		}
	}

	return result
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
	tenantIDStr := resolveTenantID(r) // platform owners can pass X-Tenant-ID to manage a tenant's settings
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

	usageThreshold := 80
	if v, ok := metadata["usage_threshold"]; ok {
		switch t := v.(type) {
		case float64:
			usageThreshold = int(t)
		case int:
			usageThreshold = t
		}
	}

	settings := map[string]any{
		"autoRenew":              boolFromMeta(metadata, "auto_renew", true),
		"billingEmail":           metadata["billing_email"],
		"notifyBeforeRenewal":    boolFromMeta(metadata, "email_notifications", true),
		"notifyOnUsageThreshold": boolFromMeta(metadata, "usage_alerts", false),
		"usageThresholdPercent":  usageThreshold,
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
	tenantIDStr := resolveTenantID(r)
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

	// Merge settings into metadata — map frontend camelCase keys to storage snake_case keys
	metadata := sub.Metadata
	if metadata == nil {
		metadata = map[string]any{}
	}
	keyMap := map[string]string{
		"autoRenew":              "auto_renew",
		"billingEmail":           "billing_email",
		"notifyBeforeRenewal":    "email_notifications",
		"notifyOnUsageThreshold": "usage_alerts",
		"usageThresholdPercent":  "usage_threshold",
	}
	for k, v := range req {
		if storageKey, ok := keyMap[k]; ok {
			metadata[storageKey] = v
		} else {
			metadata[k] = v
		}
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

// isDemoTenant returns true if the local tenant record for tenantID has slug "codevertex-demo".
// The codevertex-demo tenant has no subscription record and bypasses all subscription gating.
func (h *SubscriptionHandler) isDemoTenant(ctx context.Context, tenantID uuid.UUID) bool {
	t, err := h.client.Tenant.Query().
		Where(enttenant.IDEQ(tenantID)).
		Only(ctx)
	if err != nil {
		return false
	}
	return t.Slug == "codevertex-demo"
}

// demoBypasResponse returns a synthetic always-active subscription for the demo tenant.
func demoBypasResponse(tenantIDStr string) map[string]any {
	now := time.Now()
	return map[string]any{
		"id":                   uuid.Nil.String(),
		"tenant_id":            tenantIDStr,
		"plan_code":            "DEMO_UNLIMITED",
		"plan_name":            "Demo (No Subscription Required)",
		"status":               "ACTIVE",
		"billing_cycle":        "MONTHLY",
		"current_period_start": now.Format(time.RFC3339),
		"current_period_end":   now.AddDate(1, 0, 0).Format(time.RFC3339),
		"features":             []string{},
		"limits":               map[string]any{},
		"is_demo_bypass":       true,
	}
}

func boolFromMeta(m map[string]any, key string, defaultVal bool) bool {
	v, ok := m[key]
	if !ok {
		return defaultVal
	}
	if b, ok := v.(bool); ok {
		return b
	}
	return defaultVal
}

func (h *SubscriptionHandler) respondWithError(w http.ResponseWriter, code int, message string) {
	h.respondWithJSON(w, code, map[string]string{"error": message})
}

func (h *SubscriptionHandler) respondWithJSON(w http.ResponseWriter, code int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(payload)
}

// ExtendTrial handles POST /admin/tenants/{tenant_id}/subscription/extend-trial
// Extends the trial end date for a TRIAL subscription (platform admin only).
func (h *SubscriptionHandler) ExtendTrial(w http.ResponseWriter, r *http.Request) {
	if !httpware.IsPlatformOwner(r.Context()) {
		h.respondWithError(w, http.StatusForbidden, "forbidden")
		return
	}

	tenantIDStr := chi.URLParam(r, "tenant_id")
	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		h.respondWithError(w, http.StatusBadRequest, "invalid tenant_id")
		return
	}

	var body struct {
		TrialEndsAt string `json:"trial_ends_at"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.TrialEndsAt == "" {
		h.respondWithError(w, http.StatusBadRequest, "trial_ends_at is required (RFC3339)")
		return
	}

	newTrialEnd, err := time.Parse(time.RFC3339, body.TrialEndsAt)
	if err != nil {
		h.respondWithError(w, http.StatusBadRequest, "trial_ends_at must be RFC3339")
		return
	}

	ctx := r.Context()
	sub, err := h.client.TenantSubscription.Query().
		Where(tenantsubscription.TenantIDEQ(tenantID)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			h.respondWithError(w, http.StatusNotFound, "subscription not found")
			return
		}
		h.respondWithError(w, http.StatusInternalServerError, "internal error")
		return
	}

	if sub.Status != tenantsubscription.StatusTRIAL {
		h.respondWithError(w, http.StatusBadRequest, "subscription is not in trial")
		return
	}

	updated, err := h.client.TenantSubscription.UpdateOneID(sub.ID).
		SetTrialEndsAt(newTrialEnd).
		SetCurrentPeriodEnd(newTrialEnd).
		Save(ctx)
	if err != nil {
		h.log.Error("extend trial failed", zap.String("tenant_id", tenantIDStr), zap.Error(err))
		h.respondWithError(w, http.StatusInternalServerError, "failed to extend trial")
		return
	}

	h.log.Info("trial extended by admin", zap.String("tenant_id", tenantIDStr), zap.Time("trial_ends_at", newTrialEnd))
	h.respondWithJSON(w, http.StatusOK, map[string]any{
		"status":        "extended",
		"trial_ends_at": updated.TrialEndsAt.Format(time.RFC3339),
	})
}
