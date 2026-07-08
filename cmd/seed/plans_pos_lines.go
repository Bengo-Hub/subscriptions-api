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

// ── POS Product-Line Plans (Codevertex POS / Duka / Dawa) ────────────────────
// Three industry product lines on the shared "pos" service tag, each with
// Starter / Professional / Enterprise tiers plus a perpetual license. Pricing is
// value-competitive vs the 2026 Kenyan POS market (see shared-docs/CODEVERTEX-PRICING-MODEL.md).
//
// The MICRO / PAYG entry point for each line is NOT a subscription plan — it is the
// SC_POS_MICRO_1PCT service-charge plan (1% / min KES 2 / cap KES 50) assigned at the
// product-subscription level, which sets billing_mode=service_charge and replaces the gate.
//
// Feature sets are wired so subscription gating is meaningful per tier. basic_inventory_access
// and basic_treasury_access are auto-injected by injectIntegrationFeatures — not listed here.
// Limits use only pos-service limit keys (+ platform max_users) to stay reconcile-warning-free;
// multi-outlet is expressed via the multi_outlet FEATURE, never the ordering max_outlets LIMIT.

func seedPOSProductLinePlans(ctx context.Context, tx *ent.Tx) error {
	now := time.Now()
	serviceTag := "pos"

	type planDef struct {
		id           uuid.UUID
		planCode     string
		name         string
		description  string
		billingCycle string
		price        float64
		setupFee     float64
		tierOrder    int
		tierLimits   map[string]any
		features     []string
	}

	// Point-of-sale essentials present in every tier (incl. the PAYG micro path).
	// Every POS business runs on inventory, so basic warehouse management (real-time stock,
	// stock adjustments, stock takes, low-stock alerts) is part of the core set across all
	// tiers — including Starter — not a paid upgrade.
	core := []string{
		"pos_terminal", "order_management", "receipt_printing", "daily_reports",
		"shift_reports", "mpesa_pos",
		"stock_tracking", "low_stock_alerts",
	}
	with := func(extra ...string) []string {
		return append(append([]string{}, core...), extra...)
	}

	plans := []planDef{
		// ─── Codevertex POS (Hospitality: hotels, restaurants, bars) ───────────
		{
			id: uuid.NewSHA1(uuid.NameSpaceOID, []byte("pos:hosp:STARTER")), planCode: "POS_HOSP_STARTER",
			name: "Codevertex POS Starter (Hospitality)", description: "Cafes, small restaurants & bars turning over up to ~KES 300k/month. POS, M-Pesa Till, shift management, standard receipts, basic inventory, table management & loyalty.",
			billingCycle: "MONTHLY", price: 2500, setupFee: 5000, tierOrder: 1,
			tierLimits: map[string]any{"max_devices": 2, "max_cashiers": 3, "max_tables": 20},
			// kds is core operations for hospitality/quick_service outlets (kitchen/bar ticket
			// routing), not a paid upgrade — every tier including Starter gets it. Procurement
			// (purchase_orders/supplier_portal) is likewise core: every bar/restaurant reorders
			// stock from suppliers regardless of turnover, matching stock_tracking's already-core
			// status in the shared `core` set above — it shouldn't take a Pro upgrade to raise a PO.
			features: with("table_management", "loyalty_program", "etims_integration", "kds", "purchase_orders", "supplier_portal"),
		},
		{
			id: uuid.NewSHA1(uuid.NameSpaceOID, []byte("pos:hosp:PRO")), planCode: "POS_HOSP_PRO",
			name: "Codevertex POS Professional (Hospitality)", description: "Full restaurants & bars with rooms, ~KES 300k–2M/month. + Floor plans, multi-station KDS, bill splitting, recipe/BOM, eTIMS, Treasury invoicing.",
			billingCycle: "MONTHLY", price: 4000, setupFee: 10000, tierOrder: 2,
			tierLimits: map[string]any{"max_devices": 5, "max_cashiers": 10, "max_tables": 50},
			features:   with("table_management", "loyalty_program", "kds", "multi_cashier", "offline_sync", "happy_hour", "etims_integration", "invoice_generation", "purchase_orders", "supplier_portal"),
		},
		{
			id: uuid.NewSHA1(uuid.NameSpaceOID, []byte("pos:hosp:ENTERPRISE")), planCode: "POS_HOSP_ENTERPRISE",
			name: "Codevertex POS Hospitality (Hotels & F&B)", description: "Full hotels with F&B, conference halls & spas, ~KES 2M+/month. + Reception & check-in/out, room inventory, charge-to-room, guest folio, conference module, multi-outlet reports, API.",
			billingCycle: "MONTHLY", price: 6500, setupFee: 20000, tierOrder: 3,
			tierLimits: map[string]any{"max_devices": -1, "max_cashiers": -1, "max_tables": -1, "max_rooms": -1, "max_conference_events": -1},
			features:   with("table_management", "loyalty_program", "kds", "multi_cashier", "offline_sync", "happy_hour", "etims_integration", "invoice_generation", "purchase_orders", "supplier_portal", "hotel_module", "conference_events", "multi_outlet", "advanced_analytics", "api_access", "priority_support"),
		},
		{
			id: uuid.NewSHA1(uuid.NameSpaceOID, []byte("pos:hosp:LICENSE")), planCode: "POS_HOSP_LICENSE",
			name: "Codevertex POS Hospitality — Perpetual License", description: "One-time perpetual license for the full hospitality feature set at a single property. Cloud sync & updates via annual support.",
			billingCycle: "ONE_TIME", price: 150000, tierOrder: 11,
			tierLimits: map[string]any{"max_devices": -1, "max_cashiers": -1, "max_tables": -1, "max_rooms": -1, "max_conference_events": -1},
			features:   with("table_management", "loyalty_program", "kds", "multi_cashier", "offline_sync", "happy_hour", "etims_integration", "invoice_generation", "purchase_orders", "supplier_portal", "hotel_module", "conference_events", "multi_outlet", "advanced_analytics", "api_access", "priority_support"),
		},

		// ─── Codevertex Duka (General retail: shops, supermarkets, hardware) ────
		{
			id: uuid.NewSHA1(uuid.NameSpaceOID, []byte("pos:duka:STARTER")), planCode: "POS_DUKA_STARTER",
			name: "Codevertex Duka Starter (Retail)", description: "Small shops, kiosks & market stalls turning over up to ~KES 300k/month. POS, M-Pesa Till, basic inventory, shift management, standard receipts, barcode & loyalty.",
			billingCycle: "MONTHLY", price: 2500, setupFee: 5000, tierOrder: 1,
			tierLimits: map[string]any{"max_devices": 1, "max_cashiers": 2},
			features:   with("barcode_scanning", "loyalty_program", "etims_integration"),
		},
		{
			id: uuid.NewSHA1(uuid.NameSpaceOID, []byte("pos:duka:PRO")), planCode: "POS_DUKA_PRO",
			name: "Codevertex Duka Professional (Retail)", description: "Supermarkets, hardware stores, boutiques & wholesalers, ~KES 300k–2M/month. + Real-time stock, low-stock alerts, supplier lists, sales analytics, eTIMS.",
			billingCycle: "MONTHLY", price: 4500, setupFee: 10000, tierOrder: 2,
			tierLimits: map[string]any{"max_devices": 5, "max_cashiers": 10},
			features:   with("barcode_scanning", "loyalty_program", "stock_tracking", "low_stock_alerts", "supplier_portal", "purchase_orders", "multi_cashier", "etims_integration"),
		},
		{
			id: uuid.NewSHA1(uuid.NameSpaceOID, []byte("pos:duka:ENTERPRISE")), planCode: "POS_DUKA_ENTERPRISE",
			name: "Codevertex Duka Enterprise (Retail Chains)", description: "Multi-branch retail chains, ~KES 2M+/month. + Multi-branch management, consolidated reporting, advanced analytics, API access, priority support.",
			billingCycle: "MONTHLY", price: 7500, setupFee: 20000, tierOrder: 3,
			tierLimits: map[string]any{"max_devices": -1, "max_cashiers": -1},
			features:   with("barcode_scanning", "loyalty_program", "stock_tracking", "low_stock_alerts", "supplier_portal", "purchase_orders", "multi_cashier", "etims_integration", "multi_outlet", "advanced_analytics", "api_access", "offline_sync", "priority_support"),
		},
		{
			id: uuid.NewSHA1(uuid.NameSpaceOID, []byte("pos:duka:LICENSE")), planCode: "POS_DUKA_LICENSE",
			name: "Codevertex Duka — Perpetual License", description: "One-time perpetual license for the full retail feature set at a single store. Cloud sync & updates via annual support.",
			billingCycle: "ONE_TIME", price: 150000, tierOrder: 11,
			tierLimits: map[string]any{"max_devices": -1, "max_cashiers": -1},
			features:   with("barcode_scanning", "loyalty_program", "stock_tracking", "low_stock_alerts", "supplier_portal", "purchase_orders", "multi_cashier", "etims_integration", "multi_outlet", "advanced_analytics", "api_access", "offline_sync", "priority_support"),
		},

		// ─── Codevertex Dawa (Pharmacy: chemists, pharmacies, dispensaries) ─────
		{
			id: uuid.NewSHA1(uuid.NameSpaceOID, []byte("pos:dawa:STARTER")), planCode: "POS_DAWA_STARTER",
			name: "Codevertex Dawa Starter (Pharmacy)", description: "Small chemists & agrovets turning over up to ~KES 300k/month. POS, M-Pesa Till, basic inventory, shift management, receipts, eTIMS, batch/expiry & prescriptions.",
			billingCycle: "MONTHLY", price: 1500, setupFee: 6000, tierOrder: 1,
			tierLimits: map[string]any{"max_devices": 1, "max_cashiers": 2},
			features:   with("etims_integration", "batch_expiry_tracking", "prescription_management", "low_stock_alerts"),
		},
		{
			id: uuid.NewSHA1(uuid.NameSpaceOID, []byte("pos:dawa:PRO")), planCode: "POS_DAWA_PRO",
			name: "Codevertex Dawa Professional (Pharmacy)", description: "Full pharmacies & hospital dispensaries, ~KES 300k–2M/month. + Patient history, prescription attachment, PPB audit reports, insurance claims (NHIF/private).",
			billingCycle: "MONTHLY", price: 3000, setupFee: 12000, tierOrder: 2,
			tierLimits: map[string]any{"max_devices": 5, "max_cashiers": 10},
			features:   with("etims_integration", "batch_expiry_tracking", "prescription_management", "low_stock_alerts", "patient_history", "insurance_claims", "stock_tracking", "supplier_portal", "purchase_orders", "multi_cashier", "barcode_scanning"),
		},
		{
			id: uuid.NewSHA1(uuid.NameSpaceOID, []byte("pos:dawa:ENTERPRISE")), planCode: "POS_DAWA_ENTERPRISE",
			name: "Codevertex Dawa Enterprise (Pharmacy Chains)", description: "Pharmacy chains & wholesale distributors, ~KES 2M+/month. + Multi-branch management, consolidated reporting, advanced analytics, API access, priority support, label printing.",
			billingCycle: "MONTHLY", price: 6000, setupFee: 22000, tierOrder: 3,
			tierLimits: map[string]any{"max_devices": -1, "max_cashiers": -1},
			features:   with("etims_integration", "batch_expiry_tracking", "prescription_management", "low_stock_alerts", "patient_history", "insurance_claims", "stock_tracking", "supplier_portal", "purchase_orders", "multi_cashier", "barcode_scanning", "multi_outlet", "advanced_analytics", "api_access", "offline_sync", "label_printing", "priority_support"),
		},
		{
			id: uuid.NewSHA1(uuid.NameSpaceOID, []byte("pos:dawa:LICENSE")), planCode: "POS_DAWA_LICENSE",
			name: "Codevertex Dawa — Perpetual License", description: "One-time perpetual license for the full pharmacy feature set at a single outlet. Cloud sync & updates via annual support.",
			billingCycle: "ONE_TIME", price: 165000, tierOrder: 11,
			tierLimits: map[string]any{"max_devices": -1, "max_cashiers": -1},
			features:   with("etims_integration", "batch_expiry_tracking", "prescription_management", "low_stock_alerts", "patient_history", "insurance_claims", "stock_tracking", "supplier_portal", "purchase_orders", "multi_cashier", "barcode_scanning", "multi_outlet", "advanced_analytics", "api_access", "offline_sync", "label_printing", "priority_support"),
		},
	}

	for _, p := range plans {
		existing, err := tx.SubscriptionPlan.Get(ctx, p.id)
		if err != nil && !ent.IsNotFound(err) {
			return fmt.Errorf("lookup pos product-line plan %s: %w", p.planCode, err)
		}
		if existing != nil {
			_, err = tx.SubscriptionPlan.UpdateOneID(p.id).
				SetPlanCode(p.planCode).SetName(p.name).SetDescription(p.description).
				SetBillingCycle(p.billingCycle).SetPlanType(subscriptionplan.PlanTypeSTANDALONE_SERVICE).
				SetBasePrice(p.price).SetSetupFee(p.setupFee).SetCurrency("KES").SetIsActive(true).SetIsPublic(true).
				SetTierOrder(p.tierOrder).SetFreeTrialDays(14).
				SetTierLimitsJSON(p.tierLimits).SetServiceTag(serviceTag).SetUpdatedAt(now).Save(ctx)
		} else {
			_, err = tx.SubscriptionPlan.Create().
				SetID(p.id).SetPlanCode(p.planCode).SetName(p.name).SetDescription(p.description).
				SetBillingCycle(p.billingCycle).SetPlanType(subscriptionplan.PlanTypeSTANDALONE_SERVICE).
				SetBasePrice(p.price).SetSetupFee(p.setupFee).SetCurrency("KES").SetIsActive(true).SetIsPublic(true).
				SetTierOrder(p.tierOrder).SetFreeTrialDays(14).
				SetTierLimitsJSON(p.tierLimits).SetServiceTag(serviceTag).
				SetCreatedAt(now).SetUpdatedAt(now).Save(ctx)
		}
		if err != nil {
			return fmt.Errorf("upsert pos product-line plan %s: %w", p.planCode, err)
		}
		if err := seedPlanFeatures(ctx, tx, p.id, p.features); err != nil {
			return fmt.Errorf("seed features for pos product-line plan %s: %w", p.planCode, err)
		}
		log.Printf("  pos product-line plan: %s (%s, KES %.0f)", p.name, p.billingCycle, p.price)
	}
	return nil
}
