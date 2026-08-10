package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// EmailPlan defines the email hosting tiers (Lite/Standard/Professional) — the
// Zoho-Mail-like per-seat pricing catalog for the email-hosting product. See
// .claude/plans/codevertex-email-hosting-service-plan.md Part 3/4 for the full
// license-based subscription model this feeds into.
type EmailPlan struct {
	ent.Schema
}

// Fields of the EmailPlan.
func (EmailPlan) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).
			Default(uuid.New).
			Immutable(),
		field.String("code").
			NotEmpty().
			Unique().
			Comment("EMAIL_LITE, EMAIL_STANDARD, EMAIL_PROFESSIONAL"),
		field.String("name").
			NotEmpty().
			Comment("Display name"),
		field.Text("description").
			Optional(),
		field.Float("price_per_user_monthly").
			Comment("KES, per licensed seat per month"),
		field.Float("price_per_user_yearly").
			Optional().
			Nillable().
			Comment("KES, per licensed seat per year (discounted vs 12x monthly)"),
		field.Int("storage_per_user_gb").
			Comment("Mailbox storage quota per seat, in GB"),
		field.Int("max_aliases").
			Default(5).
			Comment("Max aliases per mailbox; -1 = unlimited"),
		field.Int("max_email_size_mb").
			Default(25).
			Comment("Max single email (incl. attachments) size in MB"),
		field.JSON("features_json", map[string]any{}).
			Default(map[string]any{}).
			Comment("Boolean feature flags (forwarding, autoresponder, calendar, contacts, shared_mailboxes, custom_sieve_filters, priority_support, admin_delegation) plus numeric rate-limit tier defaults (max_daily_sends_per_seat, max_recipients_per_day) — see plan Part 6's 4-layer rate-limiting design. Deliberately NOT typed columns so new flags/limits never need a migration."),
		field.Bool("is_active").
			Default(true),
		field.Bool("is_public").
			Default(true),
		field.Int("sort_order").
			Default(0).
			Comment("Display ordering, ascending"),
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

// Edges of the EmailPlan.
func (EmailPlan) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("licenses", EmailLicense.Type),
	}
}

// Indexes of the EmailPlan.
func (EmailPlan) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("code"),
		index.Fields("is_active"),
		index.Fields("sort_order"),
	}
}
