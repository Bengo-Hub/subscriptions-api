package billing

import (
	"context"
	"strconv"
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
// usageIncrScript: INCRBYFLOAT KEYS[1] by ARGV[1]; when ARGV[2] > 0 and the key has no TTL,
// EXPIREAT ARGV[2]. Returns the new total as a string (Redis float reply).
var usageIncrScript = redis.NewScript(`
local v = redis.call("INCRBYFLOAT", KEYS[1], ARGV[1])
local at = tonumber(ARGV[2])
if at > 0 and redis.call("TTL", KEYS[1]) < 0 then
  redis.call("EXPIREAT", KEYS[1], at)
end
return v`)

func IncrementUsage(ctx context.Context, orm *ent.Client, cache *redis.Client, log *zap.Logger, tenantID uuid.UUID, metricType string, value float64) UsageIncrementResult {
	return resolveUsage(ctx, orm, cache, log, tenantID, metricType, value, true)
}

// PeekUsage reports what IncrementUsage WOULD decide for value more units without counting
// anything: Used is the current total plus value. Used by /usage/check, where a service asks
// "may I start one more?" and the real count happens later from the completion event (pos-api
// checks before a sale and the sale is counted on pos.sale.finalized). Same fail-open contract.
func PeekUsage(ctx context.Context, orm *ent.Client, cache *redis.Client, log *zap.Logger, tenantID uuid.UUID, metricType string, value float64) UsageIncrementResult {
	return resolveUsage(ctx, orm, cache, log, tenantID, metricType, value, false)
}

func resolveUsage(ctx context.Context, orm *ent.Client, cache *redis.Client, log *zap.Logger, tenantID uuid.UUID, metricType string, value float64, increment bool) UsageIncrementResult {
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
	// Metered (period-keyed) metrics get a TTL a day past current_period_end as a safety net
	// (the key is already unique per period, so this is a cleanup backstop, not the primary
	// reset). Structural (flat-keyed) metrics never expire. Increment and TTL are one atomic
	// script: one round trip instead of three, and a crash can never leave a metered key
	// without its TTL.
	expireAt := int64(0)
	if IsOverageEligibleMetric(metricType) {
		expireAt = sub.CurrentPeriodEnd.Add(24 * time.Hour).Unix()
	}
	var newTotal float64
	if increment {
		raw, err := usageIncrScript.Run(ctx, cache, []string{cacheKey}, value, expireAt).Text()
		if err != nil {
			if log != nil {
				log.Warn("redis usage counter failed, allowing request", zap.String("key", cacheKey), zap.Error(err))
			}
			return UsageIncrementResult{}
		}
		if newTotal, err = strconv.ParseFloat(raw, 64); err != nil {
			return UsageIncrementResult{}
		}
	} else {
		current, err := cache.Get(ctx, cacheKey).Float64()
		if err != nil && err != redis.Nil {
			if log != nil {
				log.Warn("redis usage counter read failed, allowing request", zap.String("key", cacheKey), zap.Error(err))
			}
			return UsageIncrementResult{}
		}
		newTotal = current + value
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
