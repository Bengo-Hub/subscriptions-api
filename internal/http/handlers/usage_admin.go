package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	authclient "github.com/Bengo-Hub/shared-auth-client"
	httpware "github.com/Bengo-Hub/httpware"
	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/tenantsubscription"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// UsageAdminHandler provides platform-admin endpoints for viewing and manually overriding
// per-tenant usage metrics. Used when NATS events are lost or corrections are required.
type UsageAdminHandler struct {
	log   *zap.Logger
	db    *pgxpool.Pool
	orm   *ent.Client
	cache *redis.Client
}

// NewUsageAdminHandler creates a new UsageAdminHandler.
func NewUsageAdminHandler(log *zap.Logger, db *pgxpool.Pool, orm *ent.Client, cache *redis.Client) *UsageAdminHandler {
	return &UsageAdminHandler{
		log:   log.Named("usage.admin.handler"),
		db:    db,
		orm:   orm,
		cache: cache,
	}
}

type adminUsageMetric struct {
	MetricType  string  `json:"metric_type"`
	Period      string  `json:"period"`
	Used        float64 `json:"used"`
	Limit       int     `json:"limit"`
	PeriodStart string  `json:"period_start,omitempty"`
	PeriodEnd   string  `json:"period_end,omitempty"`
}

// GetTenantUsage handles GET /admin/usage/tenants/{tenant_id}
// Returns aggregated usage metrics for a specific tenant (platform admin only).
func (h *UsageAdminHandler) GetTenantUsage(w http.ResponseWriter, r *http.Request) {
	if !httpware.IsPlatformOwner(r.Context()) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}

	tenantIDStr := chi.URLParam(r, "tenant_id")
	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid tenant_id"})
		return
	}

	ctx := r.Context()

	// Fetch plan limits
	planLimits := map[string]any{}
	if h.orm != nil {
		sub, serr := h.orm.TenantSubscription.Query().
			Where(tenantsubscription.TenantIDEQ(tenantID)).
			WithPlan().
			Only(ctx)
		if serr == nil && sub.Edges.Plan != nil && sub.Edges.Plan.TierLimitsJSON != nil {
			planLimits = sub.Edges.Plan.TierLimitsJSON
		}
	}

	// Current billing period
	period := time.Now().UTC().Format("2006-01")
	from := time.Now().UTC().AddDate(0, -1, 0)
	to := time.Now().UTC()

	rows, err := h.db.Query(ctx, `
		SELECT metric_type, SUM(value) as total, MIN(created_at), MAX(created_at)
		FROM usage_events
		WHERE tenant_id = $1 AND created_at >= $2 AND created_at <= $3
		GROUP BY metric_type ORDER BY metric_type
	`, tenantID, from, to)
	if err != nil {
		h.log.Error("admin usage: query failed", zap.String("tenant_id", tenantIDStr), zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to query usage"})
		return
	}
	defer rows.Close()

	var metrics []adminUsageMetric
	for rows.Next() {
		var mt string
		var total float64
		var minT, maxT time.Time
		if err := rows.Scan(&mt, &total, &minT, &maxT); err != nil {
			continue
		}
		limit := 0
		if lk, ok := limitKeyForMetric(mt, planLimits); ok {
			switch v := planLimits[lk].(type) {
			case float64:
				limit = int(v)
			case int:
				limit = v
			}
		}
		metrics = append(metrics, adminUsageMetric{
			MetricType:  mt,
			Period:      period,
			Used:        total,
			Limit:       limit,
			PeriodStart: minT.Format(time.RFC3339),
			PeriodEnd:   maxT.Format(time.RFC3339),
		})
	}
	if metrics == nil {
		metrics = []adminUsageMetric{}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"tenant_id": tenantIDStr,
		"period":    period,
		"metrics":   metrics,
	})
}

type overrideMetricRequest struct {
	MetricType  string  `json:"metric_type"`
	Value       float64 `json:"value"`
	PeriodStart string  `json:"period_start,omitempty"`
	PeriodEnd   string  `json:"period_end,omitempty"`
	Reason      string  `json:"reason"`
}

// OverrideMetric handles PUT /admin/usage/tenants/{tenant_id}/override
// Overwrites the Redis counter and inserts a corrective usage_event row (platform admin only).
func (h *UsageAdminHandler) OverrideMetric(w http.ResponseWriter, r *http.Request) {
	if !httpware.IsPlatformOwner(r.Context()) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}

	tenantIDStr := chi.URLParam(r, "tenant_id")
	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid tenant_id"})
		return
	}

	var req overrideMetricRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if req.MetricType == "" || req.Reason == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "metric_type and reason are required"})
		return
	}

	ctx := r.Context()

	// Resolve admin user ID for audit trail
	adminUserID := ""
	if claims, ok := authclient.ClaimsFromContext(ctx); ok {
		adminUserID = claims.Subject
	}

	period := time.Now().UTC().Format("2006-01")

	// Read old Redis value for audit
	cacheKey := fmt.Sprintf("usage:limit:%s:%s:%s", tenantID.String(), req.MetricType, period)
	oldVal := 0.0
	if h.cache != nil {
		if v, err := h.cache.Get(ctx, cacheKey).Float64(); err == nil {
			oldVal = v
		}

		// Set Redis counter to the override value
		if err := h.cache.Set(ctx, cacheKey, req.Value, 0).Err(); err != nil {
			h.log.Warn("usage override: failed to update redis counter", zap.String("key", cacheKey), zap.Error(err))
		}
		// Re-apply TTL
		now := time.Now().UTC()
		expiry := time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, time.UTC)
		_ = h.cache.ExpireAt(ctx, cacheKey, expiry).Err()
	}

	// Insert a corrective usage_event row with audit metadata
	meta, _ := json.Marshal(map[string]any{
		"override":       true,
		"admin_user_id":  adminUserID,
		"reason":         req.Reason,
		"old_value":      oldVal,
		"new_value":      req.Value,
		"overridden_at":  time.Now().UTC().Format(time.RFC3339),
	})
	now := time.Now().UTC()
	_, err = h.db.Exec(ctx, `
		INSERT INTO usage_events (id, tenant_id, metric_type, service_name, value, period_start, period_end, metadata, created_at)
		VALUES ($1, $2, $3, 'admin_override', $4, $5, $6, $7, $8)
	`, uuid.New(), tenantID, req.MetricType, req.Value, now.AddDate(0, -1, 0), now, meta, now)
	if err != nil {
		h.log.Error("usage override: failed to insert event", zap.String("tenant_id", tenantIDStr), zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to record override"})
		return
	}

	h.log.Info("usage metric overridden by admin",
		zap.String("tenant_id", tenantIDStr),
		zap.String("metric_type", req.MetricType),
		zap.Float64("old_value", oldVal),
		zap.Float64("new_value", req.Value),
		zap.String("admin", adminUserID),
	)

	writeJSON(w, http.StatusOK, map[string]any{
		"status":      "overridden",
		"metric_type": req.MetricType,
		"old_value":   oldVal,
		"new_value":   req.Value,
		"period":      period,
	})
}
