package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// SupportFeeCycle is one billing period of a SupportAgreement (the standard hosting and support
// fee, or a tenant-specific special support agreement) for a one-time-license tenant.
// TenantSubscription is unique per tenant and already points at the license plan, so support
// charges can never be a second TenantSubscription row; this satellite entity carries them, the
// same pattern as OverageCharge/ProductSubscription.
type SupportFeeCycle struct {
	ent.Schema
}

func (SupportFeeCycle) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).
			Default(uuid.New).
			Immutable(),
		field.UUID("tenant_id", uuid.UUID{}).
			Comment("Denormalised for fast tenant-scoped queries (mirrors OverageCharge's pattern)"),
		field.UUID("tenant_subscription_id", uuid.UUID{}).
			Comment("FK to the tenant's ONE TenantSubscription row — the perpetual license this support fee accompanies"),
		field.UUID("support_plan_id", uuid.UUID{}).
			Optional().
			Nillable().
			Comment("SUPPORT_* catalog row for STANDARD agreement cycles; nil for SPECIAL agreements"),
		field.UUID("agreement_id", uuid.UUID{}).
			Optional().
			Nillable().
			Comment("The SupportAgreement this cycle bills. Rows created before agreements existed are linked by the enrollment job"),
		field.Time("anchor_date").
			Immutable().
			Comment("The agreement's starts_at when this cycle was generated (for STANDARD agreements, the license onboarding date)"),
		field.Int("cycle_number").
			Comment("1-based sequence within the agreement"),
		field.Time("period_start").
			Comment("Start of the billed period"),
		field.Time("period_end").
			Optional().
			Nillable().
			Comment("End (exclusive) of the billed period"),
		field.Time("due_date").
			Comment("Payment due date: period_start for ADVANCE agreements, period_end for ARREARS"),
		field.Enum("status").
			Values("PENDING", "INVOICED", "PAID", "OVERDUE", "WAIVED").
			Default("PENDING").
			Comment("PENDING: not yet invoiced. INVOICED: invoice issued, not yet due. PAID: settled. OVERDUE: due_date passed unpaid; mutations are blocked fleet-wide by RequireSupportFeeCurrentForMutations once grace_until also passes, until paid or waived. WAIVED: excused by the platform owner (or cancelled with its agreement)."),
		field.Time("paid_at").
			Optional().
			Nillable(),
		field.Time("grace_until").
			Optional().
			Nillable().
			Comment("due_date + 7 days, set the moment the cycle flips OVERDUE. Mutations stay allowed until this passes; reads are never blocked."),
		field.Float("base_price").
			Comment("Charge for this period, snapshotted from the agreement when the cycle is generated so a later re-price never changes an issued cycle"),
		// ── Per-tenant custom pricing override (mirrors TenantSubscription's existing mechanism exactly) ──
		field.Float("custom_price").
			Optional().
			Nillable().
			Comment("Platform-admin sales-agreed override for THIS tenant's support fee. Nil = use base_price"),
		field.String("custom_price_reason").
			Optional().
			Nillable(),
		field.UUID("custom_price_set_by", uuid.UUID{}).
			Optional().
			Nillable(),
		field.Time("custom_price_set_at").
			Optional().
			Nillable(),
		field.JSON("metadata", map[string]any{}).
			Optional().
			Default(map[string]any{}).
			Comment("last_invoice_id/number/pay_url/total, last_grace_reminder_date, waive/manual-payment audit"),
		field.Time("created_at").
			Default(time.Now).
			Immutable(),
		field.Time("updated_at").
			Default(time.Now).
			UpdateDefault(time.Now),
	}
}

func (SupportFeeCycle) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("tenant_subscription", TenantSubscription.Type).
			Ref("support_fee_cycles").
			Unique().
			Required().
			Field("tenant_subscription_id"),
		edge.From("support_plan", SubscriptionPlan.Type).
			Ref("support_fee_cycles").
			Unique().
			Field("support_plan_id"),
		edge.From("agreement", SupportAgreement.Type).
			Ref("cycles").
			Unique().
			Field("agreement_id"),
	}
}

func (SupportFeeCycle) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("tenant_subscription_id"),
		// The JWT claim lookup: a tenant's oldest unpaid cycle.
		index.Fields("tenant_id", "status", "due_date"),
		// Invoice / overdue / receivables scans.
		index.Fields("status", "due_date"),
		// One cycle per period per agreement: the enrollment job's idempotency key.
		index.Fields("agreement_id", "cycle_number").Unique(),
	}
}
