package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// SupportFeeCycle tracks one annual support-fee billing cycle for a perpetual/one-time-license
// tenant (e.g. POWERSUITE_DUKA_GOLD_ONE_TIME, LIBRARY_PROFESSIONAL_ONE_TIME). TenantSubscription
// has a UNIQUE index on tenant_id — a tenant has exactly one subscription row, already pointed
// at their real license plan — so the annual support charge can never be a second
// TenantSubscription row against a SUPPORT_* plan for the same tenant. This satellite entity is
// required, same pattern as OverageCharge/ProductSubscription.
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
			Comment("FK to the SUPPORT_* subscription_plans catalog row (e.g. SUPPORT_DUKA_GOLD)"),
		field.Time("anchor_date").
			Immutable().
			Comment("Tenant's real onboarding date (tenant_subscriptions.created_at at enrollment time). NEVER 'today' — every cycle's due_date is computed as anchor_date + cycle_number years, backdated for tenants already onboarded when support billing was introduced."),
		field.Int("cycle_number").
			Comment("1 = first annual cycle since onboarding, 2 = second, etc."),
		field.Time("period_start").
			Comment("anchor_date + (cycle_number-1) years"),
		field.Time("due_date").
			Comment("anchor_date + cycle_number years — payment due date"),
		field.Enum("status").
			Values("PENDING", "INVOICED", "PAID", "OVERDUE", "WAIVED").
			Default("PENDING").
			Comment("PENDING: not yet in the T-7 invoice window. INVOICED: invoice generated+emailed, not yet due. PAID: settled. OVERDUE: due_date passed unpaid — mutations blocked fleet-wide via RequireSupportFeeCurrentForMutations once grace_until also elapses; stays OVERDUE indefinitely after that (no further escalation) until paid or waived. WAIVED: platform admin manually excused this cycle."),
		field.Time("paid_at").
			Optional().
			Nillable(),
		field.Time("grace_until").
			Optional().
			Nillable().
			Comment("due_date + 7 days, set the moment the cycle flips OVERDUE. Mutations stay allowed until this passes; reads are never blocked."),
		field.Float("base_price").
			Comment("Snapshot of support_plan.base_price at cycle-creation time — survives a later re-price of the catalog row, matches setup_fee_amount's snapshot rationale on TenantSubscription"),
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
			Comment("last_invoice_id/number/pay_url/total, last_reminder_date, billing_email override — mirrors TenantSubscription.metadata's invoicing bookkeeping"),
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
			Required().
			Field("support_plan_id"),
	}
}

func (SupportFeeCycle) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("tenant_id"),
		index.Fields("tenant_subscription_id"),
		index.Fields("status"),
		index.Fields("due_date"),
		// One cycle row per year per tenant subscription — the enrollment job's idempotency key.
		index.Fields("tenant_subscription_id", "cycle_number").Unique(),
	}
}
