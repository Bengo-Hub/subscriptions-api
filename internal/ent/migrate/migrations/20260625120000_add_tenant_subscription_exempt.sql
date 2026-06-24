-- Mirror of auth-api tenant subscription_exempt; drives IsExemptTenant + synthetic exempt entitlements
ALTER TABLE "tenants" ADD COLUMN "subscription_exempt" boolean NOT NULL DEFAULT false;
