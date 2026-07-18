package consumers

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	eventslib "github.com/Bengo-Hub/shared-events"
	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/serviceconfig"
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
	// delta is the amount to add to usage per event. Defaults to +1 when zero.
	// Deletion/release events use -1 to keep structural counts (e.g. tables) in sync.
	delta float64
	// roleMetrics maps role strings to metric overrides for auth.user.created events
	roleMetrics map[string]string
	// amountField, when set, makes the recorded usage value the numeric amount carried
	// in that payload field (instead of the unit delta). Used for revenue-style metering
	// such as ISP hotspot sales (metric value = KES amount, summed over the period).
	// The field may be a JSON number or a numeric string (the ISP outbox serialises
	// Decimal amounts as strings).
	amountField string
}

// usageSubjectMappings lists all billable event subjects published by microservices.
// Subjects marked with roleMetrics require parsing the "roles" array from the payload.
var usageSubjectMappings = map[string]usageEventMapping{
	// ── Ordering ──────────────────────────────────────────────────────────────
	"ordering.order.created":      {metric: "orders", service: "ordering"},
	"ordering.webhook.dispatched": {metric: "webhooks", service: "ordering"},

	// ── POS ───────────────────────────────────────────────────────────────────
	"pos.transaction.created":     {metric: "transactions", service: "pos"},
	"pos.sale.finalized":          {metric: "transactions", service: "pos"},
	"pos.device.registered":       {metric: "devices", service: "pos"},
	"pos.table.created":           {metric: "tables", service: "pos"},
	"pos.table.deleted":           {metric: "tables", service: "pos", delta: -1},
	"pos.room.created":            {metric: "rooms", service: "pos"},
	"pos.conference.event.booked": {metric: "conference_events", service: "pos"},

	// ── Inventory ─────────────────────────────────────────────────────────────
	"inventory.product.created":   {metric: "products", service: "inventory"},
	"inventory.item.created":      {metric: "products", service: "inventory"},
	"inventory.warehouse.created": {metric: "warehouses", service: "inventory"},

	// ── Logistics ─────────────────────────────────────────────────────────────
	"logistics.delivery.created":     {metric: "deliveries", service: "logistics"},
	"logistics.task.completed":       {metric: "deliveries", service: "logistics"},
	"logistics.fleet.member_invited": {metric: "riders", service: "logistics"},
	"logistics.task.eta_updated":     {metric: "tracking_requests", service: "logistics"},
	"truload.shipment.created":       {metric: "deliveries", service: "truload"},

	// ── Auth / Staff ──────────────────────────────────────────────────────────
	// Role-aware: roles[] array inspected to route to correct metric bucket
	"auth.user.created": {metric: "staff", service: "auth", roleMetrics: map[string]string{
		"admin":         "admins",
		"outlet_admin":  "admins",
		"cashier":       "cashiers",
		"staff":         "staff",
		"waiter":        "staff",
		"kitchen_staff": "staff",
		"rider":         "riders",
	}},
	"auth.outlet.created": {metric: "outlets", service: "auth"},

	// ── Notifications ─────────────────────────────────────────────────────────
	"notifications.sms.sent":   {metric: "sms_sent", service: "notifications"},
	"notifications.email.sent": {metric: "emails_sent", service: "notifications"},
	"notifications.push.sent":  {metric: "push_sent", service: "notifications"},

	// ── MarketFlow ────────────────────────────────────────────────────────────
	"marketflow.campaign.created": {metric: "campaigns", service: "marketflow"},

	// ── Library ───────────────────────────────────────────────────────────────
	"library.member.registered": {metric: "library_members", service: "library"},
	"library.bib.created":       {metric: "library_titles", service: "library"},
	"library.branch.created":    {metric: "library_branches", service: "library"},

	// ── ISP Billing ───────────────────────────────────────────────────────────
	// isp.subscriber.created is emitted ONLY by the hotspot flow (subscriber_type
	// "hotspot") and carries the purchase "amount". We record that amount as the
	// usage value so the billing engine can SUM hotspot revenue for the cycle and
	// apply the threshold-gated service charge (3% above KES 10k monthly sales).
	"isp.subscriber.created": {metric: "hotspot_sales", service: "isp_billing", amountField: "amount"},
	// isp.subscription.renewed is the PPPoE renewal signal (subscriber_type "pppoe").
	// Counted (+1) into pppoe_subscribers so the per-subscriber/month fee has a usage
	// source. NOTE: this counts PPPoE *renewals in the period*, which approximates —
	// but is NOT identical to — the active-subscriber count, because the ISP service
	// publishes no PPPoE create/deactivate events. See isp_billing.go for the caveat.
	"isp.subscription.renewed": {metric: "pppoe_subscribers", service: "isp_billing"},
}

// usageEventPayload is the minimal common shape — all billable service events include tenant_id.
// Some publishers (e.g. the ISP transactional outbox) use the shared-events envelope, which
// keeps tenant_id at the top level but nests the business fields (amount, …) under "payload".
type usageEventPayload struct {
	TenantID string             `json:"tenant_id"`
	Roles    []string           `json:"roles"`
	Amount   json.RawMessage    `json:"amount"`
	Payload  *usageEventPayload `json:"payload"`
}

