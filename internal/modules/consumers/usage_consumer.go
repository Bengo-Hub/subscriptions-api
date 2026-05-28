package consumers

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/tenantsubscription"
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
}

// usageSubjectMappings lists all billable event subjects published by microservices.
var usageSubjectMappings = map[string]usageEventMapping{
	"ordering.order.created":      {metric: "orders", service: "ordering"},
	"cafe.order.created":          {metric: "orders", service: "cafe"},
	"inventory.product.created":   {metric: "products", service: "inventory"},
	"logistics.delivery.created":  {metric: "deliveries", service: "logistics"},
	"truload.shipment.created":    {metric: "deliveries", service: "truload"},
	"marketflow.campaign.created": {metric: "campaigns", service: "marketflow"},
	"pos.transaction.created":     {metric: "transactions", service: "pos"},
}

// usageEventPayload is the minimal common shape — all billable service events include tenant_id.
type usageEventPayload struct {
	TenantID string `json:"tenant_id"`
}

// UsageConsumer listens for billable events from microservices and records usage metrics.
type UsageConsumer struct {
	log   *zap.Logger
	db    *pgxpool.Pool
	orm   *ent.Client
	cache *redis.Client
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
	subs := make([]*nats.Subscription, 0, len(usageSubjectMappings))

	for subject, mapping := range usageSubjectMappings {
		subj := subject // capture loop variable
		m := mapping
		durableName := fmt.Sprintf("subscription-service-usage-%s",
			strings.ReplaceAll(subj, ".", "-"))

		sub, err := js.Subscribe(
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
		if err != nil {
			c.log.Warn("failed to subscribe to usage subject (non-fatal)",
				zap.String("subject", subj),
				zap.Error(err),
			)
			continue
		}
		subs = append(subs, sub)
		c.log.Info("usage consumer subscribed", zap.String("subject", subj))
	}

	<-ctx.Done()
	for _, sub := range subs {
		_ = sub.Unsubscribe()
	}
	return nil
}

func (c *UsageConsumer) handle(ctx context.Context, msg *nats.Msg, m usageEventMapping) error {
	var payload usageEventPayload
	if err := json.Unmarshal(msg.Data, &payload); err != nil || payload.TenantID == "" {
		// Bad/incomplete message — ACK to avoid infinite redelivery.
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

	// Check quota before recording — fail open on Redis errors.
	if c.cache != nil && c.orm != nil {
		if c.isLimitExceeded(ctx, tenantID, m.metric, 1.0) {
			c.log.Debug("usage limit exceeded, not recording event",
				zap.String("tenant_id", payload.TenantID),
				zap.String("metric", m.metric),
			)
			return nil
		}
	}

	now := time.Now()
	_, err = c.db.Exec(ctx, `
		INSERT INTO usage_events (id, tenant_id, metric_type, service_name, value, period_start, period_end, metadata, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`, uuid.New(), tenantID, m.metric, m.service, 1.0, nil, nil, []byte("{}"), now)
	if err != nil {
		return fmt.Errorf("insert usage_events: %w", err)
	}

	c.log.Debug("usage event recorded",
		zap.String("tenant_id", payload.TenantID),
		zap.String("metric", m.metric),
		zap.String("service", m.service),
	)
	return nil
}

// isLimitExceeded increments the Redis month counter and returns true when the tenant has exceeded their plan quota.
// Fails open on Redis/ORM errors so usage is never blocked by infrastructure issues.
func (c *UsageConsumer) isLimitExceeded(ctx context.Context, tenantID uuid.UUID, metricType string, value float64) bool {
	sub, err := c.orm.TenantSubscription.Query().
		Where(tenantsubscription.TenantIDEQ(tenantID)).
		WithPlan().
		Only(ctx)
	if err != nil || sub.Edges.Plan == nil || sub.Edges.Plan.TierLimitsJSON == nil {
		return false
	}

	limitKey := usageFindLimitKey(metricType, sub.Edges.Plan.TierLimitsJSON)
	if limitKey == "" {
		return false
	}

	var planLimit int
	switch v := sub.Edges.Plan.TierLimitsJSON[limitKey].(type) {
	case float64:
		planLimit = int(v)
	case int:
		planLimit = v
	default:
		return false
	}
	if planLimit <= 0 {
		return false
	}

	period := time.Now().UTC().Format("2006-01")
	cacheKey := fmt.Sprintf("usage:limit:%s:%s:%s", tenantID.String(), metricType, period)

	newTotal, err := c.cache.IncrByFloat(ctx, cacheKey, value).Result()
	if err != nil {
		return false
	}
	ttl, _ := c.cache.TTL(ctx, cacheKey).Result()
	if ttl < 0 {
		now := time.Now().UTC()
		expiry := time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, time.UTC)
		_ = c.cache.ExpireAt(ctx, cacheKey, expiry).Err()
	}

	return int(newTotal) > planLimit
}

func usageFindLimitKey(metricType string, planLimits map[string]any) string {
	mt := strings.ToLower(metricType)
	for k := range planLimits {
		if strings.Contains(strings.ToLower(k), mt) {
			return k
		}
	}
	return ""
}
