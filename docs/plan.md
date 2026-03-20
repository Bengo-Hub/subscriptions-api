# Subscription Service - Implementation Plan

## Executive Summary

**System Purpose**: Centralized subscription and licensing management platform for the entire BengoBox ecosystem, providing multi-tenant SaaS capabilities with tiered pricing, feature gating, usage tracking, and automated billing integration.

**March 20, 2026 update**: Added **ServiceChargePlan** entity and per-service subscription flexibility. Service charge plans allow the platform to charge a commission (percentage, fixed, or tiered) per transaction processed through a service instead of (or in addition to) flat subscription fees. Each `ProductSubscription` can now reference a `ServiceChargePlan` and/or an `override_plan_id` for per-service plan flexibility — enabling the same tenant to have different subscription tiers and service charge rates per service. 6 service charge plans seeded: ordering (5%/3%), logistics (7%), POS (2%), TruLoad (10%), and a universal flat fee. Treasury-api updated to accept service charge fields on payment intents and accumulate them during payout settlement.

**Key Capabilities**:
- **Subscription types**: **Product** (a-la-carte), **Tiered Bundles** (Starter, Growth, Prof), **One-time** (summation of product onetime fees), and **Service Charge** (commission-based per transaction).
- **Per-service flexibility**: Different services accessed by the same tenant can have different subscription plans and service charge rates.
- **Core System Features**: SSO, Notifications (Email), Subscription Service, and Payment Gateways are **included by default** in all plans/tiers and not sold as individual products.
- **Credit-Based Notifications**: SMS and WhatsApp require tenants to purchase credits at platform-set rates. Email remains free.
- Subscription plan management (Starter, Growth, Professional tiers)
- Tenant subscription lifecycle (trial, active, cancelled, expired)
- Feature entitlement validation and gating
- Multi-service usage aggregation and tracking
- Overage calculation and billing event generation
- Plan transition workflows (upgrade/downgrade with proration)
- Trinity authorization (RBAC + Licensing + Resource permissions)
- **One-time pricing**: Minimum KES 80,000 per module. A-la-carte combinations sum these individual one-time fees.
- **Treasury integration**: Subscription-api sends **source_service** (and product/tier where applicable) on every payment/billing event to treasury for money-by-source attribution (equity/royalty, analytics).

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
- `plan_limits` - Tier limits per plan
- `plan_pricing` - Pricing history and versions

**Integration Points**:
- **Admin UI**: Plan management interface
- **All Services**: Query available plans API

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
- `trial_tracking` - Trial period management

**Integration Points**:
- **auth-service**: Tenant creation → auto-assign Starter plan
- **treasury-service**: Renewal → emit billing event. All payment/billing events to treasury MUST include **source_service** (`"subscription"`) and, where applicable, **product** and **tier** so treasury can attribute income by source.

### 3. Feature Entitlements

**Subscription-Specific Features**:
- Real-time feature access validation
- Feature gate caching (Redis)
- JWT claims integration
- Manual feature overrides (admin)
- Feature usage analytics

**Entities Owned**:
- `feature_gates` - Feature access control
- `feature_overrides` - Manual admin overrides
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
- Usage analytics dashboard

**Entities Owned**:
- `usage_tracking` - Raw usage reports
- `usage_snapshots` - Daily aggregated usage
- `usage_forecasts` - Projected usage

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
- `overage_alerts` - Threshold breach tracking

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
- `proration_calculations` - Proration audit trail
- `transition_approvals` - Manual approval tracking

**Integration Points**:
- **treasury-service**: Proration invoicing
- **auth-service**: JWT claims update
- **All Services**: Feature entitlement refresh

### 7. Billing Integration

**Subscription-Specific Features**:
- Billing event generation (renewal, overage, proration, one-time)
- **source_service**: Subscription-api sends **source_service** (and product/tier where applicable) on every payment/billing event to treasury so treasury can attribute income by source (money-by-source analytics, equity/royalty allocation).
- Invoice request creation
- Payment confirmation handling
- Dunning workflows (failed payments)
- Subscription suspension on non-payment

**Entities Owned**:
- `billing_events` - Events sent to treasury (with source_service)
- `payment_confirmations` - Payment status tracking
- `dunning_attempts` - Failed payment retry tracking

**Integration Points**:
- **treasury-service**: Emit billing events with source_service, consume payment events
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
- **RBAC:** No local Permission/Role schema; RBAC is enforced via auth-api JWT. All subscription resources (plans, subscriptions, features, bundles, products) use the standard **eight actions** defined in auth-api: `add`, `read`, `read_own`, `change`, `change_own`, `delete`, `manage`, `manage_own`. The API validates JWT via `shared-auth-client` middleware; authorization checks are performed using claims from auth-api.
- **Subscription tiering in auth layer**: Each microservice (ordering, logistics, treasury, etc.) integrates subscription tier and product-level tiering in its authorization layer — e.g. feature gates and limits from subscription-service (via JWT claims or real-time check) before allowing access or usage.
- **Seed:** Core data is seeded by `cmd/seed`: (1) **Base Products** (Hidden from storefront: auth, notifications, subscription, treasury); (2) **Selectable Modules** (POS, Ordering, Logistics, Storefront, Inventory, etc.); (3) **Subscription Plans** — Urban Loft Tiers (Starter: 20k/mo, Growth: 45k/mo, Professional: 95k/mo); (4) **One-time summation logic** — A-la-carte plans sum the `onetime_price` of each included module (min 80k each).
- **Discounts & Promo Codes**: Plans support `discount_rules` for Yearly (fixed %), Loyal customers, and New Customer promotions.
- **Notification Credits**: Implementation of `TenantCredit` system for SMS/WhatsApp. Platform admins set rates; tenants purchase bucketed credits.

### Scalability
- Stateless HTTP layer
- Background workers via NATS/Redis streams
- Feature gate caching (Redis)
- Usage aggregation batch processing

### Data Modelling
- Ent schemas as single source of truth ✅
- Tenant/outlet discovery webhooks
- Outbox pattern for reliable domain events ✅ **IMPLEMENTED** (using shared-events library)
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
- Sprint 0: Platform Foundations (scaffolding, database, CI/CD)
- Sprint 1: Plan Management (plan CRUD, feature definitions)
- Sprint 2: Tenant Subscriptions (subscription lifecycle, trial management)
- Sprint 3: Feature Entitlements (feature gates, JWT integration)
- Sprint 4: Usage Tracking (multi-service aggregation, snapshots)
- Sprint 5: Overage Management (detection, calculation, billing events)
- Sprint 6: Plan Transitions (upgrade/downgrade, proration)
- Sprint 7: Billing Integration (treasury events, payment handling)
- Sprint 8: Admin UI (plan management, subscription dashboard)
- Sprint 9: Hardening (performance testing, security audit)
- Sprint 10: Launch & Support (production deployment, monitoring)

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
