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
			Values("ACTIVE", "TRIAL", "EXPIRED", "CANCELLED", "SUSPENDED", "DORMANT").
			Default("TRIAL").
			Comment("Current subscription status. DORMANT = no activity >60d and unpaid; awaiting suspend/purge"),
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
			Values("MONTHLY", "QUARTERLY", "SEMI_ANNUAL", "ANNUAL", "ONE_TIME").
			Default("MONTHLY").
			Comment("Tenant's chosen billing cycle (may override plan default). SEMI_ANNUAL/ANNUAL (>=6 months) waive the one-time setup fee"),
		field.Float("applied_discount").
			Default(0).
			Comment("Discount applied to this subscription based on rules (e.g., 20% for yearly)"),
		field.Float("setup_fee_amount").
			Default(0).
			Comment("One-time setup fee charged for this subscription (snapshot of plan.setup_fee at creation). 0 = none/waived"),
		field.Time("setup_fee_charged_at").
			Optional().
			Nillable().
			Comment("When the one-time setup fee was charged. Nil = not yet charged; set once, never re-charged on renewal"),
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
		field.Bool("referral_bonus_paid").
			Default(false).
			Comment("True once the Type-A referral bonus for THIS tenant has been paid to its referrer. The bonus rewards bringing in the referral, so it is paid once on the first successful payment — never again on renewals"),
		// ── Subscription Terms & Conditions acceptance (captured at subscribe time) ──
		field.String("terms_version").
			Optional().
			Nillable().
			Comment("Version of the subscription T&C the tenant accepted (e.g. 2026-06-20)"),
		field.Time("terms_accepted_at").
			Optional().
			Nillable().
			Comment("When the subscription T&C were accepted"),
		field.UUID("terms_accepted_by", uuid.UUID{}).
			Optional().
			Nillable().
			Comment("User who accepted the subscription T&C"),
		// ── Account dormancy lifecycle ──────────────────────────────────────────
		field.Time("last_activity_at").
			Optional().
			Nillable().
			Comment("Last billable usage event for this tenant; drives >60-day dormancy detection"),
		field.Time("dormant_at").
			Optional().
			Nillable().
			Comment("When the account was first flagged dormant (>60d idle, unpaid). Cleared on reactivation"),
		field.Time("purge_grace_ends_at").
			Optional().
			Nillable().
			Comment("End of the 7-day grace window after a dormancy notice; at expiry the account is suspended + queued for purge"),
		field.Bool("pending_purge").
			Default(false).
			Comment("True once the grace window elapsed unpaid: account suspended and awaiting platform-owner-confirmed data purge"),
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
		edge.To("email_licenses", EmailLicense.Type),
		edge.To("email_domains", TenantEmailDomain.Type),
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
		// Dormancy detection: scan by last activity
		index.Fields("last_activity_at"),
		// A referral code uniquely identifies one referrer tenant.
		index.Fields("referral_code").
			Unique(),
	}
}
