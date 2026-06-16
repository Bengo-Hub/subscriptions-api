-- Modify "subscription_plans" table
ALTER TABLE "subscription_plans" ADD COLUMN "setup_fee" double precision NOT NULL DEFAULT 0;
-- Modify "tenant_subscriptions" table
ALTER TABLE "tenant_subscriptions" ADD COLUMN "setup_fee_amount" double precision NOT NULL DEFAULT 0, ADD COLUMN "setup_fee_charged_at" timestamptz NULL;
