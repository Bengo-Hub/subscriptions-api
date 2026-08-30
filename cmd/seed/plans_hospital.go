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

// ── Codevertex Afya (hospital-service) Plans ───────────────────────────────────
//
// Authoritative spec: `CODEVERTEX AFRICA HOSPITAL MANAGEMENT SYSTEMS PRICING MODEL.md`
// (repo root) + `hospital-service/hospital-api/docs/integrations.md` §4. Mirrors the
// cross-service-block pattern established by the use-case PowerSuite families
// (plans_powersuite_usecase.go / docs/subscription-plans/plan-feature-matrix.md) — one
// family ("hospital"/"Afya"), three tiers: Clinic / Facility / Hospital. Each tier is a
// strict superset of the tier below (same invariant as PowerSuite, required by shared-ui-lib's
// tier-aware isFeatureUnlocked). service_tag="hospital" (distinct from "pos" — hospital-api is
// its own service, not a POS-line evolution).
//
// Cross-service coverage: Inventory (drug/asset — lots/batch/expiry tracking is core at every
// tier, same dispensing-safety rule as the Dawa pharmacy family), Treasury (billing/insurance/
// eTIMS), ERP (HR/payroll — Hospital tier only, per the pricing doc), Logistics (ambulance
// dispatch — Hospital tier only). Notifications (SMS/WhatsApp patient reminders) and the AI
// assistant credits are explicitly listed as OPTIONAL ADD-ONS in the pricing doc, not bundled
// into any base tier — intentionally excluded from the feature sets below.

// hospClinicalCore — patient/clinical essentials present at every Afya tier EXCEPT the
// standalone-chemist tier (see hospChemistCore) — a chemist has no OPD/reception/triage/
// consultation workflow at all, per migration-pos-pharmacy.md §6: "a chemist/dispensary is not
// a smaller product, it's the Afya Clinic tier's Pharmacy module in isolation."
func hospClinicalCore() []string {
	return []string{
		"patient_records", "reception_queuing", "consultation", "triage",
		"pharmacy_dispensing", "billing", "lab_requests_basic",
	}
}

// hospChemistCore — the entire clinical feature set for tier 0 (AFYA_CHEMIST). Deliberately NOT
// derived from hospClinicalCore(): a standalone chemist/dispensary sells OTC + dispenses against
// walk-in/external prescriptions, it never runs OPD reception/triage/consultation/lab.
func hospChemistCore() []string {
	return []string{"pharmacy_dispensing", "billing"}
}

// hospInventoryBlock — drug/asset (inventory-api) coverage per tier. Lots/batch/expiry
// tracking is core at every tier (dispensing safety), matching dawaSuiteFeatures' convention.
func hospInventoryBlock(tier int) []string {
	base := []string{
		"stock_tracking", "lots_batches", "batch_expiry_tracking", "expiry_alerts",
		"purchase_orders", "basic_reports", "basic_inventory_access",
	}
	if tier >= 2 {
		base = append(base, "multi_warehouse", "bulk_import", "requisitions", "asset_management")
	}
	if tier >= 3 {
		base = append(base, "stock_take", "low_stock_alerts", "stock_alerts", "rfqs", "procurement_contracts")
	}
	return base
}

// hospTreasuryBlock — billing/insurance (treasury-api) coverage per tier. eTIMS is never
// tier-gated (KRA legal requirement — opt-in via tenant config, not subscription).
func hospTreasuryBlock(tier int) []string {
	base := []string{
		"wallet_management", "payment_collection", "payment_links", "transaction_reports",
		"customer_management", "tax_codes", "etims_integration", "basic_treasury_access",
	}
	if tier >= 2 {
		base = append(base,
			"invoice_generation", "credit_notes", "vendor_management",
			"ledger_posting", "treasury_approvals", "insurance_claims",
		)
	}
	if tier >= 3 {
		base = append(base, "smart_tax_compliance", "reconciliation", "audit_trail")
	}
	return base
}

// hospERPBlock — HR/payroll integration (erp-api), Hospital tier ONLY per the pricing doc
// ("HR & payroll integration" is listed exclusively under Afya Hospital, unlike the generic
// PowerSuite families which unlock basic HR at tier 2).
func hospERPBlock(tier int) []string {
	if tier < 3 {
		return nil
	}
	return []string{"hr_management", "payroll", "leave_management", "attendance", "basic_reports"}
}

// hospLogisticsBlock — ambulance dispatch (logistics-api), Hospital tier only (per
// docs/integrations.md §2A, a thin reference into logistics-api's Task/PricingRule).
func hospLogisticsBlock(tier int) []string {
	if tier < 3 {
		return nil
	}
	return []string{"ambulance_dispatch", "basic_logistics_access"}
}

