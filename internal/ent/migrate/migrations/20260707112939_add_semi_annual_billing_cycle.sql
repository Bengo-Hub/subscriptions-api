-- Modify "tenant_subscriptions" table.
-- NOTE: billing_cycle gained the SEMI_ANNUAL value — it is an app-level Ent enum over a plain
-- varchar column, so no DDL is required for it. The columns below close pre-existing drift:
-- they were added to the Ent schema without a versioned migration (production already has them
-- via online migrate), hence IF NOT EXISTS so this replays cleanly everywhere.
ALTER TABLE "tenant_subscriptions"
  ADD COLUMN IF NOT EXISTS "terms_version" character varying NULL,
  ADD COLUMN IF NOT EXISTS "terms_accepted_at" timestamptz NULL,
  ADD COLUMN IF NOT EXISTS "terms_accepted_by" uuid NULL,
  ADD COLUMN IF NOT EXISTS "last_activity_at" timestamptz NULL,
  ADD COLUMN IF NOT EXISTS "dormant_at" timestamptz NULL,
  ADD COLUMN IF NOT EXISTS "purge_grace_ends_at" timestamptz NULL,
  ADD COLUMN IF NOT EXISTS "pending_purge" boolean NOT NULL DEFAULT false;
-- Create index "tenantsubscription_last_activity_at" to table: "tenant_subscriptions"
CREATE INDEX IF NOT EXISTS "tenantsubscription_last_activity_at" ON "tenant_subscriptions" ("last_activity_at");
