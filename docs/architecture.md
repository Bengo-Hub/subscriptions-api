# Subscription API — Architecture

**Last Updated:** May 22, 2026

## System Context

The Subscription API (`subscriptions-api`) is the centralized licensing, feature-gating, and subscription management backend for the Codevertex platform. It powers the **Trinity Authorization** model used across all services:

```
Authorization = RBAC (auth-service) + Licensing (subscription-service) + Resources (domain services)
```

**Production URL**: `https://pricingapi.codevertexafrica.com`
**Local Port**: `4005` (cloud: `4000`)
**Canonical Tenant**: `urban-loft` (ID: `11111111-2222-3333-4444-555555555555`)

---

## Architecture Style

Clean/Hexagonal architecture with a modular Go project layout:

```
subscriptions-api/
├── cmd/
│   ├── api/main.go            # HTTP server entrypoint
│   └── seed/main.go           # Idempotent data seeder
├── internal/
│   ├── app/app.go             # Application bootstrap & wiring
│   ├── config/                # Env-based configuration (envconfig)
│   ├── domain/
│   │   └── service_tags.go    # Canonical service tag constants (10 tags)
│   ├── ent/                   # Ent ORM generated code + schemas
│   │   └── migrate/
│   │       └── migrations/    # Atlas versioned migration SQL files
│   ├── http/
│   │   ├── handlers/          # HTTP handlers (plans, subscriptions, billing, etc.)
│   │   └── router/router.go   # chi router with auth middleware
│   ├── modules/
│   │   ├── plans/             # Plan catalog repository
│   │   ├── rbac/              # RBAC: roles, permissions, user-role assignments
│   │   ├── subscriptions/     # Subscription lifecycle service + state machine
│   │   └── outbox/            # Outbox event repository
│   ├── platform/
│   │   ├── cache/             # Redis client init
│   │   ├── database/          # pgxpool init
│   │   └── events/            # NATS connection + JetStream stream setup
│   └── shared/
│       └── logger/            # Zap logger init
├── docs/                      # This documentation
├── Dockerfile                 # Multi-stage build
└── go.mod
```

---

## Core Components

### 1. Subscription Lifecycle Service

**Location**: `internal/modules/subscriptions/service.go`

State machine governing tenant subscription transitions:

```
TRIAL ──→ ACTIVE ──→ SUSPENDED ──→ CANCELLED
  │          │          │
  │          ├──→ CANCELLED
  │          └──→ EXPIRED
  └──→ EXPIRED
  └──→ CANCELLED
```

Operations: `CreateSubscription`, `ChangePlan`, `CancelSubscription`, `RenewSubscription`, `SwitchPlan`

Every mutation writes an **outbox event** atomically within the same transaction.

### 2. Plan Catalog

**Location**: `internal/modules/plans/`

Read-only repository over Ent's `SubscriptionPlan` and `PlanFeature` entities. Plans are organized by **service_tag** — each plan belongs to exactly one billable service. Serves the plan catalog to both the admin UI and consumer services via public (no-auth) endpoints.

### 3. Outbox Publisher

**Location**: `internal/modules/outbox/` + `shared-events` v0.2.0 library

Polls the `outbox_events` table for `PENDING` events, publishes to NATS JetStream, marks as `PUBLISHED`. Retry with exponential backoff on failure. NATS subject format: `{aggregate_type}.{event_type}` (e.g. `subscription.subscription.upgraded`, `tenant.subscription.updated`).

### 4. Auth Integration

**Libraries**: `shared-auth-client` (JWT validation + API key), `httpware` (tenant/request-ID middleware)

- Validates JWTs from `sso.codevertexafrica.com` via JWKS
- Supports API key fallback (`X-API-Key: INTERNAL_SERVICE_KEY`) for service-to-service calls
- Extracts `tenant_id` from JWT claims via httpware middleware
- **No local RBAC**: subscription service validates auth via auth-api JWT claims. Plan/config mutations require `IsPlatformOwner`.

---

## Data Layer

### ORM: Ent (schema-as-code) + Atlas Migrations

Entities (current):

| Entity | Table | Purpose |
|--------|-------|---------|
| `SubscriptionPlan` | `subscription_plans` | Plan definitions with `service_tag`, dynamic `plan_type`, `discount_rules`, `tier_limits_json` |
| `PlanFeature` | `plan_features` | Feature flags per plan |
| `PlanPricingHistory` | `plan_pricing_history` | Pricing audit trail |
| `TenantSubscription` | `tenant_subscriptions` | Active subscription per tenant |
| `OutboxEvent` | `outbox_events` | Transactional outbox for NATS publishing |
| `ServiceChargePlan` | `service_charge_plans` | Commission-based pricing models (PERCENTAGE, FIXED_PER_TRANSACTION, TIERED) |
| RBAC tables | `subscriptions_permissions`, `subscriptions_roles`, `role_permissions`, `subscriptions_users`, `user_role_assignments` | Local RBAC for billing management |
| Config tables | `rate_limit_configs`, `service_configs` | DB-driven rate limits and platform config |

