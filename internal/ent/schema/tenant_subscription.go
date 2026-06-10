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
		field.Bool("allow_overage").
			Default(false).
			Comment("Opt-in master switch: when true, metered throughput limits may be exceeded and the excess accrues as OverageCharge billed on the next renewal"),
		field.Time("overage_enabled_at").
			Optional().
			Nillable().
			Comment("When the tenant last enabled extra usage (allow_overage)"),
		field.UUID("payment_method_id", uuid.UUID{}).
			Optional().
			Nillable().
			Comment("Reference to treasury payment method"),
		field.UUID("referred_by", uuid.UUID{}).
			Optional().
			Nillable().
			Comment("Tenant that referred this tenant (Type-A referral); credited when this tenant pays"),
		field.String("referral_code").
			Optional().
			Nillable().
			Comment("This tenant's own shareable referral code; others sign up with it to attribute the referral"),
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
		edge.To("overage_charges", OverageCharge.Type),
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
		// A referral code uniquely identifies one referrer tenant.
		index.Fields("referral_code").
			Unique(),
	}
}
