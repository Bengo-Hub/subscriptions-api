# Subscription API — Consumer Guide

This document is for developers integrating with the Subscription API from other BengoBox services or the subscriptions-ui frontend.

**Base URL**: `https://pricingapi.codevertexitsolutions.com/api/v1`
**Local**: `http://localhost:4005/api/v1`

---

## Authentication

All endpoints (except health checks) require one of:

1. **JWT Bearer Token** — Issued by auth-service SSO (`sso.codevertexitsolutions.com`)
   ```
   Authorization: Bearer <jwt-token>
   ```

2. **API Key** — For service-to-service calls
   ```
   X-API-Key: <api-key>
   ```

Every request should include the tenant context header:
```
X-Tenant-ID: <tenant-uuid>
```

---

## Common Flows

### 1. JWT Claims Enrichment (auth-service → subscription-api)

When auth-service issues a JWT, it calls this endpoint to include subscription data in the token:

```
GET /tenants/{tenant_id}/subscription
```

**Response** (used to populate JWT claims):
```json
{
  "tenant_id": "11111111-2222-3333-4444-555555555555",
  "plan_code": "GROWTH",
  "plan_name": "Growth (Standard)",
  "status": "TRIAL",
  "trial_ends_at": "2026-03-20T00:00:00Z",
  "current_period_start": "2026-03-06T00:00:00Z",
  "current_period_end": "2026-03-20T00:00:00Z",
  "features": [
    "customer_portal", "rider_app", "admin_dashboard",
    "mpesa_integration", "loyalty_program", "multi_outlet",
    "advanced_analytics", "promo_codes", "group_ordering"
  ],
  "limits": {
    "max_admins": 3,
    "max_riders": 15,
    "max_orders_per_day": 1000,
    "max_outlets": 3,
    "api_calls_per_month": 50000
  }
}
```

Auth-service embeds `features`, `limits`, `status`, and `plan_code` into the JWT so downstream services can authorize without calling subscription-api at runtime.

### 2. Feature Gate Check (any service → subscription-api)

For real-time feature checks (when JWT claims are stale or unavailable):

```
GET /tenants/{tenant_id}/features/{feature_code}/check
```

**Response**:
```json
{
  "feature_code": "loyalty_program",
  "enabled": true,
  "limit": null,
  "plan_required": "GROWTH"
}
```

**When to use**: Only when you need a live check (e.g., feature was just toggled). Prefer reading JWT claims for routine authorization.

### 3. Auto-Provisioning on Tenant Creation

When `auth.tenant.created` fires on NATS, the subscription-api automatically provisions a **Starter plan with 14-day trial**. No manual API call needed.

If you need to provision manually (e.g., for testing):

```
POST /tenants/{tenant_id}/subscription
Content-Type: application/json

{
  "plan_code": "STARTER",
  "bundle_code": "delivery",
  "trial_days": 14
}
```

**Response** (201 Created):
```json
{
  "id": "uuid",
  "tenant_id": "uuid",
  "plan_code": "STARTER",
  "plan_name": "Starter (Lite)",
  "status": "TRIAL",
  "bundle_code": "delivery",
  "trial_ends_at": "2026-03-20T00:00:00Z",
  "current_period_start": "2026-03-06T00:00:00Z",
  "current_period_end": "2026-03-20T00:00:00Z",
  "features": ["customer_portal", "rider_app", "admin_dashboard", "..."],
  "limits": {"max_admins": 2, "max_riders": 5, "max_orders_per_day": 300}
}
```

### 4. Plan Change (Upgrade/Downgrade)

```
PUT /tenants/{tenant_id}/subscription/plan
Content-Type: application/json

{
  "plan_code": "PROFESSIONAL"
}
```

Takes effect immediately. Publishes `subscription.upgraded` or `subscription.downgraded` event.

### 5. Cancellation

```
POST /tenants/{tenant_id}/subscription/cancel
Content-Type: application/json

{
  "reason": "Switching to competitor"
}
```

### 6. Renewal

```
POST /tenants/{tenant_id}/subscription/renew
Content-Type: application/json

{
  "plan_code": "GROWTH"
}
```

`plan_code` is optional — omit to renew on the same plan.

---

## Plan Catalog

### List All Plans

