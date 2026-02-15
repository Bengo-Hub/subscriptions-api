package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// PlanPricingHistory holds the schema definition for pricing history.
type PlanPricingHistory struct {
	ent.Schema
}

// Fields of the PlanPricingHistory.
func (PlanPricingHistory) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).
			Default(uuid.New).
			Immutable(),
		field.UUID("plan_id", uuid.UUID{}).
			Comment("Plan identifier"),
		field.Float("base_price").
			Comment("Base price at this version"),
		field.Time("effective_from").
			Comment("Effective start date"),
		field.Time("effective_to").
			Optional().
			Comment("Effective end date (null if current)"),
		field.UUID("changed_by", uuid.UUID{}).
			Optional().
			Comment("User who changed pricing (from auth-service)"),
		field.Text("change_reason").
			Optional(),
		field.JSON("metadata", map[string]any{}).
			Default(map[string]any{}),
		field.Time("created_at").
			Default(time.Now).
			Immutable(),
	}
}

// Edges of the PlanPricingHistory.
func (PlanPricingHistory) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("plan", SubscriptionPlan.Type).
			Ref("pricing_history").
			Field("plan_id").
			Required().
			Unique(),
	}
}

// Indexes of the PlanPricingHistory.
func (PlanPricingHistory) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("plan_id"),
		index.Fields("effective_from", "effective_to"),
	}
}

