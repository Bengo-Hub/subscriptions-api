package consumers

import (
	"context"
	"encoding/json"
	"time"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/tenantsubscription"
	"github.com/bengobox/subscription-service/internal/modules/billing"
	"github.com/bengobox/subscription-service/internal/modules/subscriptions"
	eventslib "github.com/Bengo-Hub/shared-events"
	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"go.uber.org/zap"
)

const treasuryPaymentSucceededSubject = "treasury.payment.succeeded"

// treasuryPaymentEvent is the payload from treasury's payment.succeeded NATS event.
// Amount is json.Number to handle both float64 and decimal string (treasury publishes
// decimal.Decimal which marshals as a quoted string, e.g. "2500.00").
type treasuryPaymentEvent struct {
	EventType string `json:"event_type"`
	Payload   struct {
		IntentID         string      `json:"intent_id"`
		TenantID         string      `json:"tenant_id"`
		ReferenceType    string      `json:"reference_type"`
		Status           string      `json:"status"`
		PlanCode         string      `json:"plan_code"`
		Amount           json.Number `json:"amount"`
		PaystackAuthCode string      `json:"paystack_auth_code"`
		CardLast4        string      `json:"card_last4"`
		CardType         string      `json:"card_type"`
		CardExpMonth     string      `json:"card_exp_month"`
		CardExpYear      string      `json:"card_exp_year"`
	} `json:"payload"`
	// Also accept flat structure (some publishers embed payload at top-level)
	TenantID         string      `json:"tenant_id"`
	ReferenceType    string      `json:"reference_type"`
	PlanCode         string      `json:"plan_code"`
	Amount           json.Number `json:"amount"`
	PaystackAuthCode string      `json:"paystack_auth_code"`
	CardLast4        string      `json:"card_last4"`
	CardType         string      `json:"card_type"`
	CardExpMonth     string      `json:"card_exp_month"`
	CardExpYear      string      `json:"card_exp_year"`
}

// TreasuryPaymentConsumer listens for treasury.payment.succeeded events:
//   - card_setup: stores Paystack authorization_code for auto-renewal
//   - subscription/renewal: activates/renews subscription, earns loyalty credits
type TreasuryPaymentConsumer struct {
	log           *zap.Logger
	orm           *ent.Client
	svc           *subscriptions.Service
	creditService *billing.CreditService
}

// NewTreasuryPaymentConsumer creates a new consumer.
func NewTreasuryPaymentConsumer(log *zap.Logger, orm *ent.Client) *TreasuryPaymentConsumer {
	return &TreasuryPaymentConsumer{
		log:           log.Named("consumers.treasury_payment"),
		orm:           orm,
		creditService: billing.NewCreditService(log, orm),
	}
}

// SetSubscriptionService injects the subscription service for activation flows.
func (c *TreasuryPaymentConsumer) SetSubscriptionService(svc *subscriptions.Service) {
	c.svc = svc
}

// Start subscribes to treasury.payment.succeeded and blocks until ctx is cancelled.
func (c *TreasuryPaymentConsumer) Start(ctx context.Context, js nats.JetStreamContext) error {
	eventslib.SubscribeQueueWithRebind(
		c.log,
		js,
		"treasury",
		treasuryPaymentSucceededSubject,
		"subscription-service-treasury-payments",
		func(msg *nats.Msg) {
			if err := c.handle(ctx, msg); err != nil {
				c.log.Error("failed to handle treasury.payment.succeeded", zap.Error(err))
				_ = msg.Nak()
				return
			}
			_ = msg.Ack()
		},
		nats.Durable("subscription-service-treasury-payments"),
		nats.AckExplicit(),
		nats.DeliverNew(),
		nats.MaxDeliver(5),
		nats.AckWait(30*time.Second),
		nats.ManualAck(),
	)

	c.log.Info("treasury.payment.succeeded consumer started")

	<-ctx.Done()
	return nil
}

