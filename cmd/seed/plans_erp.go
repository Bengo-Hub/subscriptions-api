package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/subscriptionplan"
)

// ── ERP Plans ─────────────────────────────────────────────────────────────────
//
// The ERP product line is a FULL-SUITE offering: each tier bundles the ERP module
// (HR / payroll / procurement / assets / budgeting) UNIONED with the matching-tier
// PowerSuite coverage (ordering / POS / inventory / treasury / logistics / CRM) that
// ERP integrates with — so an ERP buyer gets the whole back-office plus the commerce
// stack that feeds it (adapted from the erpnext.co.ke/erpnext-hr model to our
// micro-service architecture, per docs/subscription-plans/plan-feature-matrix.md).
//
// Four tiers: Starter (T1) → Growth (T2) → Professional (T3) → Enterprise (T4).
// Enterprise is UNLIMITED and priced by manual quote (base_price 0 +
// metadata.custom_quote=true → the pricing UI renders "Contact Sales", never a
// checkout). PowerSuite only defines 3 tiers, so T4 unions the tier-3 PowerSuite set
// with the unlimited ERP set (psTier clamps tier→3).

// erpFeatures is the single source of truth for the ERP MODULE feature set at tier N
// (1=Starter … 4=Enterprise). Tier 4 == tier 3 (all ERP modules); the difference is
// unlimited limits, not extra modules.
func erpFeatures(tier int) []string {
	switch tier {
	case 1:
		return []string{"hr_management", "payroll", "basic_procurement", "leave_management", "basic_reports", "attendance"}
	case 2:
		return []string{"hr_management", "payroll", "basic_procurement", "leave_management", "basic_reports", "attendance", "appraisals", "recruitment", "training", "asset_management", "budgeting", "advanced_reports", "multi_department", "approval_workflows"}
	default: // tier 3 + 4
		return []string{"hr_management", "payroll", "basic_procurement", "leave_management", "basic_reports", "attendance", "appraisals", "recruitment", "training", "asset_management", "budgeting", "advanced_reports", "multi_department", "approval_workflows", "api_access", "custom_workflows", "audit_trail", "priority_support", "staff_fund_from_salary"}
	}
}

// erpLimits is the single source of truth for the ERP MODULE tier_limits at tier N.
// employees / users / departments / payroll runs improve every tier; T4 = unlimited.
func erpLimits(tier int) map[string]any {
	switch tier {
	case 1:
		return map[string]any{"max_employees": 25, "max_users": 10, "max_departments": 3, "max_payroll_runs_per_month": 2}
	case 2:
		return map[string]any{"max_employees": 75, "max_users": 30, "max_departments": 10, "max_payroll_runs_per_month": 6}
	case 3:
		return map[string]any{"max_employees": 200, "max_users": 100, "max_departments": -1, "max_payroll_runs_per_month": -1}
	default: // tier 4 — Enterprise, unlimited
		return map[string]any{"max_employees": -1, "max_users": -1, "max_departments": -1, "max_payroll_runs_per_month": -1}
	}
}

// psTier clamps an ERP tier (1..4) to the PowerSuite tier range (1..3) so the T4
// Enterprise plan unions the richest (tier-3) PowerSuite coverage.
func psTier(tier int) int {
	if tier > 3 {
		return 3
	}
	return tier
}

// erpSuiteFeatures / erpSuiteLimits are the FULL-SUITE feature/limit sets for an ERP
// tier: the PowerSuite union at the clamped tier merged with the ERP module set.
func erpSuiteFeatures(tier int) []string {
	return unionFeatures(powerSuiteFeatures(psTier(tier)), erpFeatures(tier))
}
func erpSuiteLimits(tier int) map[string]any {
	return mergeLimits(powerSuiteLimits(psTier(tier)), erpLimits(tier))
}

// unionFeatures merges feature-code slices, preserving order and dropping duplicates.
func unionFeatures(sets ...[]string) []string {
	seen := map[string]bool{}
	out := make([]string, 0)
	for _, s := range sets {
		for _, c := range s {
			if !seen[c] {
				seen[c] = true
				out = append(out, c)
			}
		}
	}
	return out
}

// mergeLimits returns base overlaid by over (over wins on conflict). Returns a fresh map.
func mergeLimits(base, over map[string]any) map[string]any {
	out := make(map[string]any, len(base)+len(over))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range over {
		out[k] = v
	}
	return out
}

// erp tier metadata (index 0..3 → tier 1..4).
var erpTierCodes = [4]string{"STARTER", "GROWTH", "PROFESSIONAL", "ENTERPRISE"}
var erpTierNames = [4]string{"Starter", "Growth", "Professional", "Enterprise"}

