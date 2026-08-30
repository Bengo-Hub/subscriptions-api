# Subscription Service – Comprehensive Entity Relationship Diagram

The subscription service manages all subscription and licensing operations, providing centralized plan management, feature gating, usage tracking, and billing integration for the Codevertex ecosystem.

> **Conventions**
> - UUID primary keys (`id UUID PRIMARY KEY DEFAULT gen_random_uuid()`).
> - `tenant_id UUID NOT NULL` on all operational tables for multi-tenant isolation.
> - Timestamps are `TIMESTAMPTZ` with timezone awareness.
> - Monetary values use `NUMERIC(18,2)` with decimal precision.
> - All tables include `created_at TIMESTAMPTZ DEFAULT NOW()` and `updated_at TIMESTAMPTZ DEFAULT NOW()`.

---

## Subscription Plans

### subscription_plans

**Purpose**: Subscription plan definitions organized by service_tag and tier (Starter, Growth, Professional).

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY | Plan identifier |
| `plan_code` | VARCHAR(50) | NOT NULL, UNIQUE | Plan code (e.g. `ORDERING-GROWTH-MONTHLY`) |
| `name` | VARCHAR(255) | NOT NULL | Display name |
| `description` | TEXT | | Plan description |
| `service_tag` | VARCHAR(50) | NOT NULL | Billable service: `ordering`, `pos`, `logistics`, `inventory`, `erp`, `treasury`, `truload`, `marketflow`, `isp_billing`, `projects` |
| `billing_cycle` | VARCHAR(20) | NOT NULL, CHECK | MONTHLY, QUARTERLY, ANNUAL, ONE_TIME |
| `plan_type` | VARCHAR(50) | NOT NULL | TIERED, STANDALONE_SERVICE, BUNDLE, CUSTOM |
| `base_price` | NUMERIC(18,2) | NOT NULL | Base subscription price |
| `currency` | VARCHAR(3) | NOT NULL, DEFAULT 'KES' | ISO currency code |
| `is_active` | BOOLEAN | DEFAULT true | Active status |
| `is_public` | BOOLEAN | DEFAULT true | Publicly available |
| `tier_order` | INTEGER | NOT NULL | Display order (1=Starter, 2=Growth, 3=Professional) |
| `tier_limits_json` | JSONB | | Tier limits (max_admins, max_riders, max_orders_per_month, api_calls_per_month) |
| `discount_rules` | JSONB | | Rules for dynamic discounting (e.g. yearly, loyal) |
| `metadata` | JSONB | | Additional plan metadata |
| `created_at` | TIMESTAMPTZ | DEFAULT NOW() | Creation timestamp |
| `updated_at` | TIMESTAMPTZ | DEFAULT NOW() | Last update timestamp |

> **Added May 2026** via Atlas migration `20260521183226_add_service_tag_to_plans.sql`. Previously plans used a Product/Bundle model; `service_tag` replaces that with a direct per-service association. 85 plans seeded across 10 service tags.

**Example tier_limits_json**:
```json
{
  "max_admins": 2,
  "max_riders": 5,
  "max_orders_per_month": 1000,
  "max_outlets": 1,
  "api_calls_per_month": 10000
}
```

**Indexes**:
- `idx_subscription_plans_plan_code` ON `plan_code`
- `idx_subscription_plans_is_active` ON `is_active`
- `idx_subscription_plans_tier_order` ON `tier_order`
- `idx_subscription_plans_service_tag` ON `service_tag`

**Constraints**:
- CHECK: `billing_cycle IN ('MONTHLY', 'QUARTERLY', 'ANNUAL', 'ONE_TIME')`
- CHECK: `tier_order > 0`

### plan_features

**Purpose**: Features included in each subscription plan.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY | Feature mapping identifier |
| `plan_id` | UUID | NOT NULL, FK → subscription_plans(id) | Plan identifier |
| `feature_code` | VARCHAR(100) | NOT NULL | Feature code (loyalty_program, multi_outlet, pos_integration, etc.) |
| `is_included` | BOOLEAN | DEFAULT true | Feature included in plan |
| `limit_value` | INTEGER | | Feature-specific limit (e.g., max 5 outlets) |
| `metadata` | JSONB | | Additional feature metadata |
| `created_at` | TIMESTAMPTZ | DEFAULT NOW() | Creation timestamp |

**Feature Codes** (Examples):
- Core: `customer_portal`, `rider_app`, `admin_dashboard`, `mpesa_integration`, `sms_notifications`
- Growth: `loyalty_program`, `multi_outlet`, `advanced_analytics`, `promo_codes`, `group_ordering`
- Professional: `pos_integration`, `route_optimization`, `priority_support`, `api_webhooks`, `white_labeling`

**Indexes**:
- `idx_plan_features_plan_id` ON `plan_id`
- `idx_plan_features_feature_code` ON `feature_code`
- `idx_plan_features_unique` ON `(plan_id, feature_code)` UNIQUE

**Relations**:
- `plan_id` → `subscription_plans(id)` ON DELETE CASCADE

### plan_pricing_history

**Purpose**: Historical pricing changes for audit and proration calculations.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY | Pricing record identifier |
| `plan_id` | UUID | NOT NULL, FK → subscription_plans(id) | Plan identifier |
| `base_price` | NUMERIC(18,2) | NOT NULL | Base price at this version |
| `effective_from` | DATE | NOT NULL | Effective start date |
| `effective_to` | DATE | | Effective end date (null if current) |
| `changed_by` | UUID | FK → users | User who changed pricing |
| `change_reason` | TEXT | | Reason for pricing change |
| `metadata` | JSONB | | Additional pricing metadata |
| `created_at` | TIMESTAMPTZ | DEFAULT NOW() | Creation timestamp |