> **Note**: `Product`, `Bundle`, and `ProductSubscription` entities were removed. Plans now use `service_tag` directly to identify which billable service they belong to.

### Service Tags

10 canonical service tags defined in `internal/domain/service_tags.go`:

| Tag | Service |
|-----|---------|
| `ordering` | Ordering backend |
| `pos` | POS API |
| `logistics` | Logistics API |
| `inventory` | Inventory API |
| `erp` | ERP API |
| `treasury` | Treasury API |
| `truload` | TruLoad backend |
| `marketflow` | MarketFlow API |
| `isp_billing` | ISP Billing API |
| `projects` | Projects API |

### Migration Strategy

**Current**: **Atlas versioned migrations** in `internal/ent/migrate/migrations/`. Schema changes must be applied via Atlas, not Ent auto-migrate. Migration files:

| File | Contents |
|------|----------|
| `20260311014015_initial_schema_v3.sql` | Full initial schema |
| `20260320031754_add_service_charge_plan_and_per_service_overrides.sql` | Service charge plans |
| `20260322100158_add_rbac_rate_limit_service_config.sql` | RBAC, rate limits, service configs |
| `20260324063800_reduce_tenant_schema.sql` | Tenant schema simplification |
| `20260521183226_add_service_tag_to_plans.sql` | `service_tag` column on `subscription_plans` |

Migrations run in CI via the `run_migrations.sh` script (Helm pre-upgrade job).

### Database

PostgreSQL 16+ via `pgxpool` (connection pooling) and `database/sql` (for Ent driver).

### Caching

Redis 7+ — used for:
- **Usage rate limiting counters**: `usage:limit:{tenant_id}:{metric_type}:{YYYY-MM}` — incremented atomically on each `POST /usage/report`. If counter exceeds the plan's `rate_limit_config` for the metric, returns `429 Too Many Requests` with `X-RateLimit-Limit`, `X-RateLimit-Remaining`, and `X-RateLimit-Reset` headers.
- **Feature-gate entitlement cache**: `subscription:entitlements:{tenant_id}` and `subscription:feature:{tenant_id}:*` — written by `FeatureHandler`, invalidated via `FeatureHandler.InvalidateCache` after every subscription mutation (create, change plan, cancel, renew, webhook activation/suspension).

Subscription status and feature entitlements are also baked into JWT claims by auth-api at token issuance time, so services can check features from JWT claims without a runtime Redis lookup.

---

## Event Architecture

### Outbound (Published via Outbox → NATS JetStream)

NATS stream: `subscription` (listens on `subscription.*`). Subject format: `{aggregate_type}.{event_type}`.

| Subject | Aggregate Type | Event Type | Trigger |
|---------|---------------|------------|---------|
| `subscription.subscription.created` | subscription | subscription.created | New subscription provisioned |
| `subscription.subscription.activated` | subscription | subscription.activated | Payment confirmed |
| `subscription.subscription.upgraded` | subscription | subscription.upgraded | Plan tier increased |
| `subscription.subscription.downgraded` | subscription | subscription.downgraded | Plan tier decreased |
| `subscription.subscription.cancelled` | subscription | subscription.cancelled | User/system cancellation |
| `subscription.subscription.expired` | subscription | subscription.expired | Period/trial ended |
| `subscription.subscription.renewed` | subscription | subscription.renewed | Subscription renewed |
| `subscription.subscription.suspended` | subscription | subscription.suspended | Manual suspension or payment failure |
| `subscription.subscription.reactivated` | subscription | subscription.reactivated | Suspension lifted |
| `subscription.subscription.payment_required` | subscription | subscription.payment_required | Treasury payment failed |
| `subscription.subscription.renewal_initiated` | subscription | subscription.renewal_initiated | Renewal payment intent created |
| `subscription.addon.purchased` | subscription | addon.purchased | Tenant purchased an add-on feature |
| `tenant.subscription.updated` | tenant | subscription.updated | Any plan change (consistent event for all downstream) |

The `tenant.subscription.updated` event is emitted on **every** plan change (upgrade or downgrade) on the `tenant` aggregate. Downstream services (e.g. auth-api) subscribe to this to invalidate cached JWT claims and reissue tokens with updated subscription fields.

