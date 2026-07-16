package main

// powerSuiteFeatures is the generic all-services PowerSuite feature union at tier N
// (1=Starter, 2=Growth, 3=Professional).
//
// The generic POWERSUITE_* / POS_SUITE_* plan rows this file used to seed were SUPERSEDED
// by the per-use-case PowerSuite families (plans_powersuite_usecase.go, per
// docs/subscription-plans/) and are hard-deleted by migrateUseCasePowerSuite. These
// builders remain as the generic union: plans_erp.go's ERP-suite ONE_TIME licenses union
// them with the ERP set, and powerSuiteLimits is the base layer for useCaseSuiteLimits.
func powerSuiteFeatures(tier int) []string {
	switch tier {
	case 1:
		return []string{
			// Ordering Starter — Paystack is the default gateway on all plans;
			// direct M-Pesa Daraja (mpesa_integration) is Growth+ only.
			"online_ordering", "rider_app", "admin_dashboard",
			"paystack_gateway", "paystack_integration",
			"sms_notifications", "push_notifications", "basic_analytics",
			"custom_domain", "loyalty_program", "wallet", "delivery_zones",
			"pos_terminal", "table_management", "shift_reports", "offline_sync",
			// POS Device-1 (mpesa_pos = Lipa Na M-Pesa POS till, included at Starter)
			"mpesa_pos", "order_management", "receipt_printing", "daily_reports", "kds",
			// Logistics Starter
			"rider_management", "delivery_assignment", "live_tracking", "basic_dispatch",
			// Inventory Starter
			"stock_tracking", "low_stock_alerts", "purchase_orders", "basic_reports",
			"bulk_import",
			// Treasury Starter
			"wallet_management", "payment_collection",
			"payment_links", "transaction_reports", "invoice_generation",
			"basic_reconciliation", "customer_management",
			// MarketFlow CRM Starter
			"contact_management", "lead_management", "basic_campaigns",
			"landing_pages", "email_sequences", "ai_chat_agent", "shortlinks",
			// Gateway feature codes (checked by microservices)
			"basic_inventory_access", "basic_logistics_access", "basic_treasury_access",
		}
	case 2:
		return []string{
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
			"bulk_import", "multi_warehouse", "inventory_multiple_images", "batch_expiry_tracking", "supplier_portal",
			// Treasury Growth additions
			"wallet_management", "payment_collection", "paystack_integration",
			"payment_links", "transaction_reports", "invoice_generation",
			"basic_reconciliation", "customer_management", "vendor_management",
			"multi_currency", "bulk_payouts", "escrow_management",
			"payout_schedules", "reconciliation",
			"ar_tracking", "ap_tracking", "ledger_posting", "tax_codes",
			"etims_integration",
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
		}
	default: // tier 3 (Professional) — full access across all services
		return []string{
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
			"multi_warehouse", "inventory_multiple_images", "batch_expiry_tracking", "supplier_portal",
			"barcode_scanning", "bulk_import",
			// Treasury Professional additions
			"wallet_management", "payment_collection", "paystack_integration",
			"payment_links", "transaction_reports", "invoice_generation",
			"basic_reconciliation", "customer_management", "vendor_management",
			"multi_currency", "bulk_payouts", "escrow_management",
			"payout_schedules", "reconciliation", "audit_trail",
			"ar_tracking", "ap_tracking", "ledger_posting", "tax_codes",
			"etims_integration", "equity_payouts",
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
			"approval_workflows", "custom_workflows", "staff_fund_from_salary",
			// Gateway codes
			"basic_inventory_access", "basic_logistics_access", "basic_treasury_access",
		}
	}
}

