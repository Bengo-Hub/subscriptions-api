-- Modify "plan_features" table
ALTER TABLE "plan_features" ADD COLUMN "overage_unit_price" double precision NOT NULL DEFAULT 0;
-- Create "service_charge_plans" table
CREATE TABLE "service_charge_plans" ("id" uuid NOT NULL, "code" character varying NOT NULL, "name" character varying NOT NULL, "description" text NULL, "charge_type" character varying NOT NULL DEFAULT 'PERCENTAGE', "charge_value" double precision NOT NULL DEFAULT 0, "currency" character varying NOT NULL DEFAULT 'KES', "min_charge" double precision NULL, "max_charge" double precision NULL, "tier_rules" jsonb NULL, "applicable_services" jsonb NULL, "is_active" boolean NOT NULL DEFAULT true, "is_default" boolean NOT NULL DEFAULT false, "metadata" jsonb NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, PRIMARY KEY ("id"));
-- Create index "service_charge_plans_code_key" to table: "service_charge_plans"
CREATE UNIQUE INDEX "service_charge_plans_code_key" ON "service_charge_plans" ("code");
-- Create index "servicechargeplan_charge_type" to table: "service_charge_plans"
CREATE INDEX "servicechargeplan_charge_type" ON "service_charge_plans" ("charge_type");
-- Create index "servicechargeplan_code" to table: "service_charge_plans"
CREATE INDEX "servicechargeplan_code" ON "service_charge_plans" ("code");
-- Create index "servicechargeplan_is_active" to table: "service_charge_plans"
CREATE INDEX "servicechargeplan_is_active" ON "service_charge_plans" ("is_active");
-- Create "usage_events" table
CREATE TABLE "usage_events" ("id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "metric_type" character varying NOT NULL, "service_name" character varying NOT NULL, "value" double precision NOT NULL, "period_start" timestamptz NULL, "period_end" timestamptz NULL, "metadata" jsonb NOT NULL, "created_at" timestamptz NOT NULL, PRIMARY KEY ("id"));
-- Create index "usageevent_created_at" to table: "usage_events"
CREATE INDEX "usageevent_created_at" ON "usage_events" ("created_at");
-- Create index "usageevent_service_name" to table: "usage_events"
CREATE INDEX "usageevent_service_name" ON "usage_events" ("service_name");
-- Create index "usageevent_tenant_id" to table: "usage_events"
CREATE INDEX "usageevent_tenant_id" ON "usage_events" ("tenant_id");
-- Create index "usageevent_tenant_id_metric_type" to table: "usage_events"
CREATE INDEX "usageevent_tenant_id_metric_type" ON "usage_events" ("tenant_id", "metric_type");
-- Create index "usageevent_tenant_id_service_name_metric_type" to table: "usage_events"
CREATE INDEX "usageevent_tenant_id_service_name_metric_type" ON "usage_events" ("tenant_id", "service_name", "metric_type");
-- Modify "product_subscriptions" table
ALTER TABLE "product_subscriptions" ADD COLUMN "service_charge_plan_id" uuid NULL, ADD COLUMN "override_plan_id" uuid NULL, ADD CONSTRAINT "product_subscriptions_service__233ff1e2cb6a3b4fda7eb26abdd0a8db" FOREIGN KEY ("service_charge_plan_id") REFERENCES "service_charge_plans" ("id") ON UPDATE NO ACTION ON DELETE SET NULL, ADD CONSTRAINT "product_subscriptions_subscrip_e0275f0e95ea95b30660d7779bca7979" FOREIGN KEY ("override_plan_id") REFERENCES "subscription_plans" ("id") ON UPDATE NO ACTION ON DELETE SET NULL;
