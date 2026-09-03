package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/tenantsubscription"
	"github.com/bengobox/subscription-service/internal/modules/billing"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// UsageHandler accepts usage metric reports from microservices.
type UsageHandler struct {
	log     *zap.Logger
	db      *pgxpool.Pool
	orm     *ent.Client
	cache   *redis.Client
	overage *billing.OverageService
}

// NewUsageHandler creates a new UsageHandler.
func NewUsageHandler(log *zap.Logger, db *pgxpool.Pool, orm *ent.Client, cache *redis.Client, overage *billing.OverageService) *UsageHandler {
	return &UsageHandler{
		log:     log.Named("usage.handler"),
		db:      db,
		orm:     orm,
		cache:   cache,
		overage: overage,
	}
}

type reportUsageRequest struct {
	MetricType  string         `json:"metric_type"`
	ServiceName string         `json:"service_name"`
	Value       float64        `json:"value"`
	PeriodStart *time.Time     `json:"period_start,omitempty"`
	PeriodEnd   *time.Time     `json:"period_end,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

// ReportUsage godoc
// @Summary Report usage
// @Description Ingests a usage event from microservices for tracking consumption against subscription limits
// @Tags Usage
// @Accept json
// @Produce json
// @Security BearerAuth
// @Security ApiKeyAuth
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]string
// @Failure 429 {object} map[string]string "Usage limit exceeded — X-RateLimit-Limit, X-RateLimit-Remaining, X-RateLimit-Reset headers included"
// @Failure 500 {object} map[string]string
// @Router /usage/report [post]
func (h *UsageHandler) ReportUsage(w http.ResponseWriter, r *http.Request) {
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

	var req reportUsageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	if req.MetricType == "" || req.ServiceName == "" {
		http.Error(w, `{"error":"metric_type and service_name are required"}`, http.StatusBadRequest)
		return
	}
	if req.Metadata == nil {
		req.Metadata = map[string]any{}
	}

	// Enforce plan limits via Redis counter before recording event. When the limit is
	// exceeded we either soft-cap (allow + accrue overage) if the tenant opted in and the
	// metric is overage-eligible with a seeded price, or hard-block with a structured body
	// the UI limit-reached modal consumes.
	if h.cache != nil && h.orm != nil {
		dec := h.evaluateUsage(ctx, tenantID, req.MetricType, req.Value)
		if dec.limit > 0 {
			w.Header().Set("X-RateLimit-Limit", fmt.Sprintf("%d", dec.limit))
			w.Header().Set("X-RateLimit-Remaining", fmt.Sprintf("%d", dec.remaining))
		}
		if dec.exceeded {
			if dec.allowOverage {
				// Soft-cap: permit the event; the daily OverageService accrues the charge.
				w.Header().Set("X-Overage-Active", "true")
			} else {
				// Roll back the Redis increment so the rejected attempt isn't counted.
				h.rollbackUsageCounter(ctx, dec.cacheKey, req.Value)
				w.Header().Set("X-RateLimit-Remaining", "0")
				if dec.periodEnd != nil {
					w.Header().Set("X-RateLimit-Reset", dec.periodEnd.Format(time.RFC3339))
				}
				writeJSON(w, http.StatusPaymentRequired, map[string]any{
					"code":                "usage_limit_exceeded",
					"error":               "usage limit exceeded",
					"metric":              req.MetricType,
					"limit":               dec.limit,
					"used":                dec.used,
					"overage_eligible":    dec.overageEligible,
					"overage_unit_price":  dec.overageUnitPrice,
					"overage_unit":        dec.overageUnit,
					"accrued_overage_kes": dec.accruedOverageKes,
					"upgrade_url":         overageUpgradeURL,
				})
				return
			}
		}
	}

	metadataJSON, _ := json.Marshal(req.Metadata)
	eventID := uuid.New()
	now := time.Now()

	_, err = h.db.Exec(ctx, `
		INSERT INTO usage_events (id, tenant_id, metric_type, service_name, value, period_start, period_end, metadata, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`, eventID, tenantID, req.MetricType, req.ServiceName, req.Value, req.PeriodStart, req.PeriodEnd, metadataJSON, now)
	if err != nil {
		h.log.Error("failed to save usage event", zap.Error(err))
		http.Error(w, `{"error":"failed to record usage"}`, http.StatusInternalServerError)
		return
	}

	h.log.Info("usage event recorded",
		zap.String("tenant_id", tenantIDStr),
		zap.String("metric_type", req.MetricType),
		zap.String("service", req.ServiceName),
		zap.Float64("value", req.Value),
	)

	writeJSON(w, http.StatusCreated, map[string]any{
		"id":          eventID.String(),
		"tenant_id":   tenantID.String(),
		"metric_type": req.MetricType,
		"service":     req.ServiceName,
		"value":       req.Value,
		"created_at":  now.Format(time.RFC3339),
	})
}

// metricMeta holds display metadata for a metric type.
type metricMeta struct {
	name string
	unit string
}

// metricDisplayMap maps metric_type keys to display names and units.
var metricDisplayMap = map[string]metricMeta{
	"orders":          {name: "Orders", unit: "orders"},
	"riders":          {name: "Riders", unit: "riders"},
	"outlets":         {name: "Outlets", unit: "outlets"},
	"api_calls":       {name: "API Calls", unit: "calls"},
	"users":           {name: "Users", unit: "users"},
	"products":        {name: "Products", unit: "products"},
	"reports":         {name: "Reports", unit: "reports"},
	"drivers":         {name: "Drivers", unit: "drivers"},
	"vehicles":        {name: "Vehicles", unit: "vehicles"},
	"deliveries":      {name: "Deliveries", unit: "deliveries"},
	"contacts":        {name: "Contacts", unit: "contacts"},
	"campaigns":       {name: "Campaigns", unit: "campaigns"},
	"devices":         {name: "Devices", unit: "devices"},
	"warehouses":      {name: "Warehouses", unit: "warehouses"},
	"pos_terminals":   {name: "POS Terminals", unit: "terminals"},
	"transactions":    {name: "Transactions", unit: "transactions"},
	"inventory_items": {name: "Inventory Items", unit: "items"},
}

// limitKeyForMetric is retired — every caller now goes through billing.ResolveLimitKey,
// the single canonical metric->limit-key resolver (see usage_counter.go's doc comment for
// why two independent copies of this mapping was itself a bug).

type usageMetricRow struct {
	MetricType string  `json:"metric_type"`
	Total      float64 `json:"total"`
	EventCount int     `json:"event_count"`
}

type metricResult struct {
	Name      string `json:"name"`
	Key       string `json:"key"`
	Used      int    `json:"used"`
	Limit     int    `json:"limit"`
	Unit      string `json:"unit"`
	ResetDate string `json:"resetDate"`
}

// GetUsageSummary godoc
// @Summary Get usage summary
// @Description Returns aggregated usage metrics with plan limits for the current billing period
// @Tags Usage
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{}
// @Router /usage [get]
func (h *UsageHandler) GetUsageSummary(w http.ResponseWriter, r *http.Request) {
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

	// Fetch subscription + plan limits
	planName := ""
	periodStart := time.Time{}
	periodEnd := time.Time{}
	planLimits := map[string]any{}
	if h.orm != nil {
		sub, serr := h.orm.TenantSubscription.Query().
			Where(tenantsubscription.TenantIDEQ(tenantID)).
			WithPlan().
			Only(ctx)
		if serr == nil {
			periodStart = sub.CurrentPeriodStart
			periodEnd = sub.CurrentPeriodEnd
			if sub.Edges.Plan != nil {
				planName = sub.Edges.Plan.Name
				if sub.Edges.Plan.TierLimitsJSON != nil {
					planLimits = sub.Edges.Plan.TierLimitsJSON
				}
			}
		}
	}

	// Determine billing period: use the subscription's REAL current_period_start/end (not a
	// fixed 30-day approximation — pay-early renewal stacking and SEMI_ANNUAL/ANNUAL cycles
	// routinely make the actual period longer or shorter than one calendar month, which used
	// to silently under/over-count this aggregate against the Redis enforcement counter that
	// already keys correctly off current_period_start; see billing.UsageCounterKey), falling
	// back to the last 30 days when there's no subscription on file.
	from, to := parseUsageDateRange(r)
	if !periodEnd.IsZero() {
		to = periodEnd
		if !periodStart.IsZero() {
			from = periodStart
		} else {
			from = to.AddDate(0, -1, 0)
		}
	}
	resetDate := to.Format("2006-01-02")

	// Aggregate usage events
	rawMetrics, err := h.queryUsageSummary(ctx, tenantID, from, to, "")
	if err != nil {
		h.log.Error("failed to query usage summary", zap.Error(err))
		http.Error(w, `{"error":"failed to retrieve usage"}`, http.StatusInternalServerError)
		return
	}

	// Build metrics map: metric_type -> total
	usedByType := map[string]float64{}
	for _, m := range rawMetrics {
		usedByType[m.MetricType] = m.Total
	}

	// Build result: start from plan limits (so all configured metrics appear even if unused)
	seenKeys := map[string]bool{}
	var metrics []metricResult

	for limitKey, limitVal := range planLimits {
		// Parse limit value
		limit := 0
		switch v := limitVal.(type) {
		case float64:
			limit = int(v)
		case int:
			limit = v
		case int64:
			limit = int(v)
		}

		// Find matching metric type (e.g. "max_orders_per_month" -> "orders")
		metricType := inferMetricType(limitKey)
		if metricType == "" || seenKeys[metricType] {
			continue
		}
		seenKeys[metricType] = true

		meta, ok := metricDisplayMap[metricType]
		if !ok {
			// Derive display from limitKey
			displayKey := strings.TrimPrefix(strings.ToLower(limitKey), "max_")
			displayKey = strings.ReplaceAll(displayKey, "_per_day", "")
			displayKey = strings.ReplaceAll(displayKey, "_per_month", "")
			meta = metricMeta{
				name: strings.Title(strings.ReplaceAll(displayKey, "_", " ")),
				unit: displayKey,
			}
		}

		used := int(usedByType[metricType])
		metrics = append(metrics, metricResult{
			Name:      meta.name,
			Key:       metricType,
			Used:      used,
			Limit:     limit,
			Unit:      meta.unit,
			ResetDate: resetDate,
		})
	}

	// Also add any metric types that have usage but no plan limit
	for metricType, total := range usedByType {
		if seenKeys[metricType] {
			continue
		}
		meta, ok := metricDisplayMap[metricType]
		if !ok {
			meta = metricMeta{
				name: strings.Title(strings.ReplaceAll(metricType, "_", " ")),
				unit: metricType,
			}
		}
		metrics = append(metrics, metricResult{
			Name:      meta.name,
			Key:       metricType,
			Used:      int(total),
			Limit:     0,
			Unit:      meta.unit,
			ResetDate: resetDate,
		})
	}

	if metrics == nil {
		metrics = []metricResult{}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"metrics": metrics,
		"billingPeriod": map[string]string{
			"start": from.Format("2006-01-02"),
			"end":   to.Format("2006-01-02"),
		},
		"plan": planName,
	})
}

// GetUsageDashboard returns a simplified usage summary for the dashboard widget.
// @Summary Get usage dashboard summary
// @Description Returns key usage metrics as named objects for the dashboard
// @Tags Usage
// @Produce json
// @Security BearerAuth
// @Router /usage/summary [get]
func (h *UsageHandler) GetUsageDashboard(w http.ResponseWriter, r *http.Request) {
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

	// Fetch subscription + plan limits
	planLimits := map[string]any{}
	periodStart := time.Time{}
	periodEnd := time.Time{}
	if h.orm != nil {
		sub, serr := h.orm.TenantSubscription.Query().
			Where(tenantsubscription.TenantIDEQ(tenantID)).
			WithPlan().
			Only(ctx)
		if serr == nil {
			periodStart = sub.CurrentPeriodStart
			periodEnd = sub.CurrentPeriodEnd
			if sub.Edges.Plan != nil && sub.Edges.Plan.TierLimitsJSON != nil {
				planLimits = sub.Edges.Plan.TierLimitsJSON
			}
		}
	}

	// Determine period: the subscription's REAL current_period_start/end — see the matching
	// comment in GetUsageSummary for why a fixed 30-day lookback drifts from the truth.
	to := time.Now()
	if !periodEnd.IsZero() {
		to = periodEnd
	}
	from := to.AddDate(0, -1, 0)
	if !periodStart.IsZero() {
		from = periodStart
	}

	rawMetrics, err := h.queryUsageSummary(ctx, tenantID, from, to, "")
	if err != nil {
		h.log.Error("failed to query usage for dashboard", zap.Error(err))
		writeJSON(w, http.StatusOK, map[string]any{}) // return empty rather than error
		return
	}

	usedByType := map[string]float64{}
	for _, m := range rawMetrics {
		usedByType[m.MetricType] = m.Total
	}

	result := map[string]any{}

	// Well-known dashboard metrics
	dashboardMetrics := []string{"orders", "riders", "outlets", "api_calls"}
	for _, mt := range dashboardMetrics {
		limit := 0
		if lk, ok := billing.ResolveLimitKey(mt, planLimits); ok {
			switch v := planLimits[lk].(type) {
			case float64:
				limit = int(v)
			case int:
				limit = v
			}
		}
		result[mt] = map[string]int{
			"used":  int(usedByType[mt]),
			"limit": limit,
		}
	}

	writeJSON(w, http.StatusOK, result)
}

// inferMetricType extracts the base metric name from a limit key.
// e.g. "max_orders_per_month" -> "orders", "max_riders" -> "riders"
func inferMetricType(limitKey string) string {
	key := strings.ToLower(limitKey)
	key = strings.TrimPrefix(key, "max_")
	key = strings.ReplaceAll(key, "_per_day", "")
	key = strings.ReplaceAll(key, "_per_month", "")
	key = strings.ReplaceAll(key, "_per_hour", "")
	key = strings.TrimSuffix(key, "_count")
	key = strings.TrimSuffix(key, "_limit")
	// Normalize common variants
	if key == "order" {
		return "orders"
	}
	if key == "rider" {
		return "riders"
	}
	if key == "outlet" {
		return "outlets"
	}
	return key
}

type metricRow struct {
	MetricType string  `json:"metric_type"`
	Total      float64 `json:"total"`
	EventCount int     `json:"event_count"`
}

func (h *UsageHandler) queryUsageSummary(ctx context.Context, tenantID uuid.UUID, from, to time.Time, serviceFilter string) ([]metricRow, error) {
	query := `
		SELECT metric_type, SUM(value) as total, COUNT(*) as event_count
		FROM usage_events
		WHERE tenant_id = $1 AND created_at >= $2 AND created_at <= $3
	`
	args := []any{tenantID, from, to}
	if serviceFilter != "" {
		query += " AND service_name = $4 GROUP BY metric_type ORDER BY metric_type"
		args = append(args, serviceFilter)
	} else {
		query += " GROUP BY metric_type ORDER BY metric_type"
	}

	rows, err := h.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []metricRow
	for rows.Next() {
		var row metricRow
		if err := rows.Scan(&row.MetricType, &row.Total, &row.EventCount); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

// overageUpgradeURL is the destination the limit-reached modal links its "Upgrade" CTA to.
const overageUpgradeURL = "/settings?tab=subscription"

// usageDecision captures the outcome of evaluating a usage report against plan limits.
type usageDecision struct {
	limit             int        // plan limit for the metric (0 = unlimited / not configured)
	used              int        // running usage including this event
	remaining         int        // limit - used, floored at 0
	exceeded          bool       // used > limit
	allowOverage      bool       // exceeded but permitted (opt-in + overage-eligible + priced)
	overageEligible   bool       // metric is a metered overage-eligible throughput limit
	overageUnitPrice  float64    // KES per overage unit (per quantum)
	overageUnit       string     // human unit label, e.g. "per 100 orders"
	accruedOverageKes float64    // sum of pending overage already accrued this period
	cacheKey          string     // the exact Redis key incremented, for an exact rollback
	periodEnd         *time.Time // the tenant's current_period_end, for metered metrics only
}

// evaluateUsage atomically increments the tenant's usage counter (via the canonical
// billing.IncrementUsage, shared with the NATS usage consumer) and decides whether the
// event is within limit, soft-capped (overage), or hard-blocked. Fails open on any error.
func (h *UsageHandler) evaluateUsage(ctx context.Context, tenantID uuid.UUID, metricType string, value float64) usageDecision {
	res := billing.IncrementUsage(ctx, h.orm, h.cache, h.log, tenantID, metricType, value)
	if !res.Configured {
		return usageDecision{}
	}

	current := int(res.Used)
	rem := res.Limit - current
	if rem < 0 {
		rem = 0
	}
	periodEnd := res.PeriodEnd
	dec := usageDecision{
		limit: res.Limit, used: current, remaining: rem, exceeded: res.Exceeded,
		cacheKey: res.CacheKey, periodEnd: &periodEnd,
	}
	if !dec.exceeded {
		return dec
	}

	// Over the limit — decide soft-cap vs hard-block.
	meta, eligible := billing.MeteredMetricByType(metricType)
	if !eligible {
		// fall back to matching by the plan limit key (metric naming may differ)
		meta, eligible = billing.MeteredMetricByLimitKey(res.LimitKey)
	}
	dec.overageEligible = eligible
	if eligible {
		dec.overageUnit = meta.Unit
		if v, ok := res.PlanLimits[meta.OveragePriceKey]; ok {
			switch pv := v.(type) {
			case float64:
				dec.overageUnitPrice = pv
			case int:
				dec.overageUnitPrice = float64(pv)
			}
		}
	}
	dec.accruedOverageKes = h.pendingOverageTotal(ctx, tenantID)

	// Soft-cap only when the tenant opted in, the metric is eligible, and a price is seeded.
	if res.AllowOverage && eligible && dec.overageUnitPrice > 0 {
		dec.allowOverage = true
	}
	return dec
}

// rollbackUsageCounter decrements a usage counter after a hard-blocked attempt so the
// rejected event isn't counted against the tenant. cacheKey is the exact key evaluateUsage
// incremented (usageDecision.cacheKey) — recomputing it here instead risked drifting out of
// sync with whatever key logic actually ran the increment.
func (h *UsageHandler) rollbackUsageCounter(ctx context.Context, cacheKey string, value float64) {
	if h.cache == nil || cacheKey == "" {
		return
	}
	_ = h.cache.IncrByFloat(ctx, cacheKey, -value).Err()
}

// pendingOverageTotal sums the tenant's pending (not yet invoiced) overage charges,
// reusing the billing OverageService accumulator.
func (h *UsageHandler) pendingOverageTotal(ctx context.Context, tenantID uuid.UUID) float64 {
	if h.overage == nil {
		return 0
	}
	total, err := h.overage.GetAccumulatedOverage(ctx, tenantID)
	if err != nil {
		return 0
	}
	return total
}

// GetAlerts reads active usage threshold alerts from Redis and returns them to the caller.
// The service UI banner calls this endpoint to decide whether to show a usage warning.
func (h *UsageHandler) GetAlerts(w http.ResponseWriter, r *http.Request) {
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

	if h.cache == nil {
		writeJSON(w, http.StatusOK, map[string]any{"alerts": []any{}})
		return
	}

	// Scan for all alert keys for this tenant: usage:alert:{tenantID}:*
	pattern := fmt.Sprintf("usage:alert:%s:*", tenantID.String())
	keys, err := h.cache.Keys(ctx, pattern).Result()
	if err != nil {
		h.log.Warn("GetAlerts: redis scan failed", zap.Error(err))
		writeJSON(w, http.StatusOK, map[string]any{"alerts": []any{}})
		return
	}

	type alertItem struct {
		Metric  string  `json:"metric"`
		Limit   int     `json:"limit"`
		Current float64 `json:"current"`
		Pct     int     `json:"pct"`
	}
	alerts := make([]alertItem, 0, len(keys))
	for _, key := range keys {
		val, err := h.cache.Get(ctx, key).Result()
		if err != nil {
			continue
		}
		var item alertItem
		if err := json.Unmarshal([]byte(val), &item); err == nil {
			alerts = append(alerts, item)
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{"alerts": alerts})
}

func parseUsageDateRange(r *http.Request) (time.Time, time.Time) {
	now := time.Now()
	from := now.AddDate(0, -1, 0)
	to := now
	if s := r.URL.Query().Get("from"); s != "" {
		if t, err := time.Parse("2006-01-02", s); err == nil {
			from = t
		}
	}
	if s := r.URL.Query().Get("to"); s != "" {
		if t, err := time.Parse("2006-01-02", s); err == nil {
			to = t.Add(24*time.Hour - time.Nanosecond)
		}
	}
	return from, to
}
