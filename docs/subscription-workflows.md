# Subscription System — Workflows & Architecture Reference

> **Service:** `subscriptions-api` (Go + Ent ORM + NATS + Redis + PostgreSQL)
> **UI:** `subscriptions-ui` (Next.js App Router + TanStack Query v5)
> **Last updated:** 2026-05-30

---

## Table of Contents

1. [Architecture Overview](#1-architecture-overview)
2. [Subscription States](#2-subscription-states)
3. [New Tenant Auto-Provisioning](#3-new-tenant-auto-provisioning)
4. [Trial → First Payment → ACTIVE](#4-trial--first-payment--active)
5. [Monthly / Annual Renewal](#5-monthly--annual-renewal)
6. [Payment Failure & Dunning](#6-payment-failure--dunning)
7. [Auto-Renew Toggle](#7-auto-renew-toggle)
8. [Usage Tracking (22 Subjects)](#8-usage-tracking-22-subjects)
9. [Feature Gating](#9-feature-gating)
10. [Service Charges (Dual Revenue)](#10-service-charges-dual-revenue)
11. [Coupon Redemption & Credit Wallet](#11-coupon-redemption--credit-wallet)
12. [Purchasable Addons](#12-purchasable-addons)
13. [Custom Tenant Addons (Admin-Managed)](#13-custom-tenant-addons-admin-managed)
14. [Platform Admin — Tenant Management](#14-platform-admin--tenant-management)
15. [codevertex-demo Bypass](#15-codevertex-demo-bypass)
16. [codevertex Platform Owner — No Subscription](#16-codevertex-platform-owner--no-subscription)
17. [Frontend ↔ Backend API Map](#17-frontend--backend-api-map)
18. [Seeded Data Reference](#18-seeded-data-reference)

---

## 1. Architecture Overview

```
┌────────────────────────────────────────────────────────────────────┐
│                        subscriptions-ui                             │
│  Next.js App Router · TanStack Query v5 · Zustand · shadcn/ui      │
│                                                                     │
│  pages/                                                             │
│   /plans           /billing        /settings      /usage            │
│   /platform/tenants    /platform/subscriptions    /platform/coupons │
│   /platform/tenants/[id]   /platform/configs                        │
│                                                                     │
│  lib/api/*.ts → apiClient (Axios + X-Tenant-ID header injection)    │
│  hooks/use*.ts → TanStack Query (cache key includes tenantId)       │
└──────────────────────┬─────────────────────────────────────────────┘
                       │  HTTP (Axios + JWT Bearer + X-Tenant-ID)
                       ▼
┌──────────────────────────────────────────────────────────────────┐
│                    subscriptions-api (Go)                         │
│  chi router · Ent ORM · httpware middleware                       │
│                                                                   │
│  Handlers: subscription, billing, feature, usage, addon          │
│            coupon, plan, service_charge, config, admin            │
│                                                                   │
│  Jobs:  renewal.go (expiry + renewal, hourly)                     │
│  Consumers: payment_consumer  tenant_created  usage_consumer      │
└──┬────────────────┬──────────────────────┬───────────────────────┘
   │                │                      │
   ▼                ▼                      ▼
PostgreSQL       Redis (counters)      NATS JetStream
(Ent ORM)        usage buckets         pub/sub + outbox
                 feature cache
                 subscription cache
```

---

## 2. Subscription States

```
  [NEW TENANT] ──auto-provision──▶ TRIAL
                                      │
                              trial_ends_at
                              approaches in
                              24h window
                                      │
                     auto_renew=false │ auto_renew=true (default)
                                ▼     ▼
                            EXPIRED   Treasury payment intent
                                          │
                             payment   payment
                             failed    succeeded
                               │          │
                               ▼          ▼
                           SUSPENDED    ACTIVE ◀─── admin override
                               │          │
                     max retries │        │ period_end
                     exceeded    │        │ approaches
                               ▼          ▼
                           CANCELLED   renewal cycle →
```

| State | Description |
|---|---|
| `TRIAL` | Free trial; full feature access; `trial_ends_at` is the expiry |
| `ACTIVE` | Paid and active; `current_period_end` is the next billing date |
| `EXPIRED` | Period ended, no renewal initiated or payment not started |
| `SUSPENDED` | Payment failed; dunning in progress; feature access restricted |
| `CANCELLED` | Tenant cancelled or max dunning attempts exhausted |

---

## 3. New Tenant Auto-Provisioning

```
[auth-api] tenant created
      │
      │ publishes: auth.tenant.created
      │   { tenant_id, slug, name }
      ▼
[NATS JetStream]
      │
      │ durable consumer: subscription-service-tenant-provisioner
      ▼
[TenantCreatedConsumer.handle()]
      │
      ├─ Looks up STARTER plan (DefaultStarterPlanCode = "STARTER")
      │
      └─ Calls service.CreateSubscription({
              TenantID,
              PlanCode:   "STARTER",
              BundleCode: "delivery",
              TrialDays:  14,
         })
              │
              └─▶ TenantSubscription row created
                    status = TRIAL
                    trial_ends_at = now + 14 days
                    current_period_end = trial_ends_at
```

> **Frontend:** `/plans` page allows tenants to select a different plan at onboarding.
> Selecting a plan calls `POST /subscription` with `{ plan_code, billing_cycle, tenant_id? }`.

---

## 4. Trial → First Payment → ACTIVE

```
TRIAL subscription
  │
  │  (renewal job runs every hour; offset 30 min from expiry job)
  ▼
[renewal.go: initiateRenewals()]
  │
  ├─ Queries TRIAL/ACTIVE subs expiring within 24h
  │
  ├─ Checks auto_renew in metadata (missing = true, explicit false = skip)
  │
  ├─ FREE plan? ──yes──▶ Extend period by 30 days (no payment)
  │
  └─ PAID plan? ──yes──▶ POST /api/v1/{tenant_id}/payments/intents
                          {
                            amount:         plan.base_price,
                            currency:       "KES",
                            payment_method: "auto",    ← Paystack saved card
                            reference_type: "renewal",
                            plan_code:      plan.plan_code,
                            source_service: "subscription-service",
                          }
                               │
                               │ Treasury creates Paystack charge
                               ▼
                     [Paystack processes card]
                               │
                      success  │  failure
                         ┌─────┘  └─────┐
                         ▼              ▼
              treasury.payment      treasury.payment
              .succeeded            .failed
                         │              │
                         ▼              ▼
             [payment_consumer]   [dunning flow]
                         │
                         ├─ svc.RenewSubscription()
                         │      → status = ACTIVE
                         │      → current_period_start = now
                         │      → current_period_end = now + cycle
                         │
                         └─ creditService.EarnLoyaltyCredits()
                                → 5% of payment as KES credits
```

---

## 5. Monthly / Annual Renewal

```
ACTIVE subscription approaching current_period_end
  │
  ▼
[expiry.go: expireSubscriptions()] ── runs every hour ──▶
  │  Marks ACTIVE subs past period_end as EXPIRED
  │  Publishes outbox event: subscription.expired
  │
  ▼
[renewal.go: initiateRenewals()] ── runs every hour (offset 30m) ──▶
  │  Finds subs expiring within 24h window
  │  Same flow as Trial → ACTIVE (see §4)
  │
  ▼
Payment succeeded ──▶ RenewSubscription()
  │  Updates: status=ACTIVE, period advanced by billing_cycle
  │  Publishes outbox event: subscription.renewed
  │
  ▼
Tenant continues using platform uninterrupted
```

**Billing cycle periods:**

Every recurring plan is priced **per month** (`base_price`); the tenant chooses the billing
period at subscribe/renew time (`billing_cycle` on the tenant subscription — there are no
separate per-cycle plan rows; the legacy `*_YEARLY` rows are retired by the seed). The
period price is exactly `months × base_price` — no automatic discount.

| Cycle | Months | Period added | One-time setup fee |
|---|---|---|---|
| `MONTHLY` | 1 | +1 month | Charged on first invoice/checkout |
| `QUARTERLY` (legacy) | 3 | +3 months | Charged on first invoice/checkout |
| `SEMI_ANNUAL` | 6 | +6 months | **WAIVED** (≥ 6 months) |
| `ANNUAL` | 12 | +12 months | **WAIVED** (≥ 6 months) |
| `ONE_TIME` | — | No renewal (perpetual) | Per-plan (licence fee) |

**Setup-fee waiver (`internal/modules/subscriptions/billing_cycle.go`):** paying for
`SetupFeeWaiverMonths` (6) or more months up front waives the one-time setup/installation
fee entirely — it never appears on the invoice or checkout total. The waiver is stamped in
subscription metadata (`setup_fee_waived`, `setup_fee_waived_amount`,
`setup_fee_waiver_reason`) and `setup_fee_amount` is zeroed. **Discount exclusivity:** a
subscription with an active waiver gets no other special discount — coupon redemption is
rejected for it; manual discounts/coupons apply only where the waiver does not.

**Changing the period:** `PUT /subscription/billing-cycle { billing_cycle }` switches the
period effective next renewal (and applies the waiver immediately when the fee is still
uncharged). At checkout, `POST /subscription/initiate { plan_code, billing_cycle }` binds
the choice to the created payment intent via `pending_*` subscription metadata;
`RenewSubscription` applies it only when the paying intent matches (`pending_intent_id`),
so an abandoned checkout can never change the cycle of a later payment.

---

## 6. Payment Failure & Dunning

```
Treasury reports payment failure
  │
  │ publishes: treasury.payment.failed
  │   { tenant_id, reference_type: "renewal", intent_id }
  ▼
[payment failure consumer / webhook]
  │
  ├─ Sets subscription.status = SUSPENDED
  ├─ Increments metadata.dunning_attempt (0 → 1)
  └─ Publishes outbox event: subscription.payment_failed

                    SUSPENDED
                       │
         ┌─────────────┼─────────────┐
         │             │             │
      attempt 1     attempt 2     attempt 3
      day +1        day +3        day +7
         │             │             │
         └─────────────┴──────┬──────┘
                              │
                    payment   │   still failing
                    succeeds  │   after attempt 3
                       │      ▼
                       ▼   CANCELLED
                    ACTIVE    │
                              └─▶ outbox: subscription.cancelled
```

**Frontend dunning banner** (billing page):
- Status `SUSPENDED` → shows "Payment failed. Retry on [date]. Attempt X of 3."
- CTA: "Update Payment Method" → triggers Paystack card setup flow

---

## 7. Auto-Renew Toggle

```
Frontend: /settings page
  │
  │  PUT /subscription/settings
  │  { autoRenew: false }
  │  X-Tenant-ID: <tenant_id>
  ▼
[subscription handler: UpdateSettings()]
  │
  └─ Stores: metadata["auto_renew"] = false

At next renewal cycle:
  [renewal.go: initiateRenewals()]
    │
    ├─ Reads metadata["auto_renew"]
    │    • nil / missing → renew (default true)
    │    • explicit false → skip renewal
    │
    └─ If false: log + continue loop, no Treasury intent created
```

> **Warning:** If `auto_renew=false` AND no payment initiated, the subscription will
> transition to EXPIRED via the expiry job after `current_period_end` passes.

---

## 8. Usage Tracking (22 Subjects)

```
[microservice emits billable event]
  │
  │ e.g.: ordering.order.created { tenant_id, order_id, ... }
  │       auth.user.created       { tenant_id, roles: ["rider"] }
  │       pos.transaction.created { tenant_id, ... }
  ▼
[NATS JetStream]
  │
  │ durable consumer: subscription-service-usage-tracker
  ▼
[UsageConsumer.handle()]
  │
  ├─ Maps subject → metric_type (usageSubjectMappings)
  │   "ordering.order.created"      → metric: "orders"
  │   "auth.user.created" + rider   → metric: "riders"
  │   "pos.transaction.created"     → metric: "transactions"
  │   "logistics.delivery.created"  → metric: "deliveries"
  │   ... (22 total subjects)
  │
  ├─ Increments Redis counter:
  │   key: usage:{tenant_id}:{metric_type}:{period_start}
  │   INCRBY 1 with TTL
  │
  └─ INSERT usage_event row into PostgreSQL (audit trail)

[Tenant queries GET /usage]
  │
  ├─ Reads Redis counters (fast path)
  ├─ Compares against plan tier_limits
  └─ Returns { metrics: [{ name, key, used, limit, unit, resetDate }] }
```

**Tracked Metrics by Service:**

| Subject | Metric | Service |
|---|---|---|
| `ordering.order.created` | orders | ordering |
| `cafe.order.created` | orders | cafe |
| `pos.transaction.created` | transactions | pos |
| `pos.device.registered` | devices | pos |
| `pos.table.created` | tables | pos |
| `inventory.product.created` | products | inventory |
| `inventory.warehouse.created` | warehouses | inventory |
| `logistics.delivery.created` | deliveries | logistics |
| `logistics.fleet.member_invited` | riders | logistics |
| `logistics.task.eta_updated` | tracking_requests | logistics |
| `truload.shipment.created` | deliveries | truload |
| `auth.user.created` (role-aware) | staff / admins / riders | auth |
| `auth.outlet.created` | outlets | auth |
| `notifications.sms.sent` | sms_sent | notifications |
| `notifications.email.sent` | emails_sent | notifications |
| `notifications.push.sent` | push_sent | notifications |
| `marketflow.campaign.created` | campaigns | marketflow |

**Direct-report metrics (no NATS subject mapping)**: `etims_transactions` (2026-08-18) is reported
by treasury-api calling `POST /usage/report` directly, one call per successfully-signed external
eTIMS sale (`internal/platform/subscriptions.Client.ReportUsage` in treasury-api) — there is no
NATS event for it, since a signed sale to an external API-only consumer has no corresponding
domain event any other service would need to react to. It flows into the exact same
`meteredMetrics`/`OverageCharge` pipeline as every subject-mapped metric above; only the ingestion
path differs.

---

## 9. Feature Gating

```
[microservice] needs to check feature access
  │
  │ GET /api/v1/features/{feature_code}/check
  │     X-API-Key: INTERNAL_SERVICE_KEY
  │     X-Tenant-ID: <tenant_id>
  │     (or JWT token)
  ▼
[FeatureHandler.CheckFeature()]
  │
  ├─ Check Redis cache: features:{tenant_id}:{feature_code}
  │   Cache HIT → return cached result (TTL: 5 min)
  │
  ├─ Cache MISS:
  │   ├─ Query TenantSubscription for tenant
  │   ├─ If status = SUSPENDED → most features locked
  │   ├─ If isDemoTenant(slug) → all features allowed
  │   │
  │   ├─ Query PlanFeature for (plan_id, feature_code, is_included=true)
  │   │   → Feature exists → allowed
  │   │   → Feature missing → denied
  │   │
  │   └─ Write result to Redis (TTL 5 min)
  │
  └─ Returns { allowed: true/false, reason: "..." }

[FeatureHandler.ListFeatures()]
  │
  └─ Returns all features for tenant's current plan
     including: addon features (is_included=false) + purchased status
```

**Cache invalidation:** `InvalidateCache(ctx, tenantID)` is called on:
- Subscription status change (activate, suspend, cancel)
- Plan change
- Addon purchase / removal
- Admin override

---

## 10. Service Charges (Dual Revenue)

```
Dual revenue model:
  ┌──────────────────────────────┐
  │  Monthly subscription fee    │ ← KES 2,500–12,500/mo (Paystack)
  └──────────────────────────────┘
  ┌──────────────────────────────┐
  │  Per-transaction charge      │ ← % or flat per treasury transaction
  └──────────────────────────────┘

[treasury-api processes a transaction]
  │
  │ S2S call: GET /api/v1/service-charges/for-tenant
  │           X-API-Key: INTERNAL_SERVICE_KEY
  │           X-Tenant-ID: <tenant_id>
  │           ?service=ordering
  ▼
[ServiceChargeHandler.GetForTenant()]
  │
  ├─ Lookup TenantSubscription for tenant
  ├─ Look for explicit tenant-level charge assignment
  │   (tenant_service_charges table)
  ├─ Fallback to: default service charge plan for the service
  │
  └─ Returns: { charge_type, charge_value, min_charge, max_charge }

[treasury-api applies charge]
  │
  └─ Deducts platform cut from transaction
     e.g. SC_ORDERING_5PCT → 5% of order value, min KES 50, max KES 5,000
```

**Seeded Service Charge Plans:**

| Code | Type | Value | Service | Default |
|---|---|---|---|---|
| `SC_ORDERING_5PCT` | PERCENTAGE | 5% | ordering | ✓ |
| `SC_ORDERING_3PCT` | PERCENTAGE | 3% | ordering | – |
| `SC_LOGISTICS_7PCT` | PERCENTAGE | 7% | logistics | ✓ |
| `SC_POS_2PCT` | PERCENTAGE | 2% | pos | ✓ |
| `SC_UNIVERSAL_FLAT_50` | FLAT | KES 50 | all | – |
| `SC_TRULOAD_10PCT` | PERCENTAGE | 10% | truload | ✓ |
| `SC_MARKETFLOW_ADS` | PERCENTAGE | varies | marketflow | ✓ |
| `SC_MARKETFLOW_AI_CREDIT` | FLAT | varies | marketflow | – |

---

## 11. Coupon Redemption & Credit Wallet

```
[Tenant] enters coupon code on /billing page
  │
  │ POST /subscription/coupon/redeem
  │ { code: "WELCOME20" }
  │ X-Tenant-ID: <tenant_id>
  ▼
[CouponHandler.RedeemCoupon()]
  │
  ├─ Look up coupon by code
  ├─ Validate: is_active, not expired, max_redemptions not reached
  ├─ Validate: applicable to tenant's current plan (applicable_plan_codes[])
  │
  ├─ Calculate credit value:
  │   PERCENTAGE → current_plan_price × (value/100)
  │   FIXED_KES  → value KES directly
  │   FREE_MONTHS → n × monthly_plan_price
  │
  ├─ creditService.AddCredits(ctx, tenantID, amountKes, "coupon", ...)
  │   INSERT credit_wallet_transaction + UPDATE credit_wallet balance
  │
  ├─ Increment coupon.current_redemptions
  │
  └─ Returns { message, credits_earned, new_balance }

[At next billing cycle]
  │
  │ GET /billing/invoice-preview
  ▼
[BillingHandler.GetInvoicePreview()]
  │
  ├─ Get credit_wallet.balance_kes
  ├─ Compute estimated_total = base_price + overages + addons
  ├─ credits_to_apply = min(credits, estimated_total)
  └─ Returns: { estimated_total_kes, credits_to_apply_kes, ... }
```

**Loyalty Credits (earned on payment):**

```
[payment_consumer: treasury.payment.succeeded]
  │
  └─ creditService.EarnLoyaltyCredits(ctx, tenantID, amountKes, intentID)
       → credits = amountKes * 0.05 (5% loyalty rate)
       → INSERT credit_wallet_transaction, type="loyalty"
```

---

## 12. Purchasable Addons

Addons are `PlanFeature` records with `is_included=false` that tenants can purchase individually.

```
[Tenant] views /billing page → Addons section
  │
  │ GET /addons
  │ X-Tenant-ID: <tenant_id>
  ▼
[AddonHandler.ListAddons()]
  │
  ├─ Get tenant's plan_id from TenantSubscription
  ├─ Query PlanFeature WHERE plan_id=? AND is_included=false
  ├─ Check metadata["purchased_addons"] for already-purchased codes
  └─ Returns: [{ feature_code, plan_id, limit_value, overage_unit_price, purchased }]

[Tenant purchases addon]
  │
  │ POST /addons/{feature_code}/purchase
  │ { return_url: "..." }        ← optional, for payment redirect
  ▼
[AddonHandler.PurchaseAddon()]
  │
  ├─ Validate addon exists for plan (is_included=false)
  ├─ Check not already purchased (metadata["purchased_addons"])
  │
  ├─ FREE addon (overage_unit_price == 0)?
  │   └─▶ activateAddon() → metadata["purchased_addons"] += [feature_code]
  │        InvalidateCache() → feature check immediately reflects new addon
  │
  └─ PAID addon?
       └─▶ POST /api/v1/{tenant_id}/payments/intents
              {
                amount: feature.overage_unit_price,
                reference_type: "addon_purchase",
                metadata: { feature_code, addon: true }
              }
             Returns: { status: "payment_required", intent: { ... } }

[After payment success — treasury.payment.succeeded event]
  │  reference_type = "addon_purchase"
  ▼
[payment_consumer or webhook handler]
  └─ activateAddon() → metadata["purchased_addons"] += [feature_code]
     metadata["addon_activated_at_{code}"] = now
     InvalidateCache()
```

**Remove addon:** `DELETE /addons/{feature_code}` — removes from `metadata["purchased_addons"]`, invalidates cache.

---

## 13. Custom Tenant Addons (Admin-Managed)

Unlike plan addons (§12) which are self-service, custom addons are bespoke line items added by platform admins per-tenant (e.g. dedicated support, custom integration hours, hardware).

**Real example (2026-08-18)**: the one-time eTIMS assisted-integration fee (`billing_cycle:
"one_time"`, `unit_price_kes: 35000|85000|180000`, `service_code: "treasury"`,
`service_addon_type: "etims_integration"`) — created manually once a platform admin approves an
`IntegrationRequest` (owned by auth-api) with `integration_mode: "assisted"`. Skipped entirely for
`self_serve` requests, which pay only ongoing usage (§8, `etims_transactions`).

```
[Platform admin] on /platform/tenants/[id] page
  │
  │ POST /admin/tenants/{tenant_id}/custom-addons
  │ {
  │   name: "Dedicated Support",
  │   billing_cycle: "monthly",
  │   unit_price_kes: 5000,
  │   quantity: 1,
  │   notes: "SLA commitment Q3 2026"
  │ }
  ▼
[CustomAddonHandler.CreateAddon()]
  │
  └─ Creates custom_addon row linked to tenant
     Status: ACTIVE
     Appears in GET /billing/invoice-preview as addons_total_kes line items

[At billing time]
  └─ BillingHandler includes active custom addons in invoice preview + charge
```

---

## 14. Platform Admin — Tenant Management

```
[Platform admin] — has JWT claim is_platform_owner=true

Top-nav tenant filter (Zustand: useTenantFilterStore)
  │  selectedTenant = { id: "...", slug: "...", name: "..." }
  │
  │  apiClient injects header: X-Tenant-ID: <selectedTenant.id>
  ▼
[subscriptions-api: resolveTenantID(r)]
  │
  ├─ IsPlatformOwner(ctx) = true
  │   ├─ X-Tenant-ID header present? → return it (managing selected tenant)
  │   ├─ ?tenantId= query param?     → return it (legacy)
  │   └─ Neither?                    → return "" (platform-wide view)
  │
  └─ IsPlatformOwner(ctx) = false (regular tenant)
      ├─ X-Tenant-ID header present? → return it (explicit)
      └─ Neither?                    → httpware.GetTenantID(ctx) (JWT claims)
```

**Admin-only routes** (middleware: `requirePlatformOwner`):

```
GET  /admin/tenants                      → list all tenants
GET  /admin/subscriptions                → list all subscriptions (with dunning state)
GET  /admin/plans                        → list/manage all plans
PUT  /admin/plans/{id}                   → update plan (price, limits, trial, features)
GET  /admin/tenants/{id}/custom-addons   → list tenant custom addons
POST /admin/tenants/{id}/custom-addons   → create custom addon
PATCH /admin/tenants/{id}/custom-addons/{aid} → update addon
POST /admin/tenants/{id}/subscription/extend-trial → extend trial end date
POST /admin/tenants/{id}/credits/gift    → gift KES credits
GET  /admin/usage/tenants/{id}           → view tenant usage metrics
PUT  /admin/usage/tenants/{id}/override  → override a usage counter (NATS correction)
GET  /admin/coupons                      → list coupons
POST /admin/coupons                      → create coupon
PATCH /admin/coupons/{id}               → update coupon
DELETE /admin/coupons/{id}              → delete coupon
GET  /admin/configs                      → list service configs
POST/PUT/DELETE /admin/configs           → manage configs
GET  /platform/stats                     → MRR, active tenants, churn, trial count
```

---

## 15. codevertex-demo Bypass

```
Request from tenant with slug = "codevertex-demo"
  OR JWT claim is_demo = true
  │
  ▼
[subscription.go: isDemoTenant()]
  │
  ├─ Matches slug "codevertex-demo" OR is_demo=true in JWT
  │
  └─ Returns synthetic subscription:
       {
         id: "demo-subscription-id",
         plan_code: "DEMO_UNLIMITED",
         status: "ACTIVE",
         features: [ALL features],
         limits: { all: -1 },   ← unlimited
       }
       → No DB record required
       → Bypasses all feature gating
       → Bypasses all usage limits
```

---

## 16. codevertex Platform Owner — No Subscription

```
Request from tenant with slug = "codevertex"
  AND JWT claim is_platform_owner = true
  │
  ▼
[httpware middleware: IsPlatformOwner()]
  │
  └─ Platform owner has unrestricted access without a subscription record.
     Routes gated by requirePlatformOwner middleware are accessible.
     Routes that require tenant-specific data use resolveTenantID():
       → If X-Tenant-ID present → acts on that tenant
       → If absent → platform-wide view (admin listings, stats)
```

---

## 17. Frontend ↔ Backend API Map

| Page | Hook | API Function | Backend Endpoint |
|---|---|---|---|
| `/plans` | `usePlans()` | `lib/api/plans.ts: getPlans()` | `GET /plans` |
| `/billing` | `useBilling()` | `lib/api/billing.ts: getBilling()` | `GET /billing` |
| `/billing` | `useInvoicePreview()` | `getInvoicePreview()` | `GET /billing/invoice-preview` |
| `/billing` | `useCreditWallet()` | `getCreditWallet()` | `GET /billing/credits` |
| `/billing` | `useRedeemCoupon()` | `redeemCoupon()` | `POST /subscription/coupon/redeem` |
| `/billing` | `useSetupPaymentMethod()` | `setupPaymentMethod()` | `POST /subscription/payment-method/setup` |
| `/settings` | inline query | `apiClient.get('/subscription/settings')` | `GET /subscription/settings` |
| `/settings` | inline mutation | `apiClient.put('/subscription/settings')` | `PUT /subscription/settings` |
| `/usage` | `useUsage()` | `lib/api/usage.ts: getUsage()` | `GET /usage` |
| `/platform/subscriptions` | `useAdminSubscriptions()` | `getAdminSubscriptions()` | `GET /admin/subscriptions` |
| `/platform/tenants` | `useAdminTenants()` | `getAdminTenants()` | `GET /admin/tenants` |
| `/platform/tenants/[id]` | `useAdminTenantAddons()` | `getAdminTenantAddons(id)` | `GET /admin/tenants/{id}/custom-addons` |
| `/platform/tenants/[id]` | `useAdminTenantUsage()` | `getAdminTenantUsage(id)` | `GET /admin/usage/tenants/{id}` |
| `/platform/coupons` | `useCoupons()` | `listCoupons()` | `GET /admin/coupons` |
| `/platform/configs` | `useConfigs()` | `getConfigs()` | `GET /admin/configs` |

**TanStack Query cache key pattern:**

```typescript
// Tenant-scoped: always include tenantKey in cache key
const selectedTenant = useTenantFilterStore(s => s.selectedTenant)
const tenantKey = selectedTenant?.id ?? null

useQuery({ queryKey: ['billing', tenantKey], ... })
useQuery({ queryKey: ['credit-wallet', tenantKey], ... })

// Platform-wide admin listings: no tenant key (always global)
useQuery({ queryKey: ['admin-subscriptions'], ... })
useQuery({ queryKey: ['admin-tenants'], ... })
```

---

## 18. Seeded Data Reference

### Tenant Subscriptions

| Tenant | Slug | Plan | Billing | Status | Trial Period |
|---|---|---|---|---|---|
| Masterspace Solutions | `mss` | ORDERING_GROWTH | MONTHLY | TRIAL | 14 days |
| Kenya Urban Roads Authority | `kura` | TRULOAD_STARTER | MONTHLY | TRIAL | 14 days |
| UltiChange | `ultichange` | ORDERING_STARTER | MONTHLY | TRIAL | 14 days |
| Codevertex | `codevertex` | — | — | Platform owner (unrestricted) | — |
| Demo Tenant | `codevertex-demo` | — | — | Demo bypass (synthetic ACTIVE) | — |

### Subscription Plans

| Plan Code | Price (KES/mo) | Trial | Description |
|---|---|---|---|
| ORDERING_STARTER | 2,500 | 30 days | Core ordering, 1 outlet, 5 riders, 300 orders/day |
| ORDERING_GROWTH | 6,000 | 14 days | Multi-outlet, 15 riders, 1,000 orders/day, advanced analytics |
| ORDERING_PROFESSIONAL | 12,500 | 14 days | Enterprise, unlimited outlets, unlimited admins |
| ORDERING_STARTER_YEARLY | 27,500 | 30 days | Annual billing (≈ 10 months price) |
| ORDERING_GROWTH_YEARLY | 66,000 | 14 days | Annual billing |
| TRULOAD_STARTER | 2,500 | 14 days | TruLoad logistics org — basic |
| TRULOAD_GROWTH | 6,000 | 14 days | TruLoad with fleet management |
| TRULOAD_PROFESSIONAL | 12,500 | 14 days | TruLoad enterprise |

### Coupons

| Code | Type | Value | Applicable Plans | Max Redemptions |
|---|---|---|---|---|
| `WELCOME20` | PERCENTAGE | 20% off | All | 1 per tenant |
| `ANNUAL10` | PERCENTAGE | 10% off | Annual plans only | Unlimited |
| `DEMO_FREE` | FREE_MONTHS | 1 month free | All | 1 per tenant |
| `STARTER500` | FIXED_KES | KES 500 | STARTER plans | 100 |
| `GROWTH1000` | FIXED_KES | KES 1,000 | GROWTH plans | 50 |

### Service Charge Plans

| Code | Type | Rate | Service | Default |
|---|---|---|---|---|
| `SC_ORDERING_5PCT` | PERCENTAGE | 5% (min KES 50, max KES 5,000) | ordering | ✓ |
| `SC_ORDERING_3PCT` | PERCENTAGE | 3% | ordering | – |
| `SC_LOGISTICS_7PCT` | PERCENTAGE | 7% | logistics | ✓ |
| `SC_POS_2PCT` | PERCENTAGE | 2% | pos | ✓ |
| `SC_UNIVERSAL_FLAT_50` | FLAT | KES 50 | all | – |
| `SC_TRULOAD_10PCT` | PERCENTAGE | 10% | truload | ✓ |
| `SC_MARKETFLOW_ADS` | PERCENTAGE | varies | marketflow | ✓ |
| `SC_MARKETFLOW_AI_CREDIT` | FLAT | varies | marketflow | – |

---

## Appendix: Key Metadata Fields on TenantSubscription

The `metadata` JSON column on `tenant_subscriptions` stores dynamic subscription state:

| Key | Type | Description |
|---|---|---|
| `auto_renew` | bool | `false` = skip renewal; missing = `true` (default) |
| `paystack_auth_code` | string | Saved card authorization for auto-charge |
| `payment_method` | object | `{ type, brand, last4, expiryMonth, expiryYear }` |
| `purchased_addons` | []string | Feature codes of self-service addons purchased |
| `addon_activated_at_{code}` | RFC3339 | When a specific addon was activated |
| `dunning_attempt` | int | Current dunning retry count (1–3) |
| `billing_email` | string | Override billing email (fallback: JWT claims email) |
| `seeded` | bool | Internal flag indicating row was created by seed script |
| `tier` | string | Plan code snapshot at seed time |
| `tenant_name` | string | Tenant display name snapshot |
