# Use-Case PowerSuite Plan ↔ Feature Matrix

_Derived from the authoritative specs in this folder (Hospitality/Retail/Pharmacy Power-suite
plans). Implemented 2026-07-16 in `cmd/seed/plans_powersuite_usecase.go` (+ `_support.go`,
`plans_powersuite_builders.go`, `migrate_usecase_powersuite.go`). Keep this file in sync with
the builders when tiers change._

## Families, prices & products

Three per-use-case PowerSuite families bundling **POS + Inventory + Treasury + Ordering +
Logistics + CRM (MarketFlow) + ERP**. Plan rows evolved IN PLACE from the POS product lines
(`pos:{family}:{STARTER|PRO|ENTERPRISE}` deterministic ids → stable UUIDs/FKs).

| Family | Code prefix | T1 Basic /mo (setup) | T2 Professional /mo (setup) | T3 Gold /mo (setup) | Buy-outright T1/T2/T3 (+tier setup fee) | Annual support T1/T2/T3 |
|---|---|---|---|---|---|---|
| Hospitality | `POWERSUITE_HOSP_` | 2,500 (5k) | 4,000 (10k) | 6,500 (20k) | 45k / 90k / 150k | 9k / 19k / 30k |
| Retail (Duka) | `POWERSUITE_DUKA_` | 2,500 (5k) | 4,500 (10k) | 8,500 (20k) | 45k / 90k / 150k | 9k / 18k / 30k |
| Pharmacy (Dawa) | `POWERSUITE_DAWA_` | 1,500 (5k) | 3,000 (10k) | 6,000 (20k) | 45k / 90k / 150k | 9k / 18k / 30k |

- **Billing periods:** every recurring tier is ONE monthly-priced row; the tenant chooses
  MONTHLY / SEMI_ANNUAL / ANNUAL at subscribe/renew (price = months × base; ≥6 months waives
  the setup fee — `billing_cycle.go`). Only the `_ONE_TIME` rows are period-less.
- **`_ONE_TIME` licenses** (`POWERSUITE_{FAM}_{TIER}_ONE_TIME`): perpetual, mirror the
  recurring tier's features/limits exactly; tier setup fee charged inline on top.
- **`SUPPORT_{FAM}_{TIER}`**: ANNUAL, entitlement-only (no gating features), `is_public=false`,
  `metadata.support_plan=true`, excluded from `retireAnnualPlanRows`.

## Cross-service blocks (shared by all families)

| Block | Tier 1 | Tier 2 adds | Tier 3 adds |
|---|---|---|---|
| Ordering | online_ordering, rider_app, admin_dashboard, paystack_integration, sms/push_notifications, basic_analytics, custom_domain, loyalty_program, wallet, delivery_zones | mpesa_integration, advanced_analytics, multi_outlet, promo_codes, group_ordering, scheduled_delivery, pos_integration | route_optimization, api_webhooks, white_labeling, priority_support, premium_support |
| Logistics | rider_management, delivery_assignment, live_tracking, basic_dispatch, basic_logistics_access | route_optimisation, driver_analytics, performance_reports | api_access, webhooks, custom_integrations |
| CRM | contact_management, lead_management, basic_campaigns, shortlinks | unlimited_campaigns, landing_pages, email_sequences, ai_chat_agent, lead_scoring, funnel_builder, automation_workflows, ticketing, helpdesk, sla_policies, knowledge_base, testimonials | white_label, dedicated_account_manager |
| ERP | — (no access; ERP links show locked) | hr_management, leave_management, attendance, basic_reports (**no payroll/appraisals/recruitment/training**) | full ERP: payroll, appraisals, recruitment, training, basic_procurement, asset_management, budgeting, advanced_reports, multi_department, approval_workflows, custom_workflows, staff_fund_from_salary |
| Treasury | wallet_management, payment_collection, payment_links, transaction_reports, customer_management, **quotations**, ar_tracking, ap_tracking, tax_codes, etims_integration (never tier-gated — KRA legal) | invoice_generation, credit_notes, vendor_management, ledger_posting, treasury_approvals, smart_tax_compliance | vouchers, reconciliation, basic_reconciliation, audit_trail |
| POS core | pos_terminal, order_management, receipt_printing, daily_reports, shift_reports, mpesa_pos, offline_sync | multi_cashier | — |
| Inventory core | stock_tracking, purchase_orders, supplier_portal, basic_reports, stock_transfers (**no bulk_import / stock_take / stock alerts at T1**) | bulk_import, stock_take*, requisitions, multi_warehouse, inventory_multiple_images, low_stock_alerts*, stock_alerts* | rfqs, procurement_contracts, report_menu_engineering |

