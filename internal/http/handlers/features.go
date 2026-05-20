package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/bengobox/subscription-service/internal/modules/subscriptions"
)

const featureCacheTTL = 60 * time.Second

// FeatureHandler serves feature gate check and entitlement endpoints.
type FeatureHandler struct {
	log     *zap.Logger
	service *subscriptions.Service
	cache   *redis.Client
}

// NewFeatureHandler creates a new FeatureHandler.
func NewFeatureHandler(log *zap.Logger, svc *subscriptions.Service, cache *redis.Client) *FeatureHandler {
	return &FeatureHandler{
		log:     log.Named("features.handler"),
		service: svc,
		cache:   cache,
	}
}

type featureCheckResponse struct {
	TenantID    string `json:"tenant_id"`
	FeatureCode string `json:"feature_code"`
	Allowed     bool   `json:"allowed"`
	LimitValue  *int   `json:"limit_value,omitempty"`
	Source      string `json:"source"` // "cache" or "db"
}

// CheckFeature godoc
// @Summary Check feature entitlement
// @Description Checks if a specific feature is available for the tenant's subscription. Cached for 60s.
// @Tags Features
// @Produce json
// @Security BearerAuth
// @Param code path string true "Feature code"
// @Success 200 {object} map[string]interface{}
// @Failure 403 {object} map[string]string
// @Router /features/{code}/check [get]
func (h *FeatureHandler) CheckFeature(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	featureCode := chi.URLParam(r, "code")
	if featureCode == "" {
		http.Error(w, `{"error":"feature code required"}`, http.StatusBadRequest)
		return
	}

	tenantIDStr := resolveTenantID(r)
	if tenantIDStr == "" {
		http.Error(w, `{"error":"tenant_id required"}`, http.StatusBadRequest)
		return
	}
	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		http.Error(w, `{"error":"invalid tenant_id"}`, http.StatusBadRequest)
		return
	}

	// Try Redis cache first
	if h.cache != nil {
		cacheKey := featureCacheKey(tenantID, featureCode)
		if val, err := h.cache.Get(ctx, cacheKey).Result(); err == nil {
			var cached featureCheckResponse
			if jsonErr := json.Unmarshal([]byte(val), &cached); jsonErr == nil {
				cached.Source = "cache"
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(cached)
				return
			}
		}
	}

	// Cache miss — query DB
	result, err := h.service.GetSubscriptionResult(ctx, tenantID)
	if err != nil {
		h.log.Warn("subscription not found for feature check", zap.String("tenant_id", tenantIDStr), zap.Error(err))
		// No subscription = deny
		resp := featureCheckResponse{
			TenantID:    tenantIDStr,
			FeatureCode: featureCode,
			Allowed:     false,
			Source:      "db",
		}
		writeJSON(w, http.StatusOK, resp)
		return
	}

	allowed := false
	for _, f := range result.Features {
		if f == featureCode {
			allowed = true
			break
		}
	}

	resp := featureCheckResponse{
		TenantID:    tenantIDStr,
		FeatureCode: featureCode,
		Allowed:     allowed,
		Source:      "db",
	}

	// Cache the result
	if h.cache != nil {
		if b, err := json.Marshal(resp); err == nil {
			cacheKey := featureCacheKey(tenantID, featureCode)
			_ = h.cache.Set(ctx, cacheKey, b, featureCacheTTL).Err()
		}
	}

	writeJSON(w, http.StatusOK, resp)
}

// GetEntitlements godoc
// @Summary Get all feature entitlements
// @Description Returns the full set of features and limits for the tenant's current subscription
// @Tags Features
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{}
// @Router /features [get]
func (h *FeatureHandler) GetEntitlements(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tenantIDStr := resolveTenantID(r)
	if tenantIDStr == "" {
		http.Error(w, `{"error":"tenant_id required"}`, http.StatusBadRequest)
		return
	}
	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		http.Error(w, `{"error":"invalid tenant_id"}`, http.StatusBadRequest)
		return
	}

	// Try entitlements cache
	cacheKey := entitlementsCacheKey(tenantID)
	if h.cache != nil {
		if val, err := h.cache.Get(ctx, cacheKey).Result(); err == nil {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-Cache", "HIT")
			_, _ = w.Write([]byte(val))
			return
		}
	}

	result, err := h.service.GetSubscriptionResult(ctx, tenantID)
	if err != nil {
		h.log.Error("failed to get subscription entitlements", zap.Error(err))
		http.Error(w, `{"error":"subscription not found"}`, http.StatusNotFound)
		return
	}

	resp := map[string]any{
		"tenant_id":  tenantIDStr,
		"plan_code":  result.PlanCode,
		"status":     result.Status,
		"features":   result.Features,
		"limits":     result.Limits,
	}

	if b, err := json.Marshal(resp); err == nil {
		if h.cache != nil {
			_ = h.cache.Set(ctx, cacheKey, b, featureCacheTTL).Err()
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Cache", "MISS")
		_, _ = w.Write(b)
	}
}

// InvalidateCache removes cached feature/entitlement data for a tenant.
// Called internally after subscription mutations.
func (h *FeatureHandler) InvalidateCache(ctx context.Context, tenantID uuid.UUID) {
	if h.cache == nil {
		return
	}
	// Delete entitlements cache and any individual feature cache keys via pattern
	_ = h.cache.Del(ctx, entitlementsCacheKey(tenantID)).Err()
	// Individual feature keys follow subscription:feature:{tenantID}:* pattern
	pattern := fmt.Sprintf("subscription:feature:%s:*", tenantID.String())
	keys, err := h.cache.Keys(ctx, pattern).Result()
	if err == nil && len(keys) > 0 {
		_ = h.cache.Del(ctx, keys...).Err()
	}
}

func featureCacheKey(tenantID uuid.UUID, featureCode string) string {
	return fmt.Sprintf("subscription:feature:%s:%s", tenantID.String(), featureCode)
}

func entitlementsCacheKey(tenantID uuid.UUID) string {
	return fmt.Sprintf("subscription:entitlements:%s", tenantID.String())
}

