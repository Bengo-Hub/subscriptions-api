# Subscription Service - Integration Guide

## Overview

This document provides detailed integration information for all external services and systems integrated with the Subscription service, including internal BengoBox microservices and external third-party services.

The Subscription Service acts as the centralized licensing and subscription management platform, providing feature gating, usage tracking, and billing integration for the entire BengoBox ecosystem.

---

## Table of Contents

1. [Internal BengoBox Service Integrations](#internal-bengobox-service-integrations)
2. [Integration Patterns](#integration-patterns)
3. [Trinity Authorization Pattern](#trinity-authorization-pattern)
4. [Feature Entitlement Validation](#feature-entitlement-validation)
5. [Usage Tracking Integration](#usage-tracking-integration)
6. [Event-Driven Architecture](#event-driven-architecture)
7. [Integration Security](#integration-security)
8. [Error Handling & Resilience](#error-handling--resilience)

---

## Internal BengoBox Service Integrations

### Auth Service

**Integration Type**: Events (NATS) + REST API + JWT Claims Extension

**Use Cases**:
- Tenant/user synchronization
- JWT claims extension with subscription features
- Auto-assign Starter plan to new tenants
- Subscription status in user context

**REST API Usage**:
- `POST /api/v1/tenants/{id}/claims` - Extend JWT claims with subscription features (called by auth-service)
- `GET /api/v1/tenants/{id}/subscription` - Get tenant subscription details

**Events Consumed**:
- `auth.tenant.created` - Auto-assign Starter plan to new tenant
- `auth.tenant.updated` - Update tenant subscription context if needed

**Events Published**:
- `subscription.entitlements_changed` - Notify auth-service to refresh JWT claims
- `subscription.trial_started` - Trial period started
- `subscription.expired` - Subscription expired (auth-service may block access)

**JWT Claims Extension**:
When auth-service issues JWT tokens, it calls subscription-service to enrich claims:

```json
{
  "user_id": "uuid",
  "tenant_id": "uuid",
  "roles": ["admin", "user"],
  "subscription_features": ["loyalty_program", "multi_outlet", "api_webhooks"],
  "subscription_limits": {
    "max_riders": 15,
    "max_orders_per_day": 1000,
    "max_admins": 3
  },
  "subscription_status": "active",
  "subscription_plan": "growth"
}
```

**Configuration**:
- Auth-service base URL: `AUTH_SERVICE_BASE_URL` (environment variable)
- JWT claims cache TTL: `JWT_CLAIMS_CACHE_TTL=300s` (default)

---

### Treasury Service

**Integration Type**: Events (NATS) + REST API + Billing Events

**Use Cases**:
- Emit billing events for subscription renewals
- Emit billing events for overage charges
- Emit billing events for plan transition proration
- Receive payment confirmation to activate subscriptions
- Handle payment failures (dunning workflows)

**REST API Usage**:
- `POST /api/v1/billing/events` - Emit billing event to treasury
- `GET /api/v1/invoices/{id}` - Check invoice status (from treasury)
- `POST /api/v1/invoices` - Request invoice creation (for overages/proration)

**Events Published**:
- `subscription.billing.renewal` - Subscription renewal billing event
- `subscription.billing.overage` - Overage charge billing event
- `subscription.billing.proration` - Plan transition proration event

**Events Consumed**:
- `treasury.payment.succeeded` - Activate subscription after payment
- `treasury.payment.failed` - Initiate dunning workflow, suspend subscription
- `treasury.invoice.created` - Link invoice_id to billing_event

**Billing Event Flow**:
1. Subscription service creates billing event (renewal/overage/proration)
2. Emits event to NATS: `subscription.billing.*`
3. Treasury service consumes event and creates invoice
4. Treasury service updates billing_event with invoice_id via webhook
5. On payment success, treasury emits `treasury.payment.succeeded`
6. Subscription service activates/extends subscription

**Configuration**:
- Treasury service base URL: `TREASURY_SERVICE_BASE_URL` (environment variable)
- Billing event retry policy: 3 retries with exponential backoff

---

### Ordering Service

**Integration Type**: REST API (Feature Checks + Usage Reporting) + Events

**Use Cases**:
- Feature entitlement validation (loyalty_program, group_ordering, etc.)
- Daily order count usage reporting
- Order limit enforcement
- Overage tracking

**REST API Usage (Ordering Service Calls Subscription Service)**:
- `GET /api/v1/{tenant_id}/features/{feature_code}` - Check if feature is available
- `POST /api/v1/{tenant_id}/usage/report` - Report order count usage
- `GET /api/v1/{tenant_id}/limits/orders` - Get order limit for tenant

**Feature Check Example**:
```go
// Before allowing loyalty point redemption
featureAvailable, err := subscriptionService.HasFeature(ctx, tenantID, "loyalty_program")
if err != nil || !featureAvailable {
    return ErrFeatureNotAvailable("Loyalty program not available on current plan")
}
```

**Usage Reporting Example**:
```go
// After order placement
err := subscriptionService.ReportUsage(ctx, tenantID, "order_count", 1, map[string]any{
    "order_id": orderID,
    "date": time.Now().Format("2006-01-02"),
})
```

**Limit Enforcement Example**:
```go
// Before allowing order placement
limits, err := subscriptionService.GetLimits(ctx, tenantID)
if err != nil {
    return err
}

todayOrders, err := subscriptionService.GetUsage(ctx, tenantID, "order_count", "today")
if err != nil {
    return err
}

if todayOrders >= limits.MaxOrdersPerDay {
    // Option 1: Reject order
    return ErrDailyLimitExceeded
    
    // Option 2: Allow with overage tracking
    subscriptionService.ReportOverage(ctx, tenantID, "order_count", 1)
}
```

**Events Consumed**:
- `ordering.order.placed` - Track order count (optional, if not using REST API)
- `ordering.order.cancelled` - Adjust usage (optional)

**Events Published**:
- `subscription.limit_exceeded` - Order limit exceeded (if strict enforcement)
- `subscription.overage_detected` - Overage detected (daily batch job)

**Configuration**:
- Feature gate cache TTL: `FEATURE_GATE_CACHE_TTL=60s` (default)
- Usage reporting batch size: `USAGE_REPORT_BATCH_SIZE=100` (default)

---

### POS Service

**Integration Type**: REST API (Feature Checks + Usage Reporting) + Events

**Use Cases**:
- Feature entitlement validation (pos_integration, advanced_analytics, etc.)
- POS transaction count usage reporting
- Transaction limit enforcement

**REST API Usage**:
- `GET /api/v1/{tenant_id}/features/{feature_code}` - Check feature availability
- `POST /api/v1/{tenant_id}/usage/report` - Report POS transaction count

**Usage Reporting**:
```go
// After POS transaction
err := subscriptionService.ReportUsage(ctx, tenantID, "pos_transaction_count", 1, map[string]any{
    "transaction_id": transactionID,
    "amount": amount,
})
```

**Events Consumed**:
- `pos.transaction.completed` - Track transaction count (optional)

---

### Logistics Service

**Integration Type**: REST API (Feature Checks + Usage Reporting) + Events

**Use Cases**:
- Feature entitlement validation (route_optimization, advanced_analytics, etc.)
- Rider count usage reporting
- Delivery task count usage reporting
- Rider limit enforcement

**REST API Usage**:
- `GET /api/v1/{tenant_id}/features/{feature_code}` - Check feature availability
- `POST /api/v1/{tenant_id}/usage/report` - Report rider count, delivery tasks

**Rider Count Reporting**:
```go
// After rider onboarding
err := subscriptionService.ReportUsage(ctx, tenantID, "rider_count", 1, map[string]any{
    "rider_id": riderID,
    "report_type": "incremental", // or "snapshot"
})
```

**Rider Limit Enforcement**:
```go
// Before allowing rider creation
limits, err := subscriptionService.GetLimits(ctx, tenantID)
if err != nil {
    return err
}

currentRiders, err := subscriptionService.GetUsage(ctx, tenantID, "rider_count", "current")
if err != nil {
    return err
}

if currentRiders >= limits.MaxRiders {
    // Allow with overage (KES 250/month per rider)
    subscriptionService.ReportOverage(ctx, tenantID, "rider_count", 1)
    // Or reject if hard limit
    return ErrRiderLimitExceeded
}
```

**Events Consumed**:
- `logistics.rider.created` - Track rider count
- `logistics.task.created` - Track delivery task count (optional)

**Events Published**:
- `subscription.limit_exceeded` - Rider limit exceeded
- `subscription.overage_detected` - Rider overage detected

---

### Inventory Service

**Integration Type**: REST API (Feature Checks) + Events (Optional)

**Use Cases**:
- Feature entitlement validation (multi_warehouse, advanced_analytics, etc.)
- Inventory transaction count usage reporting (optional)

**REST API Usage**:
- `GET /api/v1/{tenant_id}/features/{feature_code}` - Check feature availability

---

### Notifications Service

**Integration Type**: Events (NATS) + REST API (Optional)

**Use Cases**:
- Subscription alerts (trial ending, payment failed, limit exceeded)
- Overage notifications
- Plan transition notifications

**Events Published**:
- `subscription.trial.ending` - Trial ending soon (7 days, 3 days, 1 day)
- `subscription.payment.failed` - Payment failure notification
- `subscription.limit.warning` - Approaching limit (80%, 90%, 95%)
- `subscription.overage.charged` - Overage charge notification
- `subscription.plan.upgraded` - Plan upgrade notification
- `subscription.plan.downgraded` - Plan downgrade notification

**Events Consumed**:
- `notifications.delivery.completed` - Track notification delivery (optional)

**Configuration**:
- Notification service base URL: `NOTIFICATIONS_SERVICE_BASE_URL` (environment variable)

---

## Integration Patterns

### 1. Feature Entitlement Validation (REST API)

**Use Case**: Real-time feature access checks

**Flow**:
1. Service receives request requiring feature
2. Service calls subscription-service: `GET /api/v1/{tenant_id}/features/{feature_code}`
3. Subscription service checks:
   - Tenant subscription status (active, trial, expired)
   - Plan includes feature
   - Manual overrides
   - Feature gate cache (Redis)
4. Returns feature availability + limits
5. Service allows or denies request

**Caching**:
- Feature gates cached in Redis (60s TTL)
- Cache key: `subscription:feature:{tenant_id}:{feature_code}`
- Cache invalidation on subscription changes

**Performance**:
- Average latency: < 10ms (cached)
- Average latency: < 50ms (uncached, database query)

---

### 2. Usage Reporting (REST API)

**Use Case**: Services report usage metrics

**Flow**:
1. Service performs action (create order, add rider, etc.)
2. Service calls subscription-service: `POST /api/v1/{tenant_id}/usage/report`
3. Subscription service records usage:
   - Raw usage tracking table
   - Daily snapshot aggregation (batch job)
   - Overage detection (daily batch job)
4. Returns acknowledgment

**Batching**:
- Services can batch multiple usage reports
- Batch size: 100 items
- Batch timeout: 5 seconds

**Idempotency**:
- Usage reports include `idempotency_key`
- Duplicate reports are deduplicated

**Example**:
```go
// Batch usage reporting
usageReports := []UsageReport{
    {MetricType: "order_count", Value: 1, Metadata: map[string]any{"order_id": "..."}},
    {MetricType: "order_count", Value: 1, Metadata: map[string]any{"order_id": "..."}},
}

err := subscriptionService.ReportUsageBatch(ctx, tenantID, usageReports)
```

---

### 3. Event-Driven Integration (NATS)

**Use Case**: Async subscription lifecycle events

**Outbound Events** (Published by Subscription Service):
- `subscription.created` - New subscription created
- `subscription.trial_started` - Trial period started
- `subscription.activated` - Subscription activated
- `subscription.renewed` - Subscription renewed
- `subscription.upgraded` - Plan upgraded
- `subscription.downgraded` - Plan downgraded
- `subscription.cancelled` - Subscription cancelled
- `subscription.expired` - Subscription expired
- `subscription.entitlements_changed` - Feature entitlements changed
- `subscription.overage_detected` - Overage detected
- `subscription.billing.renewal` - Renewal billing event
- `subscription.billing.overage` - Overage billing event
- `subscription.billing.proration` - Proration billing event

**Inbound Events** (Consumed by Subscription Service):
- `auth.tenant.created` - Auto-assign Starter plan
- `treasury.payment.succeeded` - Activate subscription
- `treasury.payment.failed` - Initiate dunning workflow
- `treasury.invoice.created` - Link invoice to billing event

---

## Trinity Authorization Pattern

The Subscription Service is part of the **Trinity Authorization** pattern:

```
Authorization = RBAC (Auth-Service) + Licensing (Subscription-Service) + Resources (Domain Services)
```

### Authorization Flow

1. **User Authentication** (Auth-Service):
   - User logs in via SSO
   - Auth-service issues JWT with user_id, tenant_id, roles

2. **JWT Claims Extension** (Subscription-Service):
   - Auth-service calls subscription-service to enrich JWT claims
   - Subscription-service adds: subscription_features, subscription_limits, subscription_status

3. **Request Authorization** (Domain Service):
   ```go
   func (s *OrderingService) CreateOrder(ctx context.Context, req CreateOrderRequest) error {
       // 1. Extract JWT claims (from Auth-Service)
       claims := ExtractJWTClaims(ctx)
       
       // 2. Check RBAC (from JWT roles)
       if !hasPermission(claims.Roles, "orders:create") {
           return ErrUnauthorized
       }
       
       // 3. Check Licensing (from JWT subscription_features)
       if !contains(claims.SubscriptionFeatures, "customer_portal") {
           return ErrFeatureNotAvailable
       }
       
       // 4. Check Resource Limits (from JWT subscription_limits or real-time check)
       if claims.DailyOrders >= claims.SubscriptionLimits.MaxOrdersPerDay {
           // Allow with overage tracking
           subscriptionService.ReportOverage(ctx, claims.TenantID, "order_count", 1)
       }
       
       // 5. Proceed with order creation
       return s.createOrder(ctx, req)
   }
   ```

### JWT Claims Structure

```json
{
  "sub": "user-uuid",
  "tenant_id": "tenant-uuid",
  "email": "user@example.com",
  "roles": ["admin", "user"],
  "subscription_features": [
    "customer_portal",
    "loyalty_program",
    "multi_outlet",
    "api_webhooks"
  ],
  "subscription_limits": {
    "max_riders": 15,
    "max_orders_per_day": 1000,
    "max_admins": 3
  },
  "subscription_status": "active",
  "subscription_plan": "growth",
  "subscription_expires_at": "2025-01-01T00:00:00Z"
}
```

### Manual Feature Overrides

Admins can manually enable/disable features:

```go
// Enable feature for tenant (override plan limitations)
err := subscriptionService.OverrideFeature(ctx, tenantID, "loyalty_program", "MANUAL_ENABLE", "Promotional access", adminUserID)

// Disable feature (even if included in plan)
err := subscriptionService.OverrideFeature(ctx, tenantID, "api_webhooks", "MANUAL_DISABLE", "Security restriction", adminUserID)
```

---

## Feature Entitlement Validation

### Feature Codes

**Core Features** (All Plans):
- `customer_portal` - Customer ordering portal
- `rider_app` - Dedicated rider application
- `admin_dashboard` - Admin dashboard access
- `mpesa_integration` - M-Pesa payment integration
- `sms_notifications` - SMS notifications
- `custom_domain` - Custom domain support

**Growth Features** (Growth+ Plans):
- `loyalty_program` - Loyalty points program
- `multi_outlet` - Multiple outlet support
- `advanced_analytics` - Advanced analytics dashboard
- `promo_codes` - Promo code management
- `group_ordering` - Group ordering feature

**Professional Features** (Professional Plan):
- `pos_integration` - POS system integration
- `route_optimization` - Route optimization algorithms
- `priority_support` - Priority customer support
- `api_webhooks` - Webhook API access
- `white_labeling` - White-label customization

### Feature Check Implementation

**Simple Check**:
```go
hasFeature, err := subscriptionService.HasFeature(ctx, tenantID, "loyalty_program")
if err != nil || !hasFeature {
    return ErrFeatureNotAvailable
}
```

**Cached Check** (Recommended):
```go
// Check cache first (Redis)
cacheKey := fmt.Sprintf("subscription:feature:%s:%s", tenantID, "loyalty_program")
cached, err := redis.Get(ctx, cacheKey)
if err == nil && cached == "true" {
    return nil // Feature available
}

// Cache miss - check subscription service
hasFeature, err := subscriptionService.HasFeature(ctx, tenantID, "loyalty_program")
if err != nil || !hasFeature {
    return ErrFeatureNotAvailable
}

// Cache result (60s TTL)
redis.Set(ctx, cacheKey, "true", 60*time.Second)
```

---

## Usage Tracking Integration

### Metric Types

- `order_count` - Daily order count (ordering-service)
- `rider_count` - Active rider count (logistics-service)
- `admin_count` - Active admin count (auth-service)
- `pos_transaction_count` - POS transaction count (pos-service)
- `delivery_task_count` - Delivery task count (logistics-service)
- `api_call_count` - API call count (all services)
- `active_user_count` - Monthly active users (auth-service)

### Usage Reporting Flow

1. **Real-Time Reporting** (Recommended):
   ```go
   // Report usage immediately after action
   err := subscriptionService.ReportUsage(ctx, tenantID, "order_count", 1, metadata)
   ```

2. **Batch Reporting** (For High Volume):
   ```go
   // Batch multiple usage reports
   reports := []UsageReport{
       {MetricType: "order_count", Value: 5, Metadata: {...}},
       {MetricType: "rider_count", Value: 1, Metadata: {...}},
   }
   err := subscriptionService.ReportUsageBatch(ctx, tenantID, reports)
   ```

3. **Snapshot Reporting** (Daily Aggregation):
   - Subscription service runs daily batch job
   - Aggregates raw usage into daily snapshots
   - Detects overages and calculates charges

### Usage Limit Enforcement

**Before Action**:
```go
limits, err := subscriptionService.GetLimits(ctx, tenantID)
if err != nil {
    return err
}

currentUsage, err := subscriptionService.GetUsage(ctx, tenantID, "order_count", "today")
if err != nil {
    return err
}

if currentUsage >= limits.MaxOrdersPerDay {
    // Option 1: Reject
    return ErrDailyLimitExceeded
    
    // Option 2: Allow with overage
    subscriptionService.ReportOverage(ctx, tenantID, "order_count", 1)
}
```

---

## Event-Driven Architecture

### Event Catalog

#### Outbound Events (Published by Subscription Service)

**subscription.created**
```json
{
  "event_id": "uuid",
  "event_type": "subscription.created",
  "tenant_id": "tenant-uuid",
  "timestamp": "2024-12-05T10:30:00Z",
  "data": {
    "subscription_id": "subscription-uuid",
    "plan_code": "STARTER",
    "status": "TRIAL",
    "trial_end": "2024-12-19T10:30:00Z"
  }
}
```

**subscription.entitlements_changed**
```json
{
  "event_id": "uuid",
  "event_type": "subscription.entitlements_changed",
  "tenant_id": "tenant-uuid",
  "timestamp": "2024-12-05T10:30:00Z",
  "data": {
    "added_features": ["loyalty_program"],
    "removed_features": [],
    "updated_limits": {
      "max_orders_per_day": 1000
    }
  }
}
```

**subscription.overage_detected**
```json
{
  "event_id": "uuid",
  "event_type": "subscription.overage_detected",
  "tenant_id": "tenant-uuid",
  "timestamp": "2024-12-05T10:30:00Z",
  "data": {
    "metric_type": "order_count",
    "plan_limit": 300,
    "actual_usage": 350,
    "overage_quantity": 50,
    "overage_charge": 187.50
  }
}
```

#### Inbound Events (Consumed by Subscription Service)

**auth.tenant.created**
```json
{
  "event_id": "uuid",
  "event_type": "auth.tenant.created",
  "tenant_id": "tenant-uuid",
  "timestamp": "2024-12-05T10:30:00Z",
  "data": {
    "tenant_id": "tenant-uuid",
    "tenant_slug": "urban-cafe",
    "name": "Urban Cafe"
  }
}
```

**treasury.payment.succeeded**
```json
{
  "event_id": "uuid",
  "event_type": "treasury.payment.succeeded",
  "tenant_id": "tenant-uuid",
  "timestamp": "2024-12-05T10:30:00Z",
  "data": {
    "payment_id": "payment-uuid",
    "invoice_id": "invoice-uuid",
    "amount": 6000.00,
    "currency": "KES"
  }
}
```

---

## Integration Security

### Authentication

**JWT Tokens**:
- Subscription service validates JWT tokens from auth-service
- Uses `shared/auth-client` library for validation
- JWKS endpoint: `https://sso.codevertexitsolutions.com/api/v1/.well-known/jwks.json`

**API Keys** (Service-to-Service):
- Service-to-service authentication via API keys
- Stored in K8s secrets
- Rotated quarterly

### Authorization

**Tenant Isolation**:
- All requests scoped by tenant_id
- Data isolation enforced at database level
- Feature gates isolated per tenant

**Admin Operations**:
- Superuser access for manual overrides
- Audit logging for all admin actions
- Approval workflows for plan changes

---

## Error Handling & Resilience

### Retry Policies

**Exponential Backoff**:
- Initial delay: 1 second
- Max delay: 30 seconds
- Max retries: 3

### Circuit Breaker

**Implementation**:
- Opens after 5 consecutive failures
- Half-open after 60 seconds
- Closes on successful request

### Fallback Strategies

**Subscription Service Unavailable**:
- Services can use cached feature gates (stale data acceptable for short periods)
- Services can allow requests with warning logs (graceful degradation)
- Alert operations team

**Feature Check Failure**:
- Default to "feature unavailable" (fail closed)
- Log error for monitoring
- Alert operations team

---

## Monitoring

### Metrics

- Feature check latency (p50, p95, p99)
- Feature check success/failure rates
- Usage reporting latency
- Usage reporting success/failure rates
- Overage detection accuracy
- Billing event emission success rates

### Alerts

- High feature check failure rate (>5%)
- Usage reporting failures
- Overage calculation errors
- Billing event emission failures
- Service unavailability

---

## References

- [Subscription Service Plan](../plan.md)
- [Subscription Service ERD](erd.md)
- [Auth Service Integration](../auth-service/auth-api/docs/integrations.md)
- [Treasury Service Integration](../finance-service/treasury-api/docs/integrations.md)
- [Ordering Service Integration](../ordering-service/ordering-backend/docs/integrations.md)
- [Logistics Service Integration](../logistics-service/logistics-api/docs/integrations.md)
- [Cross-Service Data Ownership](../docs/CROSS-SERVICE-DATA-OWNERSHIP.md)

