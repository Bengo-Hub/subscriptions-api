package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// ApiTokenTransaction is the immutable ledger for ApiTokenWallet movements: monthly plan grants,
// self-serve top-ups, per-call deductions, refunds, and admin adjustments. Mirrors
// SubscriptionCreditTransaction's shape (append-only, tenant-scoped, ref_id/ref_type for the
// originating entity).
type ApiTokenTransaction struct {
	ent.Schema
}

func (ApiTokenTransaction) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).
			Default(uuid.New).
			Immutable(),
		field.UUID("tenant_id", uuid.UUID{}),
		field.UUID("wallet_id", uuid.UUID{}).
			Comment("FK to api_token_wallets"),
		field.String("service_tag").
			MaxLen(64).
			Comment("Denormalised from the wallet for fast per-service ledger queries"),
		field.Enum("action").
			Values(
				"grant",      // monthly plan-included token allowance, credited each renewal
				"topup",      // self-serve purchase via a Treasury payment
				"deduction",  // spent on a metered API call
				"refund",     // reversal of a deduction (e.g. a failed downstream write)
				"adjustment", // platform admin manual correction
			),
		field.Int64("tokens").
			Comment("Positive = added to balance; negative = deducted"),
		field.Int64("new_balance").
			Comment("Wallet balance immediately after this transaction"),
		field.String("endpoint_pattern").
			Optional().
			Nillable().
			MaxLen(128).
			Comment("For deductions: which external API route this charge was for"),
		field.Float("unit_cost_kes").
			Optional().
			Nillable().
			Comment("For topups: the KES price paid per token"),
		field.UUID("ref_id", uuid.UUID{}).
			Optional().
			Nillable().
			Comment("ID of the referencing entity (payment_intent, external call, admin user)"),
		field.String("ref_type").
			Optional().
			Nillable().
			MaxLen(64).
			Comment("Type of referencing entity: payment, external_call, renewal, admin_adjustment"),
		field.String("description").
			Optional().
			Nillable().
			MaxLen(255),
		field.JSON("metadata", map[string]any{}).
			Optional(),
		field.Time("created_at").
			Default(time.Now).
			Immutable(),
	}
}

func (ApiTokenTransaction) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("wallet", ApiTokenWallet.Type).
			Ref("transactions").
			Unique().
			Required().
			Field("wallet_id"),
	}
}

func (ApiTokenTransaction) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("tenant_id"),
		index.Fields("wallet_id"),
		index.Fields("service_tag"),
		index.Fields("action"),
		index.Fields("created_at"),
	}
}