**Indexes**:
- `idx_plan_pricing_history_plan_id` ON `plan_id`
- `idx_plan_pricing_history_effective_dates` ON `(effective_from, effective_to)`

**Relations**:
- `plan_id` → `subscription_plans(id)` ON DELETE CASCADE
- `changed_by` → `users(id)` (via auth-service sync)

---

## Tenant Subscriptions

### tenant_subscriptions

**Purpose**: Active and historical tenant subscriptions.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY | Subscription identifier |
| `tenant_id` | UUID | NOT NULL, FK → tenants | Tenant identifier (from auth-service) |
| `plan_id` | UUID | NOT NULL, FK → subscription_plans(id) | Current subscription plan |
| `status` | VARCHAR(20) | NOT NULL, CHECK | TRIAL, ACTIVE, CANCELLED, PAUSED, EXPIRED |
| `billing_cycle` | VARCHAR(20) | NOT NULL, CHECK | MONTHLY, QUARTERLY, SEMI_ANNUAL, ANNUAL, ONE_TIME — the tenant's chosen billing period; SEMI_ANNUAL/ANNUAL (≥6 months) waive the one-time setup fee |
| `applied_discount` | NUMERIC(18,2)| DEFAULT 0 | Discount applied to this subscription based on rules |
| `current_period_start` | DATE | NOT NULL | Current billing period start |
| `current_period_end` | DATE | NOT NULL | Current billing period end |
| `trial_start` | DATE | | Trial period start date |
| `trial_end` | DATE | | Trial period end date |
| `cancelled_at` | TIMESTAMPTZ | | Cancellation timestamp |
| `cancel_reason` | TEXT | | Cancellation reason |
| `bundle_code` | VARCHAR(50) | | Code of the bundle this subscription belongs to if any |
| `paused_at` | TIMESTAMPTZ | | Pause timestamp |
| `pause_reason` | TEXT | | Pause reason |
| `next_plan_id` | UUID | FK → subscription_plans(id) | Scheduled plan for next period (upgrades/downgrades) |
| `auto_renew` | BOOLEAN | DEFAULT true | Auto-renewal flag |
| `metadata` | JSONB | | Additional subscription metadata |
| `created_at` | TIMESTAMPTZ | DEFAULT NOW() | Creation timestamp |
| `updated_at` | TIMESTAMPTZ | DEFAULT NOW() | Last update timestamp |

**Indexes**:
- `idx_tenant_subscriptions_tenant_id` ON `tenant_id` UNIQUE
- `idx_tenant_subscriptions_plan_id` ON `plan_id`
- `idx_tenant_subscriptions_status` ON `status`
- `idx_tenant_subscriptions_period_end` ON `current_period_end`
- `idx_tenant_subscriptions_trial_end` ON `trial_end`

**Relations**:
- `tenant_id` → `tenants(id)` (via auth-service sync)
- `plan_id` → `subscription_plans(id)`
- `next_plan_id` → `subscription_plans(id)`

**Constraints**:
- CHECK: `status IN ('TRIAL', 'ACTIVE', 'CANCELLED', 'PAUSED', 'EXPIRED')`
- CHECK: `billing_cycle IN ('MONTHLY', 'QUARTERLY', 'SEMI_ANNUAL', 'ANNUAL', 'ONE_TIME')`
- CHECK: `current_period_end > current_period_start`
- CHECK: `trial_end IS NULL OR trial_end >= trial_start`

### subscription_history

**Purpose**: Subscription lifecycle event tracking.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY | Event identifier |
| `tenant_subscription_id` | UUID | NOT NULL, FK → tenant_subscriptions(id) | Subscription identifier |
| `event_type` | VARCHAR(50) | NOT NULL | CREATED, TRIAL_STARTED, ACTIVATED, RENEWED, CANCELLED, PAUSED, RESUMED, EXPIRED |
| `previous_status` | VARCHAR(20) | | Previous status |
| `new_status` | VARCHAR(20) | NOT NULL | New status |
| `previous_plan_id` | UUID | FK → subscription_plans(id) | Previous plan (for transitions) |
| `new_plan_id` | UUID | FK → subscription_plans(id) | New plan |
| `actor_user_id` | UUID | FK → users | User who triggered event |
| `actor_type` | VARCHAR(20) | | USER, SYSTEM, ADMIN |
| `metadata` | JSONB | | Event-specific metadata |
| `occurred_at` | TIMESTAMPTZ | NOT NULL, DEFAULT NOW() | Event timestamp |

**Indexes**:
- `idx_subscription_history_tenant_subscription_id` ON `tenant_subscription_id`
- `idx_subscription_history_event_type` ON `event_type`
- `idx_subscription_history_occurred_at` ON `occurred_at`

**Relations**:
- `tenant_subscription_id` → `tenant_subscriptions(id)` ON DELETE CASCADE
- `previous_plan_id`, `new_plan_id` → `subscription_plans(id)`
- `actor_user_id` → `users(id)` (via auth-service sync)

**Constraints**:
- CHECK: `event_type IN ('CREATED', 'TRIAL_STARTED', 'ACTIVATED', 'RENEWED', 'CANCELLED', 'PAUSED', 'RESUMED', 'EXPIRED')`
- CHECK: `actor_type IN ('USER', 'SYSTEM', 'ADMIN')`

