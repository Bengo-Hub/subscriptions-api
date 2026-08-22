package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// SubscriptionCreditTransaction is the immutable audit trail for all credit wallet
// movements (earn, redeem, gift, auto-apply, expiry, manual adjustment).
type SubscriptionCreditTransaction struct {
	ent.Schema
}

func (SubscriptionCreditTransaction) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).
			Default(uuid.New).
			Immutable(),
		field.UUID("tenant_id", uuid.UUID{}),
		field.UUID("credit_id", uuid.UUID{}).
			Comment("FK to subscription_credits wallet"),
		field.Enum("type").
			Values(
				"earned",          // loyalty % from subscription payment
				"coupon_redeemed", // coupon code applied
				"gifted",          // platform admin manual gift
				"auto_applied",    // auto-deducted at renewal
				"expired",         // balance expiry sweep
				"manual_adjusted", // platform admin correction
				"referral_bonus",  // credit earned for referring another tenant who paid
			),
		field.Int("amount_kes").
			Comment("Positive = credit added; negative = credit deducted"),
		field.UUID("ref_id", uuid.UUID{}).
			Optional().
			Nillable().
			Comment("ID of the referencing entity (payment_intent, coupon, etc.)"),
		field.String("ref_type").
			Optional().
			Nillable().
			MaxLen(64).
			Comment("Type of referencing entity: payment, coupon, admin_gift, renewal"),
		field.String("description").
			Optional().
			Nillable().
			MaxLen(255),
		field.Time("created_at").
			Default(time.Now).
			Immutable(),
	}
}

func (SubscriptionCreditTransaction) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("credit", SubscriptionCredit.Type).
			Ref("transactions").
			Unique().
			Required().
			Field("credit_id"),
	}
}

func (SubscriptionCreditTransaction) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("tenant_id"),
		index.Fields("credit_id"),
		index.Fields("type"),
		index.Fields("created_at"),
		// Last-resort double-credit guard for the two EVENT-DRIVEN earn paths, whose ref_id is
		// the payment idempotency key: an at-least-once NATS redelivery cannot insert a second
		// credit row for the same (tenant, type, payment) even if the application-level check
		// is bypassed or races with itself.
		//
		// PARTIAL by design — the other types legitimately repeat the same ref_id:
		// auto_applied uses the tenant's own id as a self-referential ref on every renewal,
		// and gifted uses the granting admin's user id on every gift. A blanket unique index
		// would break both.
		index.Fields("tenant_id", "type", "ref_id").
			Unique().
			Annotations(entsql.IndexWhere("type IN ('earned', 'referral_bonus')")),
	}
}
