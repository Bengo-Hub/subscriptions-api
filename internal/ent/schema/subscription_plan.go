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
			Comment("MONTHLY, QUARTERLY, ANNUAL, ONE_TIME"),
		field.Float("base_price").
			Comment("Base subscription price"),
		field.Float("onetime_all_products_price").
			Optional().
			Nillable().
			Comment("Applicable for ONE_TIME custom plans. If set, this overrides product-summation."),
		field.Bool("use_sum_based_pricing").
			Default(false).
			Comment("If true, plan price is calculated as sum of individual products (standard for Custom/One-time)"),
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
		field.Enum("plan_type").
			Values("TIERED", "STANDALONE_SERVICE", "BUNDLE", "CUSTOM").
			Default("TIERED").
			Comment("Type: TIERED (Urban Cafe), STANDALONE_SERVICE, BUNDLE, CUSTOM"),
		field.String("service_tag").
			Optional().
			Nillable().
			Comment("Service this plan belongs to: ordering, truload, logistics, inventory, erp, pos, marketflow, cafe_website. Null = bundle or platform-wide."),
		field.Int("free_trial_days").
			Default(14).
			Comment("Number of free trial days for new subscribers. Platform admins can override per plan. 0 = no trial."),
		field.JSON("discount_rules", []map[string]any{}).
			Default([]map[string]any{}).
			Optional().
			Comment("Dynamic discounting rules. Types: YEARLY (percentage), LOYALTY (percentage/min_months), NEW_CUSTOMER (trial_days/percentage)."),
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
		edge.To("override_product_subscriptions", ProductSubscription.Type),
	}
}

// Indexes of the SubscriptionPlan.
func (SubscriptionPlan) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("plan_code"),
		index.Fields("is_active"),
		index.Fields("tier_order"),
		index.Fields("service_tag"),
	}
}