---

## Feature Entitlements

### feature_gates

**Purpose**: Feature access control per tenant (includes manual overrides).

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY | Feature gate identifier |
| `tenant_id` | UUID | NOT NULL, FK → tenants | Tenant identifier |
| `feature_code` | VARCHAR(100) | NOT NULL | Feature code |
| `is_enabled` | BOOLEAN | NOT NULL, DEFAULT false | Feature enabled flag |
| `override_type` | VARCHAR(20) | | PLAN_INCLUDED, MANUAL_ENABLE, MANUAL_DISABLE |
| `override_reason` | TEXT | | Override reason (for manual changes) |
| `effective_from` | DATE | | Override effective start date |
| `effective_to` | DATE | | Override effective end date (null if permanent) |
| `created_by` | UUID | FK → users | User who created override |
| `created_at` | TIMESTAMPTZ | DEFAULT NOW() | Creation timestamp |
| `updated_at` | TIMESTAMPTZ | DEFAULT NOW() | Last update timestamp |

**Indexes**:
- `idx_feature_gates_tenant_id` ON `tenant_id`
- `idx_feature_gates_feature_code` ON `feature_code`
- `idx_feature_gates_unique` ON `(tenant_id, feature_code)` UNIQUE
- `idx_feature_gates_effective_dates` ON `(effective_from, effective_to)`

**Relations**:
- `tenant_id` → `tenants(id)` (via auth-service sync)
- `created_by` → `users(id)` (via auth-service sync)

**Constraints**:
- CHECK: `override_type IN ('PLAN_INCLUDED', 'MANUAL_ENABLE', 'MANUAL_DISABLE')`

### feature_access_logs

**Purpose**: Audit trail for feature access checks (for analytics and security).

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY | Log entry identifier |
| `tenant_id` | UUID | NOT NULL | Tenant identifier |
| `user_id` | UUID | | User identifier (if user-initiated) |
| `service_code` | VARCHAR(50) | NOT NULL | Service requesting access (ordering, pos, logistics, etc.) |
| `feature_code` | VARCHAR(100) | NOT NULL | Feature code checked |
| `access_granted` | BOOLEAN | NOT NULL | Access granted flag |
| `denial_reason` | VARCHAR(100) | | Reason if access denied (PLAN_LIMITATION, LIMIT_EXCEEDED, etc.) |
| `request_metadata` | JSONB | | Request context (IP, user agent, etc.) |
| `checked_at` | TIMESTAMPTZ | NOT NULL, DEFAULT NOW() | Check timestamp |

**Indexes**:
- `idx_feature_access_logs_tenant_id` ON `tenant_id`
- `idx_feature_access_logs_service_code` ON `service_code`
- `idx_feature_access_logs_feature_code` ON `feature_code`
- `idx_feature_access_logs_checked_at` ON `checked_at` (TimescaleDB hypertable)

**Relations**:
- `tenant_id` → `tenants(id)` (via auth-service sync)
- `user_id` → `users(id)` (via auth-service sync)

---

## Usage Tracking

### usage_tracking

**Purpose**: Raw usage reports from all services.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY | Usage record identifier |
| `tenant_id` | UUID | NOT NULL, FK → tenants | Tenant identifier |
| `service_code` | VARCHAR(50) | NOT NULL | Service reporting usage (ordering, pos, logistics, auth) |
| `metric_type` | VARCHAR(100) | NOT NULL | Metric type (order_count, rider_count, api_call_count, active_user_count) |
| `metric_date` | DATE | NOT NULL | Date of metric |
| `metric_value` | INTEGER | NOT NULL | Metric quantity |
| `metadata` | JSONB | | Additional metric metadata |
| `reported_at` | TIMESTAMPTZ | NOT NULL, DEFAULT NOW() | Report timestamp |
| `created_at` | TIMESTAMPTZ | DEFAULT NOW() | Creation timestamp |

**Metric Types** (Examples):
- `order_count` - Daily order count (ordering-service)
- `rider_count` - Active rider count (logistics-service)
- `pos_transaction_count` - POS transactions (pos-service)
- `active_user_count` - Monthly active users (auth-service)
- `api_call_count` - API calls per service
- `delivery_task_count` - Delivery tasks (logistics-service)

**Indexes**:
- `idx_usage_tracking_tenant_id` ON `tenant_id`
- `idx_usage_tracking_service_code` ON `service_code`
- `idx_usage_tracking_metric_type` ON `metric_type`
- `idx_usage_tracking_metric_date` ON `metric_date`
- `idx_usage_tracking_unique` ON `(tenant_id, service_code, metric_type, metric_date)` UNIQUE

**Relations**:
- `tenant_id` → `tenants(id)` (via auth-service sync)

### usage_snapshots

**Purpose**: Aggregated daily usage snapshots for billing and analytics.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY | Snapshot identifier |
| `tenant_subscription_id` | UUID | NOT NULL, FK → tenant_subscriptions(id) | Subscription identifier |
| `snapshot_date` | DATE | NOT NULL | Snapshot date |
| `order_count` | INTEGER | DEFAULT 0 | Daily order count |
| `rider_count` | INTEGER | DEFAULT 0 | Active rider count |
| `admin_count` | INTEGER | DEFAULT 0 | Active admin count |
| `api_call_count` | INTEGER | DEFAULT 0 | API calls made |
| `active_user_count` | INTEGER | DEFAULT 0 | Monthly active users |
| `pos_transaction_count` | INTEGER | DEFAULT 0 | POS transactions |
| `delivery_task_count` | INTEGER | DEFAULT 0 | Delivery tasks |
| `metadata` | JSONB | | Additional snapshot metadata |
| `created_at` | TIMESTAMPTZ | DEFAULT NOW() | Creation timestamp |

