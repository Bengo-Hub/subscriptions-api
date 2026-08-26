package consumers

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	eventslib "github.com/Bengo-Hub/shared-events"
	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"go.uber.org/zap"

	"github.com/bengobox/subscription-service/internal/modules/billing"
)

// AuthAppPromotedSubject is the NATS subject auth-api publishes when a platform admin promotes
// an App (developer credential) from sandbox to production (see auth-api's
// AppHandler.PromoteToProduction).
const AuthAppPromotedSubject = "auth.app.promoted_to_production"

// appPromotedPayload mirrors auth-api's writeOutboxEvent data for this event.
type appPromotedPayload struct {
	AppID       string   `json:"app_id"`
	TenantID    string   `json:"tenant_id"`
	Scopes      []string `json:"scopes"`
	Environment string   `json:"environment"`
}

// scopePrefixToServiceTag maps an App scope prefix (e.g. "etims") to the ApiTokenWallet
// service_tag namespace it should be provisioned under. Mirrors auth-ui's
// requestableServices[...].scopePrefix -> service mapping (developer-portal.md) — extend this
// alongside that table as more external APIs get their own token-metered product.
var scopePrefixToServiceTag = map[string]string{
	"etims": "etims_api",
}

// AppPromotionConsumer listens for auth.app.promoted_to_production and provisions a zero-balance
// ApiTokenWallet for the tenant+service, so a freshly-approved external API integrator has
// somewhere to top up into immediately instead of hitting a confusing 404/empty-balance the first
// time they call GET /tokens/balance. This is the fix for the "nothing wires an approved
// integrator into billing" gap found auditing the external eTIMS API: promotion previously only
// flipped App.environment, with no downstream billing-side effect at all.
type AppPromotionConsumer struct {
	log    *zap.Logger
	wallet *billing.TokenWalletService
}

// NewAppPromotionConsumer creates a new consumer.
func NewAppPromotionConsumer(log *zap.Logger, wallet *billing.TokenWalletService) *AppPromotionConsumer {
	return &AppPromotionConsumer{log: log.Named("consumers.app_promotion"), wallet: wallet}
}

// Start subscribes to auth.app.promoted_to_production and blocks until ctx is cancelled.
func (c *AppPromotionConsumer) Start(ctx context.Context, js nats.JetStreamContext) error {
	eventslib.SubscribeQueueWithRebind(
		c.log,
		js,
		"auth",
		AuthAppPromotedSubject,
		"subscription-service-app-promotion",
		func(msg *nats.Msg) {
			if err := c.handle(ctx, msg); err != nil {
				c.log.Error("failed to handle auth.app.promoted_to_production", zap.Error(err))
				_ = msg.Nak()
				return
			}
			_ = msg.Ack()
		},
		nats.Durable("subscription-service-app-promotion"),
		nats.AckExplicit(),
		nats.DeliverNew(),
		nats.MaxDeliver(5),
		nats.AckWait(30*time.Second),
		nats.ManualAck(),
	)

	c.log.Info("auth.app.promoted_to_production consumer started")

	<-ctx.Done()
	return nil
}

func (c *AppPromotionConsumer) handle(ctx context.Context, msg *nats.Msg) error {
	var env struct {
		Payload appPromotedPayload `json:"payload"`
	}
	if err := json.Unmarshal(msg.Data, &env); err != nil {
		c.log.Warn("could not parse app promotion event, skipping", zap.Error(err))
		return nil
	}
	payload := env.Payload

	tenantID, err := uuid.Parse(payload.TenantID)
	if err != nil {
		c.log.Warn("app promotion event missing/invalid tenant_id, skipping", zap.String("tenant_id", payload.TenantID))
		return nil
	}

	serviceTag := serviceTagForScopes(payload.Scopes)
	if serviceTag == "" {
		c.log.Info("app promotion event has no recognized service scope, nothing to provision",
			zap.String("app_id", payload.AppID), zap.Strings("scopes", payload.Scopes))
		return nil
	}

	if c.wallet == nil {
		return nil
	}
	if err := c.wallet.EnsureWallet(ctx, tenantID, serviceTag); err != nil {
		c.log.Error("failed to provision token wallet for promoted app",
			zap.String("tenant_id", tenantID.String()), zap.String("service_tag", serviceTag), zap.Error(err))
		return err
	}
	c.log.Info("token wallet provisioned for promoted app",
		zap.String("app_id", payload.AppID), zap.String("tenant_id", tenantID.String()), zap.String("service_tag", serviceTag))
	return nil
}

// serviceTagForScopes returns the first recognized service_tag for an App's scope list (an App
// is enforced server-side, at creation, to carry scopes for exactly one service — see auth-api's
// validateTenantAppScopes — so "first match" is equivalent to "the only match").
func serviceTagForScopes(scopes []string) string {
	for _, s := range scopes {
		prefix, _, ok := strings.Cut(s, ":")
		if !ok {
			continue
		}
		if tag, ok := scopePrefixToServiceTag[prefix]; ok {
			return tag
		}
	}
	return ""
}