### Background Jobs

**Location**: `internal/jobs/renewal.go`, started from `app.Run()` as goroutines.

| Job | Interval | Logic |
|-----|----------|-------|
| **Expiry Job** | Every 1h | Queries `ACTIVE` subscriptions where `current_period_end < NOW()`. Sets each to `EXPIRED`, publishes `subscription.expired`, invalidates cache. |
| **Renewal Job** | Every 1h (offset 30m) | Queries `ACTIVE` subscriptions expiring within 24h. Free plans (`base_price == 0`): extends period directly, publishes `subscription.renewed`. Paid plans: POSTs to Treasury `/api/v1/payments/intents` with `reference_type: subscription_renewal`, publishes `subscription.renewal_initiated`. |

### Inbound (Consumed from NATS)

| Subject | Action |
|---------|--------|
| `auth.tenant.created` | Auto-assign Starter plan with 14-day trial |
| `treasury.payment.succeeded` | Activate subscription |
| `treasury.payment.failed` | Suspend/initiate dunning |

### Outbox Pattern Flow

```
1. HTTP handler calls service method
2. Service opens Tx → writes domain mutation + outbox_events row → commits
3. Outbox publisher goroutine polls PENDING events (2s interval)
4. Publishes to NATS JetStream using subject = {aggregate_type}.{event_type}
5. Marks event as PUBLISHED
6. Failed publishes: retry with exponential backoff (max 10 attempts)
```

---

## Seed Data Summary

Seeded via `go run cmd/seed/main.go` (idempotent). Deployed as a Helm pre-install/upgrade job (`seed.enabled: true`, `seed.binaryName: seed`).

- **85 Plans**: Distributed across 10 service tags. Each service has plans at STARTER/GROWTH/PROFESSIONAL tiers × MONTHLY/ANNUAL billing cycles (some with ONE_TIME). Plans carry `tier_limits_json` (max_riders, max_orders_per_day, etc.) and associated `plan_features`.
- **Service Charge Plans**: 6 seeded commission plans (e.g. `SC_TRULOAD_10PCT` at 10% for truload, `SC_ORDERING_5PCT` at 5%).
- **Demo Subscription**: Urban Loft tenant on GROWTH plan.

> All 85 plans confirmed live in production as of May 2026.

---

## API Route Map

All routes under `/api/v1` (plus health at root `/healthz` and `/readyz`):

