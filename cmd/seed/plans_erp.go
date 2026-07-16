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

// erpFeatures is the single source of truth for the ERP module feature set at tier N
// (1=Starter, 2=Growth, 3=Professional). Shared by the recurring monthly ERP standalone
// plans and the ERP-suite ONE_TIME licenses (unioned with the PowerSuite set).
// NOTE: these are the STANDALONE ERP plan tiers (ERP-first buyers get HR depth early:
// attendance from tier 1, appraisals/recruitment/training from tier 2). The use-case
// PowerSuite bundles deliberately gate harder — their tier 2 grants hr_management +
// attendance WITHOUT payroll/appraisals/recruitment/training (see psERPBlock in
// plans_powersuite_usecase.go, per docs/subscription-plans/).
func erpFeatures(tier int) []string {
	switch tier {
	case 1:
		return []string{"hr_management", "payroll", "basic_procurement", "leave_management", "basic_reports", "attendance"}
	case 2:
		return []string{"hr_management", "payroll", "basic_procurement", "leave_management", "basic_reports", "attendance", "appraisals", "recruitment", "training", "asset_management", "budgeting", "advanced_reports", "multi_department", "approval_workflows"}
	default: // tier 3
		return []string{"hr_management", "payroll", "basic_procurement", "leave_management", "basic_reports", "attendance", "appraisals", "recruitment", "training", "asset_management", "budgeting", "advanced_reports", "multi_department", "approval_workflows", "api_access", "custom_workflows", "audit_trail", "priority_support", "staff_fund_from_salary"}
	}
}

