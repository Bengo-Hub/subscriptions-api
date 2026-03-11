package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// PlanFeature holds the schema definition for plan features.
type PlanFeature struct {
	ent.Schema
}

// Fields of the PlanFeature.
func (PlanFeature) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).
			Default(uuid.New).
			Immutable(),
		field.UUID("plan_id", uuid.UUID{}).
			Comment("Plan identifier"),
		field.String("feature_code").
			NotEmpty().
			Comment("Feature code (loyalty_program, multi_outlet, pos_integration, etc.)"),
		field.Bool("is_included").
			Default(true),
		field.Int("limit_value").
			Optional().
			Comment("Feature-specific limit (e.g., max 5 outlets)"),
		field.Float("overage_unit_price").
			Default(0).
			Comment("Price per unit above the limit"),
		field.JSON("metadata", map[string]any{}).
			Default(map[string]any{}),
		field.Time("created_at").
			Default(time.Now).
			Immutable(),
	}
}

// Edges of the PlanFeature.
func (PlanFeature) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("plan", SubscriptionPlan.Type).
			Ref("features").
			Field("plan_id").
			Required().
			Unique(),
	}
}

// Indexes of the PlanFeature.
func (PlanFeature) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("plan_id"),
		index.Fields("feature_code"),
		index.Fields("plan_id", "feature_code").
			Unique(),
	}
}
