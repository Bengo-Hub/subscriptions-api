package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	httpware "github.com/Bengo-Hub/httpware"
	authclient "github.com/Bengo-Hub/shared-auth-client"
	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/tenantsubscription"
	"github.com/bengobox/subscription-service/internal/modules/billing"
	"github.com/bengobox/subscription-service/internal/modules/subscriptions"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

type SubscriptionHandler struct {
	log            *zap.Logger
	client         *ent.Client
	db             *pgxpool.Pool
	service        *subscriptions.Service
	featureHandler *FeatureHandler
	overage        *billing.OverageService
}

func NewSubscriptionHandler(log *zap.Logger, client *ent.Client, db *pgxpool.Pool, svc *subscriptions.Service, featureHandler *FeatureHandler, overage *billing.OverageService) *SubscriptionHandler {
	return &SubscriptionHandler{
		log:            log.Named("subscription.handler"),
		client:         client,
		db:             db,
		service:        svc,
		featureHandler: featureHandler,
		overage:        overage,
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
		// Demo + platform-owner tenants bypass subscription gating entirely.
		if h.service.IsExemptTenant(ctx, tenantID) {
			h.respondWithJSON(w, http.StatusOK, demoBypasResponse(tenantIDStr))
			return
		}
		h.log.Error("failed to get subscription", zap.Error(err))
		h.respondWithError(w, http.StatusNotFound, "subscription not found")
		return
	}

	h.respondWithJSON(w, http.StatusOK, sub)
}