// afyaTierFeatures — full cross-service union for Afya tier N (0=Chemist, 1=Clinic, 2=Facility,
// 3=Hospital).
func afyaTierFeatures(tier int) []string {
	clinicalCore := hospClinicalCore()
	if tier == 0 {
		clinicalCore = hospChemistCore()
	}
	f := unionFeatures(
		clinicalCore,
		hospInventoryBlock(tier),
		hospTreasuryBlock(tier),
		hospERPBlock(tier),
		hospLogisticsBlock(tier),
	)
	if tier >= 2 {
		f = append(f,
			"in_house_lab", "inpatient_module", "controlled_substance_register",
			"multi_cashier", "multi_department", "diagnosis_lab_catalogues", "discharge_summaries",
		)
	}
	if tier >= 3 {
		f = append(f,
			"theatre_module", "maternity_module", "morgue_module", "specialized_programmes",
			"multi_branch", "advanced_analytics", "api_access", "priority_support",
			"taifa_care_dedicated_onboarding", "khis_dhis2_reporting",
		)
	}
	return unionFeatures(f)
}

// afyaTierLimits — operational caps per tier, from the pricing doc's facility-size guidance
// ("~30 patients/day" Clinic; Facility/Hospital scale up; Hospital unlimited/per-branch quoted).
func afyaTierLimits(tier int) map[string]any {
	switch tier {
	case 0: // Chemist — one dispensary counter, no OPD volume concept
		return map[string]any{
			"max_outlets":                1,
			"max_branches":               1,
			"max_staff":                  3,
			"max_devices":                1,
			"inventory_max_sku":          300,
			"inventory_max_warehouses":   1,
			"max_transactions_per_month": 10000,
			"api_calls_per_month":        5000,
		}
	case 1:
		return map[string]any{
			"max_patients_per_day":       30,
			"max_outlets":                1,
			"max_branches":               1,
			"max_staff":                  10,
			"max_devices":                2,
			"inventory_max_sku":          500,
			"inventory_max_warehouses":   1,
			"max_transactions_per_month": 15000,
			"api_calls_per_month":        10000,
		}
	case 2:
		return map[string]any{
			"max_patients_per_day":       150,
			"max_outlets":                1,
			"max_branches":               1,
			"max_departments":            -1,
			"max_staff":                  50,
			"max_devices":                10,
			"inventory_max_sku":          5000,
			"inventory_max_warehouses":   3,
			"max_transactions_per_month": 20000,
			"api_calls_per_month":        50000,
		}
	default: // tier 3 — Hospital, multi-branch, quoted per branch
		return map[string]any{
			"max_patients_per_day":       -1,
			"max_outlets":                -1,
			"max_branches":               -1,
			"max_departments":            -1,
			"max_staff":                  -1,
			"max_devices":                -1,
			"inventory_max_sku":          -1,
			"inventory_max_warehouses":   -1,
			"max_transactions_per_month": -1,
			"api_calls_per_month":        200000,
		}
	}
}

type afyaTier struct {
	code     string // AFYA_CHEMIST / AFYA_CLINIC / AFYA_FACILITY / AFYA_HOSPITAL
	label    string
	tier     int
	monthly  float64
	setupFee float64
	oneTime  float64 // 0 = not sold as a perpetual license (Hospital tier is "custom" quoted)
	support  float64
	// facilityType drives hospital-ui's adaptive sidebar naming (facility-nomenclature.ts) and
	// hospital-api's Tenant.metadata cache — see docs/architecture.md's facility-tier defaults
	// table. NOT the same field as tier (tier also governs feature unlocking; facilityType is
	// purely presentation/UX naming, kept separate so a custom-quoted tenant — the pricing doc's
	// "if your facility doesn't sit neatly in one of the tiers... we price the facility" case —
	// can carry a facility_type override independent of its feature tier).
	facilityType string
}

// AFYA_CHEMIST is a genuine 4th, cheaper tier below Afya Clinic (tier 0) — a standalone
// dispensary/chemist counter, per user decision 2026-08-29 (see the migration plan). Pricing is
// a placeholder scaled below Clinic's (~60% of monthly/oneTime, ~53-55% of setup/support, same
// "narrower product prices below the full-feature comparable" principle already applied to the
// eTIMS external-API tiers) — needs real business/pricing-guide sign-off before go-live, flagged
// in `CODEVERTEX AFRICA HOSPITAL MANAGEMENT SYSTEMS PRICING MODEL.md`.
var afyaTiers = []afyaTier{
	{code: "AFYA_CHEMIST", label: "Afya Chemist", tier: 0, monthly: 4500, setupFee: 8000, oneTime: 75000, support: 10000, facilityType: "chemist"},
	{code: "AFYA_CLINIC", label: "Afya Clinic", tier: 1, monthly: 7500, setupFee: 15000, oneTime: 140000, support: 18000, facilityType: "clinic"},
	{code: "AFYA_FACILITY", label: "Afya Facility", tier: 2, monthly: 18000, setupFee: 35000, oneTime: 320000, support: 42000, facilityType: "facility"},
	{code: "AFYA_HOSPITAL", label: "Afya Hospital", tier: 3, monthly: 40000, setupFee: 75000, oneTime: 0, support: 90000, facilityType: "hospital"},
}

