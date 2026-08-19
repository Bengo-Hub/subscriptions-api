package handlers

import (
	"net/http"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/ratelimitconfig"
	"github.com/bengobox/subscription-service/internal/ent/tenantsubscription"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// RateLimitHandler resolves the effective rate limit a calling service should enforce for a
// given tenant. This is the first real consumer of RateLimitConfig (previously seeded but never
// read anywhere) and gives external API products (e.g. the eTIMS external API) a per-plan-tier
// requests/minute quota alongside the flat monthly transaction quota already in tier_limits_json.
type RateLimitHandler struct {
	log *zap.Logger
	orm *ent.Client
}

// NewRateLimitHandler creates a new RateLimitHandler.
func NewRateLimitHandler(log *zap.Logger, orm *ent.Client) *RateLimitHandler {
	return &RateLimitHandler{log: log.Named("ratelimit.handler"), orm: orm}
}

type rateLimitResponse struct {
	RequestsPerWindow int     `json:"requests_per_window"`
	WindowSeconds     int     `json:"window_seconds"`
	BurstMultiplier   float64 `json:"burst_multiplier"`
	// Source reports which tier of the resolution order produced this result: "plan" (the
	// tenant's own plan tier_limits_json), "config" (a RateLimitConfig fallback row), or
	// "default" (hardcoded safe floor) — callers can log/monitor this to catch misconfiguration.
	Source string `json:"source"`
}

// GetEffectiveRateLimit handles GET /api/v1/tenants/{tenant_id}/rate-limit?service_name=X&endpoint=Y
// S2S endpoint (same API-key-or-platform-owner-JWT gate as GetByTenantID) — resolves the rate
// limit a calling service should enforce for this tenant against this endpoint pattern.
//
// Resolution order:
//  1. The tenant's active plan's tier_limits_json["api_requests_per_minute"] — lets a specific
//     paid tier (e.g. ETIMS_API_GROWTH) grant a higher quota than the platform default.
//  2. A RateLimitConfig row for service_name, matched on the exact endpoint pattern first, then
//     the service's "*" default row.
//  3. A hardcoded safe default (60 req/min), so a misconfigured tenant/service never resolves to
//     "unlimited" by accident.
//
// GetEffectiveRateLimit godoc
// @Summary Resolve effective rate limit (S2S)
// @Description Resolves the requests/minute ceiling a calling service should enforce for a tenant against a service+endpoint pattern. Used by treasury-api's external eTIMS API middleware.
// @Tags Subscriptions
// @Produce json
// @Security BearerAuth
// @Security ApiKeyAuth
// @Param tenant_id path string true "Tenant UUID"
// @Param service_name query string true "Calling service name, e.g. treasury-api"
// @Param endpoint query string false "Endpoint pattern, e.g. /api/v1/external/etims/*"
// @Success 200 {object} rateLimitResponse
// @Failure 400 {object} map[string]string
// @Router /tenants/{tenant_id}/rate-limit [get]
func (h *RateLimitHandler) GetEffectiveRateLimit(w http.ResponseWriter, r *http.Request) {
	tenantIDStr := chi.URLParam(r, "tenant_id")
	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid tenant_id"})
		return
	}
	serviceName := r.URL.Query().Get("service_name")
	if serviceName == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "service_name required"})
		return
	}
	endpoint := r.URL.Query().Get("endpoint")
	if endpoint == "" {
		endpoint = "*"
	}

	ctx := r.Context()

	if h.orm != nil {
		sub, serr := h.orm.TenantSubscription.Query().
			Where(tenantsubscription.TenantIDEQ(tenantID)).
			WithPlan().
			Only(ctx)
		if serr == nil && sub.Edges.Plan != nil {
			if raw, ok := sub.Edges.Plan.TierLimitsJSON["api_requests_per_minute"]; ok {
				if n, ok := toPositiveInt(raw); ok {
					writeJSON(w, http.StatusOK, rateLimitResponse{
						RequestsPerWindow: n,
						WindowSeconds:     60,
						BurstMultiplier:   1.5,
						Source:            "plan",
					})
					return
				}
			}
		}

		cfg, cerr := h.orm.RateLimitConfig.Query().
			Where(
				ratelimitconfig.ServiceNameEQ(serviceName),
				ratelimitconfig.KeyTypeEQ("tenant"),
				ratelimitconfig.EndpointPatternEQ(endpoint),
				ratelimitconfig.IsActiveEQ(true),
			).
			First(ctx)
		if cerr != nil {
			cfg, cerr = h.orm.RateLimitConfig.Query().
				Where(
					ratelimitconfig.ServiceNameEQ(serviceName),
					ratelimitconfig.KeyTypeEQ("tenant"),
					ratelimitconfig.EndpointPatternEQ("*"),
					ratelimitconfig.IsActiveEQ(true),
				).
				First(ctx)
		}
		if cerr == nil && cfg != nil {
			writeJSON(w, http.StatusOK, rateLimitResponse{
				RequestsPerWindow: cfg.RequestsPerWindow,
				WindowSeconds:     cfg.WindowSeconds,
				BurstMultiplier:   cfg.BurstMultiplier,
				Source:            "config",
			})
			return
		}
	}

	writeJSON(w, http.StatusOK, rateLimitResponse{
		RequestsPerWindow: 60,
		WindowSeconds:     60,
		BurstMultiplier:   1.5,
		Source:            "default",
	})
}

func toPositiveInt(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), n > 0
	case int:
		return n, n > 0
	case int64:
		return int(n), n > 0
	default:
		return 0, false
	}
}
