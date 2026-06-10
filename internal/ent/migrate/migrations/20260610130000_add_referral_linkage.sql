-- Modify "tenant_subscriptions" table: Type-A referral linkage.
ALTER TABLE "tenant_subscriptions" ADD COLUMN "referred_by" uuid NULL, ADD COLUMN "referral_code" character varying NULL;
-- Create index "tenantsubscription_referral_code" to table: "tenant_subscriptions"
CREATE UNIQUE INDEX "tenantsubscription_referral_code" ON "tenant_subscriptions" ("referral_code");
