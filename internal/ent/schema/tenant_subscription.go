package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// TenantSubscription holds the schema definition for tenant subscriptions.
// Links a tenant to their active subscription plan with status and period tracking.
type TenantSubscription struct {
	ent.Schema
}

// Fields of the TenantSubscription.
func (TenantSubscription) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).
			Default(uuid.New).
			Immutable(),
		field.UUID("tenant_id", uuid.UUID{}).
			Comment("Reference to auth-service tenant"),
		field.UUID("plan_id", uuid.UUID{}).
			Comment("Reference to subscription_plans"),
		field.Enum("status").
			Values("ACTIVE", "TRIAL", "EXPIRED", "CANCELLED", "SUSPENDED").
			Default("TRIAL").
			Comment("Current subscription status"),
		field.Time("trial_ends_at").
			Optional().
			Nillable().
			Comment("End of trial period"),
		field.Time("current_period_start").
			Comment("Start of current billing period"),
		field.Time("current_period_end").
			Comment("End of current billing period"),
		field.Time("cancelled_at").
			Optional().
			Nillable().
			Comment("When subscription was cancelled"),
		field.String("cancel_reason").
			Optional().
			Nillable().
			Comment("Reason for cancellation"),
		field.Enum("billing_cycle").
			Values("MONTHLY", "QUARTERLY", "ANNUAL", "ONE_TIME").
			Default("MONTHLY").
			Comment("Tenant's chosen billing cycle (may override plan default)"),
		field.Float("applied_discount").
			Default(0).
			Comment("Discount applied to this subscription based on rules (e.g., 20% for yearly)"),
		field.String("bundle_code").
			Optional().
			Nillable().
			Comment("Bundle code if subscribed via bundle (delivery, pos-suite, complete)"),
		field.UUID("payment_method_id", uuid.UUID{}).
			Optional().
			Nillable().
			Comment("Reference to treasury payment method"),
		field.JSON("metadata", map[string]any{}).
			Optional().
			Default(map[string]any{}),
		field.Time("created_at").
			Default(time.Now).
			Immutable(),
		field.Time("updated_at").
			Default(time.Now).
			UpdateDefault(time.Now),
	}
}

// Edges of the TenantSubscription.
func (TenantSubscription) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("tenant", Tenant.Type).
			Ref("subscriptions").
			Unique().
			Required().
			Field("tenant_id"),
		edge.From("plan", SubscriptionPlan.Type).
			Ref("subscriptions").
			Unique().
			Required().
			Field("plan_id"),
		edge.To("product_subscriptions", ProductSubscription.Type),
	}
}

// Indexes of the TenantSubscription.
func (TenantSubscription) Indexes() []ent.Index {
	return []ent.Index{
		// Each tenant has one active subscription
		index.Fields("tenant_id").
			Unique(),
		// Query by status
		index.Fields("status"),
		// Query subscriptions expiring soon
		index.Fields("current_period_end"),
	}
}