func seedERPPlans(ctx context.Context, tx *ent.Tx) error {
	now := time.Now()
	serviceTag := "erp"

	type planDef struct {
		id           uuid.UUID
		planCode     string
		name         string
		description  string
		billingCycle string
		price        float64
		tierOrder    int
		setupFee     float64 // one-time license installation fee (recurring plans get theirs from setup_fees.go)
		trialDays    int
		metadata     map[string]any
		tierLimits   map[string]any
		features     []string
	}

	// ── Recurring monthly plans (TIERED full-suite) ──────────────────────────────
	// STARTER/GROWTH/PROFESSIONAL keep their historical plan codes + deterministic ids
	// so existing subscriber FKs stay valid across the reprice + suite-bundling change.
	recurringPrices := [4]float64{10000, 20000, 35000, 0} // T4 = custom quote
	recurringDesc := [4]string{
		"Full-suite ERP for small teams: HR, payroll, procurement and leave management, plus the commerce stack (ordering, POS, inventory, payments, CRM). Up to 25 employees.",
		"Multi-department HR, payroll, asset management, budgeting and approval workflows, plus multi-outlet commerce and finance. Up to 75 employees.",
		"Unlimited departments, full financial suite, API access, custom workflows and priority support across every service. Up to 200 employees.",
		"Enterprise ERP with unlimited scale across every service — custom-scoped and priced to your organisation. Contact sales for a quote.",
	}

	plans := []planDef{}
	for i := 0; i < 4; i++ {
		tier := i + 1
		p := planDef{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("erp:"+erpTierCodes[i])),
			planCode:     "ERP_" + erpTierCodes[i],
			name:         "ERP " + erpTierNames[i],
			description:  recurringDesc[i],
			billingCycle: "MONTHLY",
			price:        recurringPrices[i],
			tierOrder:    tier,
			trialDays:    14,
			tierLimits:   erpSuiteLimits(tier),
			features:     erpSuiteFeatures(tier),
		}
		if tier == 4 {
			// Enterprise — manual quote: no price, no trial, flagged for the "Contact Sales" UI.
			p.trialDays = 0
			p.metadata = map[string]any{"custom_quote": true}
		}
		plans = append(plans, p)
	}

	// ── Perpetual (one-time) license tiers ───────────────────────────────────────
	// Buy-outright full-suite ERP. Four tiers 250k → 2M (annual support sold separately
	// via SUPPORT_ERP_* below). ONE_TIME → billing_mode=one_time, is_perpetual; setup fee
	// charged inline (setup_fees.go skips ONE_TIME).
	oneTimePrices := [4]float64{250000, 600000, 1200000, 2000000}
	oneTimeSetup := [4]float64{30000, 50000, 80000, 120000}
	oneTimeDesc := [4]string{
		"One-time perpetual license: full-suite ERP at starter level — HR, payroll, procurement and the commerce stack for small teams.",
		"One-time perpetual license: multi-department ERP with asset management, budgeting, approval workflows and multi-outlet commerce.",
		"One-time perpetual license: full financial suite, API access and custom workflows across every service, up to 200 employees.",
		"One-time perpetual license: enterprise ERP with unlimited scale across every service — the complete platform, owned outright.",
	}
	for i := 0; i < 4; i++ {
		tier := i + 1
		plans = append(plans, planDef{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("erp:"+erpTierCodes[i]+"_ONE_TIME")),
			planCode:     "ERP_" + erpTierCodes[i] + "_ONE_TIME",
			name:         "ERP Suite " + erpTierNames[i] + " — Perpetual License",
			description:  oneTimeDesc[i],
			billingCycle: "ONE_TIME",
			price:        oneTimePrices[i],
			tierOrder:    tier,
			setupFee:     oneTimeSetup[i],
			trialDays:    0,
			tierLimits:   erpSuiteLimits(tier),
			features:     erpSuiteFeatures(tier),
		})
	}

	for _, p := range plans {
		existing, err := tx.SubscriptionPlan.Get(ctx, p.id)
		if err != nil && !ent.IsNotFound(err) {
			return fmt.Errorf("lookup erp plan %s: %w", p.planCode, err)
		}
		meta := p.metadata
		if meta == nil {
			meta = map[string]any{} // clear any stale metadata idempotently
		}
		if existing != nil {
			_, err = tx.SubscriptionPlan.UpdateOneID(p.id).
				SetPlanCode(p.planCode).SetName(p.name).SetDescription(p.description).
				SetBillingCycle(p.billingCycle).SetPlanType(subscriptionplan.PlanTypeTIERED).
				SetBasePrice(p.price).SetSetupFee(p.setupFee).SetCurrency("KES").SetIsActive(true).SetIsPublic(true).
				SetTierOrder(p.tierOrder).SetFreeTrialDays(p.trialDays).SetMetadata(meta).
				SetTierLimitsJSON(p.tierLimits).SetServiceTag(serviceTag).SetUpdatedAt(now).Save(ctx)
		} else {
			_, err = tx.SubscriptionPlan.Create().
				SetID(p.id).SetPlanCode(p.planCode).SetName(p.name).SetDescription(p.description).
				SetBillingCycle(p.billingCycle).SetPlanType(subscriptionplan.PlanTypeTIERED).
				SetBasePrice(p.price).SetSetupFee(p.setupFee).SetCurrency("KES").SetIsActive(true).SetIsPublic(true).
				SetTierOrder(p.tierOrder).SetFreeTrialDays(p.trialDays).SetMetadata(meta).
				SetTierLimitsJSON(p.tierLimits).SetServiceTag(serviceTag).
				SetCreatedAt(now).SetUpdatedAt(now).Save(ctx)
		}
		if err != nil {
			return fmt.Errorf("upsert erp plan %s: %w", p.planCode, err)
		}
		if err := seedPlanFeatures(ctx, tx, p.id, p.features); err != nil {
			return fmt.Errorf("seed features for erp plan %s: %w", p.planCode, err)
		}
		log.Printf("  erp plan: %s (%s, KES %.0f)", p.name, p.billingCycle, p.price)
	}

	if err := seedERPSupportPlans(ctx, tx); err != nil {
		return fmt.Errorf("seed erp support plans: %w", err)
	}

	// Retire the legacy flat ERP_ONE_TIME (150k, tier 4) — superseded by the tiered
	// ERP_*_ONE_TIME licenses above. Deactivated (not deleted) so any tenant currently on
	// it still resolves its entitlements, mirroring the retireAnnualPlanRows pattern.
	legacyOneTimeID := uuid.NewSHA1(uuid.NameSpaceOID, []byte("erp:ONE_TIME"))
	if n, err := tx.SubscriptionPlan.Update().
		Where(subscriptionplan.IDEQ(legacyOneTimeID)).
		SetIsActive(false).SetIsPublic(false).SetUpdatedAt(now).
		Save(ctx); err != nil {
		return fmt.Errorf("retire legacy ERP_ONE_TIME: %w", err)
	} else if n > 0 {
		log.Printf("  erp plan: retired legacy ERP_ONE_TIME (deactivated, %d row)", n)
	}
	return nil
}

