package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// Coupon defines platform-issued discount codes that tenants can redeem against
// their subscription billing. Distinct from ordering-backend PromoCode (which is
// for end-customer food order discounts).
type Coupon struct {
	ent.Schema
}

func (Coupon) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).
			Default(uuid.New).
			Immutable(),
		field.String("code").
			MaxLen(64).
			Comment("Unique coupon code (case-insensitive, stored uppercase)"),
		field.String("name").
			MaxLen(128),
		field.String("description").
			Optional().
			Nillable(),
		field.Enum("type").
			Values("percentage", "fixed_kes", "free_months").
			Comment("How the discount is calculated"),
		field.Float("value").
			Comment("Percentage (0-100), KES amount, or number of months depending on type"),
		field.JSON("applicable_plan_codes", []string{}).
			Optional().
			Comment("Plan codes this coupon applies to; empty means all plans"),
		field.Float("min_plan_price").
			Default(0).
			Comment("Minimum plan base price (KES) for this coupon to be valid"),
		field.Int("max_uses").
			Default(-1).
			Comment("-1 = unlimited"),
		field.Int("used_count").
			Default(0),
		field.Int("max_stacks").
			Default(1).
			Comment("Maximum number of times one tenant can stack/apply this coupon"),
		field.Bool("is_active").
			Default(true),
		field.Time("valid_from").
			Default(time.Now),
		field.Time("valid_until").
			Optional().
			Nillable(),
		field.UUID("created_by", uuid.UUID{}).
			Comment("Platform admin user who created the coupon"),
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

func (Coupon) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("code").Unique(),
		index.Fields("is_active"),
		index.Fields("valid_until"),
	}
}