**Indexes**:
- `idx_usage_snapshots_tenant_subscription_id` ON `tenant_subscription_id`
- `idx_usage_snapshots_snapshot_date` ON `snapshot_date`
- `idx_usage_snapshots_unique` ON `(tenant_subscription_id, snapshot_date)` UNIQUE

**Relations**:
- `tenant_subscription_id` → `tenant_subscriptions(id)` ON DELETE CASCADE

---

## Overage Management

### overage_charges

**Purpose**: Calculated overage charges for billing.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY | Overage charge identifier |
| `tenant_subscription_id` | UUID | NOT NULL, FK → tenant_subscriptions(id) | Subscription identifier |
| `usage_period_start` | DATE | NOT NULL | Usage period start |
| `usage_period_end` | DATE | NOT NULL | Usage period end |
| `metric_type` | VARCHAR(100) | NOT NULL | Metric exceeding limit (extra_riders, extra_orders) |
| `plan_limit` | INTEGER | NOT NULL | Plan limit for metric |
| `actual_usage` | INTEGER | NOT NULL | Actual usage |
| `overage_quantity` | INTEGER | NOT NULL | Overage amount (actual - limit) |
| `unit_price` | NUMERIC(18,2) | NOT NULL | Price per unit overage |
| `total_charge` | NUMERIC(18,2) | NOT NULL | Total overage charge |
| `currency` | VARCHAR(3) | NOT NULL, DEFAULT 'KES' | ISO currency code |
| `invoice_id` | UUID | FK → invoices | Generated invoice (from treasury) |
| `calculated_at` | TIMESTAMPTZ | NOT NULL, DEFAULT NOW() | Calculation timestamp |
| `invoiced_at` | TIMESTAMPTZ | | Invoice generation timestamp |
| `metadata` | JSONB | | Additional overage metadata |
| `created_at` | TIMESTAMPTZ | DEFAULT NOW() | Creation timestamp |

**Indexes**:
- `idx_overage_charges_tenant_subscription_id` ON `tenant_subscription_id`
- `idx_overage_charges_usage_period` ON `(usage_period_start, usage_period_end)`
- `idx_overage_charges_metric_type` ON `metric_type`
- `idx_overage_charges_invoice_id` ON `invoice_id`

**Relations**:
- `tenant_subscription_id` → `tenant_subscriptions(id)` ON DELETE CASCADE
- `invoice_id` → `invoices(id)` (via treasury-service integration)

**Constraints**:
- CHECK: `overage_quantity > 0`
- CHECK: `actual_usage > plan_limit`
- CHECK: `total_charge = overage_quantity * unit_price`

### overage_policies

**Purpose**: Overage pricing rules per plan and metric.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY | Policy identifier |
| `plan_id` | UUID | NOT NULL, FK → subscription_plans(id) | Plan identifier |
| `metric_type` | VARCHAR(100) | NOT NULL | Metric type (extra_riders, extra_orders) |
| `unit_price` | NUMERIC(18,2) | NOT NULL | Price per unit overage |
| `currency` | VARCHAR(3) | NOT NULL, DEFAULT 'KES' | ISO currency code |
| `overage_allowed` | BOOLEAN | DEFAULT true | Overage allowed flag |
| `hard_limit` | INTEGER | | Hard limit (reject if exceeded, null if no hard limit) |
| `is_active` | BOOLEAN | DEFAULT true | Active status |
| `metadata` | JSONB | | Additional policy metadata |
| `created_at` | TIMESTAMPTZ | DEFAULT NOW() | Creation timestamp |
| `updated_at` | TIMESTAMPTZ | DEFAULT NOW() | Last update timestamp |

**Example Policies** (Per Inception Report):
- Starter/Growth/Professional → `extra_riders`: KES 250/month per rider
- Starter/Growth/Professional → `extra_orders`: KES 375 per 100 orders/month

**Indexes**:
- `idx_overage_policies_plan_id` ON `plan_id`
- `idx_overage_policies_metric_type` ON `metric_type`
- `idx_overage_policies_unique` ON `(plan_id, metric_type)` UNIQUE

**Relations**:
- `plan_id` → `subscription_plans(id)` ON DELETE CASCADE

---

## Plan Transitions

### plan_transitions

