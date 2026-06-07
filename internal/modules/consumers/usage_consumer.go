package consumers

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/serviceconfig"
	"github.com/bengobox/subscription-service/internal/ent/tenantsubscription"
	eventslib "github.com/Bengo-Hub/shared-events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// usageEventMapping maps a NATS subject to the metric type and service name to record.
type usageEventMapping struct {
	metric  string
	service string
	// roleMetrics maps role strings to metric overrides for auth.user.created events
	roleMetrics map[string]string
}

// usageSubjectMappings lists all billable event subjects published by microservices.
// Subjects marked with roleMetrics require parsing the "roles" array from the payload.
var usageSubjectMappings = map[string]usageEventMapping{
	// ── Ordering ──────────────────────────────────────────────────────────────
	"ordering.order.created":     {metric: "orders", service: "ordering"},
	"cafe.order.created":         {metric: "orders", service: "cafe"},
	"ordering.webhook.dispatched": {metric: "webhooks", service: "ordering"},

	// ── POS ───────────────────────────────────────────────────────────────────
	"pos.transaction.created":     {metric: "transactions", service: "pos"},
	"pos.sale.finalized":          {metric: "transactions", service: "pos"},
	"pos.device.registered":       {metric: "devices", service: "pos"},
	"pos.table.created":           {metric: "tables", service: "pos"},
	"pos.room.created":            {metric: "rooms", service: "pos"},
	"pos.conference.event.booked": {metric: "conference_events", service: "pos"},

	// ── Inventory ─────────────────────────────────────────────────────────────
	"inventory.product.created":   {metric: "products", service: "inventory"},
	"inventory.item.created":      {metric: "products", service: "inventory"},
	"inventory.warehouse.created": {metric: "warehouses", service: "inventory"},

	// ── Logistics ─────────────────────────────────────────────────────────────
	"logistics.delivery.created":      {metric: "deliveries", service: "logistics"},
	"logistics.task.completed":        {metric: "deliveries", service: "logistics"},
	"logistics.fleet.member_invited":  {metric: "riders", service: "logistics"},
	"logistics.task.eta_updated":      {metric: "tracking_requests", service: "logistics"},
	"truload.shipment.created":        {metric: "deliveries", service: "truload"},

	// ── Auth / Staff ──────────────────────────────────────────────────────────
	// Role-aware: roles[] array inspected to route to correct metric bucket
	"auth.user.created":  {metric: "staff", service: "auth", roleMetrics: map[string]string{
		"admin":        "admins",
		"outlet_admin": "admins",
		"cashier":      "cashiers",
		"staff":        "staff",
		"waiter":       "staff",
		"kitchen_staff": "staff",
		"rider":        "riders",
	}},
	"auth.outlet.created": {metric: "outlets", service: "auth"},

	// ── Notifications ─────────────────────────────────────────────────────────
	"notifications.sms.sent":   {metric: "sms_sent", service: "notifications"},
	"notifications.email.sent": {metric: "emails_sent", service: "notifications"},
	"notifications.push.sent":  {metric: "push_sent", service: "notifications"},

	// ── MarketFlow ────────────────────────────────────────────────────────────
	"marketflow.campaign.created": {metric: "campaigns", service: "marketflow"},
}

// usageEventPayload is the minimal common shape — all billable service events include tenant_id.
type usageEventPayload struct {
	TenantID string   `json:"tenant_id"`
	Roles    []string `json:"roles"`
}

// UsageConsumer listens for billable events from microservices and records usage metrics.
type UsageConsumer struct {
	log   *zap.Logger
	db    *pgxpool.Pool
	orm   *ent.Client
	cache *redis.Client
	js    nats.JetStreamContext
}

// NewUsageConsumer creates a new UsageConsumer.
func NewUsageConsumer(log *zap.Logger, db *pgxpool.Pool, orm *ent.Client, cache *redis.Client) *UsageConsumer {
	return &UsageConsumer{
		log:   log.Named("consumers.usage"),
		db:    db,
		orm:   orm,
		cache: cache,
	}
}

// Start subscribes to all billable event subjects and processes them until ctx is cancelled.
func (c *UsageConsumer) Start(ctx context.Context, js nats.JetStreamContext) error {
	c.js = js

	for subject, mapping := range usageSubjectMappings {
		subj := subject // capture loop variable
		m := mapping
		durableName := fmt.Sprintf("subscription-service-usage-%s",
			strings.ReplaceAll(subj, ".", "-"))

		eventslib.SubscribeWithRebind(
			c.log,
			js,
			subj,
			func(msg *nats.Msg) {
				if err := c.handle(ctx, msg, m); err != nil {
					c.log.Error("failed to handle usage event",
						zap.String("subject", subj),
						zap.Error(err),
					)
					_ = msg.Nak()
					return
				}
				_ = msg.Ack()
			},
			nats.Durable(durableName),
			nats.AckExplicit(),
			nats.DeliverNew(),
			nats.MaxDeliver(5),
			nats.AckWait(30*time.Second),
			nats.ManualAck(),
		)
		c.log.Info("usage consumer subscribed", zap.String("subject", subj))
	}

	<-ctx.Done()
	return nil
}