// UsageConsumer listens for billable events from microservices and records usage metrics.
type UsageConsumer struct {
	log   *zap.Logger
	db    *pgxpool.Pool
	orm   *ent.Client
	cache *redis.Client
	js    nats.JetStreamContext
	idem  *eventslib.IdempotencyStore
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

// SetIdempotency wires the shared-events IdempotencyStore so each event is metered at most
// once even if JetStream redelivers it (Nak/crash/restart). Usage metering increments a
// rolling counter, so without this a redelivery would double-count. Fail-open: when the
// store is unset or the event carries no id, metering proceeds (better to risk a rare
// double than to silently drop billable usage).
func (c *UsageConsumer) SetIdempotency(idem *eventslib.IdempotencyStore) { c.idem = idem }

// Start subscribes to all billable event subjects and processes them until ctx is cancelled.
func (c *UsageConsumer) Start(ctx context.Context, js nats.JetStreamContext) error {
	c.js = js

	for subject, mapping := range usageSubjectMappings {
		subj := subject // capture loop variable
		m := mapping
		durableName := fmt.Sprintf("subscription-service-usage-%s",
			strings.ReplaceAll(subj, ".", "-"))
		// STREAM = the source stream carrying this subject, derived from the subject
		// prefix (e.g. "pos.sale.finalized" -> "pos"). Used by the helper only for the
		// one-time self-heal migration of a stale non-deliver-group durable.
		stream := subj
		if i := strings.IndexByte(subj, '.'); i > 0 {
			stream = subj[:i]
		}

		eventslib.SubscribeQueueWithRebind(
			c.log,
			js,
			stream,
			subj,
			durableName,
			func(msg *nats.Msg) {
				if err := c.handle(ctx, msg, m, durableName); err != nil {
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

func (c *UsageConsumer) handle(ctx context.Context, msg *nats.Msg, m usageEventMapping, consumerName string) error {
	// Idempotency: skip events already metered by this consumer (JetStream redelivery would
	// otherwise double-count the rolling usage). Fail-open when the store is unset or the
	// event has no id, so billable usage is never silently dropped.
	var eventID uuid.UUID
	if c.idem != nil {
		if id, err := eventslib.EventIDFromMsg(msg); err == nil {
			eventID = id
			if done, perr := c.idem.AlreadyProcessed(ctx, eventID, consumerName); perr == nil && done {
				return nil
			}
		}
	}

	var payload usageEventPayload
	if err := json.Unmarshal(msg.Data, &payload); err != nil || payload.TenantID == "" {
		c.log.Warn("usage event missing tenant_id, skipping", zap.String("metric", m.metric))
		return nil
	}

	// Events published via a shared outbox envelope wrap the business fields under a
	// nested "payload" object. Fall back to it when the top-level fields are absent so
	// tenant_id / amount resolve for both flat and enveloped publishers (e.g. the ISP
	// transactional outbox emits {..., "payload": {"tenant_id": ..., "amount": ...}}).
	if payload.TenantID == "" && payload.Payload != nil {
		payload.TenantID = payload.Payload.TenantID
	}
	// Same for roles: auth.user.created nests `roles` under payload, so without this
	// fallback the role buckets (admins/cashiers/riders) never fill and every new user
	// is metered as generic staff.
	if len(payload.Roles) == 0 && payload.Payload != nil {
		payload.Roles = payload.Payload.Roles
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

	delta := m.delta
	if delta == 0 {
		delta = 1.0
	}

	// Revenue-style metering: record the amount carried in the payload as the value
	// (rather than a unit count), so the billing engine can SUM it for the cycle. A
	// missing/zero amount is skipped — a sale with no amount carries no revenue.
	if m.amountField != "" {
		amt := payloadAmount(&payload, m.amountField)
		if amt <= 0 {
			c.log.Debug("usage event has no positive amount, skipping",
				zap.String("metric", m.metric), zap.String("tenant_id", tenantID.String()))
			return nil
		}
		delta = amt
	}

	for _, metric := range metrics {
		if err := c.recordUsage(ctx, tenantID, metric, m.service, delta); err != nil {
			return err
		}
	}

	// Mark processed only after the usage was recorded — so a recordUsage failure (Nak →
	// redelivery) re-runs rather than being skipped. At-least-once with a crash-window double
	// instead of double-on-every-redelivery.
	if c.idem != nil && eventID != uuid.Nil {
		if err := c.idem.MarkProcessed(ctx, eventID, consumerName); err != nil {
			c.log.Warn("mark usage event processed failed", zap.Error(err))
		}
	}
	return nil
}

// payloadAmount extracts a numeric amount from the event, tolerating both a JSON number
// and a numeric string (the ISP outbox serialises Decimal money as a string) and both the
// flat and enveloped ({"payload": {...}}) shapes. Only the "amount" field is supported
// today; field is accepted for forward-compatibility. Returns 0 when absent/unparseable.
func payloadAmount(p *usageEventPayload, field string) float64 {
	if field != "amount" {
		return 0
	}
	raw := p.Amount
	if len(raw) == 0 && p.Payload != nil {
		raw = p.Payload.Amount
	}
	return parseJSONAmount(raw)
}

// parseJSONAmount parses a JSON number or numeric-string token into a float; 0 on failure.
func parseJSONAmount(raw json.RawMessage) float64 {
	if len(raw) == 0 {
		return 0
	}
	// Try a bare number first, then a quoted numeric string.
	var n float64
	if err := json.Unmarshal(raw, &n); err == nil {
		return n
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if v, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err == nil {
			return v
		}
	}
	return 0
}

func (c *UsageConsumer) recordUsage(ctx context.Context, tenantID uuid.UUID, metric, service string, value float64) error {
	// Check quota and threshold before recording — fail open on Redis/ORM errors.
	// Only run the limit/threshold path for positive deltas; decrements (e.g. table
	// deletions) just adjust the counters below and never trigger a warning.
	if value > 0 && c.cache != nil && c.orm != nil {
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
	} else if value < 0 && c.cache != nil {
		// Decrement the fast-path counter so structural limits (e.g. tables) reflect
		// the deletion. Floor at 0 to avoid drift from out-of-order/replayed events.
		c.decrementCounter(ctx, tenantID, metric, value)
	}

	now := time.Now()
	_, err := c.db.Exec(ctx, `
		INSERT INTO usage_events (id, tenant_id, metric_type, service_name, value, period_start, period_end, metadata, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`, uuid.New(), tenantID, metric, service, value, nil, nil, []byte("{}"), now)
	if err != nil {
		return fmt.Errorf("insert usage_events: %w", err)
	}

	// Stamp business activity for dormancy tracking, and auto-reactivate a DORMANT
	// (warned-but-not-yet-suspended) account the instant it trades again. Cheap single
	// UPDATE; best-effort — never fail the usage event on a stamp error. SUSPENDED/
	// pending-purge accounts are left for payment or admin to clear, not mere usage.
	if value > 0 {
		if _, aerr := c.db.Exec(ctx, `
			UPDATE tenant_subscriptions
			SET last_activity_at = $2,
			    dormant_at = CASE WHEN status = 'DORMANT' THEN NULL ELSE dormant_at END,
			    purge_grace_ends_at = CASE WHEN status = 'DORMANT' THEN NULL ELSE purge_grace_ends_at END,
			    status = CASE WHEN status = 'DORMANT' THEN 'ACTIVE' ELSE status END,
			    updated_at = $2
			WHERE tenant_id = $1
		`, tenantID, now); aerr != nil {
			c.log.Warn("failed to stamp last_activity_at", zap.String("tenant_id", tenantID.String()), zap.Error(aerr))
		}
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

// decrementCounter reduces the Redis fast-path usage counter by |value|, flooring
// at 0. Used for structural decrements (e.g. table deletions). Best-effort: any
// Redis error is ignored since the usage_events sum is the billing source of truth.
func (c *UsageConsumer) decrementCounter(ctx context.Context, tenantID uuid.UUID, metricType string, value float64) {
	period := time.Now().UTC().Format("2006-01")
	cacheKey := fmt.Sprintf("usage:limit:%s:%s:%s", tenantID.String(), metricType, period)

	newTotal, err := c.cache.IncrByFloat(ctx, cacheKey, value).Result() // value is negative
	if err != nil {
		return
	}
	if newTotal < 0 {
		_ = c.cache.Set(ctx, cacheKey, 0, redis.KeepTTL).Err()
	}
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

// publishThresholdWarning surfaces an in-app usage banner via Redis (served by the
// usage/alerts endpoint). NOTE: this used to also js.Publish("usage.threshold_exceeded"),
// but no stream binds usage.> and nothing consumes it — the publish failed on every call
// and was dropped, so it was removed. If admin emails are ever wanted, emit
// subscription.usage.threshold_exceeded (the subscription stream binds subscription.>)
// and add a notifications-api consumer.
func (c *UsageConsumer) publishThresholdWarning(ctx context.Context, tenantID uuid.UUID, metric string, planLimit int, currentUsage float64) {
	pct := int(currentUsage / float64(planLimit) * 100)

	// Persist to Redis so the usage/alerts endpoint can serve the banner.
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
		"orders":            "max_orders_per_day",
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
		"deliveries":        "max_orders_per_day",
		"tracking_requests": "live_tracking_requests_per_day",
		"sms_sent":          "sms_notifications_per_day",
		"emails_sent":       "email_notifications_per_day",
		"push_sent":         "sms_notifications_per_day",
		"webhooks":          "webhook_calls_per_day",
		"library_members":   "max_library_members",
		"library_titles":    "max_library_titles",
		"library_branches":  "max_library_branches",
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
