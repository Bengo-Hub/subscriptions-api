package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// OverageCharge accumulates daily billable overages per metric when a tenant
// exceeds their plan limit. Records are rolled up into the renewal invoice by
// RenewSubscription() and can be waived by platform admins.
type OverageCharge struct {
	ent.Schema
}

func (OverageCharge) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).
			Default(uuid.New).
			Immutable(),
		field.UUID("tenant_subscription_id", uuid.UUID{}).
			Comment("FK to tenant_subscriptions"),
		field.UUID("tenant_id", uuid.UUID{}).
			Comment("Denormalised for fast tenant-scoped queries"),
		field.String("metric_type").
			MaxLen(64).
			Comment("Which plan limit was exceeded, e.g. max_orders_per_month, max_riders"),
		field.Time("period_date").
			Comment("The calendar date on which the overage occurred"),
		field.Float("units_used").
			Comment("Actual usage recorded for the metric on this date"),
		field.Int("plan_limit").
			Comment("The plan's limit value for this metric"),
		field.Float("units_over").
			Comment("units_used - plan_limit"),
		field.Float("unit_price_kes").
			Comment("KES cost per overage unit (from plan tierLimitsJSON overage_* key)"),
		field.Float("total_charge_kes").
			Comment("units_over × unit_price_kes"),
		field.Enum("status").
			Values("pending", "invoiced", "waived").
			Default("pending"),
		field.Time("invoiced_at").
			Optional().
			Nillable().
			Comment("When the charge was included in a renewal invoice"),
		field.Time("created_at").
			Default(time.Now).
			Immutable(),
	}
}

func (OverageCharge) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("subscription", TenantSubscription.Type).
			Ref("overage_charges").
			Unique().
			Required().
			Field("tenant_subscription_id"),
	}
}

func (OverageCharge) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("tenant_id"),
		index.Fields("tenant_subscription_id"),
		index.Fields("status"),
		index.Fields("period_date"),
		// Prevent duplicate overage records for same tenant+metric+date
		index.Fields("tenant_subscription_id", "metric_type", "period_date").Unique(),
	}
}