**Purpose**: Plan upgrade/downgrade tracking.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY | Transition identifier |
| `tenant_subscription_id` | UUID | NOT NULL, FK → tenant_subscriptions(id) | Subscription identifier |
| `from_plan_id` | UUID | NOT NULL, FK → subscription_plans(id) | Source plan |
| `to_plan_id` | UUID | NOT NULL, FK → subscription_plans(id) | Target plan |
| `transition_type` | VARCHAR(20) | NOT NULL, CHECK | UPGRADE, DOWNGRADE, RENEWAL |
| `transition_timing` | VARCHAR(20) | NOT NULL, CHECK | IMMEDIATE, PERIOD_END, SCHEDULED |
| `scheduled_date` | DATE | | Scheduled transition date (if not immediate) |
| `executed_at` | TIMESTAMPTZ | | Actual execution timestamp |
| `status` | VARCHAR(20) | NOT NULL, CHECK | SCHEDULED, PENDING, EXECUTED, CANCELLED, FAILED |
| `proration_credit` | NUMERIC(18,2) | DEFAULT 0 | Proration credit amount |
| `proration_charge` | NUMERIC(18,2) | DEFAULT 0 | Proration charge amount |
| `net_amount` | NUMERIC(18,2) | | Net amount (charge - credit) |
| `invoice_id` | UUID | FK → invoices | Generated invoice (from treasury) |
| `requested_by` | UUID | FK → users | User who requested transition |
| `approved_by` | UUID | FK → users | User who approved transition (if required) |
| `failure_reason` | TEXT | | Failure reason (if failed) |
| `metadata` | JSONB | | Additional transition metadata |
| `created_at` | TIMESTAMPTZ | DEFAULT NOW() | Creation timestamp |
| `updated_at` | TIMESTAMPTZ | DEFAULT NOW() | Last update timestamp |

**Indexes**:
- `idx_plan_transitions_tenant_subscription_id` ON `tenant_subscription_id`
- `idx_plan_transitions_from_plan_id` ON `from_plan_id`
- `idx_plan_transitions_to_plan_id` ON `to_plan_id`
- `idx_plan_transitions_status` ON `status`
- `idx_plan_transitions_scheduled_date` ON `scheduled_date`

**Relations**:
- `tenant_subscription_id` → `tenant_subscriptions(id)` ON DELETE CASCADE
- `from_plan_id`, `to_plan_id` → `subscription_plans(id)`
- `invoice_id` → `invoices(id)` (via treasury-service integration)
- `requested_by`, `approved_by` → `users(id)` (via auth-service sync)

**Constraints**:
- CHECK: `transition_type IN ('UPGRADE', 'DOWNGRADE', 'RENEWAL')`
- CHECK: `transition_timing IN ('IMMEDIATE', 'PERIOD_END', 'SCHEDULED')`
- CHECK: `status IN ('SCHEDULED', 'PENDING', 'EXECUTED', 'CANCELLED', 'FAILED')`
- CHECK: `net_amount = proration_charge - proration_credit`

---

## Billing Integration

### billing_events

**Purpose**: Billing events sent to treasury-service for invoice generation.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY | Event identifier |
| `tenant_id` | UUID | NOT NULL, FK → tenants | Tenant identifier |
| `event_type` | VARCHAR(50) | NOT NULL | SUBSCRIPTION_RENEWAL, OVERAGE_CHARGE, PLAN_CHANGE_PRORATION |
| `reference_type` | VARCHAR(50) | | Reference entity type (tenant_subscription, overage_charge, plan_transition) |
| `reference_id` | UUID | | Reference entity ID |
| `amount` | NUMERIC(18,2) | NOT NULL | Billing amount |
| `currency` | VARCHAR(3) | NOT NULL, DEFAULT 'KES' | ISO currency code |
| `description` | TEXT | NOT NULL | Human-readable description |
| `line_items_json` | JSONB | | Invoice line items |
| `invoice_id` | UUID | FK → invoices | Generated invoice (updated by treasury) |
| `status` | VARCHAR(20) | NOT NULL, CHECK | PENDING, SENT, INVOICED, FAILED |
| `sent_at` | TIMESTAMPTZ | | Event emission timestamp |
| `invoiced_at` | TIMESTAMPTZ | | Invoice creation timestamp |
| `failure_reason` | TEXT | | Failure reason (if failed) |
| `retry_count` | INTEGER | DEFAULT 0 | Retry attempt count |
| `metadata` | JSONB | | Additional event metadata |
| `created_at` | TIMESTAMPTZ | DEFAULT NOW() | Creation timestamp |
| `updated_at` | TIMESTAMPTZ | DEFAULT NOW() | Last update timestamp |

**Example line_items_json**:
```json
{
  "items": [
    {
      "description": "Growth Plan - December 2025",
      "quantity": 1,
      "unit_price": 6000.00,
      "subtotal": 6000.00
    },
    {
      "description": "3 Extra Riders × KES 250",
      "quantity": 3,
      "unit_price": 250.00,
      "subtotal": 750.00
    }
  ],
  "total": 6750.00
}
```

**Indexes**:
- `idx_billing_events_tenant_id` ON `tenant_id`
- `idx_billing_events_event_type` ON `event_type`
- `idx_billing_events_reference` ON `(reference_type, reference_id)`
- `idx_billing_events_status` ON `status`
- `idx_billing_events_invoice_id` ON `invoice_id`

**Relations**:
- `tenant_id` → `tenants(id)` (via auth-service sync)
- `invoice_id` → `invoices(id)` (via treasury-service integration)

**Constraints**:
- CHECK: `event_type IN ('SUBSCRIPTION_RENEWAL', 'OVERAGE_CHARGE', 'PLAN_CHANGE_PRORATION')`
- CHECK: `status IN ('PENDING', 'SENT', 'INVOICED', 'FAILED')`

---

## Integrations & Eventing

### integration_settings

