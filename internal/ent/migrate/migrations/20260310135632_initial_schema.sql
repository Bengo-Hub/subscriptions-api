-- Create "bundles" table
CREATE TABLE "bundles" ("id" uuid NOT NULL, "code" character varying NOT NULL, "name" character varying NOT NULL, "description" text NULL, "products" jsonb NOT NULL, "tiers" jsonb NOT NULL, "discount_type" character varying NOT NULL DEFAULT 'none', "discount_value" double precision NOT NULL DEFAULT 0, "is_active" boolean NOT NULL DEFAULT true, "is_default" boolean NOT NULL DEFAULT false, "sort_order" bigint NOT NULL DEFAULT 0, "metadata" jsonb NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, PRIMARY KEY ("id"));
-- Create index "bundle_code" to table: "bundles"
CREATE INDEX "bundle_code" ON "bundles" ("code");
-- Create index "bundle_is_active" to table: "bundles"
CREATE INDEX "bundle_is_active" ON "bundles" ("is_active");
-- Create index "bundle_is_default" to table: "bundles"
CREATE INDEX "bundle_is_default" ON "bundles" ("is_default");
-- Create index "bundles_code_key" to table: "bundles"
CREATE UNIQUE INDEX "bundles_code_key" ON "bundles" ("code");
-- Create "outbox_events" table
CREATE TABLE "outbox_events" ("id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "aggregate_type" character varying NOT NULL, "aggregate_id" uuid NOT NULL, "event_type" character varying NOT NULL, "payload" jsonb NOT NULL, "status" character varying NOT NULL DEFAULT 'PENDING', "attempts" bigint NOT NULL DEFAULT 0, "last_attempt_at" timestamptz NULL, "published_at" timestamptz NULL, "error_message" character varying NULL, "created_at" timestamptz NOT NULL, PRIMARY KEY ("id"));
-- Create index "outboxevent_aggregate_type_aggregate_id" to table: "outbox_events"
CREATE INDEX "outboxevent_aggregate_type_aggregate_id" ON "outbox_events" ("aggregate_type", "aggregate_id");
-- Create index "outboxevent_created_at" to table: "outbox_events"
CREATE INDEX "outboxevent_created_at" ON "outbox_events" ("created_at");
-- Create index "outboxevent_event_type" to table: "outbox_events"
CREATE INDEX "outboxevent_event_type" ON "outbox_events" ("event_type");
-- Create index "outboxevent_status" to table: "outbox_events"
CREATE INDEX "outboxevent_status" ON "outbox_events" ("status");
-- Create index "outboxevent_tenant_id" to table: "outbox_events"
CREATE INDEX "outboxevent_tenant_id" ON "outbox_events" ("tenant_id");
-- Create "subscription_plans" table
CREATE TABLE "subscription_plans" ("id" uuid NOT NULL, "plan_code" character varying NOT NULL, "name" character varying NOT NULL, "description" text NULL, "billing_cycle" character varying NOT NULL DEFAULT 'MONTHLY', "base_price" double precision NOT NULL, "currency" character varying NOT NULL DEFAULT 'KES', "is_active" boolean NOT NULL DEFAULT true, "is_public" boolean NOT NULL DEFAULT true, "tier_order" bigint NOT NULL, "tier_limits_json" jsonb NOT NULL, "metadata" jsonb NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, PRIMARY KEY ("id"));
-- Create index "subscription_plans_plan_code_key" to table: "subscription_plans"
CREATE UNIQUE INDEX "subscription_plans_plan_code_key" ON "subscription_plans" ("plan_code");
-- Create index "subscriptionplan_is_active" to table: "subscription_plans"
CREATE INDEX "subscriptionplan_is_active" ON "subscription_plans" ("is_active");
-- Create index "subscriptionplan_plan_code" to table: "subscription_plans"
CREATE INDEX "subscriptionplan_plan_code" ON "subscription_plans" ("plan_code");
-- Create index "subscriptionplan_tier_order" to table: "subscription_plans"
CREATE INDEX "subscriptionplan_tier_order" ON "subscription_plans" ("tier_order");
-- Create "plan_features" table
CREATE TABLE "plan_features" ("id" uuid NOT NULL, "feature_code" character varying NOT NULL, "is_included" boolean NOT NULL DEFAULT true, "limit_value" bigint NULL, "metadata" jsonb NOT NULL, "created_at" timestamptz NOT NULL, "plan_id" uuid NOT NULL, PRIMARY KEY ("id"), CONSTRAINT "plan_features_subscription_plans_features" FOREIGN KEY ("plan_id") REFERENCES "subscription_plans" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION);
-- Create index "planfeature_feature_code" to table: "plan_features"
CREATE INDEX "planfeature_feature_code" ON "plan_features" ("feature_code");
-- Create index "planfeature_plan_id" to table: "plan_features"
CREATE INDEX "planfeature_plan_id" ON "plan_features" ("plan_id");
-- Create index "planfeature_plan_id_feature_code" to table: "plan_features"
CREATE UNIQUE INDEX "planfeature_plan_id_feature_code" ON "plan_features" ("plan_id", "feature_code");
-- Create "plan_pricing_histories" table
CREATE TABLE "plan_pricing_histories" ("id" uuid NOT NULL, "base_price" double precision NOT NULL, "effective_from" timestamptz NOT NULL, "effective_to" timestamptz NULL, "changed_by" uuid NULL, "change_reason" text NULL, "metadata" jsonb NOT NULL, "created_at" timestamptz NOT NULL, "plan_id" uuid NOT NULL, PRIMARY KEY ("id"), CONSTRAINT "plan_pricing_histories_subscription_plans_pricing_history" FOREIGN KEY ("plan_id") REFERENCES "subscription_plans" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION);
-- Create index "planpricinghistory_effective_from_effective_to" to table: "plan_pricing_histories"
CREATE INDEX "planpricinghistory_effective_from_effective_to" ON "plan_pricing_histories" ("effective_from", "effective_to");
-- Create index "planpricinghistory_plan_id" to table: "plan_pricing_histories"
CREATE INDEX "planpricinghistory_plan_id" ON "plan_pricing_histories" ("plan_id");
-- Create "products" table
CREATE TABLE "products" ("id" uuid NOT NULL, "code" character varying NOT NULL, "name" character varying NOT NULL, "description" text NULL, "category" character varying NOT NULL, "status" character varying NOT NULL DEFAULT 'active', "dependencies" jsonb NULL, "is_platform" boolean NOT NULL DEFAULT false, "monthly_price" double precision NOT NULL DEFAULT 0, "included_in_bundle" boolean NOT NULL DEFAULT true, "sort_order" bigint NOT NULL DEFAULT 0, "metadata" jsonb NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, PRIMARY KEY ("id"));
-- Create index "product_category" to table: "products"
CREATE INDEX "product_category" ON "products" ("category");
-- Create index "product_code" to table: "products"
CREATE INDEX "product_code" ON "products" ("code");
-- Create index "product_is_platform" to table: "products"
CREATE INDEX "product_is_platform" ON "products" ("is_platform");
-- Create index "product_status" to table: "products"
CREATE INDEX "product_status" ON "products" ("status");
-- Create index "products_code_key" to table: "products"
CREATE UNIQUE INDEX "products_code_key" ON "products" ("code");
-- Create "tenants" table
CREATE TABLE "tenants" ("id" uuid NOT NULL, "name" character varying NOT NULL, "slug" character varying NOT NULL, "status" character varying NOT NULL DEFAULT 'active', "contact_email" character varying NULL, "contact_phone" character varying NULL, "logo_url" character varying NULL, "website" character varying NULL, "country" character varying NULL DEFAULT 'KE', "timezone" character varying NULL DEFAULT 'Africa/Nairobi', "brand_colors" jsonb NULL, "org_size" character varying NULL, "use_case" character varying NULL, "subscription_plan" character varying NULL, "subscription_status" character varying NULL, "subscription_expires_at" timestamptz NULL, "subscription_id" character varying NULL, "tier_limits" jsonb NULL, "metadata" jsonb NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, PRIMARY KEY ("id"));
-- Create index "tenant_slug" to table: "tenants"
CREATE UNIQUE INDEX "tenant_slug" ON "tenants" ("slug");
-- Create index "tenant_status" to table: "tenants"
CREATE INDEX "tenant_status" ON "tenants" ("status");
-- Create index "tenants_slug_key" to table: "tenants"
CREATE UNIQUE INDEX "tenants_slug_key" ON "tenants" ("slug");
-- Create "tenant_subscriptions" table
CREATE TABLE "tenant_subscriptions" ("id" uuid NOT NULL, "status" character varying NOT NULL DEFAULT 'TRIAL', "trial_ends_at" timestamptz NULL, "current_period_start" timestamptz NOT NULL, "current_period_end" timestamptz NOT NULL, "cancelled_at" timestamptz NULL, "cancel_reason" character varying NULL, "bundle_code" character varying NULL, "payment_method_id" uuid NULL, "metadata" jsonb NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "plan_id" uuid NOT NULL, "tenant_id" uuid NOT NULL, PRIMARY KEY ("id"), CONSTRAINT "tenant_subscriptions_subscription_plans_subscriptions" FOREIGN KEY ("plan_id") REFERENCES "subscription_plans" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION, CONSTRAINT "tenant_subscriptions_tenants_subscriptions" FOREIGN KEY ("tenant_id") REFERENCES "tenants" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION);
-- Create index "tenantsubscription_current_period_end" to table: "tenant_subscriptions"
CREATE INDEX "tenantsubscription_current_period_end" ON "tenant_subscriptions" ("current_period_end");
-- Create index "tenantsubscription_status" to table: "tenant_subscriptions"
CREATE INDEX "tenantsubscription_status" ON "tenant_subscriptions" ("status");
-- Create index "tenantsubscription_tenant_id" to table: "tenant_subscriptions"
CREATE UNIQUE INDEX "tenantsubscription_tenant_id" ON "tenant_subscriptions" ("tenant_id");
-- Create "product_subscriptions" table
CREATE TABLE "product_subscriptions" ("id" uuid NOT NULL, "product_code" character varying NOT NULL, "status" character varying NOT NULL DEFAULT 'active', "trial_ends_at" timestamptz NULL, "activated_at" timestamptz NULL, "deactivated_at" timestamptz NULL, "metadata" jsonb NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "product_id" uuid NOT NULL, "tenant_subscription_id" uuid NOT NULL, PRIMARY KEY ("id"), CONSTRAINT "product_subscriptions_products_product_subscriptions" FOREIGN KEY ("product_id") REFERENCES "products" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION, CONSTRAINT "product_subscriptions_tenant_s_99f2eae8cb225f2260e36e8a1c57eb7e" FOREIGN KEY ("tenant_subscription_id") REFERENCES "tenant_subscriptions" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION);
-- Create index "productsubscription_product_code" to table: "product_subscriptions"
CREATE INDEX "productsubscription_product_code" ON "product_subscriptions" ("product_code");
-- Create index "productsubscription_status" to table: "product_subscriptions"
CREATE INDEX "productsubscription_status" ON "product_subscriptions" ("status");
-- Create index "productsubscription_tenant_subscription_id" to table: "product_subscriptions"
CREATE INDEX "productsubscription_tenant_subscription_id" ON "product_subscriptions" ("tenant_subscription_id");
-- Create index "productsubscription_tenant_subscription_id_product_code" to table: "product_subscriptions"
CREATE UNIQUE INDEX "productsubscription_tenant_subscription_id_product_code" ON "product_subscriptions" ("tenant_subscription_id", "product_code");
