package consumers

import (
	"context"
	"encoding/json"
	"time"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/tenantsubscription"
	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"go.uber.org/zap"
)

const treasuryPaymentSucceededSubject = "treasury.payment.succeeded"

// treasuryPaymentEvent is the payload from treasury's payment.succeeded NATS event.
type treasuryPaymentEvent struct {
	EventType string `json:"event_type"`
	Payload   struct {
		IntentID        string `json:"intent_id"`
		TenantID        string `json:"tenant_id"`
		ReferenceType   string `json:"reference_type"`
		Status          string `json:"status"`
		PaystackAuthCode string `json:"paystack_auth_code"`
		CardLast4       string `json:"card_last4"`
		CardType        string `json:"card_type"`
		CardExpMonth    string `json:"card_exp_month"`
		CardExpYear     string `json:"card_exp_year"`
	} `json:"payload"`
	// Also accept flat structure (some publishers embed payload at top-level)
	TenantID        string `json:"tenant_id"`
	ReferenceType   string `json:"reference_type"`
	PaystackAuthCode string `json:"paystack_auth_code"`
	CardLast4       string `json:"card_last4"`
	CardType        string `json:"card_type"`
	CardExpMonth    string `json:"card_exp_month"`
	CardExpYear     string `json:"card_exp_year"`
}

// TreasuryPaymentConsumer listens for treasury.payment.succeeded events and stores
// Paystack authorization_code for card_setup intents to enable auto-renewal.
type TreasuryPaymentConsumer struct {
	log *zap.Logger
	orm *ent.Client
}

// NewTreasuryPaymentConsumer creates a new consumer.
func NewTreasuryPaymentConsumer(log *zap.Logger, orm *ent.Client) *TreasuryPaymentConsumer {
	return &TreasuryPaymentConsumer{
		log: log.Named("consumers.treasury_payment"),
		orm: orm,
	}
}

// Start subscribes to treasury.payment.succeeded and blocks until ctx is cancelled.
func (c *TreasuryPaymentConsumer) Start(ctx context.Context, js nats.JetStreamContext) error {
	sub, err := js.Subscribe(
		treasuryPaymentSucceededSubject,
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
	if err != nil {
		return err
	}

	c.log.Info("treasury.payment.succeeded consumer started")

	<-ctx.Done()
	_ = sub.Unsubscribe()
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

	if refType != "card_setup" || authCode == "" {
		// Not a card setup event or no authorization code — nothing to do.
		return nil
	}

	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		c.log.Warn("invalid tenant_id in treasury payment event", zap.String("tenant_id", tenantIDStr))
		return nil
	}

	if err := c.saveCardAuthorization(ctx, tenantID, authCode, cardType, last4, expMonth, expYear); err != nil {
		c.log.Error("failed to save card authorization", zap.String("tenant_id", tenantIDStr), zap.Error(err))
		return err
	}

	c.log.Info("card authorization stored for auto-renewal",
		zap.String("tenant_id", tenantIDStr),
		zap.String("last4", last4),
	)
	return nil
}

func (c *TreasuryPaymentConsumer) saveCardAuthorization(ctx context.Context, tenantID uuid.UUID, authCode, cardType, last4, expMonth, expYear string) error {
	sub, err := c.orm.TenantSubscription.Query().
		Where(tenantsubscription.TenantIDEQ(tenantID)).
		Only(ctx)
	if err != nil {
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
