package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// ServiceChargePlan defines a service-charge pricing model.
// When a tenant's product subscription uses a service-charge plan, the platform
// deducts a commission (percentage, fixed, or tiered) from each transaction
// processed through that service. The charge is split: service charge → platform
// account, remainder → tenant account (via treasury-api settlement).
type ServiceChargePlan struct {
	ent.Schema
}

// Fields of the ServiceChargePlan.
func (ServiceChargePlan) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).
			Default(uuid.New).
			Immutable(),
		field.String("code").
			NotEmpty().
			Unique().
			Comment("Unique plan code (e.g., SC_ORDERING_5PCT, SC_LOGISTICS_FLAT)"),
		field.String("name").
			NotEmpty().
			Comment("Display name (e.g., 'Ordering 5% Service Charge')"),
		field.Text("description").
			Optional(),
		field.Enum("charge_type").
			Values("PERCENTAGE", "FIXED_PER_TRANSACTION", "TIERED").
			Default("PERCENTAGE").
			Comment("How the charge is calculated: percentage of transaction amount, fixed per transaction, or tiered based on volume"),
		field.Float("charge_value").
			Default(0).
			Comment("Charge amount: percentage (e.g., 5.0 = 5%) or fixed amount in currency"),
		field.String("currency").
			Default("KES"),
		field.Float("min_charge").
			Optional().
			Nillable().
			Comment("Minimum charge per transaction (floor)"),
		field.Float("max_charge").
			Optional().
			Nillable().
			Comment("Maximum charge per transaction (cap)"),
		field.JSON("tier_rules", []map[string]any{}).
			Optional().
			Comment("Tiered charge rules: [{min_amount, max_amount, charge_value, charge_type}]"),
		field.Strings("applicable_services").
			Optional().
			Comment("Product codes this plan can apply to (empty = any service)"),
		field.Bool("is_active").
			Default(true),
		field.Bool("is_default").
			Default(false).
			Comment("Default service charge plan for new subscriptions (only one should be true per service)"),
		field.JSON("metadata", map[string]any{}).
			Default(map[string]any{}),
		field.Time("created_at").
			Default(time.Now).
			Immutable(),
		field.Time("updated_at").
			Default(time.Now).
			UpdateDefault(time.Now),
	}
}

// Edges of the ServiceChargePlan.
func (ServiceChargePlan) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("product_subscriptions", ProductSubscription.Type),
	}
}

// Indexes of the ServiceChargePlan.
func (ServiceChargePlan) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("code"),
		index.Fields("is_active"),
		index.Fields("charge_type"),
	}
}