func (c *UsageConsumer) handle(ctx context.Context, msg *nats.Msg, m usageEventMapping) error {
	var payload usageEventPayload
	if err := json.Unmarshal(msg.Data, &payload); err != nil || payload.TenantID == "" {
		c.log.Warn("usage event missing tenant_id, skipping", zap.String("metric", m.metric))
		return nil
	}

	tenantID, err := uuid.Parse(payload.TenantID)
	if err != nil {
		c.log.Warn("usage event invalid tenant_id, skipping",
			zap.String("tenant_id", payload.TenantID),
			zap.Error(err),
		)
		return nil
	}

	// Role-aware bucketing: auth.user.created routes to specific metric per role
	metrics := []string{m.metric}
	if len(m.roleMetrics) > 0 && len(payload.Roles) > 0 {
		roleMetricSet := map[string]struct{}{}
		for _, role := range payload.Roles {
			if target, ok := m.roleMetrics[strings.ToLower(role)]; ok {
				roleMetricSet[target] = struct{}{}
			}
		}
		if len(roleMetricSet) > 0 {
			metrics = metrics[:0]
			for metric := range roleMetricSet {
				metrics = append(metrics, metric)
			}
		}
	}

	for _, metric := range metrics {
		if err := c.recordUsage(ctx, tenantID, metric, m.service, 1.0); err != nil {
			return err
		}
	}
	return nil
}

func (c *UsageConsumer) recordUsage(ctx context.Context, tenantID uuid.UUID, metric, service string, value float64) error {
	// Check quota and threshold before recording — fail open on Redis/ORM errors.
	if c.cache != nil && c.orm != nil {
		exceeded, atThreshold, planLimit, currentTotal := c.checkLimitAndThreshold(ctx, tenantID, metric, value)
		if atThreshold && !exceeded {
			c.publishThresholdWarning(ctx, tenantID, metric, planLimit, currentTotal)
		}
		if exceeded {
			c.log.Debug("usage limit exceeded",
				zap.String("tenant_id", tenantID.String()),
				zap.String("metric", metric),
			)
		}
	}

	now := time.Now()
	_, err := c.db.Exec(ctx, `
		INSERT INTO usage_events (id, tenant_id, metric_type, service_name, value, period_start, period_end, metadata, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`, uuid.New(), tenantID, metric, service, value, nil, nil, []byte("{}"), now)
	if err != nil {
		return fmt.Errorf("insert usage_events: %w", err)
	}

	c.log.Debug("usage event recorded",
		zap.String("tenant_id", tenantID.String()),
		zap.String("metric", metric),
		zap.String("service", service),
	)
	return nil
}

// checkLimitAndThreshold increments the Redis counter and returns (exceeded, atThreshold, planLimit, newTotal).
// Returns (false, false, 0, 0) on any infrastructure error (fail-open).
func (c *UsageConsumer) checkLimitAndThreshold(ctx context.Context, tenantID uuid.UUID, metricType string, value float64) (exceeded, atThreshold bool, planLimit int, newTotal float64) {
	sub, err := c.orm.TenantSubscription.Query().
		Where(tenantsubscription.TenantIDEQ(tenantID)).
		WithPlan().
		Only(ctx)
	if err != nil || sub.Edges.Plan == nil || sub.Edges.Plan.TierLimitsJSON == nil {
		return false, false, 0, 0
	}

	limitKey := usageFindLimitKey(metricType, sub.Edges.Plan.TierLimitsJSON)
	if limitKey == "" {
		return false, false, 0, 0
	}

	var limit int
	switch v := sub.Edges.Plan.TierLimitsJSON[limitKey].(type) {
	case float64:
		limit = int(v)
	case int:
		limit = v
	default:
		return false, false, 0, 0
	}
	if limit <= 0 { // -1 = unlimited
		return false, false, 0, 0
	}

	period := time.Now().UTC().Format("2006-01")
	cacheKey := fmt.Sprintf("usage:limit:%s:%s:%s", tenantID.String(), metricType, period)

	newTotal, err = c.cache.IncrByFloat(ctx, cacheKey, value).Result()
	if err != nil {
		return false, false, 0, 0
	}
	ttl, _ := c.cache.TTL(ctx, cacheKey).Result()
	if ttl < 0 {
		now := time.Now().UTC()
		expiry := time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, time.UTC)
		_ = c.cache.ExpireAt(ctx, cacheKey, expiry).Err()
	}

	prevTotal := newTotal - value
	threshold := c.getThreshold(ctx, tenantID)

	exceeded = newTotal > float64(limit)
	// Fire threshold warning exactly once: when crossing from below to at/above threshold
	atThreshold = float64(newTotal)/float64(limit) >= threshold &&
		float64(prevTotal)/float64(limit) < threshold

	return exceeded, atThreshold, limit, newTotal
}