// seedHospitalPlans upserts the recurring MONTHLY Afya Clinic/Facility/Hospital plans.
func seedHospitalPlans(ctx context.Context, tx *ent.Tx) error {
	now := time.Now()
	for _, t := range afyaTiers {
		id := uuid.NewSHA1(uuid.NameSpaceOID, []byte("plan:"+t.code))
		name := fmt.Sprintf("Codevertex %s", t.label)
		desc := fmt.Sprintf("Codevertex Afya — %s tier: one patient record across consultation, lab, pharmacy, inpatient and billing.", t.label)
		feats := afyaTierFeatures(t.tier)
		limits := afyaTierLimits(t.tier)

		existing, err := tx.SubscriptionPlan.Get(ctx, id)
		if err != nil && !ent.IsNotFound(err) {
			return fmt.Errorf("lookup hospital plan %s: %w", t.code, err)
		}
		meta := map[string]any{"facility_type": t.facilityType}
		if existing != nil {
			_, err = tx.SubscriptionPlan.UpdateOneID(id).
				SetPlanCode(t.code).SetName(name).SetDescription(desc).
				SetBillingCycle("MONTHLY").SetPlanType(subscriptionplan.PlanTypeTIERED).
				SetBasePrice(t.monthly).SetSetupFee(t.setupFee).SetCurrency("KES").
				SetIsActive(true).SetIsPublic(true).
				SetTierOrder(t.tier).SetFreeTrialDays(14).
				SetTierLimitsJSON(limits).SetServiceTag("hospital").SetMetadata(meta).SetUpdatedAt(now).Save(ctx)
		} else {
			_, err = tx.SubscriptionPlan.Create().
				SetID(id).SetPlanCode(t.code).SetName(name).SetDescription(desc).
				SetBillingCycle("MONTHLY").SetPlanType(subscriptionplan.PlanTypeTIERED).
				SetBasePrice(t.monthly).SetSetupFee(t.setupFee).SetCurrency("KES").
				SetIsActive(true).SetIsPublic(true).
				SetTierOrder(t.tier).SetFreeTrialDays(14).
				SetTierLimitsJSON(limits).SetServiceTag("hospital").SetMetadata(meta).
				SetCreatedAt(now).SetUpdatedAt(now).Save(ctx)
		}
		if err != nil {
			return fmt.Errorf("upsert hospital plan %s: %w", t.code, err)
		}
		if err := seedPlanFeatures(ctx, tx, id, feats); err != nil {
			return fmt.Errorf("seed features for %s: %w", t.code, err)
		}
		log.Printf("  hospital plan: %s (MONTHLY, KES %.0f + %.0f setup)", name, t.monthly, t.setupFee)
	}
	return nil
}

