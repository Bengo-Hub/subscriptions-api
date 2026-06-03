-- Modify "subscriptions_roles" table
ALTER TABLE "subscriptions_roles" ALTER COLUMN "tenant_id" DROP NOT NULL;
-- Create index "subscriptionsrole_role_code" to table: "subscriptions_roles"
CREATE INDEX "subscriptionsrole_role_code" ON "subscriptions_roles" ("role_code");
