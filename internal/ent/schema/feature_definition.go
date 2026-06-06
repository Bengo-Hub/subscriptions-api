package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// FeatureDefinition is the platform-wide catalog of every feature and limit
// exposed by any service that integrates with the subscriptions-api. It is the
// single source of truth that powers the admin plan-builder UI (service-grouped,
// categorized feature picker), usage display, and the feature/limit reconciliation
// report. Plans reference these by feature_code via PlanFeature / tier_limits_json.
type FeatureDefinition struct {
	ent.Schema
}

// Fields of the FeatureDefinition.
func (FeatureDefinition) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).
			Default(uuid.New).
			Immutable(),
		field.String("feature_code").
			NotEmpty().
			Unique().
			Comment("Canonical feature/limit key referenced by plans and service gates (e.g. loyalty_program, max_warehouses)"),
		field.String("service_tag").
			NotEmpty().
			Comment("Owning service: ordering, pos, inventory, treasury, logistics, erp, marketflow, truload, isp_billing, projects, transporter_portal, platform"),
		field.String("category").
			Default("General").
			Comment("Functional grouping within a service, e.g. 'Payments & Gateways', 'Analytics'"),
		field.String("label").
			NotEmpty().
			Comment("Human-readable display name"),
		field.Text("description").
			Optional(),
		field.Enum("kind").
			Values("FEATURE", "LIMIT").
			Default("FEATURE").
			Comment("FEATURE = boolean entitlement; LIMIT = numeric quota stored in tier_limits_json"),
		field.Enum("value_type").
			Values("bool", "int", "unlimited").
			Default("bool").
			Comment("How the value is interpreted in the plan editor"),
		field.Int("default_limit").
			Optional().
			Nillable().
			Comment("Suggested default limit pre-filled in the plan editor for LIMIT kinds (-1 = unlimited)"),
		field.Bool("is_rate_limited").
			Default(false).
			Comment("True for metered limits counted via usage events (per-day/per-month)"),
		field.String("unit").
			Optional().
			Comment("Display unit, e.g. '/ day', '/ month'"),
		field.String("nats_event").
			Optional().
			Comment("Source NATS subject used to meter usage for this limit, if any"),
		field.Int("sort_order").
			Default(0).
			Comment("Display order within a service/category"),
		field.Bool("is_active").
			Default(true),
		field.Time("created_at").
			Default(time.Now).
			Immutable(),
		field.Time("updated_at").
			Default(time.Now).
			UpdateDefault(time.Now),
	}
}

// Indexes of the FeatureDefinition.
func (FeatureDefinition) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("feature_code").Unique(),
		index.Fields("service_tag"),
		index.Fields("kind"),
	}
}
