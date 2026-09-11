-- Create "support_fee_cycles" table
CREATE TABLE "support_fee_cycles" ("id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "anchor_date" timestamptz NOT NULL, "cycle_number" bigint NOT NULL, "period_start" timestamptz NOT NULL, "due_date" timestamptz NOT NULL, "status" character varying NOT NULL DEFAULT 'PENDING', "paid_at" timestamptz NULL, "grace_until" timestamptz NULL, "base_price" double precision NOT NULL, "custom_price" double precision NULL, "custom_price_reason" character varying NULL, "custom_price_set_by" uuid NULL, "custom_price_set_at" timestamptz NULL, "metadata" jsonb NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "support_plan_id" uuid NOT NULL, "tenant_subscription_id" uuid NOT NULL, PRIMARY KEY ("id"), CONSTRAINT "support_fee_cycles_subscription_plans_support_fee_cycles" FOREIGN KEY ("support_plan_id") REFERENCES "subscription_plans" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION, CONSTRAINT "support_fee_cycles_tenant_subscriptions_support_fee_cycles" FOREIGN KEY ("tenant_subscription_id") REFERENCES "tenant_subscriptions" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION);
-- Create index "supportfeecycle_due_date" to table: "support_fee_cycles"
CREATE INDEX "supportfeecycle_due_date" ON "support_fee_cycles" ("due_date");
-- Create index "supportfeecycle_status" to table: "support_fee_cycles"
CREATE INDEX "supportfeecycle_status" ON "support_fee_cycles" ("status");
-- Create index "supportfeecycle_tenant_id" to table: "support_fee_cycles"
CREATE INDEX "supportfeecycle_tenant_id" ON "support_fee_cycles" ("tenant_id");
-- Create index "supportfeecycle_tenant_subscription_id" to table: "support_fee_cycles"
CREATE INDEX "supportfeecycle_tenant_subscription_id" ON "support_fee_cycles" ("tenant_subscription_id");
-- Create index "supportfeecycle_tenant_subscription_id_cycle_number" to table: "support_fee_cycles"
CREATE UNIQUE INDEX "supportfeecycle_tenant_subscription_id_cycle_number" ON "support_fee_cycles" ("tenant_subscription_id", "cycle_number");
