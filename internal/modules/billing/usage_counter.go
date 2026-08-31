package billing

import (
	"context"
	"strings"
	"time"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/tenantsubscription"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// limitKeyCandidates maps a usage metric_type to the canonical tierLimitsJSON key that
// enforces it. The single source of truth for every usage-tracking caller — before this
// consolidation, the HTTP /usage/report handler and the NATS usage consumer each
// maintained their OWN independent metric->limit-key mapping (limitKeyForMetric vs.
// usageFindLimitKey), which could silently drift apart. Never resolve this mapping
// ad hoc at a new call site — add the metric here instead.
var limitKeyCandidates = map[string]string{
	"orders":            "max_orders_per_month",
	"transactions":      "max_transactions_per_month",
	"riders":            "max_riders",
	"devices":           "max_devices",
	"cashiers":          "max_cashiers",
	"tables":            "max_tables",
	"outlets":           "max_outlets",
	"admins":            "max_admins",
	"staff":             "max_staff",
	"products":          "inventory_max_sku",
	"warehouses":        "inventory_max_warehouses",
	"rooms":             "max_rooms",
	"conference_events": "max_conference_events",
	"deliveries":        "max_orders_per_month",
	"tracking_requests": "live_tracking_requests_per_month",
	"sms_sent":          "sms_notifications_per_month",
	"emails_sent":       "email_notifications_per_month",
	"push_sent":         "sms_notifications_per_month",
	"webhooks":          "webhook_calls_per_month",
	"library_members":   "max_library_members",
	"library_titles":    "max_library_titles",
	"library_branches":  "max_library_branches",
	"api_calls":         "api_calls_per_month",
	"campaigns":         "max_campaigns",
}

// ResolveLimitKey finds the tierLimitsJSON key that enforces a usage metric_type: first by
// the explicit candidate map above, then falling back to a fuzzy substring match for any
// metric not yet listed there (skipping "overage_*" keys, which hold per-unit prices, never
// limits). Returns ok=false when the plan has no matching limit configured at all.
func ResolveLimitKey(metricType string, planLimits map[string]any) (string, bool) {
	mt := strings.ToLower(metricType)
	if key, ok := limitKeyCandidates[mt]; ok {
		if _, exists := planLimits[key]; exists {
			return key, true
		}
	}
	for k := range planLimits {
		kl := strings.ToLower(k)
		if strings.HasPrefix(kl, "overage_") {
			continue
		}
		if strings.Contains(kl, mt) {
			return k, true
		}
	}
	return "", false
}

// UsageIncrementResult is the outcome of atomically incrementing a tenant's live usage
// counter for one metric.
type UsageIncrementResult struct {
	// Configured is false when the tenant has no subscription/plan, or the metric has no
	// configured or finite limit (absent, or -1 unlimited) — callers MUST treat this as
	// "allow the request," not as a decision. Every other field is meaningless when false.
	Configured bool
	Limit      int
	LimitKey   string
	// Used is the running total for the current period/window, including this increment.
	Used         float64
	Exceeded     bool // Used > Limit
	CacheKey     string
	PeriodEnd    time.Time
	AllowOverage bool
	PlanLimits   map[string]any // the tenant's full tier_limits_json, for callers needing more (e.g. overage unit price)
}

// IncrementUsage atomically increments the tenant's live Redis usage counter for
// metricType by value and resolves it against the tenant's current plan limit. The single
// canonical entry point for every usage-tracking writer (HTTP /usage/report handler, NATS
// usage consumer) — they must never increment/resolve independently, which is exactly how
// the platform previously ended up with two counters keyed by different, disagreeing
// windows (see UsageCounterKey's doc comment). Fails open (Configured: false) on any
// subscription/plan/Redis lookup failure — callers MUST allow the request in that case.
func IncrementUsage(ctx context.Context, orm *ent.Client, cache *redis.Client, log *zap.Logger, tenantID uuid.UUID, metricType string, value float64) UsageIncrementResult {
	sub, err := orm.TenantSubscription.Query().
		Where(tenantsubscription.TenantIDEQ(tenantID)).
		WithPlan().
		Only(ctx)
	if err != nil || sub.Edges.Plan == nil || sub.Edges.Plan.TierLimitsJSON == nil {
		return UsageIncrementResult{}
	}

	planLimits := sub.Edges.Plan.TierLimitsJSON
	limitKey, ok := ResolveLimitKey(metricType, planLimits)
	if !ok {
		return UsageIncrementResult{}
	}

	var limit int
	switch v := planLimits[limitKey].(type) {
	case float64:
		limit = int(v)
	case int:
		limit = v
	default:
		return UsageIncrementResult{}
	}
	if limit <= 0 { // -1 = unlimited, 0/absent = not enforced
		return UsageIncrementResult{}
	}

	cacheKey := UsageCounterKey(tenantID, metricType, sub.CurrentPeriodStart)
	newTotal, err := cache.IncrByFloat(ctx, cacheKey, value).Result()
	if err != nil {
		if log != nil {
			log.Warn("redis usage counter failed, allowing request", zap.String("key", cacheKey), zap.Error(err))
		}
		return UsageIncrementResult{}
	}

	// Metered (period-keyed) metrics get a TTL a day past current_period_end as a safety
	// net (the key is already unique per period, so this is a cleanup backstop, not the
	// primary reset mechanism). Structural (flat-keyed) metrics get no TTL at all — they
	// must never reset on a timer.
	if IsOverageEligibleMetric(metricType) {
		if ttl, _ := cache.TTL(ctx, cacheKey).Result(); ttl < 0 {
			_ = cache.ExpireAt(ctx, cacheKey, sub.CurrentPeriodEnd.Add(24*time.Hour)).Err()
		}
	}

	return UsageIncrementResult{
		Configured:   true,
		Limit:        limit,
		LimitKey:     limitKey,
		Used:         newTotal,
		Exceeded:     newTotal > float64(limit),
		CacheKey:     cacheKey,
		PeriodEnd:    sub.CurrentPeriodEnd,
		AllowOverage: sub.AllowOverage,
		PlanLimits:   planLimits,
	}
}
