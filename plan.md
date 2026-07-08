# Subscription Service - Implementation Plan

## Executive Summary

**System Purpose**: Centralized subscription and licensing management platform for the entire Codevertex ecosystem, providing multi-tenant SaaS capabilities with tiered pricing, feature gating, usage tracking, and automated billing integration.

**Key Capabilities**:
- Subscription plan management (Starter, Growth, Professional tiers; **one-time** subscription type)
- Default one-time pricing: KES 80,000–2,000,000 by tier (seed/config); admin API for subscription levels (List/Get plans; create/update optional for MVP)
- Tenant subscription lifecycle (trial, active, cancelled, expired)
- Feature entitlement validation and gating
- Multi-service usage aggregation and tracking
- Overage calculation and billing event generation
- Plan transition workflows (upgrade/downgrade with proration)
- Trinity authorization (RBAC + Licensing + Resource permissions)

**Entity Ownership**: This service owns all subscription-related entities: plans, features, tenant subscriptions, usage tracking, overages, feature gates, and plan transitions. **Subscription Service does NOT own**: users/tenants (auth-service), invoices/payments (treasury-service), domain data (respective services).

**Inception Report Alignment**: Implements the 3-tier SaaS model defined in Urban Cafe Food Delivery System Inception Report:
- **Starter (Lite)**: KES 2,500/month - 1-2 Admins, 5 Riders, 300 Orders/Day
- **Growth (Standard)**: KES 6,000/month - 3 Admins, 15 Riders, 1,000 Orders/Day
- **Professional (Scale)**: KES 12,500/month - Unlimited Admins, 30 Riders, 2,500 Orders/Day

**Overage Charges**: Extra Rider (KES 250/month), Extra 100 Orders (KES 375/month)

---

## Technology Stack

### Core Framework
- **Language**: Go 1.22+
- **Architecture**: Clean/Hexagonal architecture
- **HTTP Router**: chi
- **API Documentation**: OpenAPI-first contracts
- **gRPC**: ConnectRPC (optional) for high-throughput feature checks

### Data & Caching
- **Primary Database**: PostgreSQL 16+
- **ORM**: Ent (schema-as-code migrations)
- **Caching**: Redis 7+ for feature gate caching, rate limiting, usage counters
- **Message Broker**: NATS JetStream for event-driven integration
- **RBAC:** No local roles/permissions store. **Identity and roles are sourced from auth-api** (GET `/api/v1/auth/me`). Frontends (subscriptions-ui) use TanStack Query with TTL to cache /me; nav and route protection use returned roles/permissions. API authorization: JWT validation via shared-auth-client.
- **Redis:** Feature gate caching, rate limiting, usage counters. Health check validates Postgres, Redis, and NATS.
- **Events:** NATS JetStream; outbox pattern for subscription lifecycle and billing events.

### Supporting Libraries
- **Validation**: go-playground/validator
- **Configuration**: kelseyhightower/envconfig
- **Logging**: zap (structured logging)
- **Tracing**: OpenTelemetry instrumentation
- **Metrics**: Prometheus

### DevOps & Observability
- **Containerization**: Multi-stage Docker builds
- **Orchestration**: Kubernetes (via centralized devops-k8s)
- **CI/CD**: GitHub Actions → ArgoCD
- **Monitoring**: Prometheus + Grafana, OpenTelemetry
- **APM**: Jaeger distributed tracing

---

## Domain Modules & Features

### 1. Plan Management

**Subscription-Specific Features**:
- Plan catalog (Starter, Growth, Professional, Custom)
- Feature definitions and grouping
- Tier limit configuration (max admins, riders, orders, API calls)
- Plan versioning and pricing updates
- Plan comparison matrix generation

**Entities Owned**:
- `subscription_plans` - Plan definitions
- `plan_features` - Features per plan
- `plan_pricing_history` - Pricing history and versions

**Integration Points**:
- **All Services**: Query available plans API for upgrade prompts

### 2. Tenant Subscriptions

