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

// seedBundlePlans seeds dedicated plan codes for the POS Suite and Complete bundles.
//
// POS Suite: POS terminal + Treasury payments (in-store commerce).
//
// Complete Bundle: Every service in the Codevertex ecosystem bundled into 3 tiers.
// Features are the UNION of all Starter/Growth/Pro service plans at the matching tier
// — a Complete Starter tenant gets exactly the features available in each service's
// Starter plan, Complete Growth gets Growth-level features, etc.
//
// Services covered by Complete plans:
//   - Ordering (online ordering, riders, outlets, promotions)
//   - POS (terminal, KDS, table management, shift reports)
//   - Logistics (rider management, live tracking, route optimisation)
//   - Inventory (stock tracking, warehouses, purchase orders)
//   - Treasury (wallets, payments, payouts, invoicing)
//   - MarketFlow CRM (contacts, leads, campaigns, AI chat, funnels)
//   - ERP (HR, payroll, procurement, leave management) — Growth+
func seedBundlePlans(ctx context.Context, tx *ent.Tx) error {
	now := time.Now()

	type planDef struct {
		id           uuid.UUID
		planCode     string
		name         string
		description  string
		billingCycle string
		price        float64
		tierOrder    int
		serviceTag   string
		tierLimits   map[string]any
		features     []string
	}

	plans := []planDef{
		// ── POS Suite — Monthly ──────────────────────────────────────────────
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("plan:POS_SUITE_STARTER")),
			planCode:     "POS_SUITE_STARTER",
			name:         "POS Suite Starter",
			description:  "In-store POS with Mpesa, receipt printing, table management, and basic payment processing for a single outlet.",
			billingCycle: "MONTHLY",
			price:        2000.0,
			tierOrder:    1,
			serviceTag:   "pos",
			tierLimits: map[string]any{
				"max_devices":                 1,
				"max_cashiers":                2,
				"max_tables":                  20,
				"max_transactions_per_month":  2000,
				"max_admins":                  1,
				"max_staff":                   5,
				"max_outlets":                 1,
				"email_notifications_per_day": 50,
				"sms_notifications_per_day":   50,
				"max_wallets":                 2,
				"max_payment_links":           10,
				"max_currencies":              1,
			},
			features: []string{
				"pos_terminal", "mpesa_pos", "order_management", "receipt_printing",
				"daily_reports", "table_management", "shift_reports", "kds",
				"offline_sync", "basic_analytics",
				"wallet_management", "payment_collection", "mpesa_integration",
				"paystack_integration", "payment_links", "transaction_reports",
				"loyalty_program", "wallet", "basic_treasury_access",
			},
		},
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("plan:POS_SUITE_GROWTH")),
			planCode:     "POS_SUITE_GROWTH",
			name:         "POS Suite Growth",
			description:  "Multi-cashier POS with KDS, advanced analytics, multi-outlet support, and advanced payment processing.",
			billingCycle: "MONTHLY",
			price:        4500.0,
			tierOrder:    2,
			serviceTag:   "pos",
			tierLimits: map[string]any{
				"max_devices":                 5,
				"max_cashiers":                10,
				"max_tables":                  50,
				"max_transactions_per_month":  10000,
				"max_admins":                  3,
				"max_staff":                   20,
				"max_outlets":                 3,
				"email_notifications_per_day": 200,
				"sms_notifications_per_day":   100,
				"max_wallets":                 5,
				"max_payment_links":           -1,
				"max_currencies":              3,
				"max_bulk_payout_rows":        500,
			},
			features: []string{
				"pos_terminal", "mpesa_pos", "order_management", "receipt_printing",
				"daily_reports", "table_management", "shift_reports", "kds",
				"offline_sync", "basic_analytics", "advanced_analytics",
				"multi_cashier", "multi_outlet", "pos_integration",
				"wallet_management", "payment_collection", "mpesa_integration",
				"paystack_integration", "payment_links", "transaction_reports",
				"multi_currency", "bulk_payouts", "escrow_management",
				"payout_schedules", "reconciliation",
				"loyalty_program", "wallet", "basic_treasury_access",
			},
		},
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("plan:POS_SUITE_PROFESSIONAL")),
			planCode:     "POS_SUITE_PROFESSIONAL",
			name:         "POS Suite Professional",
			description:  "Enterprise POS with hotel module, API access, unlimited devices, and full treasury integration for chains.",
			billingCycle: "MONTHLY",
			price:        8000.0,
			tierOrder:    3,
			serviceTag:   "pos",
			tierLimits: map[string]any{
				"max_devices":                 -1,
				"max_cashiers":                -1,
				"max_tables":                  -1,
				"max_transactions_per_month":  -1,
				"max_admins":                  -1,
				"max_staff":                   -1,
				"max_outlets":                 -1,
				"email_notifications_per_day": -1,
				"sms_notifications_per_day":   -1,
				"max_wallets":                 -1,
				"max_payment_links":           -1,
				"max_currencies":              -1,
				"max_bulk_payout_rows":        -1,
			},
			features: []string{
				"pos_terminal", "mpesa_pos", "order_management", "receipt_printing",
				"daily_reports", "table_management", "shift_reports", "kds",
				"offline_sync", "basic_analytics", "advanced_analytics",
				"multi_cashier", "multi_outlet", "pos_integration", "hotel_module",
				"api_webhooks", "white_labeling", "priority_support", "premium_support",
				"wallet_management", "payment_collection", "mpesa_integration",
				"paystack_integration", "payment_links", "transaction_reports",
				"multi_currency", "bulk_payouts", "escrow_management",
				"payout_schedules", "reconciliation", "api_access", "webhooks",
				"custom_integrations", "audit_trail",
				"loyalty_program", "wallet", "basic_treasury_access",
			},
		},

		// ── POS Suite — Annual ───────────────────────────────────────────────
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("plan:POS_SUITE_STARTER_YEARLY")),
			planCode:     "POS_SUITE_STARTER_YEARLY",
			name:         "POS Suite Starter — Annual",
			description:  "In-store POS for a single outlet. Save with annual billing.",
			billingCycle: "ANNUAL",
			price:        22000.0,
			tierOrder:    1,
			serviceTag:   "pos",
			tierLimits: map[string]any{
				"max_devices": 1, "max_cashiers": 2, "max_tables": 20,
				"max_transactions_per_month": 2000, "max_admins": 1, "max_staff": 5,
				"max_outlets": 1, "email_notifications_per_day": 50,
				"sms_notifications_per_day": 50, "max_wallets": 2,
				"max_payment_links": 10, "max_currencies": 1,
			},
			features: []string{
				"pos_terminal", "mpesa_pos", "order_management", "receipt_printing",
				"daily_reports", "table_management", "shift_reports", "kds", "offline_sync",
				"basic_analytics", "wallet_management", "payment_collection",
				"mpesa_integration", "paystack_integration", "payment_links",
				"transaction_reports", "loyalty_program", "wallet", "basic_treasury_access",
			},
		},
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("plan:POS_SUITE_GROWTH_YEARLY")),
			planCode:     "POS_SUITE_GROWTH_YEARLY",
			name:         "POS Suite Growth — Annual",
			description:  "Multi-cashier POS for growing outlets. Save with annual billing.",
			billingCycle: "ANNUAL",
			price:        49500.0,
			tierOrder:    2,
			serviceTag:   "pos",
			tierLimits: map[string]any{
				"max_devices": 5, "max_cashiers": 10, "max_tables": 50,
				"max_transactions_per_month": 10000, "max_admins": 3, "max_staff": 20,
				"max_outlets": 3, "email_notifications_per_day": 200,
				"sms_notifications_per_day": 100, "max_wallets": 5,
				"max_payment_links": -1, "max_currencies": 3, "max_bulk_payout_rows": 500,
			},
			features: []string{
				"pos_terminal", "mpesa_pos", "order_management", "receipt_printing",
				"daily_reports", "table_management", "shift_reports", "kds", "offline_sync",
				"basic_analytics", "advanced_analytics", "multi_cashier", "multi_outlet",
				"pos_integration", "wallet_management", "payment_collection",
				"mpesa_integration", "paystack_integration", "payment_links",
				"transaction_reports", "multi_currency", "bulk_payouts", "escrow_management",
				"payout_schedules", "reconciliation", "loyalty_program", "wallet", "basic_treasury_access",
			},
		},
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("plan:POS_SUITE_PROFESSIONAL_YEARLY")),
			planCode:     "POS_SUITE_PROFESSIONAL_YEARLY",
			name:         "POS Suite Professional — Annual",
			description:  "Enterprise POS with hotel module and API access. Save with annual billing.",
			billingCycle: "ANNUAL",
			price:        88000.0,
			tierOrder:    3,
			serviceTag:   "pos",
			tierLimits: map[string]any{
				"max_devices": -1, "max_cashiers": -1, "max_tables": -1,
				"max_transactions_per_month": -1, "max_admins": -1, "max_staff": -1,
				"max_outlets": -1, "email_notifications_per_day": -1,
				"sms_notifications_per_day": -1, "max_wallets": -1,
				"max_payment_links": -1, "max_currencies": -1, "max_bulk_payout_rows": -1,
			},
			features: []string{
				"pos_terminal", "mpesa_pos", "order_management", "receipt_printing",
				"daily_reports", "table_management", "shift_reports", "kds", "offline_sync",
				"basic_analytics", "advanced_analytics", "multi_cashier", "multi_outlet",
				"pos_integration", "hotel_module", "api_webhooks", "white_labeling",
				"priority_support", "premium_support", "wallet_management", "payment_collection",
				"mpesa_integration", "paystack_integration", "payment_links", "transaction_reports",
				"multi_currency", "bulk_payouts", "escrow_management", "payout_schedules",
				"reconciliation", "api_access", "webhooks", "custom_integrations", "audit_trail",
				"loyalty_program", "wallet", "basic_treasury_access",
			},
		},

		// ── Complete Bundle — Monthly ────────────────────────────────────────
		// COMPLETE_STARTER = UNION of all *_STARTER service plans at starter limits.
		// Includes: Ordering Starter + POS Device-1 + Logistics Starter +
		//           Inventory Starter + Treasury Starter + MarketFlow Starter
		//
		// Limit corrections vs per-service starter plans:
		//   - api_calls_per_month: 30K (covers 6 services calling APIs)
		//   - sms_notifications_per_day: 150 (300 orders × 0.5 SMS rate)
		//   - webhook_calls_per_day: 500 (300 orders × 1-2 payment/status webhooks)
		//   - live_tracking_requests_per_day: 2000 (300 deliveries × 6 customer tracking polls)
		//   - max_transactions_per_month: 5000 (300 orders/day × 30 days × 50% digital)
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("plan:COMPLETE_STARTER")),
			planCode:     "COMPLETE_STARTER",
			name:         "Complete Starter",
			description:  "Every Codevertex service at starter level: online ordering, POS, logistics, inventory, payments, CRM, and storefront for a single outlet.",
			billingCycle: "MONTHLY",
			price:        4000.0,
			tierOrder:    1,
			serviceTag:   "ordering",
			tierLimits: map[string]any{
				// Ordering
				"max_orders_per_day":             300,
				"max_admins":                     2,
				"max_staff":                      10,
				"max_outlets":                    1,
				"api_calls_per_month":            30000,
				"webhook_calls_per_day":          500,
				"email_notifications_per_day":    200,
				"sms_notifications_per_day":      150,
				// Logistics
				"max_riders":                     5,
				"live_tracking_requests_per_day": 2000,
				"routing_requests_per_day":       100,
				"overage_rider_price_per_month":  250.0,
				"overage_orders_price_per_100_month": 375.0,
				// POS
				"max_devices":  1,
				"max_cashiers": 2,
				"max_tables":   20,
				// Inventory
				"inventory_max_sku":        500,
				"inventory_max_warehouses": 1,
				"max_suppliers":            10,
				// Treasury
				"max_transactions_per_month": 5000,
				"max_wallets":               3,
				"max_payment_links":         20,
				"max_currencies":            1,
				// MarketFlow CRM
				"max_contacts":       1000,
				"max_leads":          500,
				"max_campaigns":      5,
				"max_sequences":      3,
				"max_funnels":        2,
				"ai_credits_monthly": 5,
				"max_integrations":   2,
			},
			features: []string{
				// Ordering Starter
				"online_ordering", "rider_app", "admin_dashboard", "mpesa_integration",
				"sms_notifications", "push_notifications", "basic_analytics",
				"custom_domain", "loyalty_program", "wallet", "delivery_zones",
				"pos_terminal", "table_management", "shift_reports", "offline_sync",
				// POS Device-1
				"mpesa_pos", "order_management", "receipt_printing", "daily_reports", "kds",
				// Logistics Starter
				"rider_management", "delivery_assignment", "live_tracking", "basic_dispatch",
				// Inventory Starter
				"stock_tracking", "low_stock_alerts", "purchase_orders", "basic_reports",
				// Treasury Starter
				"wallet_management", "payment_collection", "paystack_integration",
				"payment_links", "transaction_reports", "invoice_generation",
				// MarketFlow CRM Starter
				"contact_management", "lead_management", "basic_campaigns",
				"landing_pages", "email_sequences", "ai_chat_agent", "shortlinks",
				// Gateway feature codes (checked by microservices)
				"basic_inventory_access", "basic_logistics_access", "basic_treasury_access",
			},
		},
		// COMPLETE_GROWTH = COMPLETE_STARTER features + all *_GROWTH additions
		// Includes MarketFlow Growth (funnels, automation, ticketing, lead scoring) +
		//          ERP Starter (HR, payroll, procurement, leave)
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("plan:COMPLETE_GROWTH")),
			planCode:     "COMPLETE_GROWTH",
			name:         "Complete Growth",
			description:  "Every Codevertex service at growth level: multi-outlet ordering, advanced analytics, route optimisation, multi-warehouse, multi-currency, CRM automation, ticketing, and HR management.",
			billingCycle: "MONTHLY",
			price:        9500.0,
			tierOrder:    2,
			serviceTag:   "ordering",
			tierLimits: map[string]any{
				// Ordering
				"max_orders_per_day":             1000,
				"max_admins":                     3,
				"max_staff":                      30,
				"max_outlets":                    3,
				"api_calls_per_month":            100000,
				"webhook_calls_per_day":          2000,
				"email_notifications_per_day":    1000,
				"sms_notifications_per_day":      500,
				// Logistics
				"max_riders":                     15,
				"live_tracking_requests_per_day": 10000,
				"routing_requests_per_day":       500,
				"overage_rider_price_per_month":  250.0,
				"overage_orders_price_per_100_month": 375.0,
				// POS
				"max_devices":  5,
				"max_cashiers": 10,
				"max_tables":   50,
				// Inventory — aligned with INVENTORY_GROWTH standalone
				"inventory_max_sku":        5000,
				"inventory_max_warehouses": 5,
				"max_suppliers":            50,
				// Treasury
				"max_transactions_per_month": 20000,
				"max_wallets":               8,
				"max_payment_links":         -1,
				"max_currencies":            3,
				"max_bulk_payout_rows":      500,
				// MarketFlow CRM Growth
				"max_contacts":       10000,
				"max_leads":          5000,
				"max_campaigns":      -1,
				"max_sequences":      -1,
				"max_funnels":        20,
				"ai_credits_monthly": 100,
				"max_integrations":   10,
				"max_tickets":        -1,
				"max_agents":         5,
				// ERP (HR starter-level)
				"max_employees":   50,
				"max_departments": 5,
			},
			features: []string{
				// Ordering Growth (all Starter features +)
				"online_ordering", "rider_app", "admin_dashboard", "mpesa_integration",
				"sms_notifications", "push_notifications", "basic_analytics", "advanced_analytics",
				"custom_domain", "loyalty_program", "wallet", "delivery_zones",
				"multi_outlet", "promo_codes", "group_ordering", "paystack_gateway",
				"scheduled_delivery",
				"pos_terminal", "pos_integration", "table_management", "shift_reports",
				"kds", "offline_sync",
				// POS Device-5 additions
				"mpesa_pos", "order_management", "receipt_printing", "daily_reports",
				"multi_cashier",
				// Logistics Growth additions
				"rider_management", "delivery_assignment", "live_tracking", "basic_dispatch",
				"route_optimisation", "driver_analytics", "performance_reports",
				// Inventory Growth additions
				"stock_tracking", "low_stock_alerts", "purchase_orders", "basic_reports",
				"multi_warehouse", "batch_expiry_tracking", "supplier_portal",
				// Treasury Growth additions
				"wallet_management", "payment_collection", "paystack_integration",
				"payment_links", "transaction_reports", "invoice_generation",
				"multi_currency", "bulk_payouts", "escrow_management",
				"payout_schedules", "reconciliation",
				"ar_tracking", "ap_tracking",
				// MarketFlow CRM Growth
				"contact_management", "lead_management", "unlimited_campaigns",
				"landing_pages", "email_sequences", "ai_chat_agent",
				"lead_scoring", "funnel_builder", "automation_workflows", "webhooks",
				"ticketing", "helpdesk", "sla_policies",
				"knowledge_base", "testimonials", "shortlinks",
				// ERP — HR/payroll for small teams
				"hr_management", "payroll", "basic_procurement", "leave_management",
				// Gateway codes
				"basic_inventory_access", "basic_logistics_access", "basic_treasury_access",
			},
		},
		// COMPLETE_PROFESSIONAL = COMPLETE_GROWTH features + all *_PROFESSIONAL additions
		// All operational limits are unlimited; api_calls capped at 500K/month.
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("plan:COMPLETE_PROFESSIONAL")),
			planCode:     "COMPLETE_PROFESSIONAL",
			name:         "Complete Professional",
			description:  "Every Codevertex service at enterprise level: unlimited outlets, hotel module, API access, barcode scanning, audit trail, CRM automation, full ERP, and priority support.",
			billingCycle: "MONTHLY",
			price:        18000.0,
			tierOrder:    3,
			serviceTag:   "ordering",
			tierLimits: map[string]any{
				// Ordering — unlimited for professional
				"max_orders_per_day":             -1,
				"max_admins":                     -1,
				"max_staff":                      -1,
				"max_outlets":                    -1,
				"api_calls_per_month":            500000,
				"webhook_calls_per_day":          -1,
				"email_notifications_per_day":    -1,
				"sms_notifications_per_day":      -1,
				// Logistics — unlimited
				"max_riders":                     -1,
				"live_tracking_requests_per_day": -1,
				"routing_requests_per_day":       -1,
				"overage_rider_price_per_month":  250.0,
				"overage_orders_price_per_100_month": 375.0,
				// POS — unlimited
				"max_devices":  -1,
				"max_cashiers": -1,
				"max_tables":   -1,
				// Inventory — unlimited
				"inventory_max_sku":        -1,
				"inventory_max_warehouses": -1,
				"max_suppliers":            -1,
				// Treasury — unlimited
				"max_transactions_per_month": -1,
				"max_wallets":               -1,
				"max_payment_links":         -1,
				"max_currencies":            -1,
				"max_bulk_payout_rows":      -1,
				// MarketFlow CRM — unlimited
				"max_contacts":       -1,
				"max_leads":          -1,
				"max_campaigns":      -1,
				"max_sequences":      -1,
				"max_funnels":        -1,
				"ai_credits_monthly": -1,
				"max_integrations":   -1,
				"max_tickets":        -1,
				"max_agents":         -1,
				// ERP — unlimited
				"max_employees":   -1,
				"max_departments": -1,
			},
			features: []string{
				// Ordering Professional
				"online_ordering", "rider_app", "admin_dashboard", "mpesa_integration",
				"sms_notifications", "push_notifications", "basic_analytics", "advanced_analytics",
				"custom_domain", "loyalty_program", "wallet", "delivery_zones",
				"multi_outlet", "promo_codes", "group_ordering", "paystack_gateway",
				"scheduled_delivery",
				"pos_terminal", "pos_integration", "table_management", "shift_reports",
				"kds", "hotel_module", "offline_sync", "route_optimization",
				"api_webhooks", "white_labeling", "priority_support", "premium_support",
				// POS / full POS
				"mpesa_pos", "order_management", "receipt_printing", "daily_reports",
				"multi_cashier",
				// Logistics Professional additions
				"rider_management", "delivery_assignment", "live_tracking", "basic_dispatch",
				"route_optimisation", "driver_analytics", "performance_reports",
				"api_access", "webhooks", "custom_integrations",
				// Inventory Professional additions
				"stock_tracking", "low_stock_alerts", "purchase_orders", "basic_reports",
				"multi_warehouse", "batch_expiry_tracking", "supplier_portal",
				"barcode_scanning", "bulk_import",
				// Treasury Professional additions
				"wallet_management", "payment_collection", "paystack_integration",
				"payment_links", "transaction_reports", "invoice_generation",
				"multi_currency", "bulk_payouts", "escrow_management",
				"payout_schedules", "reconciliation", "audit_trail",
				"ar_tracking", "ap_tracking", "ledger_posting", "tax_codes",
				// MarketFlow CRM Professional (webhooks already included from logistics section)
				"contact_management", "lead_management", "unlimited_campaigns",
				"landing_pages", "email_sequences", "ai_chat_agent",
				"lead_scoring", "funnel_builder", "automation_workflows",
				"ticketing", "helpdesk", "sla_policies",
				"knowledge_base", "testimonials", "shortlinks",
				"white_label", "dedicated_account_manager",
				// ERP Professional (full suite)
				"hr_management", "payroll", "basic_procurement", "leave_management",
				"asset_management", "budgeting", "advanced_reports", "multi_department",
				"approval_workflows", "custom_workflows",
				// Gateway codes
				"basic_inventory_access", "basic_logistics_access", "basic_treasury_access",
			},
		},

		// ── Complete Bundle — Annual ─────────────────────────────────────────
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("plan:COMPLETE_STARTER_YEARLY")),
			planCode:     "COMPLETE_STARTER_YEARLY",
			name:         "Complete Starter — Annual",
			description:  "Every Codevertex service at starter level for a single outlet. Save with annual billing.",
			billingCycle: "ANNUAL",
			price:        44000.0,
			tierOrder:    1,
			serviceTag:   "ordering",
			tierLimits: map[string]any{
				"max_orders_per_day": 300, "max_admins": 2, "max_staff": 10,
				"max_outlets": 1, "api_calls_per_month": 30000, "webhook_calls_per_day": 500,
				"email_notifications_per_day": 200, "sms_notifications_per_day": 150,
				"max_riders": 5, "live_tracking_requests_per_day": 2000,
				"routing_requests_per_day": 100,
				"overage_rider_price_per_month": 250.0, "overage_orders_price_per_100_month": 375.0,
				"max_devices": 1, "max_cashiers": 2, "max_tables": 20,
				"inventory_max_sku": 500, "inventory_max_warehouses": 1, "max_suppliers": 10,
				"max_transactions_per_month": 5000, "max_wallets": 3,
				"max_payment_links": 20, "max_currencies": 1,
				"max_contacts": 1000, "max_leads": 500, "max_campaigns": 5,
				"max_sequences": 3, "max_funnels": 2, "ai_credits_monthly": 5,
				"max_integrations": 2,
			},
			features: []string{
				"online_ordering", "rider_app", "admin_dashboard", "mpesa_integration",
				"sms_notifications", "push_notifications", "basic_analytics",
				"custom_domain", "loyalty_program", "wallet", "delivery_zones",
				"pos_terminal", "table_management", "shift_reports", "offline_sync",
				"mpesa_pos", "order_management", "receipt_printing", "daily_reports", "kds",
				"rider_management", "delivery_assignment", "live_tracking", "basic_dispatch",
				"stock_tracking", "low_stock_alerts", "purchase_orders", "basic_reports",
				"wallet_management", "payment_collection", "paystack_integration",
				"payment_links", "transaction_reports", "invoice_generation",
				"contact_management", "lead_management", "basic_campaigns",
				"landing_pages", "email_sequences", "ai_chat_agent", "shortlinks",
				"basic_inventory_access", "basic_logistics_access", "basic_treasury_access",
			},
		},
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("plan:COMPLETE_GROWTH_YEARLY")),
			planCode:     "COMPLETE_GROWTH_YEARLY",
			name:         "Complete Growth — Annual",
			description:  "Every Codevertex service at growth level — multi-outlet, route optimisation, multi-currency, CRM automation, HR. Save with annual billing.",
			billingCycle: "ANNUAL",
			price:        104500.0,
			tierOrder:    2,
			serviceTag:   "ordering",
			tierLimits: map[string]any{
				"max_orders_per_day": 1000, "max_admins": 3, "max_staff": 30,
				"max_outlets": 3, "api_calls_per_month": 100000, "webhook_calls_per_day": 2000,
				"email_notifications_per_day": 1000, "sms_notifications_per_day": 500,
				"max_riders": 15, "live_tracking_requests_per_day": 10000,
				"routing_requests_per_day": 500,
				"overage_rider_price_per_month": 250.0, "overage_orders_price_per_100_month": 375.0,
				"max_devices": 5, "max_cashiers": 10, "max_tables": 50,
				"inventory_max_sku": 5000, "inventory_max_warehouses": 5, "max_suppliers": 50,
				"max_transactions_per_month": 20000, "max_wallets": 8,
				"max_payment_links": -1, "max_currencies": 3, "max_bulk_payout_rows": 500,
				"max_contacts": 10000, "max_leads": 5000, "max_campaigns": -1,
				"max_sequences": -1, "max_funnels": 20, "ai_credits_monthly": 100,
				"max_integrations": 10, "max_tickets": -1, "max_agents": 5,
				"max_employees": 50, "max_departments": 5,
			},
			features: []string{
				"online_ordering", "rider_app", "admin_dashboard", "mpesa_integration",
				"sms_notifications", "push_notifications", "basic_analytics", "advanced_analytics",
				"custom_domain", "loyalty_program", "wallet", "delivery_zones",
				"multi_outlet", "promo_codes", "group_ordering", "paystack_gateway",
				"scheduled_delivery",
				"pos_terminal", "pos_integration", "table_management", "shift_reports",
				"kds", "offline_sync",
				"mpesa_pos", "order_management", "receipt_printing", "daily_reports", "multi_cashier",
				"rider_management", "delivery_assignment", "live_tracking", "basic_dispatch",
				"route_optimisation", "driver_analytics", "performance_reports",
				"stock_tracking", "low_stock_alerts", "purchase_orders", "basic_reports",
				"multi_warehouse", "batch_expiry_tracking", "supplier_portal",
				"wallet_management", "payment_collection", "paystack_integration",
				"payment_links", "transaction_reports", "invoice_generation",
				"multi_currency", "bulk_payouts", "escrow_management",
				"payout_schedules", "reconciliation", "ar_tracking", "ap_tracking",
				"contact_management", "lead_management", "unlimited_campaigns",
				"landing_pages", "email_sequences", "ai_chat_agent",
				"lead_scoring", "funnel_builder", "automation_workflows", "webhooks",
				"ticketing", "helpdesk", "sla_policies",
				"knowledge_base", "testimonials", "shortlinks",
				"hr_management", "payroll", "basic_procurement", "leave_management",
				"basic_inventory_access", "basic_logistics_access", "basic_treasury_access",
			},
		},
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("plan:COMPLETE_PROFESSIONAL_YEARLY")),
			planCode:     "COMPLETE_PROFESSIONAL_YEARLY",
			name:         "Complete Professional — Annual",
			description:  "Every Codevertex service at enterprise level — unlimited scale, API access, hotel module, full CRM, full ERP, audit trail. Save with annual billing.",
			billingCycle: "ANNUAL",
			price:        198000.0,
			tierOrder:    3,
			serviceTag:   "ordering",
			tierLimits: map[string]any{
				"max_orders_per_day": -1, "max_admins": -1, "max_staff": -1,
				"max_outlets": -1, "api_calls_per_month": 500000, "webhook_calls_per_day": -1,
				"email_notifications_per_day": -1, "sms_notifications_per_day": -1,
				"max_riders": -1, "live_tracking_requests_per_day": -1,
				"routing_requests_per_day": -1,
				"overage_rider_price_per_month": 250.0, "overage_orders_price_per_100_month": 375.0,
				"max_devices": -1, "max_cashiers": -1, "max_tables": -1,
				"inventory_max_sku": -1, "inventory_max_warehouses": -1, "max_suppliers": -1,
				"max_transactions_per_month": -1, "max_wallets": -1,
				"max_payment_links": -1, "max_currencies": -1, "max_bulk_payout_rows": -1,
				"max_contacts": -1, "max_leads": -1, "max_campaigns": -1,
				"max_sequences": -1, "max_funnels": -1, "ai_credits_monthly": -1,
				"max_integrations": -1, "max_tickets": -1, "max_agents": -1,
				"max_employees": -1, "max_departments": -1,
			},
			features: []string{
				"online_ordering", "rider_app", "admin_dashboard", "mpesa_integration",
				"sms_notifications", "push_notifications", "basic_analytics", "advanced_analytics",
				"custom_domain", "loyalty_program", "wallet", "delivery_zones",
				"multi_outlet", "promo_codes", "group_ordering", "paystack_gateway",
				"scheduled_delivery",
				"pos_terminal", "pos_integration", "table_management", "shift_reports",
				"kds", "hotel_module", "offline_sync", "route_optimization",
				"api_webhooks", "white_labeling", "priority_support", "premium_support",
				"mpesa_pos", "order_management", "receipt_printing", "daily_reports", "multi_cashier",
				"rider_management", "delivery_assignment", "live_tracking", "basic_dispatch",
				"route_optimisation", "driver_analytics", "performance_reports",
				"api_access", "webhooks", "custom_integrations",
				"stock_tracking", "low_stock_alerts", "purchase_orders", "basic_reports",
				"multi_warehouse", "batch_expiry_tracking", "supplier_portal",
				"barcode_scanning", "bulk_import",
				"wallet_management", "payment_collection", "paystack_integration",
				"payment_links", "transaction_reports", "invoice_generation",
				"multi_currency", "bulk_payouts", "escrow_management",
				"payout_schedules", "reconciliation", "audit_trail",
				"ar_tracking", "ap_tracking", "ledger_posting", "tax_codes",
				"contact_management", "lead_management", "unlimited_campaigns",
				"landing_pages", "email_sequences", "ai_chat_agent",
				"lead_scoring", "funnel_builder", "automation_workflows",
				"ticketing", "helpdesk", "sla_policies",
				"knowledge_base", "testimonials", "shortlinks",
				"white_label", "dedicated_account_manager",
				"hr_management", "payroll", "basic_procurement", "leave_management",
				"asset_management", "budgeting", "advanced_reports", "multi_department",
				"approval_workflows", "custom_workflows",
				"basic_inventory_access", "basic_logistics_access", "basic_treasury_access",
			},
		},
	}

	for _, p := range plans {
		existing, err := tx.SubscriptionPlan.Get(ctx, p.id)
		if err != nil && !ent.IsNotFound(err) {
			return fmt.Errorf("lookup plan %s: %w", p.planCode, err)
		}

		if existing != nil {
			_, err = tx.SubscriptionPlan.UpdateOneID(p.id).
				SetPlanCode(p.planCode).
				SetName(p.name).
				SetDescription(p.description).
				SetBillingCycle(p.billingCycle).
				SetPlanType(subscriptionplan.PlanTypeTIERED).
				SetBasePrice(p.price).
				SetCurrency("KES").
				SetIsActive(true).
				SetIsPublic(true).
				SetTierOrder(p.tierOrder).
				SetFreeTrialDays(0).
				SetTierLimitsJSON(p.tierLimits).
				SetServiceTag(p.serviceTag).
				SetUpdatedAt(now).
				Save(ctx)
			if err != nil {
				return fmt.Errorf("update plan %s: %w", p.planCode, err)
			}
		} else {
			_, err = tx.SubscriptionPlan.Create().
				SetID(p.id).
				SetPlanCode(p.planCode).
				SetName(p.name).
				SetDescription(p.description).
				SetBillingCycle(p.billingCycle).
				SetPlanType(subscriptionplan.PlanTypeTIERED).
				SetBasePrice(p.price).
				SetCurrency("KES").
				SetIsActive(true).
				SetIsPublic(true).
				SetTierOrder(p.tierOrder).
				SetFreeTrialDays(0).
				SetTierLimitsJSON(p.tierLimits).
				SetServiceTag(p.serviceTag).
				SetCreatedAt(now).
				SetUpdatedAt(now).
				Save(ctx)
			if err != nil {
				return fmt.Errorf("create plan %s: %w", p.planCode, err)
			}
		}

		if err := seedPlanFeatures(ctx, tx, p.id, p.features); err != nil {
			return fmt.Errorf("seed features for plan %s: %w", p.planCode, err)
		}

		log.Printf("  bundle plan: %s (%s, KES %.0f)", p.name, p.billingCycle, p.price)
	}

	return nil
}
