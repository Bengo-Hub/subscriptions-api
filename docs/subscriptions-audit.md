# Subscriptions Platform Audit & Implementation Notes

_Last updated: 2026-06-06._

This document captures the audit of the subscriptions platform (plans, features, limits,
gating, usage, scenarios) and records what was implemented vs. what remains. It accompanies
the work that added the **feature catalog**, the **rich plan builder**, **plan-type/scenario
resolution**, and **service-side enforcement**.

---

## 1. Architecture recap

- **subscriptions-api** (Go + Ent + Postgres + Redis) owns plans, features, limits,
  service-charge plans, products, usage events, overage charges.
- **auth-api** mints JWTs enriched with `sub_plan`, `sub_status`, `subscription_features`,
  `sub_limits`, `sub_expires`, `billing_mode`, `is_demo` from
  `GET /api/v1/tenants/{id}/subscription`.
- **Services** (ordering, pos, inventory, treasury, …) gate features/limits from the JWT
  claims via `shared-auth-client` (`HasFeature`, `GetLimit`) and per-service `gate.go`
  middleware. **Services consume `shared-auth-client` as the published module
  `github.com/Bengo-Hub/auth-client` (pinned via `replace`)** — changes to the local
  `shared/auth-client` source only take effect after a release + version bump.

---

## 2. Feature & limit catalog (NEW — single source of truth)

Previously features were freeform `feature_code` strings on `plan_feature` rows, with the
only categorization living in a hardcoded `FEATURE_INFO`/`LIMIT_INFO` map in
`subscriptions-ui`. There was **no DB catalog**, so no reliable per-service list of every
feature/limit.

**Implemented:**
- New Ent entity `FeatureDefinition` (`internal/ent/schema/feature_definition.go`):
  `feature_code` (unique), `service_tag`, `category`, `label`, `description`, `kind`
  (FEATURE|LIMIT), `value_type` (bool|int|unlimited), `default_limit`, `is_rate_limited`,
  `unit`, `nats_event`, `sort_order`, `is_active`. Migration
  `20260606120000_add_feature_definitions_catalog.sql` (+ `atlas.sum` regenerated offline
  via `cmd/hashmigrations`).
- Comprehensive seed `cmd/seed/feature_catalog.go` — **every feature/limit code used across
  all `plans_*.go` seeds is captured and categorized per service** (ordering, pos, inventory,
  treasury, logistics, erp, marketflow, truload, transporter_portal, isp_billing, projects,
  platform). Verified by a static diff of seed codes vs. catalog: the only "uncovered" code
  is `paystack_gateway`, which is an intentional alias (see below).
- Catalog API: `GET /api/v1/features/catalog?service=&kind=` (read) and admin CRUD
  `POST /api/v1/admin/feature-catalog`, `DELETE /api/v1/admin/feature-catalog/{id}`
  (platform owner only) — `internal/http/handlers/feature_catalog.go`.
- **Catalog-aware plan seeding**: `seedPlanFeaturesWithLimits` and the addon seeder now
  normalize legacy codes to canonical ones, dedupe, and `WARN` on any code missing from the
  catalog (`canonicalFeatureCode` / `isInCatalog`).

**Key-drift normalization:** `paystack_gateway` → `paystack_integration` (alias table in
`feature_catalog.go`); extend `featureCodeAliases` when consolidating future duplicates
instead of editing every `plans_*.go`.

---

## 3. Plan types & per-tenant scenario resolution

The model supports three scenarios; this work made them resolve consistently:

| Scenario | Source | Behaviour |
|---|---|---|
| Recurring (MONTHLY/QUARTERLY/ANNUAL) | `subscription_plans.billing_cycle` | gated by `current_period_end` + 7-day grace |
| One-time licence (ONE_TIME) | `billing_cycle = ONE_TIME` | **perpetual entitlement** — never expires |
| Service charge | `ServiceChargePlan` / tenant `billing_mode` | bypasses gating (pays per transaction) |

**Implemented:**
- `plan_type` (TIERED/STANDALONE_SERVICE/BUNDLE/CUSTOM) is now persisted, mapped, and
  selectable (was a dead enum) — `plans` repo + handler + admin form.
- `SubscriptionResult` now returns `billing_cycle`, `billing_mode`
  (`recurring|one_time|service_charge`), `plan_type`, `is_perpetual`
  (`internal/modules/subscriptions/service.go` `buildResult`). ONE_TIME plans force
  `access_status = active` and `is_perpetual = true`.
