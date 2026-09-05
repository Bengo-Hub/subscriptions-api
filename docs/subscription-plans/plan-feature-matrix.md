# Use-Case PowerSuite Plan ↔ Feature Matrix

_Derived from the authoritative specs in this folder (Hospitality/Retail Power-suite
plans). Implemented 2026-07-16 in `cmd/seed/plans_powersuite_usecase.go` (+ `_support.go`,
`plans_powersuite_builders.go`, `migrate_usecase_powersuite.go`). Keep this file in sync with
the builders when tiers change._

> **Pharmacy (Dawa) family retired 2026-08-29.** pos-api's `pharmacy` use case was decisively
> migrated to hospital-service (Codevertex Afya) — see
> `hospital-service/hospital-api/docs/migration-pos-pharmacy.md`. The `powersuite-pharmacy`
> bundle and its three `POWERSUITE_DAWA_*` plan rows are removed from `cmd/seed/bundles.go`.
> A chemist/dispensary tenant's real successor is hospital-api's own `AFYA_CHEMIST` tier
> (`service_tag: "hospital"`, `cmd/seed/plans_hospital.go`), a different product family, not a
> same-family PowerSuite successor. **Follow-up resolved 2026-08-30**: auth-api's
> `defaultTrialPlan` (`internal/services/auth/service.go`) now routes a `pharmacy`/`chemist`
> use_case tenant straight to `AFYA_CHEMIST` — `CreateTrialSubscription` resolves
> `facility_type: "chemist"` automatically from that plan's own metadata, no extra plumbing
> needed. `agrovet` was moved out to `POWERSUITE_DUKA_BASIC` instead (it sells agricultural/
> veterinary inputs over the counter, not human-health dispensing — it was only ever grouped
> with pharmacy/chemist under the retired DAWA family's broad "drug-adjacent retail" umbrella,
> not because it needs hospital-service's clinical workflow).

## Families, prices & products

Two per-use-case PowerSuite families bundling **POS + Inventory + Treasury + Ordering +
Logistics + CRM (MarketFlow) + ERP**. Plan rows evolved IN PLACE from the POS product lines
(`pos:{family}:{STARTER|PRO|ENTERPRISE}` deterministic ids → stable UUIDs/FKs).

| Family | Code prefix | T1 Basic /mo (setup) | T2 Professional /mo (setup) | T3 Gold /mo (setup) | Buy-outright T1/T2/T3 (+tier setup fee) | Annual support T1/T2/T3 |
|---|---|---|---|---|---|---|
| Hospitality | `POWERSUITE_HOSP_` | 2,500 (5k) | 4,000 (10k) | 6,500 (20k) | 45k / 90k / 150k | 9k / 19k / 30k |
| Retail (Duka) | `POWERSUITE_DUKA_` | 2,500 (5k) | 4,500 (10k) | 8,500 (20k) | 45k / 90k / 150k | 9k / 18k / 30k |

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

Treasury note: hospitality gets `vendor_management` from T2 ("All"); retail lists
Suppliers & Vendors at T1 so its `psTreasuryBlock(vendorAtT1=true)`.

## Family-specific deltas

| | Hospitality | Retail (Duka) |
|---|---|---|
| T1 extras | table_management, kds, happy_hour | barcode_scanning |
| T2 extras | facility_booking, manufacturing, fixed_assets, report_stock_reconciliation, report_food_cost_variance | layaway, commissions, **warranties** (retail-only), manufacturing, fixed_assets, report_stock_reconciliation, report_food_cost_variance |
| T3 extras | hotel_module, conference_events, events_module | (procurement completion only) |
| Never granted | lots_batches, batch_expiry_tracking, expiry_alerts | lots_batches, batch_expiry_tracking, expiry_alerts, hotel stack |

Limits = `powerSuiteLimits(tier)` overlaid with family POS caps (`useCaseSuiteLimits`):
HOSP 2/3/20-tables → 5/10/50 → unlimited (+rooms/conference at Gold); DUKA 1/2 → 5/10 →
unlimited, no tables/rooms keys.

## Superseded rows (hard-deleted by `migrateUseCasePowerSuite`, subscribers migrated first)

| Doomed | Successor |
|---|---|
| `POWERSUITE_{STARTER,GROWTH,PROFESSIONAL}` (+`_YEARLY`, +`_ONE_TIME`), `POS_SUITE_*` (+`_YEARLY`) | same-tier family row by tenant `use_case` (retail-ish→DUKA, pharmacy/chemist/agrovet→DAWA¹, else HOSP); `_ONE_TIME`→family `_ONE_TIME`; `_YEARLY` subscribers keep `billing_cycle=ANNUAL` |
| `POS_DEVICE_{1,5,10}` (+`_YEARLY`) | family BASIC/PRO/GOLD |
| `POS_LICENSE_PER_DEVICE` / `POS_LICENSE_COMPLETE` | family `BASIC_ONE_TIME` / `GOLD_ONE_TIME` |
| `POS_{HOSP,DUKA,DAWA}_LICENSE`¹ (tier-11) | family `GOLD_ONE_TIME` |
| legacy flat `ERP_ONE_TIME` (150k) | `ERP_GROWTH_ONE_TIME` (same price) |
| every remaining `*_YEARLY` row platform-wide | same code minus `_YEARLY` (sub keeps ANNUAL cycle); `ISP_*_YEARLY` → `ISP_BILLING_STARTER` |

¹ The `DAWA` family itself was retired 2026-08-29 (see the callout at the top of this doc) —
`migrate_usecase_powersuite.go`'s pharmacy/chemist/agrovet→DAWA mapping is left in the code
unchanged (a leftover doomed-plan subscriber safely logs+skips rather than erroring, per the
Safety note below, and no real tenant was ever on a doomed `POWERSUITE_DAWA_*`/`POS_DAWA_LICENSE`
code), but it no longer resolves to a real plan for a NEW migration going forward.

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

## Enforcement rollout (2026-07-16, same session — backends + UIs)

Seed verified locally: migrations + `go run ./cmd/seed` → 0 WARN, catalog validation passed,
all superseded rows migrated + hard-deleted, exit 0.

- **auth-api**: `defaultTrialPlan` routes use_case → `POWERSUITE_{HOSP|DUKA|DAWA}_BASIC`.
- **inventory-api**: route gates for stock_take/lots_batches/rfqs/requisitions/
  procurement_contracts/manufacturing/events_module/fixed_assets/report_*/bulk_import;
  **warranties module** (`extras_warranties.go`, mutations gated `warranties`); NEW: field-level
  gates on PUT /inventory/settings (stock_alerts, expiry_alerts, lots_batches,
  batch_expiry_tracking — enabling requires the feature) + `stock_alerts` gate on
  /inventory/analytics/reorder-alerts.
- **treasury-api**: invoice_generation vs quotations split, credit_notes, treasury_approvals,
  smart_tax_compliance, ledger_posting, reconciliation, tax_codes (vouchers has no distinct
  backend surface — rides ledger_posting; gated in treasury-ui nav via `vouchers`).
- **pos-api**: NEW layaway + commissions feature gates; purchase-orders proxy DELETED
  (inventory owns POs; `tenantSlugFromRequest` moved to online_orders_rider.go).
- **erp-api**: payroll/appraisals/recruitment/training/attendance gates (pre-existing).
- **pos-ui**: Purchase Orders page/hook/api DELETED (dashboard quick-action links to
  inventory-ui); Accounting → single Treasury link; CRM & Marketing → single CRM link; NEW
  gated ERP link (hr_management); Sync Monitor moved to platform-owner-only section + page
  guard; settings tabs already use-case-gated.
- **inventory-ui**: MODULE_FEATURE extended (stock_take, lots_batches, rfqs, requisitions,
  procurement_contracts, manufacturing, events_module, fixed_assets, warranties, per-item
  report_* codes); "Ingredient Utilization" → "Stock Reconciliation" (route unchanged); NEW
  Warranties page (`/warranties`, retail-only nav) + gated ERP link; alert/lot/expiry settings
  toggles wrapped in FeatureLock (matching the new API enforcement).
- **treasury-ui**: Sales & Invoicing group gate dropped (T1 keeps Quotations/Customers);
  per-item gates invoice_generation/quotations/credit_notes/vouchers/treasury_approvals;
  dashboard Tax & Compliance card gated smart_tax_compliance; gated ERP link.
- **erp-ui**: nav lock badges + route-level block gates (layout.tsx) for payroll, attendance,
  appraisals (+performance), training, recruitment.
- **subscriptions-ui**: tenant plans view filters `isPublic !== false` (SUPPORT_* rows hidden);
  subscribe page already offers MONTHLY/SEMI_ANNUAL/ANNUAL.

### Still pending (operational / decisions)
1. **Prod rollout**: deploy subscriptions-api → run seed Job → check `MIGRATE …` log →
   treasury invoice-correction pass (void+regen PENDING invoices whose plan price changed) →
   deploy backends/UIs.
2. **Quick-service + services plan families** (doc NOTE): not minted — they currently resolve
   to the HOSP family. Needs a pricing decision (docs say "lower prices" without figures).
3. Backlog from subscriptions-audit.md §5: schedule `CalculateDailyOverages` (daily worker),
   wire per-service usage reporting to /usage/report, remaining capacity limits
   (pos max_devices/max_cashiers…, ordering, inventory max_products/max_suppliers),
   discount-rule format mismatch (UI `ANNUAL_DISCOUNT`+value vs API `YEARLY`+percentage).

## `use_case` column added (2026-09-05)

Fixed a real, reported bug: because all PowerSuite families share `service_tag: "pos"`, a
retail tenant browsing the pricing page saw hospitality/pharmacy family plans mixed in with
their own (`service_tag` alone can't disambiguate the family — only `plan_code` could,
un-queryably). `SubscriptionPlan` gained an additive, indexed `use_case` column (nullable —
absent means "not vertical-specific, matches any tenant") and `ListPlans`/`ListPlansWithPrices`
gained a matching `use_case` query param. `cmd/seed`'s existing `useCaseFamily` struct now
carries `useCase` (hosp→`hospitality`, duka→`retail`, dawa→`pharmacy`) and every PowerSuite +
`SUPPORT_*` create/update call sets it — no separate backfill script; re-running the normal
seed Job backfills existing rows (verified locally, not yet re-run in prod — see item 1 above,
this is now also part of that same rollout step). auth-ui's Billing tab and subscriptions-ui's
own plans page (`app/plans/page.tsx`) both consume this to filter what a tenant sees; the
subscriptions-ui fix also plugged a stale gap from the DAWA→AFYA_CHEMIST migration note above
— `hospital` was never a key in that page's `USECASE_GROUPS` map at all, so a hospital-service
tenant fell through to "show every group" instead of just its own `AFYA_*` plans.