// seedERPSupportPlans seeds the ANNUAL support plans that accompany the ERP perpetual
// licenses (SUPPORT_ERP_{STARTER,GROWTH,PROFESSIONAL,ENTERPRISE}): updates, cloud sync and
// support billed yearly. Mirrors seedUseCasePowerSuiteSupportPlans exactly —
// entitlement-only (no gating features), is_public=false (sold/assigned alongside a license),
// metadata.support_plan=true, service_tag=platform. Support = 20% of the license price.
func seedERPSupportPlans(ctx context.Context, tx *ent.Tx) error {
	now := time.Now()
	prices := [4]float64{50000, 120000, 240000, 400000}
	for i := 0; i < 4; i++ {
		tier := i + 1
		planCode := "SUPPORT_ERP_" + erpTierCodes[i]
		id := uuid.NewSHA1(uuid.NameSpaceOID, []byte("plan:"+planCode))
		name := "Annual Support — ERP Suite " + erpTierNames[i]
		desc := fmt.Sprintf("Annual software support, updates and cloud sync for the ERP Suite %s perpetual license.", erpTierNames[i])

		existing, err := tx.SubscriptionPlan.Get(ctx, id)
		if err != nil && !ent.IsNotFound(err) {
			return fmt.Errorf("lookup erp support plan %s: %w", planCode, err)
		}
		if existing != nil {
			_, err = tx.SubscriptionPlan.UpdateOneID(id).
				SetPlanCode(planCode).SetName(name).SetDescription(desc).
				SetBillingCycle("ANNUAL").SetPlanType(subscriptionplan.PlanTypeTIERED).
				SetBasePrice(prices[i]).SetSetupFee(0).SetCurrency("KES").
				SetIsActive(true).SetIsPublic(false).
				SetTierOrder(tier).SetFreeTrialDays(0).
				SetTierLimitsJSON(map[string]any{}).SetServiceTag("platform").
				SetMetadata(map[string]any{"support_plan": true}).
				SetUpdatedAt(now).Save(ctx)
		} else {
			_, err = tx.SubscriptionPlan.Create().
				SetID(id).SetPlanCode(planCode).SetName(name).SetDescription(desc).
				SetBillingCycle("ANNUAL").SetPlanType(subscriptionplan.PlanTypeTIERED).
				SetBasePrice(prices[i]).SetSetupFee(0).SetCurrency("KES").
				SetIsActive(true).SetIsPublic(false).
				SetTierOrder(tier).SetFreeTrialDays(0).
				SetTierLimitsJSON(map[string]any{}).SetServiceTag("platform").
				SetMetadata(map[string]any{"support_plan": true}).
				SetCreatedAt(now).SetUpdatedAt(now).Save(ctx)
		}
		if err != nil {
			return fmt.Errorf("upsert erp support plan %s: %w", planCode, err)
		}
		if err := seedPlanFeatures(ctx, tx, id, nil); err != nil {
			return fmt.Errorf("seed features for %s: %w", planCode, err)
		}
		log.Printf("  annual support plan: %s (ANNUAL, KES %.0f)", name, prices[i])
	}
	return nil
}
