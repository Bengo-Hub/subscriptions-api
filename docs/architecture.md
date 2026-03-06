# Subscription API — Architecture

## System Context

The Subscription API (`subscriptions-api`) is the centralized licensing, feature-gating, and subscription management backend for the BengoBox platform. It powers the **Trinity Authorization** model used across all services:

```
Authorization = RBAC (auth-service) + Licensing (subscription-service) + Resources (domain services)
```

**Production URL**: `https://pricingapi.codevertexitsolutions.com`
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
│   ├── ent/                   # Ent ORM generated code + schemas
│   ├── http/
│   │   ├── handlers/          # HTTP handlers (plans, subscriptions, addons)
│   │   └── router/router.go   # chi router with auth middleware
│   ├── modules/
│   │   ├── plans/             # Plan catalog repository
│   │   ├── subscriptions/     # Subscription lifecycle service + state machine
│   │   └── outbox/            # Outbox event repository
│   ├── platform/
│   │   ├── cache/             # Redis client init
│   │   ├── database/          # pgxpool init
│   │   └── events/            # NATS connection
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

Operations: `CreateSubscription`, `ChangePlan`, `CancelSubscription`, `RenewSubscription`, `ActivateProduct`, `DeactivateProduct`

Every mutation writes an **outbox event** atomically within the same transaction.

### 2. Plan Catalog

**Location**: `internal/modules/plans/`

Read-only repository over Ent's `SubscriptionPlan`, `PlanFeature`, `Bundle` entities. Serves the plan/bundle/feature catalog to both the admin UI and consumer services.

### 3. Outbox Publisher

**Location**: `internal/modules/outbox/` + `shared-events` library

Polls the `outbox_events` table for `PENDING` events, publishes to NATS JetStream subject `subscriptions.events`, marks as `PUBLISHED`. Retry with exponential backoff on failure.

### 4. Auth Integration

**Libraries**: `shared-auth-client` (JWT validation + API key), `httpware` (tenant/request-ID middleware)

- Validates JWTs from `sso.codevertexitsolutions.com` via JWKS
- Supports API key fallback for service-to-service calls
- Extracts `tenant_id` from `X-Tenant-ID` header via httpware middleware
- **RBAC:** No local roles/permissions; authorization is via auth-api JWT. Subscription resources (plans, subscriptions, features, bundles, products) use the eight actions in auth-api: `add`, `read`, `read_own`, `change`, `change_own`, `delete`, `manage`, `manage_own`. See `docs/plan.md` (Security) for full seed and RBAC notes.

---

## Data Layer

### ORM: Ent (schema-as-code)

Entities (current):

| Entity | Table | Purpose |
|--------|-------|---------|
| `SubscriptionPlan` | `subscription_plans` | Plan definitions (STARTER, GROWTH, PROFESSIONAL × monthly/yearly) |
| `PlanFeature` | `plan_features` | Feature flags per plan |
| `PlanPricingHistory` | `plan_pricing_history` | Pricing audit trail |
| `TenantSubscription` | `tenant_subscriptions` | Active subscription per tenant |
| `ProductSubscription` | `product_subscriptions` | Many-to-many: subscription ↔ products |
| `Product` | `products` | 8 platform products + 5 add-ons |
| `Bundle` | `bundles` | 3 curated product bundles (delivery, pos-suite, complete) |
| `OutboxEvent` | `outbox_events` | Transactional outbox for NATS publishing |

### Migration Strategy

**Current**: Ent auto-migrate (`ormClient.Schema.Create(ctx)`) on startup when `RUN_MIGRATIONS=true`.
**Target (MVP)**: Transition to **Atlas** for versioned, reviewable migrations.

### Database

PostgreSQL 16+ via `pgxpool` (connection pooling) and `database/sql` (for Ent driver).

### Caching

Redis 7+ — currently initialized but not yet used for feature-gate caching. MVP goal: cache feature checks with 60s TTL under key pattern `subscription:feature:{tenant_id}:{feature_code}`.

---

## Event Architecture

### Outbound (Published via Outbox → NATS JetStream)