// powerSuiteLimits is the single source of truth for the PowerSuite tier_limits at
// tier N (1/2/3). Returns a fresh map each call. Tier 3 mirrors the recurring
// Professional plan (all operational caps -1/unlimited; api_calls capped at 500K/mo).
func powerSuiteLimits(tier int) map[string]any {
	switch tier {
	case 1:
		return map[string]any{
			// Ordering
			"max_orders_per_day":          300,
			"max_admins":                  2,
			"max_staff":                   10,
			"max_outlets":                 1,
			"api_calls_per_month":         30000,
			"webhook_calls_per_day":       500,
			"email_notifications_per_day": 200,
			"sms_notifications_per_day":   150,
			// Logistics
			"max_riders":                               5,
			"live_tracking_requests_per_day":           2000,
			"routing_requests_per_day":                 100,
			"overage_rider_price_per_month":            250.0,
			"overage_orders_price_per_100_month":       375.0,
			"overage_transactions_price_per_100_month": 200.0,
			"overage_sms_price_per_100":                150.0,
			// POS
			"max_devices":  1,
			"max_cashiers": 2,
			"max_tables":   20,
			// Inventory
			"inventory_max_sku":             500,
			"inventory_max_warehouses":      1,
			"inventory_max_images_per_item": 1,
			"max_suppliers":                 10,
			// Treasury
			"max_transactions_per_month": 5000,
			"max_wallets":                3,
			"max_payment_links":          20,
			"max_currencies":             1,
			// MarketFlow CRM
			"max_contacts":       1000,
			"max_leads":          500,
			"max_campaigns":      5,
			"max_sequences":      3,
			"max_funnels":        2,
			"ai_credits_monthly": 5,
			"max_integrations":   2,
		}
	case 2:
		return map[string]any{
			// Ordering
			"max_orders_per_day":          1000,
			"max_admins":                  3,
			"max_staff":                   30,
			"max_outlets":                 3,
			"api_calls_per_month":         100000,
			"webhook_calls_per_day":       2000,
			"email_notifications_per_day": 1000,
			"sms_notifications_per_day":   500,
			// Logistics
			"max_riders":                               15,
			"live_tracking_requests_per_day":           10000,
			"routing_requests_per_day":                 500,
			"overage_rider_price_per_month":            250.0,
			"overage_orders_price_per_100_month":       375.0,
			"overage_transactions_price_per_100_month": 200.0,
			"overage_sms_price_per_100":                150.0,
			// POS
			"max_devices":  5,
			"max_cashiers": 10,
			"max_tables":   50,
			// Inventory — aligned with INVENTORY_GROWTH standalone
			"inventory_max_sku":             5000,
			"inventory_max_warehouses":      5,
			"inventory_max_images_per_item": 5,
			"max_suppliers":                 50,
			// Treasury
			"max_transactions_per_month": 20000,
			"max_wallets":                8,
			"max_payment_links":          -1,
			"max_currencies":             3,
			"max_bulk_payout_rows":       500,
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
		}
	default: // tier 3 (Professional)
		return map[string]any{
			// Ordering — unlimited for professional
			"max_orders_per_day":          -1,
			"max_admins":                  -1,
			"max_staff":                   -1,
			"max_outlets":                 -1,
			"api_calls_per_month":         500000,
			"webhook_calls_per_day":       -1,
			"email_notifications_per_day": -1,
			"sms_notifications_per_day":   -1,
			// Logistics — unlimited
			"max_riders":                               -1,
			"live_tracking_requests_per_day":           -1,
			"routing_requests_per_day":                 -1,
			"overage_rider_price_per_month":            250.0,
			"overage_orders_price_per_100_month":       375.0,
			"overage_transactions_price_per_100_month": 200.0,
			"overage_sms_price_per_100":                150.0,
			// POS — unlimited
			"max_devices":  -1,
			"max_cashiers": -1,
			"max_tables":   -1,
			// Inventory — unlimited
			"inventory_max_sku":             -1,
			"inventory_max_warehouses":      -1,
			"inventory_max_images_per_item": -1,
			"max_suppliers":                 -1,
			// Treasury — unlimited
			"max_transactions_per_month": -1,
			"max_wallets":                -1,
			"max_payment_links":          -1,
			"max_currencies":             -1,
			"max_bulk_payout_rows":       -1,
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
		}
	}
}
