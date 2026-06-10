-- Modify "tenant_subscriptions" table
ALTER TABLE "tenant_subscriptions" ADD COLUMN "allow_overage" boolean NOT NULL DEFAULT false, ADD COLUMN "overage_enabled_at" timestamptz NULL;
