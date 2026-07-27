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

// ── Use-Case PowerSuite Plans (Hospitality / Duka / Dawa) ─────────────────────
//
// Authoritative spec: docs/subscription-plans/{Hospitality,Retail,Pharmacy}-Power-suite-plans.txt
// (+ the derived matrix in docs/subscription-plans/plan-feature-matrix.md).
//
// Three per-use-case PowerSuite families, each bundling POS + Inventory + Treasury +
// Ordering + Logistics + CRM + ERP across three tiers (Basic / Professional / Gold).
// These rows EVOLVED IN PLACE from the POS product lines (POS_HOSP_/POS_DUKA_/POS_DAWA_):
// the deterministic seed ids keep their historical "pos:{family}:{TIER}" namespace strings
// so plan UUIDs — and every FK that references them (plan features, tenant subscriptions) —
// stay stable across the rename (same pattern as the COMPLETE_→POWERSUITE_ rename).
//
// Feature sets are built from single-source per-service blocks below. INVARIANT: within a
// family every tier is a strict superset of the tier below — that is the primary correctness
// contract for shared-ui-lib's tier-aware isFeatureUnlocked. basic_inventory_access /
// basic_treasury_access are auto-injected by injectIntegrationFeatures (service_tag=pos);
// basic_logistics_access is included explicitly since these bundles cover logistics.

// psOrderingBlock — Ordering service coverage per tier (basic / standard / all).
func psOrderingBlock(tier int) []string {
	base := []string{
		"online_ordering", "rider_app", "admin_dashboard", "paystack_integration",
		"sms_notifications", "push_notifications", "basic_analytics",
		"custom_domain", "loyalty_program", "wallet", "delivery_zones",
	}
	if tier >= 2 {
		base = append(base,
			"mpesa_integration", "advanced_analytics", "multi_outlet", "promo_codes",
			"group_ordering", "scheduled_delivery", "pos_integration",
		)
	}
	if tier >= 3 {
		base = append(base,
			"route_optimization", "api_webhooks", "white_labeling",
			"priority_support", "premium_support",
		)
	}
	return base
}

// psLogisticsBlock — Logistics coverage per tier (basic rider app access at every tier).
func psLogisticsBlock(tier int) []string {
	base := []string{"rider_management", "delivery_assignment", "live_tracking", "basic_dispatch", "basic_logistics_access"}
	if tier >= 2 {
		base = append(base, "route_optimisation", "driver_analytics", "performance_reports")
	}
	if tier >= 3 {
		base = append(base, "api_access", "webhooks", "custom_integrations")
	}
	return base
}

// psCRMBlock — MarketFlow CRM coverage per tier (contacts + basic access at tier 1).
func psCRMBlock(tier int) []string {
	base := []string{"contact_management", "lead_management", "basic_campaigns", "shortlinks"}
	if tier >= 2 {
		base = append(base,
			"unlimited_campaigns", "landing_pages", "email_sequences", "ai_chat_agent",
			"lead_scoring", "funnel_builder", "automation_workflows",
			"ticketing", "helpdesk", "sla_policies", "knowledge_base", "testimonials",
		)
	}
	if tier >= 3 {
		base = append(base, "white_label", "dedicated_account_manager")
	}
	return base
}

// psERPBlock — ERP coverage per tier. Tier 1 = NO erp access (the ERP link in pos/
// inventory/treasury UIs shows locked). Tier 2 = basic HR (attendance + employee sync from
// pos/treasury/inventory) WITHOUT payroll, appraisals, recruitment or training. Tier 3 = all.
func psERPBlock(tier int) []string {
	if tier < 2 {
		return nil
	}
	base := []string{"hr_management", "leave_management", "attendance", "basic_reports"}
	if tier >= 3 {
		base = append(base,
			"payroll", "appraisals", "recruitment", "training",
			"basic_procurement", "asset_management", "budgeting", "advanced_reports",
			"multi_department", "approval_workflows", "custom_workflows", "staff_fund_from_salary",
		)
	}
	return base
}

