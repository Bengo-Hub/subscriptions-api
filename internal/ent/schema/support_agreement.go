package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// SupportAgreement is a recurring support charge attached to a one-time (perpetual) license
// subscription. Two kinds share one billing engine:
//
//   - STANDARD: the hosting and support fee every one-time tenant pays. Exactly one per license
//     subscription, auto-created by the support-fee enrollment job from the matching SUPPORT_*
//     catalog row. Defaults to ANNUAL in arrears; the platform owner can switch it to monthly,
//     quarterly, semi-annual or a custom cycle.
//   - SPECIAL: extra support agreed with a specific tenant (dedicated engineer, on-site visits,
//     custom work retainers). Created by the platform owner with an agreed amount and cycle.
//
// Each agreement produces SupportFeeCycle rows (one per billing period). An unpaid cycle past its
// due date plus grace blocks data mutations fleet-wide through the same support_fee_* JWT claims
// and authclient.RequireSupportFeeCurrentForMutations gate the annual fee always used.
type SupportAgreement struct {
	ent.Schema
}

func (SupportAgreement) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).
			Default(uuid.New).
			Immutable(),
		field.UUID("tenant_id", uuid.UUID{}).
			Comment("Denormalised for tenant-scoped queries"),
		field.UUID("tenant_subscription_id", uuid.UUID{}).
			Comment("The one-time license subscription this agreement accompanies"),
		field.UUID("support_plan_id", uuid.UUID{}).
			Optional().
			Nillable().
			Comment("SUPPORT_* catalog row for STANDARD agreements; nil for SPECIAL ones"),
		field.Enum("kind").
			Values("STANDARD", "SPECIAL").
			Default("STANDARD"),
		field.String("name").
			NotEmpty().
			Comment("Shown on invoices and emails, e.g. 'Hosting and support' or 'Priority on-site support'"),
		field.Enum("billing_cycle").
			Values("MONTHLY", "QUARTERLY", "SEMI_ANNUAL", "ANNUAL", "CUSTOM").
			Default("ANNUAL"),
		field.Int("interval_count").
			Default(12).
			Positive().
			Comment("Length of one billing period in interval_unit. Presets store their month count (1, 3, 6, 12); CUSTOM stores any agreed value"),
		field.Enum("interval_unit").
			Values("MONTH", "DAY").
			Default("MONTH"),
		field.Float("amount").
			Optional().
			Nillable().
			Comment("Agreed charge per billing period. Nil on a STANDARD agreement means the SUPPORT_* annual price prorated to the period length"),
		field.String("currency").
			Default("KES"),
		field.Enum("billing_timing").
			Values("ADVANCE", "ARREARS").
			Default("ARREARS").
			Comment("ADVANCE: due at the start of each period. ARREARS: due at the end (the original annual support model)"),
		field.Time("starts_at").
			Comment("Anchor of the billing schedule. STANDARD agreements use the license onboarding date"),
		field.Time("ends_at").
			Optional().
			Nillable().
			Comment("No periods starting on or after this instant are billed; the agreement then ends"),
		field.Enum("status").
			Values("ACTIVE", "PAUSED", "ENDED").
			Default("ACTIVE").
			Comment("PAUSED stops new periods being billed; already issued cycles stay payable"),
		field.Time("next_period_start").
			Comment("Start of the next period to bill. The enrollment job only scans agreements whose next period falls inside the invoice lead window, so its cost tracks due work rather than tenant count"),
		field.Int("cycle_count").
			Default(0).
			NonNegative().
			Comment("Number of cycles generated so far (the next cycle_number is cycle_count+1)"),
		field.String("notes").
			Optional().
			Nillable(),
		field.UUID("created_by", uuid.UUID{}).
			Optional().
			Nillable(),
		field.JSON("metadata", map[string]any{}).
			Optional().
			Default(map[string]any{}).
			Comment("billing_email override, change audit trail"),
		field.Time("created_at").
			Default(time.Now).
			Immutable(),
		field.Time("updated_at").
			Default(time.Now).
			UpdateDefault(time.Now),
	}
}

func (SupportAgreement) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("tenant_subscription", TenantSubscription.Type).
			Ref("support_agreements").
			Unique().
			Required().
			Field("tenant_subscription_id"),
		edge.From("support_plan", SubscriptionPlan.Type).
			Ref("support_agreements").
			Unique().
			Field("support_plan_id"),
		edge.To("cycles", SupportFeeCycle.Type),
	}
}

func (SupportAgreement) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("tenant_id", "status"),
		// The enrollment job's scan: ACTIVE agreements whose next period is due for billing.
		index.Fields("status", "next_period_start"),
		// One STANDARD agreement per license subscription (SPECIAL ones are unlimited).
		index.Fields("tenant_subscription_id").
			Unique().
			Annotations(entsql.IndexWhere("kind = 'STANDARD'")),
	}
}
