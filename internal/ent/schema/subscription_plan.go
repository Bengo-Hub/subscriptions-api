package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// SubscriptionPlan holds the schema definition for subscription plans.
type SubscriptionPlan struct {
	ent.Schema
}

// Fields of the SubscriptionPlan.
func (SubscriptionPlan) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).
			Default(uuid.New).
			Immutable(),
		field.String("plan_code").
			NotEmpty().
			Unique().
			Comment("Plan code (STARTER, GROWTH, PROFESSIONAL, CUSTOM)"),
		field.String("name").
			NotEmpty().
			Comment("Display name"),
		field.Text("description").
			Optional(),
		field.String("billing_cycle").
			NotEmpty().
			Default("MONTHLY").
			Comment("MONTHLY, QUARTERLY, ANNUAL"),
		field.Float("base_price").
			Comment("Base subscription price"),
		field.String("currency").
			Default("KES").
			Comment("ISO currency code"),
		field.Bool("is_active").
			Default(true),
		field.Bool("is_public").
			Default(true),
		field.Int("tier_order").
			Comment("Display order (1=Starter, 2=Growth, 3=Professional)"),
		field.JSON("tier_limits_json", map[string]any{}).
			Default(map[string]any{}).
			Comment("Tier limits (max_admins, max_riders, max_orders_per_day, etc.)"),
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

// Edges of the SubscriptionPlan.
func (SubscriptionPlan) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("features", PlanFeature.Type),
		edge.To("pricing_history", PlanPricingHistory.Type),
		edge.To("subscriptions", TenantSubscription.Type),
	}
}

// Indexes of the SubscriptionPlan.
func (SubscriptionPlan) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("plan_code"),
		index.Fields("is_active"),
		index.Fields("tier_order"),
	}
}