| Event | Trigger |
|-------|---------|
| `subscription.created` | New subscription provisioned |
| `subscription.activated` | Payment confirmed |
| `subscription.upgraded` | Plan tier increased |
| `subscription.downgraded` | Plan tier decreased |
| `subscription.cancelled` | User/system cancellation |
| `subscription.expired` | Period/trial ended |
| `subscription.renewed` | Subscription renewed |

### Inbound (Consumed from NATS)

| Event | Action |
|-------|--------|
| `auth.tenant.created` | Auto-assign Starter plan with 14-day trial |
| `treasury.payment.succeeded` | Activate subscription |
| `treasury.payment.failed` | Suspend/initiate dunning |

### Outbox Pattern Flow

```
1. HTTP handler calls service method
2. Service opens Tx → writes domain mutation + outbox_events row → commits
3. Outbox publisher goroutine polls PENDING events (5s interval)
4. Publishes to NATS JetStream → marks PUBLISHED
5. Failed publishes retry with exponential backoff (max 3 attempts)
```

---

## Seed Data Summary

Seeded via `go run cmd/seed/main.go` (idempotent):

- **13 Products**: 3 platform (auth, notifications, subscription), 3 core (ordering, logistics, treasury), 2 standard add-ons (pos, storefront), 5 integration add-ons
- **3 Bundles**: delivery (default), pos-suite, complete
- **6 Plans**: STARTER/GROWTH/PROFESSIONAL × MONTHLY/ANNUAL
- **Demo Subscription**: Urban Loft on GROWTH plan, 14-day trial, delivery bundle

---

## API Route Map

All routes under `/api/v1`:

| Method | Path | Auth | Purpose |
|--------|------|------|---------|
| GET | `/healthz` | No | Liveness probe |
| GET | `/readyz` | No | Readiness probe |
| GET | `/metrics` | No | Prometheus metrics |
| GET | `/plans` | Yes | List all plans |
| GET | `/plans/code/{code}` | Yes | Get plan by code |
| GET | `/plans/{id}` | Yes | Get plan by ID |
| GET | `/tenants/{tenant_id}/subscription` | Yes | Get tenant subscription (JWT claims source) |
| GET | `/tenants/{tenant_id}/features/{feature_code}/check` | Yes | Feature gate check |
| POST | `/tenants/{tenant_id}/subscription` | Yes | Create subscription |
| PUT | `/tenants/{tenant_id}/subscription/plan` | Yes | Change plan |
| POST | `/tenants/{tenant_id}/subscription/cancel` | Yes | Cancel subscription |
| POST | `/tenants/{tenant_id}/subscription/renew` | Yes | Renew subscription |
| GET | `/tenants/{tenant_id}/products` | Yes | List subscribed products |
| POST | `/tenants/{tenant_id}/products/{code}/activate` | Yes | Activate product |
| POST | `/tenants/{tenant_id}/products/{code}/deactivate` | Yes | Deactivate product |

Plus add-on routes registered by `AddonHandler`.

---

## Infrastructure

| Concern | Technology |
|---------|-----------|
| Language | Go 1.22+ |
| Router | chi v5 |
| ORM | Ent |
| Database | PostgreSQL 16+ (pgxpool) |
| Cache | Redis 7+ |
| Events | NATS JetStream |
| Auth | shared-auth-client (JWKS + API key) |
| Logging | zap (structured) |
| Container | Multi-stage Docker build |
| CI/CD | GitHub Actions → ArgoCD |
| Orchestration | Kubernetes (devops-k8s) |

---

## Key Design Decisions

1. **Ent over raw SQL** — Schema-as-code with generated type-safe queries, automatic migrations during development
2. **Outbox pattern** — Guarantees at-least-once event delivery without distributed transactions
3. **Trinity Authorization** — Subscription claims baked into JWT, eliminating runtime calls for most feature checks
4. **Product/Bundle model** — Tenants subscribe to bundles (product groupings) rather than individual products, simplifying pricing
5. **Idempotent seeder** — `cmd/seed/main.go` can be re-run safely; uses upsert logic
