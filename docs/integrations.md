# Subscription Service - Integration Guide

**Last Updated:** May 22, 2026

## Overview

This document describes how external services integrate with the Subscription service. The Subscription Service is the centralized licensing and subscription management platform, providing feature gating, plan management, and billing integration for the entire Codevertex ecosystem.

**Key principle**: Feature gates and subscription status are baked into the JWT at token issuance by auth-api. Services read from JWT claims at runtime — no runtime calls to subscriptions-api are needed for routine feature checks.

---

## Table of Contents

1. [JWT Claims Structure](#jwt-claims-structure)
2. [Trinity Authorization Pattern](#trinity-authorization-pattern)
3. [Internal Service Integrations](#internal-service-integrations)
4. [S2S Authentication](#s2s-authentication)
5. [API Endpoints Reference](#api-endpoints-reference)
6. [Event-Driven Architecture](#event-driven-architecture)
7. [Error Handling & Resilience](#error-handling--resilience)

---

## JWT Claims Structure

When auth-api issues a JWT, it calls `GET /api/v1/tenants/{tenant_id}/subscription` on subscriptions-api to enrich the token with subscription data. The JWT contains these subscription fields:

```json
{
  "sub": "user-uuid",
  "tenant_id": "tenant-uuid",
  "email": "user@example.com",
  "roles": ["admin", "user"],
  "sub_plan": "ORDERING-GROWTH-MONTHLY",
  "sub_status": "ACTIVE",
  "sub_features": [
    "customer_portal",
    "loyalty_program",
    "multi_outlet",
    "api_webhooks"
  ],
  "sub_limits": {
    "max_riders": 15,
    "max_orders_per_day": 1000,
    "max_admins": 3
  },
  "sub_expires": 1751328000
}
```

**Field names** (exact JSON keys in the JWT):

| JWT Field | Go Struct Field | Description |
|-----------|----------------|-------------|
| `sub_plan` | `SubscriptionPlan` | Plan code (e.g. `ORDERING-GROWTH-MONTHLY`) |
| `sub_status` | `SubscriptionStatus` | Status: `ACTIVE`, `TRIAL`, `EXPIRED`, `CANCELLED`, `PAUSED` |
| `sub_features` | `SubscriptionFeatures` | Feature codes enabled for this plan |
| `sub_limits` | `SubscriptionLimits` | Plan limits as `map[string]int` |
| `sub_expires` | `SubscriptionExpires` | Current period end as Unix timestamp (int64) |

> **Critical**: Do NOT use `subscription_features`, `subscription_limits`, `subscription_status`, or `subscription_plan` — those are internal DB column names, not JWT keys. The JWT uses the short `sub_*` prefix.

### Reading Claims in Go Services

Using `shared-auth-client`:

```go
claims, ok := authclient.ClaimsFromContext(r.Context())
if !ok {
    // No JWT — handle as unauthenticated
    return
}

// Check subscription status
if claims.IsSubscriptionActive() {
    // ACTIVE or TRIAL
}

// Check specific feature
for _, f := range claims.SubscriptionFeatures {
    if f == "loyalty_program" {
        // feature enabled
    }
}

// Read limits
maxRiders := claims.SubscriptionLimits["max_riders"]

// Bypass checks for platform owners and superusers
if claims.IsSuperuser() || claims.IsPlatformOwner {
    // full access
}
```

---

## Trinity Authorization Pattern

```
Authorization = RBAC (Auth-Service) + Licensing (Subscription-Service) + Resources (Domain Services)
```

### Full Request Authorization Flow

```go
func (s *OrderingService) CreateOrder(ctx context.Context, req CreateOrderRequest) error {
    claims, ok := authclient.ClaimsFromContext(ctx)
    if !ok {
        return ErrUnauthorized
    }

    // Layer 1: RBAC — from JWT roles
    if !hasPermission(claims.Roles, "orders:create") {
        return ErrForbidden
    }

    // Layer 2: Licensing — from JWT sub_features (no runtime call needed)
    if !claims.IsSuperuser() && !claims.IsPlatformOwner && !claims.IsSubscriptionActive() {
        return ErrSubscriptionInactive
    }

    // Layer 3: Resource limits — from JWT sub_limits
    maxOrders := claims.SubscriptionLimits["max_orders_per_day"]
    if maxOrders > 0 && todayOrders >= maxOrders {
        return ErrDailyLimitExceeded
    }

    return s.createOrder(ctx, req)
}
```

### Subscription Gate Middleware (All Go Services)

All mutation routes are guarded by this inline middleware (mutations-only; GET/HEAD/OPTIONS pass through):

```go
api.Use(func(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
            next.ServeHTTP(w, r)
            return
        }
        claims, ok := authclient.ClaimsFromContext(r.Context())
        if !ok {
            next.ServeHTTP(w, r)
            return
        }
        if claims.IsSuperuser() || claims.IsPlatformOwner || claims.IsSubscriptionActive() {
            next.ServeHTTP(w, r)
            return
        }
        w.Header().Set("Content-Type", "application/json")
        w.WriteHeader(http.StatusForbidden)
        _, _ = w.Write([]byte(`{"error":"Your subscription is not active. Please renew to continue.","code":"subscription_inactive","upgrade":true}`))
    })
})
```

---

## Internal Service Integrations

### Auth Service

**Integration type**: NATS events (inbound) + REST API (S2S, outbound for JWT enrichment)

**How auth-api uses subscriptions-api**:

1. At JWT issuance, auth-api calls `GET /api/v1/tenants/{tenant_id}/subscription` with `X-API-Key: INTERNAL_SERVICE_KEY` to fetch subscription data and embed `sub_plan`, `sub_status`, `sub_features`, `sub_limits`, `sub_expires` into the token.
2. When subscriptions-api emits a `tenant.subscription.updated` event (on any plan change), auth-api consumes it and marks cached tokens for re-issuance.

**Events consumed by subscriptions-api from auth-api**:
- `auth.tenant.created` → auto-assign Starter plan with 14-day trial to new tenant

**S2S endpoint**:
- `GET /api/v1/tenants/{tenant_id}/subscription` — Returns full subscription with features and limits. Called with `X-API-Key` header.

---

### Treasury Service

**Integration type**: NATS events (bidirectional)

**Events published by subscriptions-api** (consumed by treasury):
- `subscription.subscription.created` — New subscription provisioned; treasury may create initial invoice
- `subscription.subscription.renewed` — Renewal billing event

**Events consumed by subscriptions-api** (published by treasury):
- `treasury.payment.succeeded` → Activate subscription
- `treasury.payment.failed` → Suspend/initiate dunning workflow

**Subscription initiation flow**:

1. Tenant calls `POST /api/v1/subscription/initiate`
2. Subscriptions-api creates a payment intent via treasury-api
3. Response includes `initiate_url` — tenant is redirected to the shared payment page
4. Treasury processes payment and emits `treasury.payment.succeeded`
5. Subscriptions-api activates the subscription

**Treasury webhook contract** (S2S — `POST /api/v1/webhooks/treasury/payment-status`):

```
Auth: X-API-Key: INTERNAL_SERVICE_KEY
Content-Type: application/json

{
  "payment_intent_id": "pi_xxxx",
  "status": "completed" | "failed",
  "tenant_id": "uuid",
  "plan_code": "ORDERING-GROWTH-MONTHLY",
  "amount": 6000.00,
  "currency": "KES"
}
```

- `completed` → sets subscription `ACTIVE`, updates `current_period_start/end`, publishes `subscription.activated`, invalidates feature cache.
- `failed` → sets subscription `SUSPENDED`, publishes `subscription.payment_required`, invalidates feature cache.

**Add-on purchase flow** (paid add-ons):

1. Tenant calls `POST /api/v1/addons/{feature_code}/purchase`
2. Subscriptions-api POSTs to Treasury `/api/v1/{tenant_id}/payments/intents` with `reference_type: "addon_purchase"`
3. On payment completion, Treasury calls the webhook above with `metadata.addon: true`

**Renewal flow** (background job):

1. Hourly job finds `ACTIVE` subscriptions expiring within 24h
2. Paid plans: POSTs to Treasury `/api/v1/payments/intents` with `reference_type: "subscription_renewal"`
3. On payment completion, Treasury calls the webhook — `completed` triggers another renewal cycle

**External eTIMS API metering** (2026-08-18) — a narrower, one-directional slice of the same
usage pipeline above, specifically for treasury-api's `/api/v1/external/etims/*` route group
(API-key-authenticated external consumers, not tenant-JWT sessions):

1. Treasury-api's `TransmitExternalSale` reports one `etims_transactions` usage event per
   successfully-signed sale via the existing `POST /usage/report` (same call shape pos-api/
   ordering-backend already use — see `internal/platform/subscriptions.Client.ReportUsage` in
   treasury-api).
2. `etims_transactions` is registered in `internal/modules/billing/overage_eligibility.go`'s
   `meteredMetrics` map (`PlanLimitKey: etims_transactions_per_month`, `OveragePriceKey:
   overage_etims_price_per_100_month`) — no new billing code, it flows through the exact same
   `UsageEvent` → `OverageCharge` → renewal-invoice pipeline every other metered metric uses.
3. This metric is meaningful ONLY for a `tenant_subscription` on the `etims-api-access` product
   (`ETIMS_API_BASIC`/`_GROWTH`/`_SCALE` plans, seeded in `cmd/seed/etims_api.go`) — an onboarded
   platform tenant's own eTIMS usage through its regular POS/treasury plan never reports this
   metric and is entirely unaffected.
4. The one-time assisted-integration fee (KES 35,000/85,000/180,000) is applied via the existing
   `CustomAddon` mechanism (`billing_cycle=one_time`), created manually by a platform admin once
   an `IntegrationRequest` (owned by auth-api) is approved as `integration_mode=assisted` — never
   automatic, and never for `self_serve` requests.

See treasury-api's [`docs/integrations/external-etims-api.md`](../../finance-service/treasury-api/docs/integrations/external-etims-api.md) for the full external-developer-facing reference.

---

### Ordering, POS, Logistics, Inventory, ERP, Projects Services

**Integration type**: JWT claims (primary) — no runtime REST calls for feature gates

Services read subscription data from the JWT at request time:

```go
claims, _ := authclient.ClaimsFromContext(ctx)

// Feature check — no HTTP call needed
hasLoyalty := false
for _, f := range claims.SubscriptionFeatures {
    if f == "loyalty_program" {
        hasLoyalty = true
        break
    }
}

// Limit check
maxRiders := claims.SubscriptionLimits["max_riders"]
```

**When to call subscriptions-api at runtime**: Only if you need data not in the JWT — e.g., checking a different tenant's subscription status (admin flows), or verifying after plan change before the next token refresh. Use `GET /api/v1/tenants/{tenant_id}/subscription` with `X-API-Key`.

**Per-service subscription view** (auth-ui billing tab):

`GET /api/v1/tenants/{tenant_id}/subscriptions` returns a `ServiceSubscriptionsResult` with the tenant's overall subscription plus a per-`service_tag` breakdown:

```json
{
  "tenant_id": "uuid",
  "subscription": {
    "id": "uuid",
    "plan_code": "ORDERING-GROWTH-MONTHLY",
    "plan_name": "Ordering Growth Monthly",
    "status": "ACTIVE",
    "current_period_end": "2026-06-30"
  },
  "services": [
    {
      "service_tag": "ordering",
      "status": "ACTIVE",
      "plan_code": "ORDERING-GROWTH-MONTHLY",
      "plan_name": "Ordering Growth Monthly",
      "current_period_end": "2026-06-30"
    },
    {
      "service_tag": "pos",
      "status": "NONE"
    }
  ]
}
```

---

### Notifications Service

**Integration type**: NATS events (subscriptions-api → notifications-api)

Subscriptions-api emits lifecycle events that notifications-api maps to email/SMS/push notifications:

| Event | Notification |
|-------|-------------|
| `subscription.subscription.created` | Welcome / trial started |
| `subscription.subscription.expired` | Subscription expired — action required |
| `subscription.subscription.cancelled` | Cancellation confirmation |
| `subscription.subscription.upgraded` | Plan upgrade confirmation |
| `subscription.subscription.downgraded` | Plan downgrade notification |

**S2S endpoint** (for scheduled expiry warnings):

`GET /api/v1/subscriptions/expiring` — Returns subscriptions expiring within a configurable window. Called nightly by notifications-api with `X-API-Key`.

---

### Auth UI (Billing Tab)

**Integration type**: REST API via Next.js proxy

The auth-ui billing tab calls `GET /api/subscriptions?tenant_id={id}` on the Next.js server. The server-side proxy (`app/api/subscriptions/route.ts`) forwards to `GET /api/v1/tenants/{tenant_id}/subscriptions` on subscriptions-api using `INTERNAL_SERVICE_KEY`.

---

## S2S Authentication

All service-to-service calls use the single shared API key:

```
Header: X-API-Key: <INTERNAL_SERVICE_KEY>
```

This is the **only** S2S auth mechanism. Do not create per-service key variables.

Example:

```go
req, _ := http.NewRequestWithContext(ctx, "GET",
    fmt.Sprintf("%s/api/v1/tenants/%s/subscription", subscriptionsBaseURL, tenantID),
    nil,
)
req.Header.Set("X-API-Key", os.Getenv("INTERNAL_SERVICE_KEY"))
```

---

## API Endpoints Reference

### Public Endpoints (No Auth)

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/plans` | List all active plans (all service tags) |
| GET | `/api/v1/plans/{id}` | Get plan by UUID |
| GET | `/api/v1/plans/code/{code}` | Get plan by plan_code |
| GET | `/api/v1/service-charges/plans` | List service charge plans |
| GET | `/api/v1/service-charges/plans/{code}` | Get service charge plan by code |

### Authenticated Endpoints

| Method | Path | Auth | Caller |
|--------|------|------|--------|
| GET | `/api/v1/subscription` | JWT | Tenant (self) |
| POST | `/api/v1/subscription` | JWT | Tenant (self) |
| PUT | `/api/v1/subscription/plan` | JWT | Tenant (self) |
| POST | `/api/v1/subscription/initiate` | JWT | Tenant (self) |
| GET | `/api/v1/subscription/settings` | JWT | Tenant (self) |
| PUT | `/api/v1/subscription/settings` | JWT | Tenant (self) |
| GET | `/api/v1/billing` | JWT | Tenant (self) |
| GET | `/api/v1/tenants/{tenant_id}/subscription` | API key or PO JWT | auth-api (JWT enrichment), S2S |
| GET | `/api/v1/tenants/{tenant_id}/subscriptions` | JWT or API key | auth-ui billing tab |
| GET | `/api/v1/subscriptions/expiring` | API key | notifications-api (scheduled) |
| PUT | `/api/v1/subscriptions/{id}/switch-plan` | JWT | Billing UI plan change |
| GET | `/api/v1/features` | JWT | Tenant (self) |
| GET | `/api/v1/features/{code}/check` | JWT | Any service |
| POST | `/api/v1/usage/report` | JWT or API key | Domain services (returns 429 when plan limit exceeded) |
| GET | `/api/v1/usage` | JWT | Tenant (self) |
| GET | `/api/v1/addons` | JWT | Tenant (self) — list available add-ons |
| POST | `/api/v1/addons/{code}/purchase` | JWT | Tenant (self) — purchase an add-on |
| DELETE | `/api/v1/addons/{code}` | JWT | Tenant (self) — remove an add-on |
| POST | `/api/v1/webhooks/treasury/payment-status` | X-API-Key | Treasury (S2S webhook) |

---

## Event-Driven Architecture

### NATS Subject Format

`{aggregate_type}.{event_type}` — e.g. `subscription.subscription.upgraded`, `tenant.subscription.updated`

### Outbound Events (Published by Subscriptions-API)

| Subject | When |
|---------|------|
| `subscription.subscription.created` | New subscription provisioned |
| `subscription.subscription.activated` | Payment confirmed, subscription active |
| `subscription.subscription.upgraded` | Plan tier increased |
| `subscription.subscription.downgraded` | Plan tier decreased |
| `subscription.subscription.cancelled` | Cancellation |
| `subscription.subscription.expired` | Period/trial ended |
| `subscription.subscription.renewed` | Renewal (free plan period extended) |
| `subscription.subscription.suspended` | Manual suspension or payment failure |
| `subscription.subscription.reactivated` | Suspension lifted |
| `subscription.subscription.payment_required` | Treasury payment failed |
| `subscription.subscription.renewal_initiated` | Renewal payment intent created (paid plans) |
| `subscription.addon.purchased` | Add-on feature purchased |
| `tenant.subscription.updated` | Any plan change (consistent event on tenant aggregate) |

**Event payload example** (`subscription.subscription.upgraded`):

```json
{
  "id": "uuid",
  "event_type": "subscription.upgraded",
  "aggregate_type": "subscription",
  "aggregate_id": "subscription-uuid",
  "tenant_id": "tenant-uuid",
  "payload": {
    "subscription_id": "uuid",
    "from_plan": "ORDERING-STARTER-MONTHLY",
    "to_plan": "ORDERING-GROWTH-MONTHLY",
    "effective_date": "2026-05-22"
  },
  "timestamp": "2026-05-22T10:30:00Z",
  "version": "1.0"
}
```

### Inbound Events (Consumed by Subscriptions-API)

| Subject | Action |
|---------|--------|
| `auth.tenant.created` | Auto-assign Starter plan with 14-day trial |
| `treasury.payment.succeeded` | Activate subscription |
| `treasury.payment.failed` | Suspend/initiate dunning workflow |

---

## Integration Security

### Authentication

- **JWT**: Validated via JWKS from `https://sso.codevertexafrica.com/api/v1/.well-known/jwks.json`
- **API Key**: `X-API-Key: INTERNAL_SERVICE_KEY` — single shared key for all S2S calls

### Tenant Isolation

- All subscription data scoped by `tenant_id`
- `X-Tenant-ID` header extracted by httpware middleware and matched against JWT claims
- Platform Owner (slug `codevertex`) can access any tenant's data via admin routes

---

## Error Handling & Resilience

### Subscription 403 (Inactive Subscription)

All subscription enforcement returns this discriminated error body:

```json
{
  "error": "Your subscription is not active. Please renew to continue.",
  "code": "subscription_inactive",
  "upgrade": true
}
```

The `upgrade: true` field distinguishes subscription 403s from RBAC 403s. Frontends show an upgrade banner/modal instead of redirecting to unauthorized.

### Usage Rate Limiting (429)

When a domain service calls `POST /api/v1/usage/report` and the tenant has exceeded their plan's metric limit for the current month, the endpoint returns:

```http
HTTP/1.1 429 Too Many Requests
X-RateLimit-Limit: 1000
X-RateLimit-Remaining: 0
X-RateLimit-Reset: 2026-06-01T00:00:00Z
Content-Type: application/json

{"error":"usage limit exceeded","metric":"order_count","limit":"1000"}
```

The limit is loaded from the tenant's active `TenantSubscription` → `SubscriptionPlan.tier_limits_json` (matched to `metric_type` by `limitKeyForMetric`/`inferMetricType`, see `evaluateUsage` in `internal/http/handlers/usage.go`) — **not** the `rate_limit_configs` table, despite the similar name. This is a monthly quota check (has this tenant used up this metric's included allowance this billing period), distinct from request-rate throttling. The Redis counter key is `usage:limit:{tenant_id}:{metric_type}:{YYYY-MM}` — resets automatically at the start of the next month via Redis `EXPIREAT`.

Domain services that receive a 429 should surface an upgrade prompt or reject the operation. Superusers and Platform Owners bypass enforcement.

### Request-Rate Limiting (`rate_limit_configs`)

The `rate_limit_configs` table (seeded in `cmd/seed/rate_limits.go`) is a separate mechanism: a
requests-per-minute ceiling, not a monthly usage quota. `GET /api/v1/tenants/{tenant_id}/rate-limit`
(S2S, `internal/http/handlers/rate_limit.go`) resolves the effective limit for a
`(tenant, service_name, endpoint)` triple — checking the tenant's plan `tier_limits_json` first
(e.g. `api_requests_per_minute` on the `ETIMS_API_*` plans), then falling back to a
`rate_limit_configs` row for that `service_name`, then a hardcoded default. The first live
consumer is treasury-api's external eTIMS API (`internal/http/middleware/apikey_auth.go`), which
caches the resolved limit for 5 minutes and enforces it with a local Redis sliding-window
counter — see `docs/integrations/external-etims-api.md` in treasury-api for the caller-facing
contract (response headers, 429 shape).

### Outbox Retry Policy

- Poll interval: 2 seconds
- Max attempts: 10
- Events marked `FAILED` after max attempts

### S2S Resilience

- JWT enrichment failure: auth-api falls back to issuing token without subscription claims (fail-open, subscription enforcement still applies at each service)
- Subscriptions-api unavailable: services continue using current JWT claims until token expires

---

## References

- [Architecture](architecture.md)
- [ERD](erd.md)
- [Plan](plan.md)
- [Subscription Gating Guide](../../../shared-docs/subscription-gating-guide.md)
- [Trinity Authorization Pattern](../../../shared-docs/TRINITY-AUTHORIZATION-PATTERN.md)