// psTreasuryBlock — Treasury coverage per tier.
//
// Tier 1: Transactions + Customers + Quotations + Purchases & Bills + core Tax & Compliance
// (tax codes/periods/rates) + eTIMS (KRA compliance is a legal requirement — never tier-gated).
// NO Accounting module (ledger/vouchers/reconciliation), NO invoices/credit notes, NO approvals,
// NO smart tax & compliance card. vendorAtT1 grants Suppliers & Vendors from tier 1
// (retail + pharmacy specs list it; hospitality gets it at tier 2 with "All").
func psTreasuryBlock(tier int, vendorAtT1 bool) []string {
	base := []string{
		"wallet_management", "payment_collection", "payment_links", "transaction_reports",
		"customer_management", "quotations", "ar_tracking", "ap_tracking",
		"tax_codes", "etims_integration",
	}
	if vendorAtT1 {
		base = append(base, "vendor_management")
	}
	if tier >= 2 {
		base = append(base,
			"invoice_generation", "credit_notes", "vendor_management",
			"ledger_posting", "treasury_approvals", "smart_tax_compliance",
		)
	}
	if tier >= 3 {
		base = append(base, "vouchers", "reconciliation", "basic_reconciliation", "audit_trail")
	}
	return base
}

// psPOSCore — till essentials present in every family at every tier.
func psPOSCore() []string {
	return []string{
		"pos_terminal", "order_management", "receipt_printing", "daily_reports",
		"shift_reports", "mpesa_pos", "offline_sync",
	}
}

// psInventoryCore — inventory essentials at tier 1 for every family: catalog + stock +
// procurement basics (PO/GRN + suppliers) + basic reports + transfers. Per the specs tier 1
// has NO bulk import, NO stock take and NO stock alerts.
func psInventoryCore() []string {
	return []string{"stock_tracking", "purchase_orders", "supplier_portal", "basic_reports", "stock_transfers"}
}

// hospSuiteFeatures — Hospitality family (hotels, restaurants, bars, cafes).
// Never grants lots & batches / expiry tracking / expiry alerts (excluded at every tier).
func hospSuiteFeatures(tier int) []string {
	f := unionFeatures(psPOSCore(), psInventoryCore(), psOrderingBlock(tier), psLogisticsBlock(tier), psCRMBlock(tier), psERPBlock(tier), psTreasuryBlock(tier, false))
	f = append(f, "table_management", "kds", "happy_hour") // Floor & Service / Display Board / Hotel→Happy Hour from tier 1
	if tier >= 2 {
		f = append(f,
			"multi_cashier", "facility_booking", "storefront_banner",
			"bulk_import", "stock_take", "requisitions", "manufacturing", "fixed_assets",
			"multi_warehouse", "inventory_multiple_images",
			"report_stock_reconciliation", "report_food_cost_variance",
			"low_stock_alerts", "stock_alerts",
		)
	}
	if tier >= 3 {
		f = append(f,
			"hotel_module", "conference_events",
			"rfqs", "procurement_contracts", "events_module", "report_menu_engineering",
		)
	}
	return unionFeatures(f)
}

// dukaSuiteFeatures — Retail family (shops, supermarkets, hardware, boutiques).
// Never grants lots & batches / expiry tracking / expiry alerts or the hotel stack.
// Warranties is a RETAIL-ONLY module, tier 2+.
func dukaSuiteFeatures(tier int) []string {
	f := unionFeatures(psPOSCore(), psInventoryCore(), psOrderingBlock(tier), psLogisticsBlock(tier), psCRMBlock(tier), psERPBlock(tier), psTreasuryBlock(tier, true))
	f = append(f, "barcode_scanning")
	if tier >= 2 {
		f = append(f,
			"multi_cashier", "layaway", "commissions", "storefront_banner",
			"bulk_import", "stock_take", "requisitions", "manufacturing", "fixed_assets",
			"multi_warehouse", "inventory_multiple_images", "warranties",
			"report_stock_reconciliation", "report_food_cost_variance",
			"low_stock_alerts", "stock_alerts",
		)
	}
	if tier >= 3 {
		f = append(f, "rfqs", "procurement_contracts", "report_menu_engineering")
	}
	return unionFeatures(f)
}