**Purpose**: Configuration for external service integrations.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY | Setting identifier |
| `tenant_id` | UUID | FK → tenants | Tenant-specific settings (null for global) |
| `service_code` | VARCHAR(50) | NOT NULL | Service code (auth, treasury, ordering, pos, logistics, notifications) |
| `config_json` | JSONB | | Service configuration |
| `status` | VARCHAR(20) | NOT NULL, CHECK | ACTIVE, INACTIVE, ERROR |
| `last_sync_at` | TIMESTAMPTZ | | Last successful sync timestamp |
| `metadata` | JSONB | | Additional integration metadata |
| `created_at` | TIMESTAMPTZ | DEFAULT NOW() | Creation timestamp |
| `updated_at` | TIMESTAMPTZ | DEFAULT NOW() | Last update timestamp |

**Indexes**:
- `idx_integration_settings_tenant_id` ON `tenant_id`
- `idx_integration_settings_service_code` ON `service_code`
- `idx_integration_settings_status` ON `status`

**Relations**:
- `tenant_id` → `tenants(id)` (via auth-service sync)

**Constraints**:
- CHECK: `status IN ('ACTIVE', 'INACTIVE', 'ERROR')`

### outbox_events

**Purpose**: Reliable event publishing to NATS/Kafka.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY | Event identifier |
| `tenant_id` | UUID | NOT NULL | Tenant identifier |
| `aggregate_type` | VARCHAR(100) | NOT NULL | Aggregate type (subscription, plan, feature_gate) |
| `aggregate_id` | UUID | NOT NULL | Aggregate ID |
| `event_type` | VARCHAR(100) | NOT NULL | Event type (subscription.created, subscription.upgraded, etc.) |
| `payload` | JSONB | NOT NULL | Event payload |
| `status` | VARCHAR(20) | NOT NULL, CHECK | PENDING, PUBLISHED, FAILED |
| `attempts` | INTEGER | DEFAULT 0 | Publish attempt count |
| `last_attempt_at` | TIMESTAMPTZ | | Last publish attempt timestamp |
| `published_at` | TIMESTAMPTZ | | Successful publish timestamp |
| `error_message` | TEXT | | Error message (if failed) |
| `created_at` | TIMESTAMPTZ | DEFAULT NOW() | Creation timestamp |

**Outbound Event Types** (NATS subject = `{aggregate_type}.{event_type}`):
- `subscription.subscription.created` — New subscription created
- `subscription.subscription.activated` — Subscription activated after payment
- `subscription.subscription.upgraded` — Plan upgraded
- `subscription.subscription.downgraded` — Plan downgraded
- `subscription.subscription.renewed` — Subscription renewed
- `subscription.subscription.cancelled` — Subscription cancelled
- `subscription.subscription.expired` — Subscription expired
- `tenant.subscription.updated` — Consistent event on tenant aggregate; emitted on every plan change

**Indexes**:
- `idx_outbox_events_tenant_id` ON `tenant_id`
- `idx_outbox_events_aggregate` ON `(aggregate_type, aggregate_id)`
- `idx_outbox_events_event_type` ON `event_type`
- `idx_outbox_events_status` ON `status`
- `idx_outbox_events_created_at` ON `created_at`

**Relations**:
- `tenant_id` → `tenants(id)` (via auth-service sync)

**Constraints**:
- CHECK: `status IN ('PENDING', 'PUBLISHED', 'FAILED')`

---

## Relationships & External Services

**Entity Ownership**: This service owns all subscription-related entities. It references (but does not own) entities from other services:
- **Users, Tenants, Outlets**: References from auth-service (never stores user/tenant accounts)
- **Invoices**: References from treasury-service (stores `invoice_id` only after treasury creates invoice)

**Integration Patterns**:
- `tenant_id` → `tenants(id)` (via auth-service tenant discovery webhooks)
- `user_id` → `users(id)` (via auth-service user sync)
- `invoice_id` → `invoices(id)` (via treasury-service billing events)

**Event Flows**:

**Outbound Events** (Published by Subscription Service):
- `subscription.created` - Inform auth-service to extend JWT claims
- `subscription.trial_started` - Notify notifications-service for trial welcome
- `subscription.upgraded` - Update feature entitlements across all services
- `subscription.downgraded` - Disable premium features
- `subscription.renewed` - Emit billing event to treasury
- `subscription.cancelled` - Gracefully disable access
- `subscription.expired` - Block access, notify user
- `subscription.overage_detected` - Emit billing event for overage charges
- `subscription.entitlements_changed` - Refresh caches in all services

**Inbound Events** (Consumed by Subscription Service):
- `auth.tenant.created` - Auto-assign Starter plan to new tenant
- `treasury.payment.succeeded` - Activate subscription after payment
- `treasury.payment.failed` - Initiate dunning workflow
- `treasury.invoice.created` - Link invoice_id to billing_event

**Service Dependencies**:
- **auth-service**: Tenant/user sync, JWT claims extension
- **treasury-service**: Billing event emission, payment confirmation
- **ordering-service**: Usage reporting (order_count)
- **pos-service**: Usage reporting (pos_transactions)
- **logistics-service**: Usage reporting (rider_count, delivery_tasks)
- **notifications-service**: Subscription alerts, trial reminders

See `docs/integrations.md` for complete integration patterns and API specifications.

---

## Cross-Service Entity Alignment

This section defines how Subscription Service entities relate to other services:

| Entity Type | Owner Service | Subscription Reference | Notes |
|-------------|--------------|----------------------|-------|
| **Users/Identities** | `auth-service` | `user_id` (UUID) - created_by, requested_by, etc. | Auth service is single source of truth for users. Subscription only stores references. |
| **Tenants/Organizations** | `auth-service` | `tenant_id` (UUID) | Tenant metadata managed by auth-service. Subscription receives discovery webhooks. |
| **Invoices** | `treasury-service` | `invoice_id` (UUID) in billing_events, overage_charges, plan_transitions | Treasury owns invoices. Subscription emits billing events, treasury creates invoices. |
| **Payments** | `treasury-service` | Via events (payment.succeeded, payment.failed) | Treasury processes payments. Subscription consumes payment confirmation events. |
| **Orders** | `ordering-service` | Via usage_tracking (metric_type=order_count) | Ordering reports daily order count. No order data stored in subscription. |
| **Riders** | `logistics-service` | Via usage_tracking (metric_type=rider_count) | Logistics reports active rider count. No rider data stored in subscription. |
| **POS Transactions** | `pos-service` | Via usage_tracking (metric_type=pos_transaction_count) | POS reports transaction count. No POS data stored in subscription. |

### Integration Conventions

- **All references to subscription service** use `tenant_id` + `feature_code` or `metric_type` (never duplicate subscription logic)
- **All services report usage** to subscription service (not auth, not treasury)
- **Auth service extends JWT claims** with subscription metadata from this service
- **Treasury service invoices** based on billing events from this service

---

## Seed & Defaults

**Subscription Plans** — 85 plans seeded (confirmed production as of May 2026):

Plans follow the naming convention `{SERVICE_TAG}-{TIER}-{BILLING_CYCLE}` (e.g. `ORDERING-GROWTH-MONTHLY`). Each service tag (`ordering`, `pos`, `logistics`, `inventory`, `erp`, `treasury`, `truload`, `marketflow`, `isp_billing`, `projects`) has STARTER/GROWTH/PROFESSIONAL × MONTHLY/ANNUAL variants.

Example plans:
```
ORDERING-STARTER-MONTHLY  — 2,500 KES/mo, 5 riders, 1,000 orders/month
ORDERING-GROWTH-MONTHLY   — 6,000 KES/mo, 15 riders, 3,000 orders/month
ORDERING-PROFESSIONAL-MONTHLY — 12,500 KES/mo, 30 riders, unlimited orders/month
```

**Feature Mappings**:
- All plans: `customer_portal`, `rider_app`, `admin_dashboard`, `mpesa_integration`, `sms_notifications`, `custom_domain`
- Growth+: `loyalty_program`, `multi_outlet`, `advanced_analytics`, `promo_codes`, `group_ordering`
- Professional: `pos_integration`, `route_optimization`, `priority_support`, `api_webhooks`

**Overage Policies**:
- All plans: `extra_riders` → KES 250/month per rider
- All plans: `extra_orders` → KES 375 per 100 orders/month

---

## Service Charge Plans (Added March 2026)

| Table | Key Columns | Purpose |
|-------|-------------|---------|
| `service_charge_plans` | `id`, `code` (unique), `name`, `description`, `charge_type` (PERCENTAGE/FIXED_PER_TRANSACTION/TIERED), `charge_value`, `currency`, `min_charge`, `max_charge`, `tier_rules` (JSONB), `applicable_services` (JSONB string array), `is_active`, `is_default`, `metadata`, `created_at`, `updated_at` | Defines commission-based pricing models. When a tenant's product subscription references a service charge plan, the platform takes a percentage or fixed fee per transaction. Applied during treasury-api payout settlement. |

### Per-Service Subscription Flexibility

`product_subscriptions` now includes:
- `service_charge_plan_id` (FK → `service_charge_plans`) — optional; when set, the product uses commission-based billing
- `override_plan_id` (FK → `subscription_plans`) — optional; when set, the product uses a different tier than the tenant's main plan

This enables scenarios like:
- Tenant A on STARTER plan for ordering, but PROFESSIONAL for logistics
- Tenant B using 5% service charge for ordering (no flat subscription)
- TruLoad service using 10% per-transaction commission

### Seeded Service Charge Plans

| Code | Type | Value | Services | Default |
|------|------|-------|----------|---------|
| SC_ORDERING_5PCT | PERCENTAGE | 5% (min KES 50, cap KES 5,000) | ordering | Yes |
| SC_ORDERING_3PCT | PERCENTAGE | 3% (min KES 50, cap KES 5,000) | ordering | No |
| SC_LOGISTICS_7PCT | PERCENTAGE | 7% | logistics | Yes |
| SC_POS_2PCT | PERCENTAGE | 2% (cap KES 2,000) | pos | Yes |
| SC_UNIVERSAL_FLAT_50 | FIXED_PER_TRANSACTION | KES 50 | any | No |
| SC_TRULOAD_10PCT | PERCENTAGE | 10% | truload | Yes |

---

## RBAC & Platform Configuration

### subscriptions_permissions

**Purpose**: Granular permission definitions for the subscriptions service.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY | Permission identifier |
| `permission_code` | VARCHAR | NOT NULL, UNIQUE | Permission code: `subscriptions.plans.view`, etc. |
| `name` | VARCHAR | NOT NULL | Display name |
| `module` | VARCHAR | NOT NULL | Module: plans, features, bundles, pricing, subscriptions, usage, billing, config, users |
| `action` | VARCHAR | NOT NULL | Action: add, view, view_own, change, change_own, delete, delete_own, manage, manage_own |
| `resource` | VARCHAR | | Resource target |
| `description` | TEXT | | Human-readable description |
| `created_at` | TIMESTAMPTZ | DEFAULT NOW() | Creation timestamp |

**Indexes**: `permission_code` (unique), `module`, `action`, `(module, action)`.

### subscriptions_roles

