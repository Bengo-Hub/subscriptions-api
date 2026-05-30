-- Create "coupons" table
CREATE TABLE "coupons" ("id" uuid NOT NULL, "code" character varying NOT NULL, "name" character varying NOT NULL, "description" character varying NULL, "type" character varying NOT NULL, "value" double precision NOT NULL, "applicable_plan_codes" jsonb NULL, "min_plan_price" double precision NOT NULL DEFAULT 0, "max_uses" bigint NOT NULL DEFAULT -1, "used_count" bigint NOT NULL DEFAULT 0, "max_stacks" bigint NOT NULL DEFAULT 1, "is_active" boolean NOT NULL DEFAULT true, "valid_from" timestamptz NOT NULL, "valid_until" timestamptz NULL, "created_by" uuid NOT NULL, "metadata" jsonb NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, PRIMARY KEY ("id"));
-- Create index "coupon_code" to table: "coupons"
CREATE UNIQUE INDEX "coupon_code" ON "coupons" ("code");
-- Create index "coupon_is_active" to table: "coupons"
CREATE INDEX "coupon_is_active" ON "coupons" ("is_active");
-- Create index "coupon_valid_until" to table: "coupons"
CREATE INDEX "coupon_valid_until" ON "coupons" ("valid_until");
-- Create "custom_addons" table
CREATE TABLE "custom_addons" ("id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "name" character varying NOT NULL, "description" character varying NULL, "service_code" character varying NULL, "service_addon_type" character varying NULL, "billing_cycle" character varying NOT NULL DEFAULT 'monthly', "unit_price_kes" bigint NOT NULL, "quantity" bigint NOT NULL DEFAULT 1, "status" character varying NOT NULL DEFAULT 'active', "notes" character varying NULL, "created_by_user_id" uuid NOT NULL, "metadata" jsonb NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, PRIMARY KEY ("id"));
-- Create index "customaddon_service_code" to table: "custom_addons"
CREATE INDEX "customaddon_service_code" ON "custom_addons" ("service_code");
-- Create index "customaddon_tenant_id" to table: "custom_addons"
CREATE INDEX "customaddon_tenant_id" ON "custom_addons" ("tenant_id");
-- Create index "customaddon_tenant_id_status" to table: "custom_addons"
CREATE INDEX "customaddon_tenant_id_status" ON "custom_addons" ("tenant_id", "status");
-- Create "overage_charges" table
CREATE TABLE "overage_charges" ("id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "metric_type" character varying NOT NULL, "period_date" timestamptz NOT NULL, "units_used" double precision NOT NULL, "plan_limit" bigint NOT NULL, "units_over" double precision NOT NULL, "unit_price_kes" double precision NOT NULL, "total_charge_kes" double precision NOT NULL, "status" character varying NOT NULL DEFAULT 'pending', "invoiced_at" timestamptz NULL, "created_at" timestamptz NOT NULL, "tenant_subscription_id" uuid NOT NULL, PRIMARY KEY ("id"), CONSTRAINT "overage_charges_tenant_subscriptions_overage_charges" FOREIGN KEY ("tenant_subscription_id") REFERENCES "tenant_subscriptions" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION);
-- Create index "overagecharge_period_date" to table: "overage_charges"
CREATE INDEX "overagecharge_period_date" ON "overage_charges" ("period_date");
-- Create index "overagecharge_status" to table: "overage_charges"
CREATE INDEX "overagecharge_status" ON "overage_charges" ("status");
-- Create index "overagecharge_tenant_id" to table: "overage_charges"
CREATE INDEX "overagecharge_tenant_id" ON "overage_charges" ("tenant_id");
-- Create index "overagecharge_tenant_subscription_id" to table: "overage_charges"
CREATE INDEX "overagecharge_tenant_subscription_id" ON "overage_charges" ("tenant_subscription_id");
-- Create index "overagecharge_tenant_subscription_id_metric_type_period_date" to table: "overage_charges"
CREATE UNIQUE INDEX "overagecharge_tenant_subscription_id_metric_type_period_date" ON "overage_charges" ("tenant_subscription_id", "metric_type", "period_date");
-- Create "subscription_credits" table
CREATE TABLE "subscription_credits" ("id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "balance_kes" bigint NOT NULL DEFAULT 0, "lifetime_earned_kes" bigint NOT NULL DEFAULT 0, "loyalty_rate" double precision NOT NULL DEFAULT 0.02, "updated_at" timestamptz NOT NULL, PRIMARY KEY ("id"));
-- Create index "subscriptioncredit_tenant_id" to table: "subscription_credits"
CREATE UNIQUE INDEX "subscriptioncredit_tenant_id" ON "subscription_credits" ("tenant_id");
-- Create "subscription_credit_transactions" table
CREATE TABLE "subscription_credit_transactions" ("id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "type" character varying NOT NULL, "amount_kes" bigint NOT NULL, "ref_id" uuid NULL, "ref_type" character varying NULL, "description" character varying NULL, "created_at" timestamptz NOT NULL, "credit_id" uuid NOT NULL, PRIMARY KEY ("id"), CONSTRAINT "subscription_credit_transactio_9bc149e341c49aff56dd10eb8619bbbc" FOREIGN KEY ("credit_id") REFERENCES "subscription_credits" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION);
-- Create index "subscriptioncredittransaction_created_at" to table: "subscription_credit_transactions"
CREATE INDEX "subscriptioncredittransaction_created_at" ON "subscription_credit_transactions" ("created_at");
-- Create index "subscriptioncredittransaction_credit_id" to table: "subscription_credit_transactions"
CREATE INDEX "subscriptioncredittransaction_credit_id" ON "subscription_credit_transactions" ("credit_id");
-- Create index "subscriptioncredittransaction_tenant_id" to table: "subscription_credit_transactions"
CREATE INDEX "subscriptioncredittransaction_tenant_id" ON "subscription_credit_transactions" ("tenant_id");
-- Create index "subscriptioncredittransaction_type" to table: "subscription_credit_transactions"
CREATE INDEX "subscriptioncredittransaction_type" ON "subscription_credit_transactions" ("type");
