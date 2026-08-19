package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// EmailLicense represents a per-user email hosting license a tenant has purchased
// and (optionally) assigned to a mailbox address — the per-seat unit the
// email-provisioner bridge service watches to provision/suspend Stalwart mailboxes.
// See .claude/plans/codevertex-email-hosting-service-plan.md Part 3.
type EmailLicense struct {
	ent.Schema
}

// Fields of the EmailLicense.
func (EmailLicense) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).
			Default(uuid.New).
			Immutable(),
		field.UUID("tenant_subscription_id", uuid.UUID{}).
			Comment("FK to tenant_subscriptions — the tenant that purchased this seat"),
		field.UUID("product_subscription_id", uuid.UUID{}).
			Comment("FK to product_subscriptions for the email-hosting product"),
		field.UUID("email_plan_id", uuid.UUID{}).
			Comment("FK to email_plans — the tier this license is on"),
		field.String("assigned_to_email").
			Optional().
			Nillable().
			Comment("Mailbox address this license is assigned to, e.g. user@tenantdomain.com"),
		field.UUID("assigned_to_user_id", uuid.UUID{}).
			Optional().
			Nillable().
			Comment("FK to the auth-api user this license is assigned to, if it maps to a platform account"),
		field.Enum("status").
			Values("AVAILABLE", "ASSIGNED", "SUSPENDED", "EXPIRED", "DELETED").
			Default("AVAILABLE"),
		field.String("suspend_reason").
			Optional().
			Nillable().
			Comment("Set when status=SUSPENDED — e.g. bounce_threshold_exceeded, complaint_threshold_exceeded, billing, manual. Drives the abuse-response escalation ladder in plan Part 6."),
		field.Int("storage_quota_gb").
			Comment("Denormalized from the EmailPlan at assignment time, so a later plan-price change doesn't silently resize an already-provisioned mailbox"),
		field.JSON("features_json", map[string]any{}).
			Default(map[string]any{}).
			Comment("Denormalized copy of the EmailPlan's features_json at assignment/upgrade time"),
		field.Time("assigned_at").
			Optional().
			Nillable(),
		field.Time("expires_at").
			Optional().
			Nillable(),
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

// Edges of the EmailLicense.
func (EmailLicense) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("tenant_subscription", TenantSubscription.Type).
			Ref("email_licenses").
			Field("tenant_subscription_id").
			Required().
			Unique(),
		edge.From("product_subscription", ProductSubscription.Type).
			Ref("email_licenses").
			Field("product_subscription_id").
			Required().
			Unique(),
		edge.From("email_plan", EmailPlan.Type).
			Ref("licenses").
			Field("email_plan_id").
			Required().
			Unique(),
	}
}

// Indexes of the EmailLicense.
func (EmailLicense) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("tenant_subscription_id", "status"),
		// One license per mailbox address per email-hosting product subscription.
		index.Fields("product_subscription_id", "assigned_to_email").
			Unique(),
		index.Fields("status"),
	}
}