\* family variances below.

Treasury note: hospitality gets `vendor_management` from T2 ("All"); retail + pharmacy list
Suppliers & Vendors at T1 so their `psTreasuryBlock(vendorAtT1=true)`.

## Family-specific deltas

| | Hospitality | Retail (Duka) | Pharmacy (Dawa) |
|---|---|---|---|
| T1 extras | table_management, kds, happy_hour | barcode_scanning | lots_batches, batch_expiry_tracking, expiry_alerts |
| T2 extras | facility_booking, manufacturing, fixed_assets, report_stock_reconciliation, report_food_cost_variance | layaway, commissions, **warranties** (retail-only), manufacturing, fixed_assets, report_stock_reconciliation, report_food_cost_variance | prescription_management, patient_history, insurance_claims, barcode_scanning (stock_take/stock alerts still excluded) |
| T3 extras | hotel_module, conference_events, events_module | (procurement completion only) | label_printing, stock_take, manufacturing, fixed_assets, low_stock_alerts, stock_alerts, report_stock_reconciliation |
| Never granted | lots_batches, batch_expiry_tracking, expiry_alerts | lots_batches, batch_expiry_tracking, expiry_alerts, hotel stack | warranties, events_module, menu/food-cost reports, hotel stack |

Limits = `powerSuiteLimits(tier)` overlaid with family POS caps (`useCaseSuiteLimits`):
HOSP 2/3/20-tables → 5/10/50 → unlimited (+rooms/conference at Gold); DUKA & DAWA 1/2 → 5/10 →
unlimited, no tables/rooms keys.

## Superseded rows (hard-deleted by `migrateUseCasePowerSuite`, subscribers migrated first)

| Doomed | Successor |
|---|---|
| `POWERSUITE_{STARTER,GROWTH,PROFESSIONAL}` (+`_YEARLY`, +`_ONE_TIME`), `POS_SUITE_*` (+`_YEARLY`) | same-tier family row by tenant `use_case` (retail-ish→DUKA, pharmacy→DAWA, else HOSP); `_ONE_TIME`→family `_ONE_TIME`; `_YEARLY` subscribers keep `billing_cycle=ANNUAL` |
| `POS_DEVICE_{1,5,10}` (+`_YEARLY`) | family BASIC/PRO/GOLD |
| `POS_LICENSE_PER_DEVICE` / `POS_LICENSE_COMPLETE` | family `BASIC_ONE_TIME` / `GOLD_ONE_TIME` |
| `POS_{HOSP,DUKA,DAWA}_LICENSE` (tier-11) | family `GOLD_ONE_TIME` |
| legacy flat `ERP_ONE_TIME` (150k) | `ERP_GROWTH_ONE_TIME` (same price) |
| every remaining `*_YEARLY` row platform-wide | same code minus `_YEARLY` (sub keeps ANNUAL cycle); `ISP_*_YEARLY` → `ISP_BILLING_STARTER` |

Safety: a doomed plan whose subscriber has no resolvable successor is logged + KEPT.
Every re-point is logged `MIGRATE sub <id> tenant <id> <old> -> <new>` — feed that log into the
treasury invoice-correction pass (void + regenerate PENDING subscription invoices whose plan
price changed; PAID invoices are never touched).

## Kept as-is (per spec notes)

ERP standalone plans (`ERP_{STARTER,GROWTH,PROFESSIONAL}` + `_ONE_TIME` tiers — now with
attendance from T1 and appraisals/recruitment/training from T2), all TruLoad plans, and every
standalone service line (INVENTORY_/TREASURY_/LOGISTICS_/MARKETFLOW/ISP/PROJECTS/LIBRARY,
service-charge plans). ERP-suite `_ONE_TIME` licenses still union `powerSuiteFeatures(tier)`
(generic builders kept in `plans_powersuite_builders.go`).