**Purpose**: Role definitions scoped per tenant.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY | Role identifier |
| `tenant_id` | UUID | NOT NULL | Tenant identifier |
| `role_code` | VARCHAR | NOT NULL | Role code: subscriptions_admin, billing_manager, viewer |
| `name` | VARCHAR | NOT NULL | Display name |
| `description` | TEXT | | Human-readable description |
| `is_system_role` | BOOLEAN | DEFAULT false | System roles cannot be deleted |
| `created_at` | TIMESTAMPTZ | DEFAULT NOW() | Creation timestamp |
| `updated_at` | TIMESTAMPTZ | DEFAULT NOW() | Last update timestamp |

**Indexes**: `tenant_id`, `(tenant_id, role_code)` (unique), `is_system_role`.

### role_permissions

**Purpose**: Junction table linking roles to permissions.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `role_id` | UUID | NOT NULL, FK | Role identifier |
| `permission_id` | UUID | NOT NULL, FK | Permission identifier |

**Indexes**: `(role_id, permission_id)` (unique), `role_id`, `permission_id`.

### subscriptions_users

**Purpose**: Local user reference synced from auth-service (JIT provisioned).

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY | User identifier |
| `tenant_id` | UUID | NOT NULL | Tenant identifier |
| `auth_service_user_id` | UUID | NOT NULL, UNIQUE | Reference to auth-service user |
| `email` | VARCHAR | NOT NULL | Denormalized email |
| `status` | VARCHAR | DEFAULT 'active' | Status: active, inactive, suspended |
| `sync_status` | VARCHAR | DEFAULT 'synced' | Sync status: synced, pending, failed |
| `last_sync_at` | TIMESTAMPTZ | | Last successful sync |
| `created_at` | TIMESTAMPTZ | DEFAULT NOW() | Creation timestamp |
| `updated_at` | TIMESTAMPTZ | DEFAULT NOW() | Last update timestamp |

**Indexes**: `tenant_id`, `auth_service_user_id` (unique), `(tenant_id, auth_service_user_id)` (unique), `status`, `sync_status`.

### user_role_assignments

**Purpose**: Maps users to roles within a tenant.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY | Assignment identifier |
| `tenant_id` | UUID | NOT NULL | Tenant identifier |
| `user_id` | UUID | NOT NULL, FK | User identifier (subscriptions_users) |
| `role_id` | UUID | NOT NULL, FK | Role identifier |
| `assigned_by` | UUID | NOT NULL | User who assigned the role |
| `assigned_at` | TIMESTAMPTZ | DEFAULT NOW() | Assignment timestamp |
| `expires_at` | TIMESTAMPTZ | | Optional expiration |

**Indexes**: `(tenant_id, user_id, role_id)` (unique), `tenant_id`, `user_id`, `role_id`, `expires_at`.

### rate_limit_configs

**Purpose**: Database-driven rate limiting configuration.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY | Config identifier |
| `service_name` | VARCHAR | NOT NULL | Service identifier (subscriptions-api) |
| `key_type` | VARCHAR | NOT NULL | Rate limit key: ip, tenant, user, endpoint, global |
| `endpoint_pattern` | VARCHAR | DEFAULT '*' | Endpoint pattern to match |
| `requests_per_window` | INTEGER | DEFAULT 60 | Max requests per window |
| `window_seconds` | INTEGER | DEFAULT 60 | Time window in seconds |
| `burst_multiplier` | FLOAT | DEFAULT 1.5 | Burst spike multiplier |
| `is_active` | BOOLEAN | DEFAULT true | Active flag |
| `description` | VARCHAR | | Description |
| `created_at` | TIMESTAMPTZ | DEFAULT NOW() | Creation timestamp |
| `updated_at` | TIMESTAMPTZ | DEFAULT NOW() | Last update timestamp |

**Indexes**: `(service_name, key_type, endpoint_pattern)` (unique), `service_name`, `is_active`.

### service_configs

**Purpose**: Key-value service configuration; platform-level defaults and tenant-specific overrides.

| Column | Type | Constraints | Description |
|--------|------|-------------|-------------|
| `id` | UUID | PRIMARY KEY | Config identifier |
| `tenant_id` | UUID | NULLABLE | Nil = platform default; set = tenant override |
| `config_key` | VARCHAR | NOT NULL | Configuration key |
| `config_value` | TEXT | NOT NULL | Configuration value (JSON string) |
| `config_type` | VARCHAR | DEFAULT 'string' | Value type: string, int, bool, json, float |
| `description` | VARCHAR | | Description |
| `is_secret` | BOOLEAN | DEFAULT false | Masked in API responses if true |
| `created_at` | TIMESTAMPTZ | DEFAULT NOW() | Creation timestamp |
| `updated_at` | TIMESTAMPTZ | DEFAULT NOW() | Last update timestamp |

**Indexes**: `(tenant_id, config_key)` (unique), `config_key`.

### RBAC Design Notes

- **Plans, features, bundles** are PLATFORM-LEVEL entities (no tenant_id). Permissions for managing these require `is_platform_owner`.
- **Only tenant_subscription** is tenant-scoped.
- System roles (`subscriptions_admin`, `billing_manager`, `viewer`) are seeded per tenant and cannot be deleted.
- Permissions follow the format `subscriptions.{module}.{action}`.

---

Regenerate this ERD whenever Ent schemas evolve. Always run `go generate ./internal/ent` before committing schema changes and update integration docs accordingly.
