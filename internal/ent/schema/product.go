package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// Product represents a subscribable microservice/module in the BengoBox ecosystem.
// Platform products (auth, notifications, subscription) are always included.
// Product/add-on products can be subscribed to individually or via bundles.
type Product struct {
	ent.Schema
}

// Fields of the Product.
func (Product) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).
			Default(uuid.New).
			Immutable(),
		field.String("code").
			NotEmpty().
			Unique().
			Comment("Unique product code (ordering, logistics, treasury, pos, etc.)"),
		field.String("name").
			NotEmpty().
			Comment("Display name"),
		field.Text("description").
			Optional(),
		field.Enum("category").
			Values("platform", "product", "add_on").
			Comment("platform=always included, product=core subscribable, add_on=optional extra"),
		field.Enum("status").
			Values("active", "deprecated", "coming_soon").
			Default("active"),
		field.JSON("dependencies", []string{}).
			Optional().
			Comment("Product codes this product depends on (e.g., logistics requires ordering)"),
		field.Bool("is_platform").
			Default(false).
			Comment("Platform products are always included at no extra cost"),
		field.Float("monthly_price").
			Default(0).
			Comment("Standalone monthly price in KES (0 for platform products)"),
		field.Bool("included_in_bundle").
			Default(true).
			Comment("Whether this product is included in bundle subscriptions"),
		field.Int("sort_order").
			Default(0).
			Comment("Display ordering"),
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

// Edges of the Product.
func (Product) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("product_subscriptions", ProductSubscription.Type),
	}
}

// Indexes of the Product.
func (Product) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("code"),
		index.Fields("category"),
		index.Fields("status"),
		index.Fields("is_platform"),
	}
}
