package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// TenantEmailDomain represents a tenant's own custom domain onboarded for
// email hosting (as opposed to the shared platform domain). Real relational
// schema, not a metadata blob — it has its own verification lifecycle
// (PENDING_DNS/VERIFIED/FAILED) and needs a unique index on domain, both of
// which don't fit the "small additive field on an existing entity" case that
// justifies using EmailLicense.metadata elsewhere in this plan. See
// .claude/plans/email-hosting-license-crud-mailbox-ui-deliverability-2026-08-19.md
// Part 1.3.
type TenantEmailDomain struct {
	ent.Schema
}

// Fields of the TenantEmailDomain.
func (TenantEmailDomain) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).
			Default(uuid.New).
			Immutable(),
		field.UUID("tenant_subscription_id", uuid.UUID{}).
			Comment("FK to tenant_subscriptions — the tenant that owns this domain"),
		field.String("domain").
			NotEmpty().
			Comment("The tenant's own domain, e.g. acmecorp.com — globally unique across the platform"),
		field.Enum("status").
			Values("PENDING_DNS", "VERIFIED", "FAILED").
			Default("PENDING_DNS"),
		field.String("dkim_selector").
			Optional().
			Nillable().
			Comment("The dated DKIM selector Stalwart generated for this domain at creation, e.g. cvx2026a"),
		field.String("stalwart_domain_id").
			Optional().
			Nillable().
			Comment("Stalwart's own x:Domain object id, returned by email-provisioner's CreateDomain"),
		field.Time("verified_at").
			Optional().
			Nillable(),
		field.Time("last_checked_at").
			Optional().
			Nillable().
			Comment("Last time VerifyDomainNow ran a live DNS check, pass or fail"),
		field.String("failure_reason").
			Optional().
			Nillable().
			Comment("Set when status=FAILED — which record(s) didn't match on the last check"),
		field.JSON("metadata", map[string]any{}).
			Default(map[string]any{}).
			Comment("Display/audit snapshot of the DNS records shown to the tenant (MX/SPF/DKIM/DMARC/autoconfig) — a display blob, not queried on, so metadata is the right fit here unlike this entity's own lifecycle fields"),
		field.Time("created_at").
			Default(time.Now).
			Immutable(),
		field.Time("updated_at").
			Default(time.Now).
			UpdateDefault(time.Now),
	}
}

// Edges of the TenantEmailDomain.
func (TenantEmailDomain) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("tenant_subscription", TenantSubscription.Type).
			Ref("email_domains").
			Field("tenant_subscription_id").
			Required().
			Unique(),
	}
}

// Indexes of the TenantEmailDomain.
func (TenantEmailDomain) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("domain").Unique(),
		index.Fields("tenant_subscription_id", "status"),
	}
}