// getThreshold returns the configured warning threshold (default 0.80 = 80%).
func (c *UsageConsumer) getThreshold(ctx context.Context, tenantID uuid.UUID) float64 {
	_ = tenantID // future: per-tenant threshold from service_configs
	cfg, err := c.orm.ServiceConfig.Query().
		Where(serviceconfig.ConfigKeyEQ("usage.warning_threshold"), serviceconfig.TenantIDIsNil()).
		Only(ctx)
	if err != nil {
		return 0.80
	}
	if v, err := strconv.ParseFloat(cfg.ConfigValue, 64); err == nil {
		return v
	}
	return 0.80
}

// publishThresholdWarning writes an outbox event for the notifications service to
// send an email to the tenant admin and surface an in-app banner alert.
func (c *UsageConsumer) publishThresholdWarning(ctx context.Context, tenantID uuid.UUID, metric string, planLimit int, currentUsage float64) {
	pct := int(currentUsage / float64(planLimit) * 100)
	payload := map[string]any{
		"id":            uuid.New().String(),
		"tenant_id":     tenantID.String(),
		"aggregate_type": "usage",
		"aggregate_id":  tenantID.String(),
		"event_type":    "usage.threshold_exceeded",
		"payload": map[string]any{
			"tenant_id":      tenantID.String(),
			"metric_type":    metric,
			"plan_limit":     planLimit,
			"current_usage":  currentUsage,
			"threshold_pct":  pct,
			"period":         time.Now().UTC().Format("2006-01"),
			"notification": map[string]any{
				"target": "tenant_admin",
			},
		},
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"version":   "1.0",
	}

	payloadJSON, _ := json.Marshal(payload)

	if _, err := c.js.Publish("usage.threshold_exceeded", payloadJSON); err != nil {
		c.log.Warn("failed to publish threshold warning",
			zap.String("tenant_id", tenantID.String()),
			zap.String("metric", metric),
			zap.Error(err),
		)
	}

	// Also persist to Redis so the usage/alerts endpoint can serve the banner
	alertKey := fmt.Sprintf("usage:alert:%s:%s", tenantID.String(), metric)
	alertData, _ := json.Marshal(map[string]any{
		"metric":  metric,
		"limit":   planLimit,
		"current": currentUsage,
		"pct":     pct,
	})
	_ = c.cache.Set(ctx, alertKey, alertData, 24*time.Hour).Err()

	c.log.Info("usage threshold warning published",
		zap.String("tenant_id", tenantID.String()),
		zap.String("metric", metric),
		zap.Int("pct", pct),
	)
}

// isLimitExceeded is kept for backwards compatibility — wraps checkLimitAndThreshold.
func (c *UsageConsumer) isLimitExceeded(ctx context.Context, tenantID uuid.UUID, metricType string, value float64) bool {
	exceeded, _, _, _ := c.checkLimitAndThreshold(ctx, tenantID, metricType, value)
	return exceeded
}

func usageFindLimitKey(metricType string, planLimits map[string]any) string {
	mt := strings.ToLower(metricType)
	candidates := map[string]string{
		"orders":           "max_orders_per_day",
		"transactions":     "max_transactions_per_month",
		"riders":           "max_riders",
		"devices":          "max_devices",
		"cashiers":         "max_cashiers",
		"tables":           "max_tables",
		"outlets":          "max_outlets",
		"admins":           "max_admins",
		"staff":            "max_staff",
		"products":         "inventory_max_sku",
		"warehouses":       "inventory_max_warehouses",
		"rooms":            "max_rooms",
		"conference_events": "max_conference_events",
		"deliveries":       "max_orders_per_day",
		"tracking_requests": "live_tracking_requests_per_day",
		"sms_sent":         "sms_notifications_per_day",
		"emails_sent":      "email_notifications_per_day",
		"push_sent":        "sms_notifications_per_day",
		"webhooks":         "webhook_calls_per_day",
	}

	if key, ok := candidates[mt]; ok {
		if _, exists := planLimits[key]; exists {
			return key
		}
	}

	// Fallback: fuzzy match
	for k := range planLimits {
		if strings.Contains(strings.ToLower(k), mt) {
			return k
		}
	}
	return ""
}
