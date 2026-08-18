package main

import (
	"context"
	"fmt"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/featuredefinition"
)

// catalogEntry is one row in the platform feature/limit catalog.
type catalogEntry struct {
	code       string
	service    string // service_tag
	category   string
	label      string
	kind       featuredefinition.Kind
	valueType  featuredefinition.ValueType
	metered    bool // is_rate_limited
	unit       string
	nats       string
	defaultLim *int
}

func feat(code, service, category, label string) catalogEntry {
	return catalogEntry{code: code, service: service, category: category, label: label, kind: featuredefinition.KindFEATURE, valueType: featuredefinition.ValueTypeBool}
}

// lim is a capacity limit (value stored in tier_limits_json, not metered via usage events).
func lim(code, service, category, label, unit string) catalogEntry {
	return catalogEntry{code: code, service: service, category: category, label: label, kind: featuredefinition.KindLIMIT, valueType: featuredefinition.ValueTypeInt, unit: unit}
}

// meteredLim is a rate-limited quota counted via usage events on a NATS subject.
func meteredLim(code, service, category, label, unit, nats string) catalogEntry {
	e := lim(code, service, category, label, unit)
	e.metered = true
	e.nats = nats
	return e
}

// featureCatalog is the single source of truth for every feature & limit exposed
// by any service that integrates with the subscriptions-api, categorized per service.
// Keep this in sync with the FEATURE_INFO/LIMIT_INFO maps in subscriptions-ui and
// the RequireFeature()/CheckLimit() keys used by each service gate.
var featureCatalog = func() []catalogEntry {
	const (
		ordering   = "ordering"
		pos        = "pos"
		inventory  = "inventory"
		treasury   = "treasury"
		logistics  = "logistics"
		erp        = "erp"
		marketflow = "marketflow"
		truload    = "truload"
		portal     = "transporter_portal"
		isp        = "isp_billing"
		projects   = "projects"
		library    = "library"
		platform   = "platform"
		hospital   = "hospital"
		etimsAPI   = "etims_api"
	)
	entries := []catalogEntry{
		// ─── Treasury / Finance ───────────────────────────────────────────────
		feat("wallet_management", treasury, "Payments & Gateways", "Digital Wallet Management"),
		feat("payment_collection", treasury, "Payments & Gateways", "Online Payment Collection"),
		feat("payment_links", treasury, "Payments & Gateways", "Shareable Payment Links"),
		feat("paystack_integration", treasury, "Payments & Gateways", "Paystack Gateway"),
		feat("mpesa_integration", treasury, "Payments & Gateways", "M-Pesa (Daraja) Integration"),
		feat("multi_currency", treasury, "Payments & Gateways", "Multi-Currency Support"),
		feat("bulk_payouts", treasury, "Payments & Gateways", "Bulk Payouts"),
		feat("escrow_management", treasury, "Payments & Gateways", "Escrow Management"),
		feat("payout_schedules", treasury, "Payments & Gateways", "Automated Payout Schedules"),
		feat("equity_payouts", treasury, "Payments & Gateways", "Equity & Royalty Payouts"),
		feat("invoice_generation", treasury, "Accounting", "Invoice & Quotation Generation"),
		feat("transaction_reports", treasury, "Accounting", "Transaction Reports"),
		feat("basic_reconciliation", treasury, "Accounting", "Basic Bank Reconciliation"),
		feat("reconciliation", treasury, "Accounting", "Full Bank Reconciliation"),
		feat("ar_tracking", treasury, "Accounting", "Accounts Receivable (AR) Tracking"),
		feat("ap_tracking", treasury, "Accounting", "Accounts Payable (AP) Tracking"),
		feat("ledger_posting", treasury, "Accounting", "Double-Entry Ledger Posting"),
		feat("tax_codes", treasury, "Accounting", "Tax Code Management (VAT/EAC)"),
		feat("etims_integration", treasury, "Accounting", "KRA eTIMS Integration"),
		// Standalone external eTIMS API product (ETIMS_API_BASIC/GROWTH/SCALE plans, service_tag
		// "etims_api") -- distinct from etims_integration above (bundled feature onboarded tenants
		// already get on their regular treasury/POS plan) and kept under its own catalog service
		// tag (not treasury) so plan<->catalog tier-limit validation lines up with the plan's own
		// service_tag, even though treasury-api is what actually enforces/reports it.
		feat("etims_api_access", etimsAPI, "Accounting", "eTIMS API Access (external)"),
		meteredLim("etims_transactions_per_month", etimsAPI, "Limits", "eTIMS API Transactions", "/ month", "treasury.etims.transaction.transmitted"),
		lim("overage_etims_price_per_100_month", etimsAPI, "Overage", "eTIMS API Overage (per 100)", "KES / month"),
		feat("customer_management", treasury, "Accounting", "Customer Ledger"),
		feat("vendor_management", treasury, "Accounting", "Vendor Management"),
		// Module-granular codes for the use-case PowerSuite tiers: Sales & Invoicing splits
		// (quotations tier 1, invoices/credit notes tier 2), accounting vouchers (tier 3),
		// the approvals module (tier 2+) and the smart tax & compliance dashboard card.
		feat("quotations", treasury, "Accounting", "Quotations & Estimates"),
		feat("credit_notes", treasury, "Accounting", "Credit Notes"),
		feat("vouchers", treasury, "Accounting", "Payment & Journal Vouchers"),
		feat("treasury_approvals", treasury, "Accounting", "Approval Workflows (Treasury)"),
		feat("smart_tax_compliance", treasury, "Accounting", "Smart Tax & Compliance Advisor"),
		lim("max_wallets", treasury, "Limits", "Digital Wallets", ""),
		lim("max_payment_links", treasury, "Limits", "Payment Links", ""),
		lim("max_currencies", treasury, "Limits", "Currencies", ""),
		lim("max_bulk_payout_rows", treasury, "Limits", "Bulk Payout Rows", ""),
		meteredLim("max_transactions_per_month", treasury, "Limits", "Payment Transactions", "/ month", "pos.transaction.created"),

		// ─── Ordering ─────────────────────────────────────────────────────────
		feat("online_ordering", ordering, "Ordering", "Online Ordering Storefront"),
		feat("rider_app", ordering, "Ordering", "Rider Mobile App"),
		feat("admin_dashboard", ordering, "Ordering", "Admin Dashboard"),
		feat("delivery_zones", ordering, "Ordering", "Delivery Zone & Fee Management"),
		feat("scheduled_delivery", ordering, "Ordering", "Scheduled / Timed Deliveries"),
		feat("promo_codes", ordering, "Ordering", "Promo Codes & Discounts"),
		feat("group_ordering", ordering, "Ordering", "Group & Collaborative Orders"),
		feat("loyalty_program", ordering, "Ordering", "Loyalty Points Program"),
		feat("wallet", ordering, "Ordering", "Customer Wallet Balance"),
		feat("custom_domain", ordering, "Ordering", "Custom Domain"),
		feat("multi_outlet", ordering, "Ordering", "Multi-Outlet / Branch Management"),
		feat("pos_integration", ordering, "Ordering", "POS ↔ Online Order Integration"),
		feat("hotel_module", ordering, "Ordering", "Hotel & Hospitality Module"),
		feat("route_optimization", ordering, "Ordering", "Route Optimization"),
		feat("api_webhooks", ordering, "Ordering", "API & Outbound Webhook Events"),
		feat("white_labeling", ordering, "Ordering", "White-Label Storefront"),
		feat("basic_inventory_access", ordering, "Cross-Service Access", "Basic Inventory Access"),
		feat("basic_logistics_access", ordering, "Cross-Service Access", "Basic Logistics Access"),
		feat("basic_treasury_access", ordering, "Cross-Service Access", "Basic Treasury Access"),
		lim("max_admins", ordering, "Limits", "Admins", ""),
		lim("max_staff", ordering, "Limits", "Staff Members", ""),
		lim("max_outlets", ordering, "Limits", "Outlets / Branches", ""),
		meteredLim("max_orders_per_day", ordering, "Limits", "Max Orders", "/ day", "ordering.order.created"),
		meteredLim("webhook_calls_per_day", ordering, "Limits", "Webhook Calls", "/ day", "ordering.webhook.dispatched"),
		meteredLim("email_notifications_per_day", ordering, "Limits", "Emails", "/ day", "notifications.email.sent"),
		meteredLim("sms_notifications_per_day", ordering, "Limits", "SMS", "/ day", "notifications.sms.sent"),
		lim("overage_rider_price_per_month", ordering, "Overage", "Extra Rider Overage", "KES / rider / month"),
		lim("overage_orders_price_per_100_month", ordering, "Overage", "Orders Overage (per 100)", "KES / month"),
		// Bundle (POWERSUITE_*) overage pricing — stored in tier_limits_json alongside caps.
		lim("overage_transactions_price_per_100_month", treasury, "Overage", "Transactions Overage (per 100)", "KES / month"),
		lim("overage_sms_price_per_100", platform, "Overage", "SMS Overage (per 100)", "KES / 100 SMS"),

		// ─── POS ──────────────────────────────────────────────────────────────
		feat("pos_terminal", pos, "POS", "POS Terminal Software"),
		feat("order_management", pos, "POS", "Order Management"),
		feat("receipt_printing", pos, "POS", "Receipt Printing"),
		feat("daily_reports", pos, "POS", "Daily Reports & Till Closure"),
		feat("shift_reports", pos, "POS", "Shift Reports"),
		feat("table_management", pos, "POS", "Table & Floor Management"),
		feat("kds", pos, "POS", "Kitchen Display System (KDS)"),
		feat("offline_sync", pos, "POS", "Offline Mode & Auto-Sync"),
		feat("multi_cashier", pos, "POS", "Multi-Cashier Support"),
		feat("mpesa_pos", pos, "POS", "M-Pesa POS Payments"),
		feat("happy_hour", pos, "POS", "Happy Hour Pricing"),
		feat("conference_events", pos, "POS", "Conference & Events Module"),
		// Bookable-space management (co-working desks, meeting/conference rooms sold from the
		// till) — lighter-weight than hotel_module; granted Pro+ on hospitality plans.
		feat("facility_booking", pos, "POS", "Facility & Space Booking"),
		// Retail till features already gated by pos-ui nav (subFeature) and pos-api — were
		// previously uncatalogued (fail-open in the tier-aware UI gate).
		feat("layaway", pos, "POS", "Layaway / Deposit Sales"),
		feat("commissions", pos, "POS", "Staff Sales Commissions"),
		// Storefront marketing banner (Discounts → "Show on storefront"): surfaces a promotion
		// as a banner on the customer-facing ordering storefront. Pro/Gold PowerSuite only —
		// see plans_powersuite_usecase.go's tier>=2 blocks.
		feat("storefront_banner", pos, "POS", "Storefront Promotions Banner"),
		// Pharmacy (Codevertex Dawa) — clinical & compliance features served by pos-api.
		feat("prescription_management", pos, "Pharmacy", "Prescription Management & Dispensing"),
		feat("patient_history", pos, "Pharmacy", "Patient History & Records"),
		feat("insurance_claims", pos, "Pharmacy", "Insurance Claims (NHIF / Private)"),
		feat("label_printing", pos, "Pharmacy", "Dispensing Label Printing"),
		lim("max_devices", pos, "Limits", "POS Devices", ""),
		lim("max_cashiers", pos, "Limits", "Cashiers", ""),
		lim("max_tables", pos, "Limits", "Tables", ""),
		lim("max_rooms", pos, "Limits", "Hotel Rooms", ""),
		lim("max_conference_events", pos, "Limits", "Conference Events", ""),

		// ─── Inventory ────────────────────────────────────────────────────────
		feat("stock_tracking", inventory, "Inventory", "Real-Time Stock Tracking"),
		feat("low_stock_alerts", inventory, "Inventory", "Low-Stock Alerts"),
		feat("purchase_orders", inventory, "Inventory", "Purchase Orders & GRN"),
		feat("basic_reports", inventory, "Inventory", "Basic Inventory Reports"),
		feat("multi_warehouse", inventory, "Inventory", "Multi-Warehouse Management"),
		feat("batch_expiry_tracking", inventory, "Inventory", "Batch & Expiry Tracking (FIFO)"),
		feat("supplier_portal", inventory, "Inventory", "Supplier Portal"),
		feat("barcode_scanning", inventory, "Inventory", "Barcode / QR Scanning"),
		feat("bulk_import", inventory, "Inventory", "Bulk CSV Import"),
		// Multi-image catalog: unlocks attaching more than one image per product (the first
		// image is always allowed). Paired with the inventory_max_images_per_item cap below.
		feat("inventory_multiple_images", inventory, "Inventory", "Multiple Product Images"),
		// Module-granular codes for the use-case PowerSuite tiers (docs/subscription-plans/*):
		// warehouse sub-modules, procurement stages, manufacturing, events, assets, warranties,
		// per-report gating and alert settings toggles.
		feat("stock_take", inventory, "Inventory", "Stock Take / Cycle Counts"),
		feat("lots_batches", inventory, "Inventory", "Lots & Batches"),
		feat("stock_transfers", inventory, "Inventory", "Inter-Warehouse Transfers"),
		feat("rfqs", inventory, "Procurement", "RFQs (Request for Quotation)"),
		feat("requisitions", inventory, "Procurement", "Purchase Requisitions"),
		feat("procurement_contracts", inventory, "Procurement", "Supplier Contracts"),
		feat("manufacturing", inventory, "Manufacturing", "Manufacturing & Production Batches"),
		feat("events_module", inventory, "Events", "Events & Banqueting"),
		feat("fixed_assets", inventory, "Assets", "Fixed Assets Register"),
		feat("warranties", inventory, "Assets", "Warranty Management (Retail)"),
		feat("report_stock_reconciliation", inventory, "Reports", "Stock Reconciliation Report"),
		feat("report_menu_engineering", inventory, "Reports", "Menu Engineering Report"),
		feat("report_food_cost_variance", inventory, "Reports", "Food Cost Variance Report"),
		feat("stock_alerts", inventory, "Alerts", "Low-Stock Alert Settings"),
		feat("expiry_alerts", inventory, "Alerts", "Expiry Alert Settings"),
		lim("inventory_max_images_per_item", inventory, "Limits", "Images per Product", ""),
		lim("max_warehouses", inventory, "Limits", "Warehouses", ""),
		lim("max_products", inventory, "Limits", "Products", ""),
		lim("max_suppliers", inventory, "Limits", "Suppliers", ""),
		meteredLim("inventory_max_sku", inventory, "Limits", "Products (SKUs)", "", "inventory.product.created"),
		meteredLim("inventory_max_warehouses", inventory, "Limits", "Warehouses (metered)", "", "inventory.warehouse.created"),

		// ─── Logistics ────────────────────────────────────────────────────────
		feat("rider_management", logistics, "Logistics", "Rider Fleet Management"),
		feat("delivery_assignment", logistics, "Logistics", "Delivery Task Assignment"),
		feat("live_tracking", logistics, "Logistics", "Live GPS Rider Tracking"),
		feat("basic_dispatch", logistics, "Logistics", "Dispatch Management"),
		feat("route_optimisation", logistics, "Logistics", "Route Optimisation (Valhalla)"),
		feat("driver_analytics", logistics, "Logistics", "Driver Performance Analytics"),
		feat("performance_reports", logistics, "Logistics", "Fleet Performance Reports"),
		// Thin reference into logistics-api's Task/PricingRule — no new dispatch engine, see
		// hospital-api/docs/integrations.md §2A. Hospital tier only (plans_hospital.go).
		feat("ambulance_dispatch", logistics, "Logistics", "Ambulance & Emergency Dispatch"),
		lim("max_riders", logistics, "Limits", "Riders", ""),
		meteredLim("live_tracking_requests_per_day", logistics, "Limits", "Live Tracking Requests", "/ day", "logistics.task.eta_updated"),
		meteredLim("routing_requests_per_day", logistics, "Limits", "Route Calculations", "/ day", ""),

		// ─── Hospital / Codevertex Afya (hospital-service) ────────────────────
		// See plans_hospital.go + CODEVERTEX AFRICA HOSPITAL MANAGEMENT SYSTEMS PRICING
		// MODEL.md. Cross-service features (insurance_claims, stock_take, lots_batches,
		// asset_management, multi_department, hr_management, payroll, etc.) are declared
		// under their owning service above and reused as-is — not duplicated here.
		feat("patient_records", hospital, "Clinical", "Patient Records (EMR)"),
		feat("reception_queuing", hospital, "Clinical", "Reception & Queuing"),
		feat("consultation", hospital, "Clinical", "Consultation"),
		feat("triage", hospital, "Clinical", "Triage"),
		feat("pharmacy_dispensing", hospital, "Clinical", "Pharmacy Dispensing & OTC Sale"),
		feat("billing", hospital, "Clinical", "Billing & Cashier"),
		feat("lab_requests_basic", hospital, "Clinical", "Basic Referred-Out Lab Requests"),
		feat("in_house_lab", hospital, "Clinical", "In-House Laboratory (Full Catalogue)"),
		feat("inpatient_module", hospital, "Clinical", "Inpatient Management (Ward/Bed/Admission)"),
		feat("controlled_substance_register", hospital, "Clinical", "Controlled-Substance Dispensing Register"),
		feat("diagnosis_lab_catalogues", hospital, "Clinical", "Diagnosis & Lab-Test Catalogues"),
		feat("discharge_summaries", hospital, "Clinical", "Discharge Summaries"),
		feat("theatre_module", hospital, "Clinical", "Theatre / Operating Room Scheduling"),
		feat("maternity_module", hospital, "Clinical", "Maternity Management"),
		feat("morgue_module", hospital, "Clinical", "Morgue Management"),
		feat("specialized_programmes", hospital, "Clinical", "Specialized Programmes (ANC/PNC/ART/TB/Immunization)"),
		feat("multi_branch", hospital, "Operations", "Multi-Branch Consolidated Reporting"),
		feat("taifa_care_dedicated_onboarding", hospital, "Compliance", "Dedicated Taifa Care HMIS Onboarding"),
		feat("khis_dhis2_reporting", hospital, "Compliance", "KHIS/DHIS2 Aggregate Reporting (ADX)"),
		lim("max_patients_per_day", hospital, "Limits", "Patients per Day", ""),
		lim("max_branches", hospital, "Limits", "Branches", ""),

		// ─── ERP ──────────────────────────────────────────────────────────────
		feat("hr_management", erp, "ERP", "HR Management"),
		feat("payroll", erp, "ERP", "Payroll Processing"),
		feat("basic_procurement", erp, "ERP", "Purchase Requisitions"),
		feat("leave_management", erp, "ERP", "Leave Management"),
		feat("asset_management", erp, "ERP", "Asset Management"),
		feat("budgeting", erp, "ERP", "Budget Planning"),
		feat("advanced_reports", erp, "ERP", "Advanced Reporting"),
		feat("multi_department", erp, "ERP", "Multi-Department Structure"),
		feat("approval_workflows", erp, "ERP", "Approval Workflows"),
		feat("custom_workflows", erp, "ERP", "Custom Workflows"),
		// Staff take goods on credit/layaway at POS and repay via payroll deductions
		// (POS↔ERP staff-purchase recoverable). Top-tier only.
		feat("staff_fund_from_salary", erp, "ERP", "Staff Fund-from-Salary Credit"),
		// HRM sub-module codes for the use-case PowerSuite tiers: bundle tier 2 grants
		// hr_management + attendance (employee sync) WITHOUT payroll/appraisals/recruitment/
		// training; tier 3 grants everything.
		feat("attendance", erp, "ERP", "Attendance & Time Tracking"),
		feat("appraisals", erp, "ERP", "Appraisals & Performance"),
		feat("recruitment", erp, "ERP", "Recruitment"),
		feat("training", erp, "ERP", "Training Management"),
		lim("max_employees", erp, "Limits", "Employees", ""),
		lim("max_departments", erp, "Limits", "Departments", ""),
		lim("max_payroll_runs_per_month", erp, "Limits", "Payroll Runs", "/ month"),

		// ─── MarketFlow (CRM & Marketing) ─────────────────────────────────────
		feat("contact_management", marketflow, "CRM", "CRM Contact Management"),
		feat("lead_management", marketflow, "CRM", "Lead Management"),
		feat("deal_pipeline", marketflow, "CRM", "Sales Deal Pipeline"),
		feat("lead_scoring", marketflow, "CRM", "AI Lead Scoring"),
		feat("basic_campaigns", marketflow, "Marketing", "Email & SMS Campaigns"),
		feat("unlimited_campaigns", marketflow, "Marketing", "Unlimited Campaigns"),
		feat("landing_pages", marketflow, "Marketing", "Landing Pages"),
		feat("email_sequences", marketflow, "Marketing", "Email Drip Sequences"),
		feat("funnel_builder", marketflow, "Marketing", "Marketing Funnel Builder"),
		feat("automation_workflows", marketflow, "Marketing", "Marketing Automation Workflows"),
		feat("ai_chat_agent", marketflow, "Marketing", "AI Chat Agent (RAG-powered)"),
		feat("whatsapp_integration", marketflow, "Marketing", "WhatsApp Business Integration"),
		feat("storefront", marketflow, "Marketing", "Business Storefront"),
		feat("shortlinks", marketflow, "Marketing", "Branded URL Shortlinks"),
		feat("profile_pages", marketflow, "Marketing", "Public Business Profile Pages"),
		feat("testimonials", marketflow, "Marketing", "Testimonials & Reviews"),
		feat("ticketing", marketflow, "Support", "Helpdesk Ticketing"),
		feat("helpdesk", marketflow, "Support", "Helpdesk Portal"),
		feat("sla_policies", marketflow, "Support", "SLA Policies"),
		feat("knowledge_base", marketflow, "Support", "Knowledge Base"),
		feat("white_label", marketflow, "Support", "White Label"),
		feat("dedicated_account_manager", marketflow, "Support", "Dedicated Account Manager"),
		lim("max_contacts", marketflow, "Limits", "CRM Contacts", ""),
		lim("max_leads", marketflow, "Limits", "CRM Leads", ""),
		lim("max_campaigns", marketflow, "Limits", "Campaigns", ""),
		lim("max_sequences", marketflow, "Limits", "Email Sequences", ""),
		lim("max_funnels", marketflow, "Limits", "Marketing Funnels", ""),
		lim("max_integrations", marketflow, "Limits", "Third-Party Integrations", ""),
		lim("max_tickets", marketflow, "Limits", "Support Tickets", ""),
		lim("max_agents", marketflow, "Limits", "Support Agents", ""),
		meteredLim("ai_credits_monthly", marketflow, "Limits", "AI Chat Credits", "/ month", ""),

		// ─── TruLoad (Weighbridge) ────────────────────────────────────────────
		feat("commercial_weighing", truload, "Weighbridge", "Commercial Weighing"),
		feat("enforcement_weighing", truload, "Weighbridge", "Enforcement Weighing"),
		feat("cargo_types", truload, "Weighbridge", "Cargo Type Management"),
		feat("tare_management", truload, "Weighbridge", "Tare Weight Management"),
		feat("tolerance_settings", truload, "Weighbridge", "Tolerance Settings"),
		feat("prosecution", truload, "Weighbridge", "Overload Prosecution"),
		feat("case_management", truload, "Weighbridge", "Case Management"),
		feat("invoicing", truload, "Weighbridge", "Weighbridge Invoicing"),
		feat("reporting", truload, "Weighbridge", "Weighbridge Reporting"),
		feat("transporter_portal", truload, "Weighbridge", "Transporter Portal Access"),
		lim("max_stations", truload, "Limits", "Weighbridge Stations", ""),
		lim("max_sites", truload, "Limits", "Weighbridge Sites", ""),
		meteredLim("max_weighings_month", truload, "Limits", "Weighings", "/ month", "truload.weighing.created"),

		// ─── Transporter Portal ───────────────────────────────────────────────
		feat("portal_access", portal, "Transporter Portal", "Transporter Portal Access"),
		feat("ticket_download", portal, "Transporter Portal", "Weight Ticket Downloads (PDF)"),
		feat("email_notifications", portal, "Transporter Portal", "Email Notifications"),
		feat("multi_site_access", portal, "Transporter Portal", "Cross-Site (Multi-Tenant) History"),
		feat("data_export", portal, "Transporter Portal", "CSV / Bulk Data Export"),
		feat("driver_reports", portal, "Transporter Portal", "Driver Trip Reports"),
		feat("vehicle_trends", portal, "Transporter Portal", "Vehicle Utilisation Trends"),
		feat("consignment_tracking", portal, "Transporter Portal", "Consignment Tracking"),
		feat("analytics", portal, "Transporter Portal", "Advanced Fleet Analytics"),
		lim("history_days", portal, "Limits", "Ticket History", "days"),

		// ─── ISP Billing (hotspot + PPPoE; modeled on Centipid) ───────────────
		feat("hotspot_management", isp, "ISP", "Hotspot Management"),
		feat("pppoe_management", isp, "ISP", "PPPoE Management"),
		feat("captive_portal", isp, "ISP", "Captive Portal"),
		feat("radius_auth", isp, "ISP", "RADIUS Authentication"),
		feat("voucher_system", isp, "ISP", "Voucher Management"),
		feat("bandwidth_limiting", isp, "ISP", "Bandwidth Limiting"),
		feat("fup_management", isp, "ISP", "Fair Usage Policy (FUP)"),
		feat("customer_management", isp, "ISP", "Customer Management"),
		feat("multi_router", isp, "ISP", "Multi-Router (Unlimited MikroTiks)"),
		feat("remote_winbox", isp, "ISP", "Remote WinBox Management"),
		feat("mpesa_integration", isp, "ISP", "M-Pesa Integration"),
		feat("invoice_generation", isp, "ISP", "Automated Invoicing"),
		feat("realtime_notifications", isp, "ISP", "Real-time Notifications"),
		feat("basic_analytics", isp, "ISP", "Basic Analytics"),
		feat("advanced_analytics", isp, "ISP", "Advanced Analytics"),
		feat("performance_reports", isp, "ISP", "Performance Reports"),
		feat("expense_tracking", isp, "ISP", "Expense Tracking"),
		feat("custom_domain", isp, "ISP", "Custom Domain"),
		feat("white_label", isp, "ISP", "White-Label Captive Portal"),
		feat("white_label_portal", isp, "ISP", "White-Label Customer Portal"),
		feat("api_access", isp, "ISP", "API Access"),
		feat("custom_integrations", isp, "ISP", "Custom Integrations"),
		feat("priority_support", isp, "ISP", "Priority Support"),
		feat("audit_trail", isp, "ISP", "Audit Trail"),
		lim("max_routers", isp, "Limits", "Routers", ""),
		lim("max_customers", isp, "Limits", "Customers / Subscribers", ""),
		lim("max_users", isp, "Limits", "Staff Users", ""),
		// PPPoE active subscribers — drives the per-subscriber/month fee
		// (Centipid $0.25 ≈ KES 35), metered via the isp.subscriber.created event.
		meteredLim("max_pppoe_subscribers", isp, "Limits", "PPPoE Subscribers", "/ month", "isp.subscriber.created"),
		meteredLim("max_vouchers_per_month", isp, "Limits", "Vouchers", "/ month", ""),
		// SMS bundles are prepaid TenantCredits in notifications-api and are NOT enforced as a plan
		// limit; this catalog entry only exists because ISP plan tiers carry a nominal max_sms_per_month
		// in tier_limits (keeps validateCatalogLinkage green — no service gates on it).
		lim("max_sms_per_month", isp, "Limits", "SMS", "/ month"),
		// ISP billing-engine inputs read from tier_limits (see plans_isp_billing.go): the per-subscriber
		// PPPoE fee + the service-charge percentage/threshold. Catalogued as limits so the strict
		// linkage check passes; they are pricing inputs, not enforced capacity caps.
		lim("pppoe_per_subscriber_fee", isp, "Billing", "PPPoE Per-Subscriber Fee", "KES / subscriber"),
		lim("service_charge_percentage", isp, "Billing", "Service Charge", "%"),
		lim("service_charge_threshold", isp, "Billing", "Service Charge Threshold", "KES"),

		// ─── Projects & Invoicing ─────────────────────────────────────────────
		feat("project_management", projects, "Projects", "Project Management"),
		feat("gantt_chart", projects, "Projects", "Gantt Charts"),
		feat("milestone_billing", projects, "Projects", "Milestone Billing"),
		feat("task_tracking", projects, "Projects", "Task Tracking"),
		feat("time_tracking", projects, "Projects", "Time Tracking"),
		feat("team_collaboration", projects, "Projects", "Team Collaboration"),
		feat("client_portal", projects, "Projects", "Client Portal"),
		feat("expense_tracking", projects, "Projects", "Expense Tracking"),
		feat("budget_tracking", projects, "Projects", "Budget Tracking"),
		feat("basic_invoicing", projects, "Invoicing", "Basic Invoicing"),
		feat("advanced_invoicing", projects, "Invoicing", "Advanced Invoicing"),
		feat("recurring_invoices", projects, "Invoicing", "Recurring Invoices"),
		lim("max_projects", projects, "Limits", "Projects", ""),
		lim("max_clients", projects, "Limits", "Clients", ""),
		lim("max_staff_users", projects, "Limits", "Staff Users", ""),
		lim("max_storage_gb", projects, "Limits", "Storage", "GB"),
		meteredLim("max_invoices_per_month", projects, "Limits", "Invoices", "/ month", ""),

		// ─── Platform & API (cross-cutting) ───────────────────────────────────
		feat("custom_branding", platform, "Platform & API", "Custom Branding (Logo, Colors, Font)"),
		feat("basic_analytics", platform, "Platform & API", "Analytics Dashboard"),
		feat("advanced_analytics", platform, "Platform & API", "Advanced Analytics (Superset)"),
		feat("api_access", platform, "Platform & API", "REST API Access"),
		feat("webhooks", platform, "Platform & API", "Outbound Webhook Subscriptions"),
		feat("custom_integrations", platform, "Platform & API", "Custom Third-Party Integrations"),
		feat("audit_trail", platform, "Platform & API", "Audit Trail"),
		feat("priority_support", platform, "Platform & API", "Priority Support"),
		feat("premium_support", platform, "Platform & API", "Premium Support (SLA)"),
		feat("sms_notifications", platform, "Platform & API", "SMS Notifications"),
		feat("push_notifications", platform, "Platform & API", "Push Notifications"),
		lim("max_users", platform, "Limits", "Users", ""),
		meteredLim("api_calls_per_month", platform, "Limits", "API Calls", "/ month", ""),

		// ─── Purchasable Add-ons (is_included=false on plans) ─────────────────
		feat("extra_rider_slot", ordering, "Add-ons", "Extra Rider Slot"),
		feat("extra_outlet", ordering, "Add-ons", "Extra Outlet / Branch"),
		feat("hotel_module_addon", ordering, "Add-ons", "Hotel Module Add-on"),
		feat("route_optimization_addon", ordering, "Add-ons", "Route Optimization Add-on"),
		feat("advanced_analytics_addon", platform, "Add-ons", "Advanced Analytics Add-on"),
		feat("extra_api_quota_bundle", platform, "Add-ons", "Extra API Quota Bundle"),
		feat("dedicated_support_addon", platform, "Add-ons", "Dedicated Support Add-on"),
		feat("sms_bundle_100", platform, "Add-ons", "SMS Bundle (100)"),
		feat("sms_bundle_500", platform, "Add-ons", "SMS Bundle (500)"),
		feat("ai_credits_topup", marketflow, "Add-ons", "AI Credits Top-up"),
		feat("extra_transporter_portal", truload, "Add-ons", "Extra Transporter Portal Seat"),

		// ─── Library Management ───────────────────────────────────────────────
		feat("library_catalog", library, "Library", "Catalog & OPAC Search"),
		feat("library_circulation", library, "Library", "Circulation (Checkout / Return / Renew)"),
		feat("library_holds", library, "Library", "Holds & Reservations"),
		feat("library_members", library, "Library", "Member Management"),
		feat("library_fines", library, "Library", "Fines & Fees"),
		feat("library_ebooks", library, "Library", "E-books & Controlled Digital Lending"),
		lim("max_library_titles", library, "Limits", "Catalog Titles", ""),
		lim("max_library_members", library, "Limits", "Library Members", ""),
		lim("max_library_branches", library, "Limits", "Library Branches", ""),
	}
	return entries
}()

