-- Modify "tenant_subscriptions" table
ALTER TABLE "tenant_subscriptions" ADD COLUMN "custom_base_price" double precision NULL, ADD COLUMN "custom_price_reason" character varying NULL, ADD COLUMN "custom_price_set_by" uuid NULL, ADD COLUMN "custom_price_set_at" timestamptz NULL;
