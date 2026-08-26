package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// ApiTokenWallet is a per-tenant, per-service prepaid token balance for metered external API
// products (e.g. the external eTIMS fiscalization API, service_tag "etims_api"). Deliberately
// generalized by service_tag rather than eTIMS-specific, so a future external API (notifications,
// SSO) can reuse the same primitive instead of a copy-pasted wallet. Modeled on SubscriptionCredit
// (the existing KES loyalty wallet) but tracks TOKENS, not KES, and is gated in real time at
// request time (a call is refused once the balance runs out) rather than applied once at renewal.
type ApiTokenWallet struct {
	ent.Schema
}

func (ApiTokenWallet) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).
			Default(uuid.New).
			Immutable(),
		field.UUID("tenant_id", uuid.UUID{}).
			Comment("One wallet per (tenant, service_tag)"),
		field.String("service_tag").
			MaxLen(64).
			Comment("Which external API product this wallet meters, e.g. etims_api"),
		field.Int64("balance").
			Default(0).
			Comment("Current spendable token balance"),
		field.Int64("lifetime_granted").
			Default(0).
			Comment("Cumulative tokens ever granted or purchased (for display/analytics only)"),
		field.Int64("low_balance_threshold").
			Default(50).
			Comment("Balance at/below which a low-balance warning is surfaced to the tenant"),
		field.Time("created_at").
			Default(time.Now).
			Immutable(),
		field.Time("updated_at").
			Default(time.Now).
			UpdateDefault(time.Now),
	}
}

func (ApiTokenWallet) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("transactions", ApiTokenTransaction.Type),
	}
}

func (ApiTokenWallet) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("tenant_id", "service_tag").Unique(),
	}
}