// catalogCodeIndex is the set of canonical feature/limit codes in the catalog,
// built once from featureCatalog. Used to validate plan-feature seeding.
var catalogCodeIndex = func() map[string]bool {
	m := make(map[string]bool, len(featureCatalog))
	for _, e := range featureCatalog {
		m[e.code] = true
	}
	return m
}()

// catalogLimitIndex maps every LIMIT-kind catalog code to its owning service_tag.
// It is the authoritative set of limit keys a plan's tier_limits_json may use, and
// powers the seed-time validation that keeps plan limits linked to the catalog
// instead of drifting as free-form strings. Built once from featureCatalog.
var catalogLimitIndex = func() map[string]string {
	m := make(map[string]string)
	for _, e := range featureCatalog {
		if e.kind == featuredefinition.KindLIMIT {
			m[e.code] = e.service
		}
	}
	return m
}()

// featureCodeAliases maps legacy / duplicate feature codes emitted by older plan
// seeds to their canonical catalog code, eliminating feature-key drift. Extend
// this when consolidating duplicate keys instead of editing every plans_*.go.
//
// NOTE: this table is shared by feature codes AND tier-limit keys (both pass
// through canonicalFeatureCode). Do not add a limit-only alias whose key also
// names a distinct FEATURE add-on (e.g. ai_credits_topup) — it would corrupt
// seedPlanFeatures. Fix such drift at the plan-seed source instead.
var featureCodeAliases = map[string]string{
	"paystack_gateway": "paystack_integration", // duplicate online-gateway key
	"paystack":         "paystack_integration",
}