// erpLimits is the single source of truth for the ERP module tier_limits at tier N.
func erpLimits(tier int) map[string]any {
	switch tier {
	case 1:
		return map[string]any{"max_employees": 20, "max_users": 5, "max_departments": 3}
	case 2:
		return map[string]any{"max_employees": 100, "max_users": 20, "max_departments": -1}
	default: // tier 3
		return map[string]any{"max_employees": -1, "max_users": -1, "max_departments": -1}
	}
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
		setupFee     float64                   // one-time license installation fee (recurring plans get theirs from setup_fees.go)
		planType     subscriptionplan.PlanType // STANDALONE_SERVICE for the ERP-only plans; TIERED for the multi-service ERP suite
		tierLimits   map[string]any
		features     []string
	}

	plans := []planDef{
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("erp:STARTER")),
			planCode:     "ERP_STARTER",
			name:         "ERP Starter",
			description:  "Core HR, payroll, and basic procurement for small teams. Up to 20 employees.",
			billingCycle: "MONTHLY",
			price:        2000.0,
			tierOrder:    1,
			tierLimits:   erpLimits(1),
			features:     erpFeatures(1),
		},
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("erp:GROWTH")),
			planCode:     "ERP_GROWTH",
			name:         "ERP Growth",
			description:  "Multi-department HR, payroll, asset management, budgeting, and advanced reporting. Up to 100 employees.",
			billingCycle: "MONTHLY",
			price:        5000.0,
			tierOrder:    2,
			tierLimits:   erpLimits(2),
			features:     erpFeatures(2),
		},
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("erp:PROFESSIONAL")),
			planCode:     "ERP_PROFESSIONAL",
			name:         "ERP Professional",
			description:  "Unlimited employees, full financial suite, API access, custom workflows, and priority support.",
			billingCycle: "MONTHLY",
			price:        10000.0,
			tierOrder:    3,
			tierLimits:   erpLimits(3),
			features:     erpFeatures(3),
		},
		// ── ERP Suite — Perpetual (one-time) license tiers ───────────────────────────
		// The ERP SUITE = full PowerSuite coverage (ordering/POS/inventory/logistics/
		// treasury/core) PLUS the full ERP module. Features/limits = union of the PowerSuite
		// tier set and the ERP module tier set, so gating stays uniform with both product
		// families. Tier 3 = full access across all services. ONE_TIME → non-expiring
		// (billing_mode=one_time, is_perpetual). Setup fees set inline (setup_fees.go skips ONE_TIME).
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("erp:STARTER_ONE_TIME")),
			planCode:     "ERP_STARTER_ONE_TIME",
			name:         "ERP Suite Starter — Perpetual License",
			description:  "One-time purchase of the ERP Suite at starter level: full commerce stack (ordering, POS, inventory, logistics, payments, CRM) plus core ERP — HR, payroll, procurement, and leave management for small teams.",
			billingCycle: "ONE_TIME",
			price:        80000.0,
			tierOrder:    1,
			setupFee:     15000.0,
			planType:     subscriptionplan.PlanTypeTIERED,
			tierLimits:   mergeLimits(powerSuiteLimits(1), erpLimits(1)),
			features:     unionFeatures(powerSuiteFeatures(1), erpFeatures(1)),
		},
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("erp:GROWTH_ONE_TIME")),
			planCode:     "ERP_GROWTH_ONE_TIME",
			name:         "ERP Suite Growth — Perpetual License",
			description:  "One-time purchase of the ERP Suite at growth level: multi-outlet commerce plus multi-department HR, payroll, asset management, budgeting, advanced reporting, and approval workflows.",
			billingCycle: "ONE_TIME",
			price:        150000.0,
			tierOrder:    2,
			setupFee:     20000.0,
			planType:     subscriptionplan.PlanTypeTIERED,
			tierLimits:   mergeLimits(powerSuiteLimits(2), erpLimits(2)),
			features:     unionFeatures(powerSuiteFeatures(2), erpFeatures(2)),
		},
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("erp:PROFESSIONAL_ONE_TIME")),
			planCode:     "ERP_PROFESSIONAL_ONE_TIME",
			name:         "ERP Suite Professional — Perpetual License",
			description:  "One-time purchase of the full ERP Suite at enterprise level — full access across every service: unlimited scale, hotel module, API access, audit trail, custom workflows, full financial suite, and priority support.",
			billingCycle: "ONE_TIME",
			price:        250000.0,
			tierOrder:    3,
			setupFee:     30000.0,
			planType:     subscriptionplan.PlanTypeTIERED,
			tierLimits:   mergeLimits(powerSuiteLimits(3), erpLimits(3)),
			features:     unionFeatures(powerSuiteFeatures(3), erpFeatures(3)),
		},
	}

	for _, p := range plans {
		existing, err := tx.SubscriptionPlan.Get(ctx, p.id)
		if err != nil && !ent.IsNotFound(err) {
			return fmt.Errorf("lookup erp plan %s: %w", p.planCode, err)
		}
		planType := p.planType
		if planType == "" {
			planType = subscriptionplan.PlanTypeSTANDALONE_SERVICE
		}
		if existing != nil {
			_, err = tx.SubscriptionPlan.UpdateOneID(p.id).
				SetPlanCode(p.planCode).SetName(p.name).SetDescription(p.description).
				SetBillingCycle(p.billingCycle).SetPlanType(planType).
				SetBasePrice(p.price).SetSetupFee(p.setupFee).SetCurrency("KES").SetIsActive(true).SetIsPublic(true).
				SetTierOrder(p.tierOrder).SetTierLimitsJSON(p.tierLimits).SetServiceTag(serviceTag).SetUpdatedAt(now).Save(ctx)
		} else {
			_, err = tx.SubscriptionPlan.Create().
				SetID(p.id).SetPlanCode(p.planCode).SetName(p.name).SetDescription(p.description).
				SetBillingCycle(p.billingCycle).SetPlanType(planType).
				SetBasePrice(p.price).SetSetupFee(p.setupFee).SetCurrency("KES").SetIsActive(true).SetIsPublic(true).
				SetTierOrder(p.tierOrder).SetTierLimitsJSON(p.tierLimits).SetServiceTag(serviceTag).
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

	// Retire the legacy flat ERP_ONE_TIME (150k, tier 4) — superseded by the tiered
	// ERP_*_ONE_TIME licenses above. Deactivated (not deleted) so any tenant currently on
	// it still resolves its entitlements, mirroring the retireAnnualPlanRows/retireLegacyPOSPlans pattern.
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