| Method | Path | Auth | Purpose |
|--------|------|------|---------|
| GET | `/healthz` | No | Liveness/readiness probe |
| GET | `/readyz` | No | Readiness probe (alias) |
| GET | `/api/v1/healthz` | No | Health (Helm probe path) |
| GET | `/v1/docs/*` | No | Swagger UI |
| GET | `/api/v1/openapi.json` | No | OpenAPI spec |
| **Plans (PUBLIC — no auth)** | | | |
| GET | `/api/v1/plans` | **No** | List all active plans |
| GET | `/api/v1/plans/{id}` | **No** | Get plan by ID |
| GET | `/api/v1/plans/code/{code}` | **No** | Get plan by plan_code |
| GET | `/api/v1/service-charges/plans` | **No** | List service charge plans |
| GET | `/api/v1/service-charges/plans/{code}` | **No** | Get service charge plan by code |
| **Tenant Subscription (JWT or API key required)** | | | |
| GET | `/api/v1/subscription` | Yes | Get current tenant's subscription |
| POST | `/api/v1/subscription` | Yes | Create subscription for current tenant |
| PUT | `/api/v1/subscription/plan` | Yes | Change plan |
| POST | `/api/v1/subscription/initiate` | Yes | Initiate payment for subscription |
| GET | `/api/v1/subscription/settings` | Yes | Get subscription settings |
| PUT | `/api/v1/subscription/settings` | Yes | Update subscription settings |
| GET | `/api/v1/billing` | Yes | Get billing summary |
| **S2S / Cross-Tenant** | | | |
| GET | `/api/v1/tenants/{tenant_id}/subscription` | Yes (S2S or PO) | Get subscription by tenant ID — used by auth-api for JWT enrichment |
| GET | `/api/v1/tenants/{tenant_id}/subscriptions` | Yes | Per-service-tag subscription view — used by billing UI (auth-ui) |
| GET | `/api/v1/subscriptions/expiring` | Yes | List expiring subscriptions — used by notifications-api |
| PUT | `/api/v1/subscriptions/{id}/switch-plan` | Yes | Switch plan by subscription ID |
| **Feature Gates** | | | |
| GET | `/api/v1/features` | Yes | Get all feature entitlements for current tenant |
| GET | `/api/v1/features/{code}/check` | Yes | Check specific feature availability |
| **Usage Reporting** | | | |
| POST | `/api/v1/usage/report` | Yes | Report usage metric |
| GET | `/api/v1/usage` | Yes | Get usage summary |
| GET | `/api/v1/usage/summary` | Yes | Usage summary (alias) |
| **Service Charges** | | | |
| GET | `/api/v1/tenants/{tenantID}/service-charges` | Yes | Get tenant service charge config |
| **Add-ons (JWT required)** | | | |
| GET | `/api/v1/addons` | Yes | List available add-on features for tenant's plan |
| POST | `/api/v1/addons/{feature_code}/purchase` | Yes | Purchase an add-on (free: instant; paid: Treasury intent) |
| DELETE | `/api/v1/addons/{feature_code}` | Yes | Remove a purchased add-on |
| **Webhooks (S2S — X-API-Key)** | | | |
| POST | `/api/v1/webhooks/treasury/payment-status` | X-API-Key | Treasury payment completion/failure callback — activates or suspends subscription |
| **Admin (Platform Owner only)** | | | |
| POST | `/api/v1/admin/plans` | Yes (PO) | Create plan |
| PUT | `/api/v1/admin/plans/{id}` | Yes (PO) | Update plan |
| DELETE | `/api/v1/admin/plans/{id}` | Yes (PO) | Delete plan |
| POST | `/api/v1/admin/service-charges` | Yes (PO) | Create service charge plan |
| PUT | `/api/v1/admin/service-charges/{id}` | Yes (PO) | Update service charge plan |
| DELETE | `/api/v1/admin/service-charges/{id}` | Yes (PO) | Delete service charge plan |
| GET | `/api/v1/admin/tenants` | Yes (PO) | List all tenants |
| POST | `/api/v1/admin/tenants/{tenant_id}/subscription` | Yes (PO) | Assign plan to tenant |
| GET | `/api/v1/admin/subscriptions` | Yes (PO) | List all subscriptions |
| PUT | `/api/v1/admin/subscriptions/{id}/status` | Yes (PO) | Update subscription status |
| GET | `/api/v1/platform/stats` | Yes (PO) | Platform-wide subscription stats |
| GET | `/api/v1/admin/configs` | Yes (PO) | List service configs |
| POST | `/api/v1/admin/configs` | Yes (PO) | Create service config |
| PUT | `/api/v1/admin/configs/{id}` | Yes (PO) | Update service config |
| DELETE | `/api/v1/admin/configs/{id}` | Yes (PO) | Delete service config |

**PO** = Platform Owner (tenant slug `codevertex`) only.

---

## Infrastructure

| Concern | Technology |
|---------|-----------|
| Language | Go 1.22+ |
| Router | chi v5 |
| ORM | Ent (schema-as-code) |
| Migrations | Atlas (versioned SQL files) |
| Database | PostgreSQL 16+ (pgxpool) |
| Cache | Redis 7+ (usage rate-limit counters + feature-gate entitlement cache) |
| Events | NATS JetStream (`EVENTS_NATS_URL`), stream `subscription` |
| Auth | shared-auth-client v0.6.1 (JWKS + API key) |
| Logging | zap (structured) |
| Container | Multi-stage Docker build |
| CI/CD | GitHub Actions → ArgoCD |
| Orchestration | Kubernetes (devops-k8s, namespace `subscriptions`) |

---

## Key Design Decisions

1. **service_tag over Product/Bundle model** — Plans now belong to exactly one billable service via `service_tag`. This replaces the earlier Product/Bundle/ProductSubscription many-to-many model with a simpler per-service-tag subscription view.
2. **Atlas over Ent auto-migrate** — Schema changes require explicit versioned migration files. This prevents accidental destructive migrations in production.
3. **JWT-baked subscription claims** — Subscription status, features, and limits are embedded in the JWT at issuance. Services read from JWT claims (no runtime calls to subscriptions-api for feature gates), eliminating latency and a dependency.
4. **Outbox pattern** — Guarantees at-least-once event delivery without distributed transactions. Both directional events (`subscription.upgraded/downgraded`) and a consistent `tenant.subscription.updated` event are emitted on plan changes.
5. **Public plan catalog** — `/plans`, `/plans/{id}`, `/plans/code/{code}` require no authentication. Pricing pages, onboarding flows, and unauthenticated quote tools can call these freely.
6. **Idempotent seeder** — `cmd/seed/main.go` can be re-run safely; uses upsert logic. 85 plans across 10 service tags are seeded and confirmed live.