```
GET /plans
```

Returns all active plans with features and tier limits.

### Get Plan by Code

```
GET /plans/code/GROWTH
```

### Available Plan Codes

| Code | Name | Monthly (KES) | Annual (KES) |
|------|------|---------------|--------------|
| `STARTER` | Starter (Lite) | 2,500 | 27,500 |
| `GROWTH` | Growth (Standard) | 6,000 | 66,000 |
| `PROFESSIONAL` | Professional (Scale) | 12,500 | 137,500 |
| `STARTER_YEARLY` | Starter — Annual | — | 27,500 |
| `GROWTH_YEARLY` | Growth — Annual | — | 66,000 |
| `PROFESSIONAL_YEARLY` | Professional — Annual | — | 137,500 |

---

## Product Subscriptions

Tenants subscribe to bundles which activate specific products.

### List Subscribed Products

```
GET /tenants/{tenant_id}/products
```

### Activate a Product

```
POST /tenants/{tenant_id}/products/{code}/activate
```

### Deactivate a Product

```
POST /tenants/{tenant_id}/products/{code}/deactivate
```

### Product Codes

| Code | Name | Category |
|------|------|----------|
| `auth` | Authentication & SSO | Platform |
| `notifications` | Notifications | Platform |
| `subscription` | Subscription Management | Platform |
| `ordering` | Online Ordering | Product |
| `logistics` | Delivery & Logistics | Product |
| `treasury` | Payments & Invoicing | Product |
| `pos` | Point of Sale | Add-On |
| `storefront` | Website & Storefront | Add-On |
| `google_maps` | Google Maps Integration | Add-On |
| `paystack_gateway` | Paystack Payment Gateway | Add-On |
| `sms_credits` | SMS Credit Pack | Add-On |
| `premium_support` | Premium Support | Add-On |

### Bundle Codes

| Code | Products | Default |
|------|----------|---------|
| `delivery` | ordering, logistics, treasury, storefront | Yes |
| `pos-suite` | pos, treasury | No |
| `complete` | ordering, logistics, treasury, pos, storefront | No |

---

## Event Contracts

### Events Published (subscribe via NATS JetStream)

Subject: `subscriptions.events`

| Event Type | Payload Fields |
|------------|----------------|
| `subscription.created` | `tenant_id`, `plan_code`, `status`, `bundle_code`, `trial_days` |
| `subscription.activated` | `tenant_id` |
| `subscription.upgraded` | `tenant_id`, `new_plan_code`, `old_plan_id`, `direction` |
| `subscription.downgraded` | `tenant_id`, `new_plan_code`, `old_plan_id`, `direction` |
| `subscription.cancelled` | `tenant_id`, `reason` |
| `subscription.renewed` | `tenant_id` |
| `subscription.expired` | `tenant_id` |

All events include envelope: `id`, `tenant_id`, `aggregate_type`, `aggregate_id`, `event_type`, `timestamp`, `version`.

### Events Consumed

| Subject | Handler |
|---------|---------|
| `auth.tenant.created` | Auto-provision Starter plan with trial |
| `treasury.payment.succeeded` | Activate subscription |
| `treasury.payment.failed` | Suspend subscription |

---

## Error Responses

All errors return:
```json
{
  "error": "human-readable error message"
}
```

| Status | Meaning |
|--------|---------|
| 400 | Invalid request (bad UUID, missing field) |
| 401 | Missing or invalid auth |
| 404 | Subscription/plan not found |
| 409 | Conflict (tenant already has subscription) |
| 500 | Internal server error |

---

## Testing with Urban Loft

The demo tenant `urban-loft` (ID: `11111111-2222-3333-4444-555555555555`) is pre-seeded on the GROWTH plan with a 14-day trial and the delivery bundle (ordering, logistics, treasury, storefront).

```bash
# Get subscription status
curl -H "X-API-Key: $API_KEY" \
  https://pricingapi.codevertexitsolutions.com/api/v1/tenants/11111111-2222-3333-4444-555555555555/subscription

# Check a feature
curl -H "X-API-Key: $API_KEY" \
  https://pricingapi.codevertexitsolutions.com/api/v1/tenants/11111111-2222-3333-4444-555555555555/features/loyalty_program/check
```
