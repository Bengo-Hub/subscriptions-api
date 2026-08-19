-- Create "tenant_email_domains" table
CREATE TABLE "tenant_email_domains" ("id" uuid NOT NULL, "domain" character varying NOT NULL, "status" character varying NOT NULL DEFAULT 'PENDING_DNS', "dkim_selector" character varying NULL, "stalwart_domain_id" character varying NULL, "verified_at" timestamptz NULL, "last_checked_at" timestamptz NULL, "failure_reason" character varying NULL, "metadata" jsonb NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "tenant_subscription_id" uuid NOT NULL, PRIMARY KEY ("id"), CONSTRAINT "tenant_email_domains_tenant_subscriptions_email_domains" FOREIGN KEY ("tenant_subscription_id") REFERENCES "tenant_subscriptions" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION);
-- Create index "tenantemaildomain_domain" to table: "tenant_email_domains"
CREATE UNIQUE INDEX "tenantemaildomain_domain" ON "tenant_email_domains" ("domain");
-- Create index "tenantemaildomain_tenant_subscription_id_status" to table: "tenant_email_domains"
CREATE INDEX "tenantemaildomain_tenant_subscription_id_status" ON "tenant_email_domains" ("tenant_subscription_id", "status");