func (c *TreasuryPaymentConsumer) handle(ctx context.Context, msg *nats.Msg) error {
	var ev treasuryPaymentEvent
	if err := json.Unmarshal(msg.Data, &ev); err != nil {
		c.log.Warn("could not parse treasury payment event, skipping", zap.Error(err))
		return nil
	}

	// Resolve fields — prefer nested payload, fall back to flat
	tenantIDStr := ev.Payload.TenantID
	if tenantIDStr == "" {
		tenantIDStr = ev.TenantID
	}
	refType := ev.Payload.ReferenceType
	if refType == "" {
		refType = ev.ReferenceType
	}
	authCode := ev.Payload.PaystackAuthCode
	if authCode == "" {
		authCode = ev.PaystackAuthCode
	}
	last4 := ev.Payload.CardLast4
	if last4 == "" {
		last4 = ev.CardLast4
	}
	cardType := ev.Payload.CardType
	if cardType == "" {
		cardType = ev.CardType
	}
	expMonth := ev.Payload.CardExpMonth
	if expMonth == "" {
		expMonth = ev.CardExpMonth
	}
	expYear := ev.Payload.CardExpYear
	if expYear == "" {
		expYear = ev.CardExpYear
	}

	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		c.log.Warn("invalid tenant_id in treasury payment event", zap.String("tenant_id", tenantIDStr))
		return nil
	}

	// Store card authorization for any event that includes one (card_setup or subscription)
	if authCode != "" {
		if err := c.saveCardAuthorization(ctx, tenantID, authCode, cardType, last4, expMonth, expYear); err != nil {
			c.log.Error("failed to save card authorization", zap.String("tenant_id", tenantIDStr), zap.Error(err))
			// Non-fatal: continue processing
		} else {
			c.log.Info("card authorization stored for auto-renewal",
				zap.String("tenant_id", tenantIDStr),
				zap.String("last4", last4),
			)
		}
	}

	if refType != "subscription" && refType != "renewal" {
		// card_setup or unknown — nothing more to do
		return nil
	}

	// Subscription payment: activate/renew
	if c.svc != nil {
		planCode := ev.Payload.PlanCode
		if planCode == "" {
			planCode = ev.PlanCode
		}
		if _, err := c.svc.RenewSubscription(ctx, subscriptions.RenewInput{
			TenantID: tenantID,
			PlanCode: planCode,
		}); err != nil {
			c.log.Error("failed to renew subscription on payment.succeeded", zap.String("tenant_id", tenantIDStr), zap.Error(err))
			return err
		}
		c.log.Info("subscription renewed via NATS payment event", zap.String("tenant_id", tenantIDStr))
	}

	// Earn loyalty credits on successful subscription payment
	amountKes := jsonNumberToInt(ev.Payload.Amount)
	if amountKes == 0 {
		amountKes = jsonNumberToInt(ev.Amount)
	}
	if amountKes > 0 {
		refID := uuid.New()
		if intentIDStr := ev.Payload.IntentID; intentIDStr != "" {
			if id, err := uuid.Parse(intentIDStr); err == nil {
				refID = id
			}
		}
		if err := c.creditService.EarnLoyaltyCredits(ctx, tenantID, amountKes, refID); err != nil {
			c.log.Warn("failed to earn loyalty credits", zap.String("tenant_id", tenantIDStr), zap.Error(err))
		}

		// Type-A referral reward: if this paying tenant was referred by another tenant,
		// credit the referrer's wallet a share of the payment (idempotent on the payment ref).
		if sub, err := c.orm.TenantSubscription.Query().
			Where(tenantsubscription.TenantIDEQ(tenantID)).
			Only(ctx); err == nil && sub.ReferredBy != nil {
			if err := c.creditService.EarnReferralBonus(ctx, *sub.ReferredBy, tenantID, amountKes, refID); err != nil {
				c.log.Warn("failed to credit referral bonus", zap.String("tenant_id", tenantIDStr), zap.Error(err))
			}
		}
	}

	return nil
}

func jsonNumberToInt(n json.Number) int {
	if f, err := n.Float64(); err == nil {
		return int(f)
	}
	return 0
}

func (c *TreasuryPaymentConsumer) saveCardAuthorization(ctx context.Context, tenantID uuid.UUID, authCode, cardType, last4, expMonth, expYear string) error {
	sub, err := c.orm.TenantSubscription.Query().
		Where(tenantsubscription.TenantIDEQ(tenantID)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			// No subscription yet — card setup may precede subscription creation.
			// Log and skip; the card details must be re-entered after subscribing.
			c.log.Warn("no subscription found to attach card authorization",
				zap.String("tenant_id", tenantID.String()),
				zap.String("last4", last4),
			)
			return nil
		}
		return err
	}

	meta := make(map[string]any)
	for k, v := range sub.Metadata {
		meta[k] = v
	}
	meta["paystack_auth_code"] = authCode
	meta["payment_method"] = map[string]any{
		"type":        "card",
		"brand":       cardType,
		"last4":       last4,
		"expiryMonth": expMonth,
		"expiryYear":  expYear,
	}

	_, err = c.orm.TenantSubscription.UpdateOneID(sub.ID).
		SetMetadata(meta).
		Save(ctx)
	return err
}
