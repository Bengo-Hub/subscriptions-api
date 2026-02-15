package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// BundleProduct represents a product included in a bundle with its plan mapping.
type BundleProduct struct {
	ProductCode string `json:"product_code"`
	PlanCode    string `json:"plan_code,omitempty"` // If empty, inherits bundle tier
}

// BundleTier represents pricing for a specific tier within a bundle.
type BundleTier struct {
	PlanCode     string  `json:"plan_code"` // STARTER, GROWTH, PROFESSIONAL
	MonthlyPrice float64 `json:"monthly_price"`
	YearlyPrice  float64 `json:"yearly_price"`
}

// Bundle represents a curated combination of products offered at a discount.
// The Urban Loft inception report tiers map to the "delivery" bundle.
type Bundle struct {
	ent.Schema
}

// Fields of the Bundle.
func (Bundle) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).
			Default(uuid.New).
			Immutable(),
		field.String("code").
			NotEmpty().
			Unique().
			Comment("Unique bundle code (delivery, pos-suite, complete)"),
		field.String("name").
			NotEmpty().
			Comment("Display name"),
		field.Text("description").
			Optional(),
		field.JSON("products", []BundleProduct{}).
			Comment("Products included in this bundle"),
		field.JSON("tiers", []BundleTier{}).
			Comment("Pricing per tier for this bundle"),
		field.Enum("discount_type").
			Values("percentage", "fixed", "none").
			Default("none").
			Comment("How bundle discount is calculated vs individual product prices"),
		field.Float("discount_value").
			Default(0).
			Comment("Discount amount (percentage or fixed KES)"),
		field.Bool("is_active").
			Default(true),
		field.Bool("is_default").
			Default(false).
			Comment("Default bundle assigned to new tenants on signup"),
		field.Int("sort_order").
			Default(0),
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

// Edges of the Bundle.
func (Bundle) Edges() []ent.Edge {
	return nil
}

// Indexes of the Bundle.
func (Bundle) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("code"),
		index.Fields("is_active"),
		index.Fields("is_default"),
	}
}