// seedHospitalOneTimePlans seeds the perpetual buy-outright licenses for Clinic/Facility
// (Hospital tier is explicitly "custom" quoted per branch in the pricing doc — no fixed
// one-time row). Grants EXACTLY its recurring tier's features/limits, same as the PowerSuite
// one-time pattern.
func seedHospitalOneTimePlans(ctx context.Context, tx *ent.Tx) error {
	now := time.Now()
	for _, t := range afyaTiers {
		if t.oneTime <= 0 {
			continue
		}
		planCode := t.code + "_ONE_TIME"
		id := uuid.NewSHA1(uuid.NameSpaceOID, []byte("plan:"+planCode))
		name := fmt.Sprintf("Codevertex %s — Perpetual License", t.label)
		desc := fmt.Sprintf("One-time buy-outright license for Codevertex %s. Updates and support via the annual support plan.", t.label)
		feats := afyaTierFeatures(t.tier)
		limits := afyaTierLimits(t.tier)

		existing, err := tx.SubscriptionPlan.Get(ctx, id)
		if err != nil && !ent.IsNotFound(err) {
			return fmt.Errorf("lookup hospital one-time plan %s: %w", planCode, err)
		}
		meta := map[string]any{"facility_type": t.facilityType}
		if existing != nil {
			_, err = tx.SubscriptionPlan.UpdateOneID(id).
				SetPlanCode(planCode).SetName(name).SetDescription(desc).
				SetBillingCycle("ONE_TIME").SetPlanType(subscriptionplan.PlanTypeTIERED).
				SetBasePrice(t.oneTime).SetSetupFee(t.setupFee).SetCurrency("KES").
				SetIsActive(true).SetIsPublic(true).
				SetTierOrder(t.tier).SetFreeTrialDays(0).
				SetTierLimitsJSON(limits).SetServiceTag("hospital").SetMetadata(meta).SetUpdatedAt(now).Save(ctx)
		} else {
			_, err = tx.SubscriptionPlan.Create().
				SetID(id).SetPlanCode(planCode).SetName(name).SetDescription(desc).
				SetBillingCycle("ONE_TIME").SetPlanType(subscriptionplan.PlanTypeTIERED).
				SetBasePrice(t.oneTime).SetSetupFee(t.setupFee).SetCurrency("KES").
				SetIsActive(true).SetIsPublic(true).
				SetTierOrder(t.tier).SetFreeTrialDays(0).
				SetTierLimitsJSON(limits).SetServiceTag("hospital").SetMetadata(meta).
				SetCreatedAt(now).SetUpdatedAt(now).Save(ctx)
		}
		if err != nil {
			return fmt.Errorf("upsert hospital one-time plan %s: %w", planCode, err)
		}
		if err := seedPlanFeatures(ctx, tx, id, feats); err != nil {
			return fmt.Errorf("seed features for %s: %w", planCode, err)
		}
		log.Printf("  hospital license: %s (ONE_TIME, KES %.0f + %.0f setup)", name, t.oneTime, t.setupFee)
	}
	return nil
}

// seedHospitalSupportPlans seeds the ANNUAL support plans accompanying each tier (both
// recurring subscribers renewing annually and perpetual-license holders buying ongoing
// support). Entitlement-only — never unlocks product features. is_public=false: assigned by
// the platform owner, not self-served. service_tag=platform so setup_fees.go's per-service
// defaults never apply — same convention as SUPPORT_{HOSP,DUKA,DAWA}_*.
func seedHospitalSupportPlans(ctx context.Context, tx *ent.Tx) error {
	now := time.Now()
	for _, t := range afyaTiers {
		planCode := "SUPPORT_" + t.code
		id := uuid.NewSHA1(uuid.NameSpaceOID, []byte("plan:"+planCode))
		name := fmt.Sprintf("Annual Support — Codevertex %s", t.label)
		desc := fmt.Sprintf("Annual software support, updates and cloud sync for Codevertex %s.", t.label)

		existing, err := tx.SubscriptionPlan.Get(ctx, id)
		if err != nil && !ent.IsNotFound(err) {
			return fmt.Errorf("lookup hospital support plan %s: %w", planCode, err)
		}
		if existing != nil {
			_, err = tx.SubscriptionPlan.UpdateOneID(id).
				SetPlanCode(planCode).SetName(name).SetDescription(desc).
				SetBillingCycle("ANNUAL").SetPlanType(subscriptionplan.PlanTypeTIERED).
				SetBasePrice(t.support).SetSetupFee(0).SetCurrency("KES").
				SetIsActive(true).SetIsPublic(false).
				SetTierOrder(t.tier).SetFreeTrialDays(0).
				SetTierLimitsJSON(map[string]any{}).SetServiceTag("platform").
				SetMetadata(map[string]any{"support_plan": true}).
				SetUpdatedAt(now).Save(ctx)
		} else {
			_, err = tx.SubscriptionPlan.Create().
				SetID(id).SetPlanCode(planCode).SetName(name).SetDescription(desc).
				SetBillingCycle("ANNUAL").SetPlanType(subscriptionplan.PlanTypeTIERED).
				SetBasePrice(t.support).SetSetupFee(0).SetCurrency("KES").
				SetIsActive(true).SetIsPublic(false).
				SetTierOrder(t.tier).SetFreeTrialDays(0).
				SetTierLimitsJSON(map[string]any{}).SetServiceTag("platform").
				SetMetadata(map[string]any{"support_plan": true}).
				SetCreatedAt(now).SetUpdatedAt(now).Save(ctx)
		}
		if err != nil {
			return fmt.Errorf("upsert hospital support plan %s: %w", planCode, err)
		}
		if err := seedPlanFeatures(ctx, tx, id, nil); err != nil {
			return fmt.Errorf("seed features for %s: %w", planCode, err)
		}
		log.Printf("  hospital annual support plan: %s (ANNUAL, KES %.0f)", name, t.support)
	}
	return nil
}