// canonicalFeatureCode resolves a feature code to its canonical catalog code via
// the alias table (identity if no alias). It does not validate membership.
func canonicalFeatureCode(code string) string {
	if c, ok := featureCodeAliases[code]; ok {
		return c
	}
	return code
}

// IsInCatalog reports whether a (canonicalized) code exists in the catalog.
func isInCatalog(code string) bool {
	return catalogCodeIndex[canonicalFeatureCode(code)]
}

// isCatalogLimit reports whether a (canonicalized) code is a LIMIT-kind entry in
// the catalog — i.e. a valid key for a plan's tier_limits_json.
func isCatalogLimit(code string) bool {
	_, ok := catalogLimitIndex[canonicalFeatureCode(code)]
	return ok
}

// limitServiceTag returns the owning service_tag of a LIMIT code, and whether it
// exists as a catalog LIMIT.
func limitServiceTag(code string) (string, bool) {
	s, ok := catalogLimitIndex[canonicalFeatureCode(code)]
	return s, ok
}

// seedFeatureCatalog upserts every catalog entry by feature_code (idempotent).
func seedFeatureCatalog(ctx context.Context, tx *ent.Tx) error {
	for i, e := range featureCatalog {
		valueType := e.valueType
		if err := featuredefinition.ValueTypeValidator(valueType); err != nil {
			valueType = featuredefinition.ValueTypeBool
		}

		existing, err := tx.FeatureDefinition.Query().
			Where(featuredefinition.FeatureCodeEQ(e.code)).
			Only(ctx)

		switch {
		case err == nil:
			upd := existing.Update().
				SetServiceTag(e.service).
				SetCategory(e.category).
				SetLabel(e.label).
				SetKind(e.kind).
				SetValueType(valueType).
				SetIsRateLimited(e.metered).
				SetUnit(e.unit).
				SetNatsEvent(e.nats).
				SetSortOrder(i).
				SetIsActive(true)
			if e.defaultLim != nil {
				upd = upd.SetDefaultLimit(*e.defaultLim)
			} else {
				upd = upd.ClearDefaultLimit()
			}
			if _, serr := upd.Save(ctx); serr != nil {
				return fmt.Errorf("update feature definition %s: %w", e.code, serr)
			}
		case ent.IsNotFound(err):
			cr := tx.FeatureDefinition.Create().
				SetFeatureCode(e.code).
				SetServiceTag(e.service).
				SetCategory(e.category).
				SetLabel(e.label).
				SetKind(e.kind).
				SetValueType(valueType).
				SetIsRateLimited(e.metered).
				SetUnit(e.unit).
				SetNatsEvent(e.nats).
				SetSortOrder(i).
				SetIsActive(true)
			if e.defaultLim != nil {
				cr = cr.SetDefaultLimit(*e.defaultLim)
			}
			if _, serr := cr.Save(ctx); serr != nil {
				return fmt.Errorf("create feature definition %s: %w", e.code, serr)
			}
		default:
			return fmt.Errorf("lookup feature definition %s: %w", e.code, err)
		}
	}
	return nil
}