**Subscription-Specific Features**:
- Subscription creation (onboarding)
- Trial period management (14-day default)
- Subscription activation and renewal
- Cancellation workflows
- Pause/resume functionality
- Subscription expiry handling

**Entities Owned**:
- `tenant_subscriptions` - Active subscriptions
- `subscription_history` - Subscription lifecycle events

**Integration Points**:
- **auth-service**: Tenant creation → auto-assign Starter plan
- **treasury-service**: Renewal → emit billing event. All payment/billing events to treasury MUST include **source_service** (`"subscription"`) and, where applicable, **product** and **tier** for income attribution.

### 3. Feature Entitlements

**Subscription-Specific Features**:
- Real-time feature access validation
- Feature gate caching (Redis)
- JWT claims integration
- Manual feature overrides (admin)
- Feature usage analytics

**Entities Owned**:
- `feature_gates` - Feature access control
- `feature_access_logs` - Access audit trail

**Integration Points**:
- **auth-service**: JWT claims extension
- **All Services**: Feature access validation API

### 4. Usage Tracking

**Subscription-Specific Features**:
- Multi-service usage aggregation
- Metric types: order_count, rider_count, api_calls, active_users
- Daily/monthly snapshots
- Usage forecasting and trending
- Usage analytics dashboard (consumed via APIs)

**Entities Owned**:
- `usage_tracking` - Raw usage reports
- `usage_snapshots` - Daily aggregated usage

**Integration Points**:
- **ordering-service**: Report order count
- **pos-service**: Report POS transactions
- **logistics-service**: Report rider count, delivery tasks
- **auth-service**: Report active users
- **All Services**: Report API call counts

### 5. Overage Management

**Subscription-Specific Features**:
- Overage detection (daily batch job)
- Overage calculation (quantity × unit price)
- Overage threshold configuration
- Overage alerts and notifications
- Billing event emission to treasury

**Entities Owned**:
- `overage_charges` - Calculated overages
- `overage_policies` - Overage rules per plan

**Integration Points**:
- **treasury-service**: Emit overage billing events
- **notifications-service**: Overage alerts

### 6. Plan Transitions

**Subscription-Specific Features**:
- Upgrade/downgrade scheduling
- Proration calculation
- Immediate vs. period-end transitions
- Grace period handling
- Transition approval workflows
- Feature activation/deactivation

**Entities Owned**:
- `plan_transitions` - Transition records

**Integration Points**:
- **treasury-service**: Proration invoicing
- **auth-service**: JWT claims update
- **All Services**: Feature entitlement refresh

### 7. Billing Integration

**Subscription-Specific Features**:
- Billing event generation (renewal, overage, proration)
- Invoice request creation
- Payment confirmation handling
- Dunning workflows (failed payments)
- Subscription suspension on non-payment

**Entities Owned**:
- `billing_events` - Events sent to treasury

**Integration Points**:
- **treasury-service**: Emit billing events, consume payment events
- **notifications-service**: Payment failure alerts

---

## Cross-Cutting Concerns

### Testing
- Go test suites with table-driven tests
- Testcontainers for integration testing
- Pact for contract tests
- Feature gate performance testing

### Observability
- Structured logging (zap)
- Tracing via OpenTelemetry
- Metrics exported via Prometheus
- Distributed tracing via Tempo/Jaeger

### Security
- OWASP ASVS baseline
- TLS everywhere
- Secrets via Vault/Parameter Store
- Rate limiting & anomaly detection middleware
- JWT validation via auth-service

### Scalability
- Stateless HTTP layer
- Background workers via NATS/Redis streams
- Feature gate caching (Redis)
- Usage aggregation batch processing

### Data Modelling
- Ent schemas as single source of truth
- Tenant/outlet discovery webhooks
- Outbox pattern for reliable domain events
- Immutable audit trail for subscription changes

---

## API & Protocol Strategy

