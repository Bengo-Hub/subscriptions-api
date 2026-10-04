-- Create "support_agreements" table
CREATE TABLE "support_agreements" ("id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "kind" character varying NOT NULL DEFAULT 'STANDARD', "name" character varying NOT NULL, "billing_cycle" character varying NOT NULL DEFAULT 'ANNUAL', "interval_count" bigint NOT NULL DEFAULT 12, "interval_unit" character varying NOT NULL DEFAULT 'MONTH', "amount" double precision NULL, "currency" character varying NOT NULL DEFAULT 'KES', "billing_timing" character varying NOT NULL DEFAULT 'ARREARS', "starts_at" timestamptz NOT NULL, "ends_at" timestamptz NULL, "status" character varying NOT NULL DEFAULT 'ACTIVE', "next_period_start" timestamptz NOT NULL, "cycle_count" bigint NOT NULL DEFAULT 0, "notes" character varying NULL, "created_by" uuid NULL, "metadata" jsonb NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "support_plan_id" uuid NULL, "tenant_subscription_id" uuid NOT NULL, PRIMARY KEY ("id"), CONSTRAINT "support_agreements_subscription_plans_support_agreements" FOREIGN KEY ("support_plan_id") REFERENCES "subscription_plans" ("id") ON UPDATE NO ACTION ON DELETE SET NULL, CONSTRAINT "support_agreements_tenant_subscriptions_support_agreements" FOREIGN KEY ("tenant_subscription_id") REFERENCES "tenant_subscriptions" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION);
-- Create index "supportagreement_status_next_period_start" to table: "support_agreements"
CREATE INDEX "supportagreement_status_next_period_start" ON "support_agreements" ("status", "next_period_start");
-- Create index "supportagreement_tenant_id_status" to table: "support_agreements"
CREATE INDEX "supportagreement_tenant_id_status" ON "support_agreements" ("tenant_id", "status");
-- Create index "supportagreement_tenant_subscription_id" to table: "support_agreements"
CREATE UNIQUE INDEX "supportagreement_tenant_subscription_id" ON "support_agreements" ("tenant_subscription_id") WHERE ((kind)::text = 'STANDARD'::text);
-- Modify "support_fee_cycles" table
ALTER TABLE "support_fee_cycles" DROP CONSTRAINT "support_fee_cycles_subscription_plans_support_fee_cycles", ALTER COLUMN "support_plan_id" DROP NOT NULL, ADD COLUMN "period_end" timestamptz NULL, ADD COLUMN "agreement_id" uuid NULL, ADD CONSTRAINT "support_fee_cycles_subscription_plans_support_fee_cycles" FOREIGN KEY ("support_plan_id") REFERENCES "subscription_plans" ("id") ON UPDATE NO ACTION ON DELETE SET NULL, ADD CONSTRAINT "support_fee_cycles_support_agreements_cycles" FOREIGN KEY ("agreement_id") REFERENCES "support_agreements" ("id") ON UPDATE NO ACTION ON DELETE SET NULL;
-- Create index "supportfeecycle_agreement_id_cycle_number" to table: "support_fee_cycles"
CREATE UNIQUE INDEX "supportfeecycle_agreement_id_cycle_number" ON "support_fee_cycles" ("agreement_id", "cycle_number");
-- Create index "supportfeecycle_status_due_date" to table: "support_fee_cycles"
CREATE INDEX "supportfeecycle_status_due_date" ON "support_fee_cycles" ("status", "due_date");
-- Create index "supportfeecycle_tenant_id_status_due_date" to table: "support_fee_cycles"
CREATE INDEX "supportfeecycle_tenant_id_status_due_date" ON "support_fee_cycles" ("tenant_id", "status", "due_date");
-- Drop legacy indexes superseded by the composite ones above. The old per-subscription unique
-- (tenant_subscription_id, cycle_number) would reject the first cycle of any second agreement.
DROP INDEX IF EXISTS "supportfeecycle_tenant_subscription_id_cycle_number";
DROP INDEX IF EXISTS "supportfeecycle_tenant_id";
DROP INDEX IF EXISTS "supportfeecycle_status";
DROP INDEX IF EXISTS "supportfeecycle_due_date";
