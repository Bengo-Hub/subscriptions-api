package consumers

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"go.uber.org/zap"

	"github.com/bengobox/subscription-service/internal/modules/subscriptions"
)

const (
	// AuthTenantCreatedSubject is the NATS subject published by auth-service when a new tenant is created.
	AuthTenantCreatedSubject = "auth.tenant.created"

	// DefaultStarterPlanCode is assigned to new tenants during auto-provisioning.
	DefaultStarterPlanCode = "STARTER"

	// DefaultStarterBundle is the default bundle for new tenants.
	DefaultStarterBundle = "delivery"

	// DefaultTrialDays is the trial period for auto-provisioned subscriptions.
	DefaultTrialDays = 14
)

// tenantCreatedPayload mirrors the auth-service tenant.created event payload.
type tenantCreatedPayload struct {
	TenantID uuid.UUID `json:"tenant_id"`
	Slug     string    `json:"slug"`
	Name     string    `json:"name"`
}

// TenantCreatedConsumer listens for auth.tenant.created events and auto-provisions
// a Starter subscription with a 14-day trial for the new tenant.
type TenantCreatedConsumer struct {
	log     *zap.Logger
	service *subscriptions.Service
}

// NewTenantCreatedConsumer creates a new consumer for auth.tenant.created events.
func NewTenantCreatedConsumer(log *zap.Logger, svc *subscriptions.Service) *TenantCreatedConsumer {
	return &TenantCreatedConsumer{
		log:     log.Named("consumers.tenant_created"),
		service: svc,
	}
}

// Start subscribes to auth.tenant.created via NATS JetStream durable consumer and processes events.
// Blocks until ctx is cancelled. Intended to be run in a goroutine.
func (c *TenantCreatedConsumer) Start(ctx context.Context, js nats.JetStreamContext) error {
	// Ensure the stream exists — if auth-service publishes to a shared stream we just subscribe.
	// Use a durable consumer so we resume after restart without missing events.
	sub, err := js.Subscribe(
		AuthTenantCreatedSubject,
		func(msg *nats.Msg) {
			if err := c.handle(ctx, msg); err != nil {
				c.log.Error("failed to handle tenant.created event", zap.Error(err))
				// Negative ACK triggers redelivery per JetStream semantics.
				_ = msg.Nak()
				return
			}
			_ = msg.Ack()
		},
		nats.Durable("subscription-service-tenant-provisioner"),
		nats.AckExplicit(),
		nats.DeliverNew(),
		nats.MaxDeliver(5),
		nats.AckWait(30*time.Second),
		nats.ManualAck(),
	)
	if err != nil {
		return err
	}

	c.log.Info("tenant.created consumer started", zap.String("subject", AuthTenantCreatedSubject))

	<-ctx.Done()
	_ = sub.Unsubscribe()
	return nil
}

func (c *TenantCreatedConsumer) handle(ctx context.Context, msg *nats.Msg) error {
	var payload tenantCreatedPayload
	if err := json.Unmarshal(msg.Data, &payload); err != nil {
		c.log.Warn("could not parse tenant.created payload", zap.Error(err))
		// Bad message format — ACK to avoid infinite redelivery.
		return nil
	}

	if payload.TenantID == uuid.Nil {
		c.log.Warn("tenant.created event missing tenant_id, skipping")
		return nil
	}

	c.log.Info("provisioning starter subscription for new tenant",
		zap.String("tenant_id", payload.TenantID.String()),
		zap.String("slug", payload.Slug),
	)

	_, err := c.service.CreateSubscription(ctx, subscriptions.CreateInput{
		TenantID:   payload.TenantID,
		PlanCode:   DefaultStarterPlanCode,
		BundleCode: DefaultStarterBundle,
		TrialDays:  DefaultTrialDays,
	})
	if err != nil {
		// If subscription already exists, that's fine — idempotent.
		c.log.Warn("auto-provisioning skipped (subscription may already exist)",
			zap.String("tenant_id", payload.TenantID.String()),
			zap.Error(err),
		)
		// Don't return error — we don't want to retry for "already exists" cases.
		return nil
	}

	c.log.Info("starter subscription provisioned",
		zap.String("tenant_id", payload.TenantID.String()),
		zap.String("plan", DefaultStarterPlanCode),
		zap.Int("trial_days", DefaultTrialDays),
	)
	return nil
}
