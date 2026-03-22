package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// SubscriptionsPermission holds the schema definition for subscriptions service permissions.
type SubscriptionsPermission struct {
	ent.Schema
}

// Fields of the SubscriptionsPermission.
func (SubscriptionsPermission) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).
			Default(uuid.New).
			Immutable(),
		field.String("permission_code").
			NotEmpty().
			Unique().
			Comment("Permission code: subscriptions.plans.view, etc."),
		field.String("name").
			NotEmpty().
			Comment("Display name"),
		field.String("module").
			NotEmpty().
			Comment("Module: plans, features, bundles, pricing, subscriptions, usage, billing, config, users"),
		field.String("action").
			NotEmpty().
			Comment("Action: add, view, view_own, change, change_own, delete, delete_own, manage, manage_own"),
		field.String("resource").
			Optional().
			Comment("Resource: plans, features, etc."),
		field.Text("description").
			Optional(),
		field.Time("created_at").
			Default(time.Now).
			Immutable(),
	}
}

// Edges of the SubscriptionsPermission.
func (SubscriptionsPermission) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("roles", SubscriptionsRole.Type).Ref("permissions").Through("role_permissions", RolePermission.Type),
	}
}

// Indexes of the SubscriptionsPermission.
func (SubscriptionsPermission) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("permission_code").Unique(),
		index.Fields("module"),
		index.Fields("action"),
		index.Fields("module", "action"),
	}
}