- **REST-first**: Versioned routes (`/v1/{tenant}/subscriptions`), documented via OpenAPI
- **gRPC**: ConnectRPC for high-throughput feature checks (optional)
- **Webhooks**: Billing events, feature entitlement changes
- **Events**: NATS JetStream for async integration
- **Idempotency**: Keys, correlation IDs, distributed tracing context propagation

---

## Compliance & Risk Controls

- Align with Kenya Data Protection Act: explicit consent flows, user data export/delete endpoints, audit logging
- Subscription compliance: billing transparency, proration accuracy, cancellation policies
- Usage tracking compliance: data retention policies, usage data privacy
- Disaster recovery playbook, RTO/RPO targets (<1 hour)

---

## Sprint Delivery Plan

See `docs/sprints/` folder for detailed sprint plans:
- [x] Sprint 0: Platform Foundations (scaffolding, database, CI/CD) ✅
- Sprint 1: Plan Management (plan CRUD, feature definitions)
- Sprint 2: Tenant Subscriptions (subscription lifecycle, trial management)
- Sprint 3: Feature Entitlements (feature gates, JWT integration)
- Sprint 4: Usage Tracking (multi-service aggregation, snapshots)
- Sprint 5: Overage Management (detection, calculation, billing events)
- Sprint 6: Plan Transitions (upgrade/downgrade, proration)
- Sprint 7: Billing Integration (treasury events, payment handling)
- Sprint 8: Hardening (performance testing, security audit)
- Sprint 9: Launch & Support (production deployment, monitoring)

---

## Auth-Service Integration (Sprint 3 Dependency)

**Objective:** Enable auth-service to enrich JWT tokens with subscription data at login time.

**API Endpoint Required:**
```
GET /api/v1/tenants/{tenant_id}/subscription
Authorization: Bearer <service-jwt> OR X-API-Key: <key>
```

**Response Format:**
```json
{
  "tenant_id": "uuid",
  "plan_code": "PROFESSIONAL",
  "plan_name": "Professional",
  "status": "ACTIVE",
  "trial_ends_at": null,
  "current_period_start": "2026-01-01T00:00:00Z",
  "current_period_end": "2026-02-01T00:00:00Z",
  "features": [
    "group_ordering",
    "route_optimization",
    "multi_warehouse",
    "advanced_analytics"
  ],
  "limits": {
    "monthly_orders": 10000,
    "max_riders": 30,
    "max_admins": -1,
    "api_calls_per_day": 100000
  }
}
```

**Events Published:**
- `subscription.entitlements_changed` - When features/limits change
  - Payload: `{tenant_id, old_plan, new_plan, features, limits}`
  - auth-service subscribes to invalidate session cache

**Caching Strategy:**
- auth-service caches subscription data in Redis (5 min TTL)
- On `subscription.entitlements_changed` event, auth-service invalidates cache
- Graceful degradation: if subscription-service unavailable, use cached data or defaults

---

## Feature Gate Check API (For Services Not Using JWT)

**Endpoint:**
```
GET /api/v1/tenants/{tenant_id}/features/{feature_code}/check
Authorization: Bearer <jwt> OR X-API-Key: <key>
```

**Response:**
```json
{
  "feature_code": "group_ordering",
  "enabled": true,
  "limit": null,
  "plan_required": "GROWTH"
}
```

**Note:** Services using `shared-auth-client` v0.2.0+ should check features via JWT claims (zero latency) rather than this API.

---

## Runtime Ports & Environments

- **Local development**: Service runs on port **4005**
- **Cloud deployment**: All backend services listen on **port 4000** for consistency behind ingress controllers

---

## References

- [Integration Guide](docs/integrations.md)
- [Entity Relationship Diagram](docs/erd.md)
- [Sprint Plans](docs/sprints/)
- [API Documentation](docs/api/openapi.yaml)
- [Licensing Architecture Audit](../brain/65f2e568-bd71-4629-9dbb-590785a495eb/licensing-architecture-audit.md)

**Note**: This is a backend-only service (no dedicated UI). Admin functions for plan management are exposed via existing admin dashboards consuming this service's APIs.
