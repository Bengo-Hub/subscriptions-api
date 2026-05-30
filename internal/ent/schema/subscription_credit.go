package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// SubscriptionCredit is the per-tenant credit wallet for subscription billing.
// Credits are earned via loyalty (% of each subscription payment) or gifted by
// platform admins and auto-applied on the next renewal invoice.
type SubscriptionCredit struct {
	ent.Schema
}

func (SubscriptionCredit) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).
			Default(uuid.New).
			Immutable(),
		field.UUID("tenant_id", uuid.UUID{}).
			Comment("One wallet per tenant"),
		field.Int("balance_kes").
			Default(0).
			Comment("Current spendable balance in KES (integer cents not used; 1 unit = 1 KES)"),
		field.Int("lifetime_earned_kes").
			Default(0).
			Comment("Cumulative amount ever earned (for loyalty tier display)"),
		field.Float("loyalty_rate").
			Default(0.02).
			Comment("Fraction of each subscription payment that earns credits (default 2%)"),
		field.Time("updated_at").
			Default(time.Now).
			UpdateDefault(time.Now),
	}
}

func (SubscriptionCredit) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("transactions", SubscriptionCreditTransaction.Type),
	}
}

func (SubscriptionCredit) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("tenant_id").Unique(),
	}
}
