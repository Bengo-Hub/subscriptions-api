package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// ProductSubscription tracks which products a tenant has active within their subscription.
// Enables per-product activation/deactivation and individual product trials.
type ProductSubscription struct {
	ent.Schema
}

// Fields of the ProductSubscription.
func (ProductSubscription) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).
			Default(uuid.New).
			Immutable(),
		field.UUID("tenant_subscription_id", uuid.UUID{}).
			Comment("FK to tenant_subscriptions"),
		field.String("product_code").
			NotEmpty().
			Comment("Product code (ordering, logistics, etc.)"),
		field.UUID("product_id", uuid.UUID{}).
			Comment("FK to products"),
		field.Enum("status").
			Values("active", "trial", "read_only", "suspended", "inactive").
			Default("active"),
		field.Time("trial_ends_at").
			Optional().
			Nillable().
			Comment("End of product-level trial period"),
		field.Time("activated_at").
			Optional().
			Nillable().
			Comment("When this product was activated"),
		field.Time("deactivated_at").
			Optional().
			Nillable().
			Comment("When this product was deactivated"),
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

// Edges of the ProductSubscription.
func (ProductSubscription) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("tenant_subscription", TenantSubscription.Type).
			Ref("product_subscriptions").
			Field("tenant_subscription_id").
			Required().
			Unique(),
		edge.From("product", Product.Type).
			Ref("product_subscriptions").
			Field("product_id").
			Required().
			Unique(),
	}
}

// Indexes of the ProductSubscription.
func (ProductSubscription) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("tenant_subscription_id"),
		index.Fields("product_code"),
		index.Fields("status"),
		// A tenant subscription can only have one entry per product
		index.Fields("tenant_subscription_id", "product_code").
			Unique(),
	}
}
