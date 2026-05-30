package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// CustomAddon is a platform-admin-only recurring or one-time charge attached to a
// specific tenant. Addons can be tied to a platform service (e.g. "notifications"
// for email hosting) or be independent (e.g. dedicated support line).
// Only users with is_platform=true can create or modify addons.
type CustomAddon struct {
	ent.Schema
}

func (CustomAddon) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).
			Default(uuid.New).
			Immutable(),
		field.UUID("tenant_id", uuid.UUID{}).
			Comment("Tenant this addon is billed to"),
		field.String("name").
			MaxLen(128),
		field.String("description").
			Optional().
			Nillable(),
		field.String("service_code").
			Optional().
			Nillable().
			MaxLen(64).
			Comment("Platform service this addon extends, e.g. 'notifications', 'pos'. Null = independent addon."),
		field.String("service_addon_type").
			Optional().
			Nillable().
			MaxLen(64).
			Comment("Sub-type within the service, e.g. 'email_hosting', 'sms_bundle', 'custom_domain'"),
		field.Enum("billing_cycle").
			Values("monthly", "annual", "one_time").
			Default("monthly"),
		field.Int("unit_price_kes").
			Comment("Price per unit in KES"),
		field.Int("quantity").
			Default(1),
		field.Enum("status").
			Values("active", "paused", "cancelled").
			Default("active"),
		field.String("notes").
			Optional().
			Nillable().
			MaxLen(512).
			Comment("Internal notes for platform admins"),
		field.UUID("created_by_user_id", uuid.UUID{}).
			Comment("Platform admin user who attached this addon"),
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

func (CustomAddon) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("tenant_id"),
		index.Fields("tenant_id", "status"),
		index.Fields("service_code"),
	}
}