// GetReferralCode godoc
// @Summary Get the tenant's referral code
// @Description Returns (creating if needed) the calling tenant's shareable Type-A referral code.
// @Tags Subscriptions
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Router /subscription/referral-code [get]
func (h *SubscriptionHandler) GetReferralCode(w http.ResponseWriter, r *http.Request) {
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
	code, err := h.service.GetOrCreateReferralCode(ctx, tenantID)
	if err != nil {
		h.respondWithError(w, http.StatusNotFound, "no subscription for tenant")
		return
	}
	h.respondWithJSON(w, http.StatusOK, map[string]any{"referral_code": code})
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

	// Stamp the accepting user from the JWT when the client didn't supply one.
	if in.TermsAcceptedBy == uuid.Nil {
		if claims, ok := authclient.ClaimsFromContext(ctx); ok {
			if uid, uerr := claims.UserID(); uerr == nil {
				in.TermsAcceptedBy = uid
			}
		}
	}

	sub, err := h.service.CreateSubscription(ctx, in)
	if err != nil {
		if subscriptions.IsExemptErr(err) {
			h.respondWithJSON(w, http.StatusOK, demoBypasResponse(in.TenantID.String()))
			return
		}
		if errors.Is(err, subscriptions.ErrTermsNotAccepted) {
			h.respondWithError(w, http.StatusBadRequest, err.Error())
			return
		}
		h.log.Error("failed to create subscription", zap.Error(err))
		h.respondWithError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if h.featureHandler != nil {
		h.featureHandler.InvalidateCache(ctx, sub.TenantID)
	}

	h.respondWithJSON(w, http.StatusCreated, sub)
}

// GetTerms returns the current subscription Terms & Conditions (version + text) for the
// subscribe flow to display. Public — no subscription required to read the terms.
func (h *SubscriptionHandler) GetTerms(w http.ResponseWriter, r *http.Request) {
	h.respondWithJSON(w, http.StatusOK, map[string]any{
		"version": subscriptions.CurrentTermsVersion,
		"content": subscriptions.SubscriptionTerms,
	})
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
		if subscriptions.IsExemptErr(err) {
			h.respondWithJSON(w, http.StatusOK, demoBypasResponse(in.TenantID.String()))
			return
		}
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
		if subscriptions.IsExemptErr(err) {
			// Demo/platform tenants never check out — report an already-active no-op.
			h.respondWithJSON(w, http.StatusOK, map[string]any{"status": "active", "is_bypass": true})
			return
		}
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
		if h.service.IsExemptTenant(r.Context(), tenantID) {
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
	// usage_limits requires a per-tenant aggregate over usage_events. S2S callers
	// on the login critical path (e.g. auth-api JWT enrichment) don't use it, so
	// they pass ?include_usage=false to skip the aggregate. Defaults to including
	// it for the frontend, which caches it for offline enforcement.
	if r.URL.Query().Get("include_usage") != "false" {
		resp["usage_limits"] = h.buildUsageLimits(r.Context(), tenantID, sub)
	}

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

	// planLimits as map[string]any so the SAME metric<->limit-key matching the live enforcement
	// path uses (limitKeyForMetric/inferMetricType, both in usage.go) resolves here too. A naive
	// exact-key lookup (metricType == limitKey) would miss EVERY metric: usage_events.metric_type
	// is always a short name ("orders") while plan limits are always prefixed
	// (max_orders_per_month, inventory_max_sku, …). That mismatch used to fall into the "no plan
	// limit configured" branch below and report limit:0 for every metric — making even an
	// unlimited (-1) top-tier plan look like it was already over its limit.
	planLimits := make(map[string]any, len(sub.Limits))
	for k, v := range sub.Limits {
		planLimits[k] = v
	}

	// Start from configured plan limits (so a metric with zero usage this period still surfaces
	// with its real limit, not only metrics that already have events).
	for limitKey, limit := range sub.Limits {
		metricType := inferMetricType(limitKey)
		if metricType == "" {
			continue
		}
		if _, exists := result[metricType]; exists {
			continue
		}
		result[metricType] = map[string]int{"used": usedByType[metricType], "limit": limit}
	}
	// Any metric with usage not already resolved above — fuzzy-match it to its real plan-limit
	// key before falling back to "no plan limit configured" (0).
	for metricType, used := range usedByType {
		if _, exists := result[metricType]; exists {
			continue
		}
		limit := 0
		if limitKey, ok := limitKeyForMetric(metricType, planLimits); ok {
			limit = sub.Limits[limitKey]
		}
		result[metricType] = map[string]int{"used": used, "limit": limit}
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

// UpdateBillingCycle godoc
// @Summary Change billing period
// @Description Switches the tenant's billing period (MONTHLY, SEMI_ANNUAL, ANNUAL) effective
// from the next renewal. Periods of 6+ months waive the one-time setup fee if it has not
// been charged yet.
// @Tags Subscriptions
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]string
// @Router /subscription/billing-cycle [put]
func (h *SubscriptionHandler) UpdateBillingCycle(w http.ResponseWriter, r *http.Request) {
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

	var body struct {
		BillingCycle string `json:"billing_cycle"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.BillingCycle == "" {
		h.respondWithError(w, http.StatusBadRequest, "billing_cycle required")
		return
	}

	res, err := h.service.UpdateBillingCycle(ctx, tenantID, body.BillingCycle)
	if err != nil {
		if subscriptions.IsExemptErr(err) {
			h.respondWithJSON(w, http.StatusOK, demoBypasResponse(tenantIDStr))
			return
		}
		h.respondWithError(w, http.StatusBadRequest, err.Error())
		return
	}

	if h.featureHandler != nil {
		h.featureHandler.InvalidateCache(ctx, tenantID)
	}
	h.respondWithJSON(w, http.StatusOK, res)
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

// EnableOverage godoc
// @Summary Enable extra usage (overage)
// @Description Opt the tenant in to pay-as-you-go extra usage. Once enabled, metered
// throughput limits may be exceeded and the excess accrues to the next renewal invoice.
// @Tags Subscriptions
// @Security BearerAuth
// @Success 200 {object} map[string]interface{}
// @Router /subscription/overage/enable [post]
func (h *SubscriptionHandler) EnableOverage(w http.ResponseWriter, r *http.Request) {
	h.setOverage(w, r, true)
}

// DisableOverage godoc
// @Summary Disable extra usage (overage)
// @Tags Subscriptions
// @Security BearerAuth
// @Success 200 {object} map[string]interface{}
// @Router /subscription/overage/disable [post]
func (h *SubscriptionHandler) DisableOverage(w http.ResponseWriter, r *http.Request) {
	h.setOverage(w, r, false)
}

func (h *SubscriptionHandler) setOverage(w http.ResponseWriter, r *http.Request, enabled bool) {
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

	res, err := h.service.SetAllowOverage(ctx, tenantID, enabled)
	if err != nil {
		// Demo/platform tenants have no real subscription — treat as a no-op success.
		if h.service.IsExemptTenant(ctx, tenantID) || httpware.IsPlatformOwner(ctx) {
			h.respondWithJSON(w, http.StatusOK, map[string]any{"allow_overage": enabled, "is_bypass": true})
			return
		}
		h.log.Error("failed to set allow_overage", zap.Error(err))
		h.respondWithError(w, http.StatusNotFound, "subscription not found")
		return
	}
	h.respondWithJSON(w, http.StatusOK, res)
}

// GetOverage godoc
// @Summary Get extra-usage status and pending overage
// @Description Returns the allow_overage flag, the total pending (un-invoiced) overage in
// KES, and a per-metric breakdown for the current period.
// @Tags Subscriptions
// @Security BearerAuth
// @Success 200 {object} map[string]interface{}
// @Router /subscription/overage [get]
func (h *SubscriptionHandler) GetOverage(w http.ResponseWriter, r *http.Request) {
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

	allowOverage := false
	if sub, serr := h.service.GetSubscriptionResult(ctx, tenantID); serr == nil {
		allowOverage = sub.AllowOverage
	}

	pendingTotal := 0.0
	breakdown := []map[string]any{}
	if h.overage != nil {
		if total, terr := h.overage.GetAccumulatedOverage(ctx, tenantID); terr == nil {
			pendingTotal = total
		}
		if charges, cerr := h.overage.ListPendingByTenant(ctx, tenantID); cerr == nil {
			for _, c := range charges {
				breakdown = append(breakdown, map[string]any{
					"metric_type":      c.MetricType,
					"period_date":      c.PeriodDate.Format("2006-01-02"),
					"units_over":       c.UnitsOver,
					"plan_limit":       c.PlanLimit,
					"unit_price_kes":   c.UnitPriceKes,
					"total_charge_kes": c.TotalChargeKes,
				})
			}
		}
	}

	h.respondWithJSON(w, http.StatusOK, map[string]any{
		"allow_overage":     allowOverage,
		"pending_total_kes": pendingTotal,
		"breakdown":         breakdown,
	})
}

// demoBypasResponse returns a synthetic always-active subscription for an exempt
// (demo / platform-owner) tenant that owns no real subscription record.
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