// dawaSuiteFeatures — Pharmacy family (chemists, pharmacies, dispensaries, agrovets).
// Lots & batches + expiry tracking + expiry alerts are core at EVERY tier (dispensing safety).
// Pharmacy clinical modules (prescriptions, patients, claims) unlock at tier 2 per the spec
// (tier 1 "Pharmacy → no access"); stock take + stock alerts unlock at tier 3.
func dawaSuiteFeatures(tier int) []string {
	f := unionFeatures(psPOSCore(), psInventoryCore(), psOrderingBlock(tier), psLogisticsBlock(tier), psCRMBlock(tier), psERPBlock(tier), psTreasuryBlock(tier, true))
	f = append(f, "lots_batches", "batch_expiry_tracking", "expiry_alerts")
	if tier >= 2 {
		f = append(f,
			"prescription_management", "patient_history", "insurance_claims",
			"multi_cashier", "barcode_scanning", "storefront_banner",
			"bulk_import", "requisitions", "multi_warehouse", "inventory_multiple_images",
		)
	}
	if tier >= 3 {
		f = append(f,
			"label_printing",
			"stock_take", "rfqs", "procurement_contracts", "manufacturing", "fixed_assets",
			"low_stock_alerts", "stock_alerts", "report_stock_reconciliation",
		)
	}
	return unionFeatures(f)
}

// useCaseSuiteLimits — powerSuiteLimits(tier) overlaid with family-specific POS caps.
// Retail/pharmacy have no tables (or rooms); hospitality Gold adds unlimited rooms +
// conference events.
func useCaseSuiteLimits(family string, tier int) map[string]any {
	base := powerSuiteLimits(tier)
	switch family {
	case "hosp":
		switch tier {
		case 1:
			return mergeLimits(base, map[string]any{"max_devices": 2, "max_cashiers": 3, "max_tables": 20})
		case 2:
			return mergeLimits(base, map[string]any{"max_devices": 5, "max_cashiers": 10, "max_tables": 50})
		default:
			return mergeLimits(base, map[string]any{"max_devices": -1, "max_cashiers": -1, "max_tables": -1, "max_rooms": -1, "max_conference_events": -1})
		}
	default: // duka, dawa — no tables/rooms
		delete(base, "max_tables")
		switch tier {
		case 1:
			return mergeLimits(base, map[string]any{"max_devices": 1, "max_cashiers": 2})
		case 2:
			return mergeLimits(base, map[string]any{"max_devices": 5, "max_cashiers": 10})
		default:
			return mergeLimits(base, map[string]any{"max_devices": -1, "max_cashiers": -1})
		}
	}
}

// useCaseFamilies drives the recurring, one-time and support seeds (single source of truth).
type useCaseFamily struct {
	key       string // deterministic-id namespace segment (hosp/duka/dawa)
	code      string // plan-code segment (HOSP/DUKA/DAWA)
	label     string
	features  func(tier int) []string
	monthly   [3]float64 // tier 1..3 monthly price
	setupFees [3]float64 // tier 1..3 setup/installation fee (shared by recurring + one-time)
	oneTime   [3]float64 // tier 1..3 buy-outright price
	support   [3]float64 // tier 1..3 annual support price
}

