package consumers

import (
	"context"
	"encoding/json"
	"time"

	eventslib "github.com/Bengo-Hub/shared-events"
	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"go.uber.org/zap"

	"github.com/bengobox/subscription-service/internal/modules/subscriptions"
)

// AuthOutletCreatedSubject is the NATS subject auth-api (the platform's SSO/tenant-identity
// service) publishes to whenever a tenant admin creates a new outlet — see
// auth-service/auth-api/internal/httpapi/handlers/outlet_handler.go's publishOutletEvent. pos-api
// already consumes this subject to mirror the outlet locally; this is a second, independent
// consumer for the same event.
const AuthOutletCreatedSubject = "auth.outlet.created"

// outletCreatedPayload mirrors the fields auth-api's publishOutletEvent actually sends (see
// withOutletContacts's payload map in outlet_handler.go) — only the fields this consumer needs.
type outletCreatedPayload struct {
	TenantID uuid.UUID `json:"tenant_id"`
	UseCase  string    `json:"use_case"`
}

// OutletCreatedConsumer listens for auth.outlet.created and auto-provisions the matching
// PowerSuite family overlay for a tenant whose new outlet's use_case their current plan doesn't
// already cover — see subscriptions.Service.MaybeProvisionUseCaseFamily for the full policy.
type OutletCreatedConsumer struct {
	log     *zap.Logger
	service *subscriptions.Service
}

// NewOutletCreatedConsumer creates a new consumer for auth.outlet.created events.
func NewOutletCreatedConsumer(log *zap.Logger, svc *subscriptions.Service) *OutletCreatedConsumer {
	return &OutletCreatedConsumer{
		log:     log.Named("consumers.outlet_created"),
		service: svc,
	}
}

// Start subscribes to auth.outlet.created via a NATS JetStream durable consumer. Blocks until ctx
// is cancelled — intended to be run in a goroutine, mirroring TenantCreatedConsumer.Start exactly.
func (c *OutletCreatedConsumer) Start(ctx context.Context, js nats.JetStreamContext) error {
	eventslib.SubscribeQueueWithRebind(
		c.log,
		js,
		"auth",
		AuthOutletCreatedSubject,
		"subscription-service-outlet-provisioner",
		func(msg *nats.Msg) {
			if err := c.handle(ctx, msg); err != nil {
				c.log.Error("failed to handle outlet.created event", zap.Error(err))
				_ = msg.Nak()
				return
			}
			_ = msg.Ack()
		},
		nats.Durable("subscription-service-outlet-provisioner"),
		nats.AckExplicit(),
		nats.DeliverNew(),
		nats.MaxDeliver(5),
		nats.AckWait(30*time.Second),
		nats.ManualAck(),
	)

	c.log.Info("outlet.created consumer started", zap.String("subject", AuthOutletCreatedSubject))

	<-ctx.Done()
	return nil
}

func (c *OutletCreatedConsumer) handle(ctx context.Context, msg *nats.Msg) error {
	// shared-events nests business fields under `payload`; tenant_id is ALSO present at the
	// envelope's top level (same double-nesting TenantCreatedConsumer already accounts for).
	var env struct {
		TenantID uuid.UUID            `json:"tenant_id"`
		Payload  outletCreatedPayload `json:"payload"`
	}
	if err := json.Unmarshal(msg.Data, &env); err != nil {
		c.log.Warn("could not parse outlet.created payload", zap.Error(err))
		return nil // bad message format — ACK to avoid infinite redelivery
	}
	payload := env.Payload
	if payload.TenantID == uuid.Nil {
		payload.TenantID = env.TenantID
	}
	if payload.TenantID == uuid.Nil {
		c.log.Warn("outlet.created event missing tenant_id, skipping")
		return nil
	}
	if payload.UseCase == "" {
		return nil
	}

	if err := c.service.MaybeProvisionUseCaseFamily(ctx, payload.TenantID, payload.UseCase); err != nil {
		return err
	}
	return nil
}