- auth-api skips the JWT expiry for perpetual licences and resolves `billing_mode` from the
  subscription response (tenant metadata still takes precedence) —
  `auth-service/auth-api/internal/services/auth/service.go`.

---

## 4. Rich platform-owner plan builder (subscriptions-ui)

`src/app/plans/page.tsx` `AdminPlansView` now:
- Loads the DB catalog (`GET /features/catalog`) via React Query.
- **`CatalogFeaturePicker`** — pick a service → its features/limits load grouped by category;
  toggling a FEATURE adds/removes it from the plan, toggling a LIMIT adds it to tier limits
  (pre-filled with `default_limit`); shows an "N of M in plan" counter.
- New form fields wired to the payload: `planType`, `serviceTag` selectors, and a
  `discountRules` editor. The manual feature/tier-limit editors remain as a fallback.
- Backend already persists features on create/update via `upsertFeatures`.

---

## 5. Service-side gating & limit enforcement

### Implemented
- **shared-auth-client hardening** (`shared/auth-client/middleware.go`): `RequireFeature`,
  `RequireAnyFeature`, `RequirePlan` now bypass **platform owner / superuser / demo /
  service-charge** (previously superuser-only — would have wrongly blocked demo/service-charge
  tenants). _Takes effect after a `shared-auth-client` release + consumer bump._
- **Treasury feature gating (was ZERO):** new `treasury-api/internal/platform/subscriptions/gate.go`
  (local, correct bypasses regardless of pinned shared version) applied in the router:
  ledger → `ledger_posting`, invoicing → `invoice_generation`, AR/AP → `ar_tracking`,
  reconciliation → `reconciliation`, tax/eTIMS → `tax_codes`.
- **Inventory `max_warehouses` enforcement (critical gap):** `CreateWarehouse` now counts
  existing warehouses and `AssertLimit("max_warehouses", n)` → 402 when at/over plan limit.
  New local `inventory-api/internal/platform/subscriptions/gate.go` (`CheckLimit`/`AssertLimit`).

### Remaining (cross-service / cross-release — mechanical with the gates now in place)
1. **Release `shared-auth-client`** and bump `auth-client` in all consumers so the hardened
   bypass logic propagates platform-wide.
2. **Usage reporting → `usage_events`.** No service currently `POST`s to
   `/api/v1/usage/report`, so metered limits (`api_calls_per_month`,
   `max_transactions_per_month`, `*_per_day`) are never counted and the Redis enforcement in
   `usage.go` never trips. Wire reporting at the call sites (or via a NATS usage consumer in
   subscriptions-api keyed off the catalog's `nats_event` column — e.g.
   `inventory.warehouse.created`, `ordering.order.created`).
3. **Schedule overage accrual.** `internal/modules/billing/overage_service.go`
   `CalculateDailyOverages` is fully implemented (reads `usage_events`, writes
   `overage_charge`, rolled into renewal invoices) but is **not scheduled** and depends on
   (2). Add a daily worker/cron (~00:05 UTC).
4. **Remaining capacity limits** (now one-liners via the local gates): pos
   `max_devices`/`max_cashiers`/`max_tables`/`max_rooms`; ordering
   `max_admins`/`max_riders`/`max_outlets`/`max_staff`; inventory `max_products`/`max_suppliers`.

---

## 6. Reconciliation status (catalog ↔ seeds ↔ gates)

- **Catalog ↔ plan seeds:** complete — every seeded feature/limit code has a catalog entry
  (verified by static diff; `paystack_gateway` aliased to `paystack_integration`).
- **Gates ↔ catalog:** treasury/inventory keys gated now match canonical catalog codes.
- **Discount-rule format mismatch (follow-up):** the UI emits
  `{type: ANNUAL_DISCOUNT, value}` while `plans.applyDiscountRules` switches on
  `type: "YEARLY"` + `percentage`. Editor persists rules; reconcile the pricing calc format
  in a follow-up.

---

## 7. Verification

- Go builds: subscriptions-api, auth-api, treasury-api, inventory-api, shared/auth-client — all green.
- `subscriptions-ui` `tsc --noEmit` — clean.
- DB-dependent checks (run against a dev DB): `go run ./cmd/migrate` then
  `go run ./cmd/seed` (catalog seeds first; watch for `WARN seed: … not in the feature catalog`),
  then `GET /api/v1/features/catalog?service=treasury`.
- Frontend: log in as a branded tenant and confirm the subscription bar shows the tenant
  plan + brand accent identically across treasury/inventory/pos/ordering.