var useCaseFamilies = []useCaseFamily{
	{
		key: "hosp", code: "HOSP", label: "Hospitality",
		features:  hospSuiteFeatures,
		monthly:   [3]float64{2500, 4000, 6500},
		setupFees: [3]float64{5000, 10000, 20000},
		oneTime:   [3]float64{45000, 90000, 150000},
		support:   [3]float64{9000, 19000, 30000},
	},
	{
		key: "duka", code: "DUKA", label: "Retail (Duka)",
		features:  dukaSuiteFeatures,
		monthly:   [3]float64{2500, 4500, 8500},
		setupFees: [3]float64{5000, 10000, 20000},
		oneTime:   [3]float64{45000, 90000, 150000},
		support:   [3]float64{9000, 18000, 30000},
	},
	{
		key: "dawa", code: "DAWA", label: "Pharmacy (Dawa)",
		features:  dawaSuiteFeatures,
		monthly:   [3]float64{1500, 3000, 6000},
		setupFees: [3]float64{5000, 10000, 20000},
		oneTime:   [3]float64{45000, 90000, 150000},
		support:   [3]float64{9000, 18000, 30000},
	},
}

var useCaseTierNames = [3]string{"Basic", "Professional", "Gold"}
var useCaseTierCodes = [3]string{"BASIC", "PRO", "GOLD"}

// legacy POS-line id namespaces — tier index → historical namespace segment. Keeping these
// preserves the plan UUIDs of the POS_HOSP_/POS_DUKA_/POS_DAWA_ rows being evolved in place.
var legacyTierNamespace = [3]string{"STARTER", "PRO", "ENTERPRISE"}

// seedUseCasePowerSuitePlans upserts the recurring MONTHLY rows for all three families.
func seedUseCasePowerSuitePlans(ctx context.Context, tx *ent.Tx) error {
	now := time.Now()
	for _, fam := range useCaseFamilies {
		for t := 0; t < 3; t++ {
			tier := t + 1
			id := uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf("pos:%s:%s", fam.key, legacyTierNamespace[t])))
			planCode := fmt.Sprintf("POWERSUITE_%s_%s", fam.code, useCaseTierCodes[t])
			name := fmt.Sprintf("PowerSuite %s — %s", fam.label, useCaseTierNames[t])
			desc := fmt.Sprintf("PowerSuite %s tier %d: POS, inventory, treasury, ordering, logistics, CRM and ERP for %s businesses.",
				fam.label, tier, fam.label)
			feats := fam.features(tier)
			limits := useCaseSuiteLimits(fam.key, tier)

			existing, err := tx.SubscriptionPlan.Get(ctx, id)
			if err != nil && !ent.IsNotFound(err) {
				return fmt.Errorf("lookup use-case powersuite plan %s: %w", planCode, err)
			}
			if existing != nil {
				_, err = tx.SubscriptionPlan.UpdateOneID(id).
					SetPlanCode(planCode).SetName(name).SetDescription(desc).
					SetBillingCycle("MONTHLY").SetPlanType(subscriptionplan.PlanTypeTIERED).
					SetBasePrice(fam.monthly[t]).SetSetupFee(fam.setupFees[t]).SetCurrency("KES").
					SetIsActive(true).SetIsPublic(true).
					SetTierOrder(tier).SetFreeTrialDays(14).
					SetTierLimitsJSON(limits).SetServiceTag("pos").SetUpdatedAt(now).Save(ctx)
			} else {
				_, err = tx.SubscriptionPlan.Create().
					SetID(id).SetPlanCode(planCode).SetName(name).SetDescription(desc).
					SetBillingCycle("MONTHLY").SetPlanType(subscriptionplan.PlanTypeTIERED).
					SetBasePrice(fam.monthly[t]).SetSetupFee(fam.setupFees[t]).SetCurrency("KES").
					SetIsActive(true).SetIsPublic(true).
					SetTierOrder(tier).SetFreeTrialDays(14).
					SetTierLimitsJSON(limits).SetServiceTag("pos").
					SetCreatedAt(now).SetUpdatedAt(now).Save(ctx)
			}
			if err != nil {
				return fmt.Errorf("upsert use-case powersuite plan %s: %w", planCode, err)
			}
			if err := seedPlanFeatures(ctx, tx, id, feats); err != nil {
				return fmt.Errorf("seed features for %s: %w", planCode, err)
			}
			log.Printf("  use-case powersuite plan: %s (MONTHLY, KES %.0f + %.0f setup)", name, fam.monthly[t], fam.setupFees[t])
		}
	}
	return nil
}
