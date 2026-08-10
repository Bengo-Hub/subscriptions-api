-- Create "email_plans" table
CREATE TABLE "email_plans" ("id" uuid NOT NULL, "code" character varying NOT NULL, "name" character varying NOT NULL, "description" text NULL, "price_per_user_monthly" double precision NOT NULL, "price_per_user_yearly" double precision NULL, "storage_per_user_gb" bigint NOT NULL, "max_aliases" bigint NOT NULL DEFAULT 5, "max_email_size_mb" bigint NOT NULL DEFAULT 25, "features_json" jsonb NOT NULL, "is_active" boolean NOT NULL DEFAULT true, "is_public" boolean NOT NULL DEFAULT true, "sort_order" bigint NOT NULL DEFAULT 0, "metadata" jsonb NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, PRIMARY KEY ("id"));
-- Create index "email_plans_code_key" to table: "email_plans"
CREATE UNIQUE INDEX "email_plans_code_key" ON "email_plans" ("code");
-- Create index "emailplan_code" to table: "email_plans"
CREATE INDEX "emailplan_code" ON "email_plans" ("code");
-- Create index "emailplan_is_active" to table: "email_plans"
CREATE INDEX "emailplan_is_active" ON "email_plans" ("is_active");
-- Create index "emailplan_sort_order" to table: "email_plans"
CREATE INDEX "emailplan_sort_order" ON "email_plans" ("sort_order");
-- Create "email_licenses" table
CREATE TABLE "email_licenses" ("id" uuid NOT NULL, "assigned_to_email" character varying NULL, "assigned_to_user_id" uuid NULL, "status" character varying NOT NULL DEFAULT 'AVAILABLE', "suspend_reason" character varying NULL, "storage_quota_gb" bigint NOT NULL, "features_json" jsonb NOT NULL, "assigned_at" timestamptz NULL, "expires_at" timestamptz NULL, "metadata" jsonb NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "email_plan_id" uuid NOT NULL, "product_subscription_id" uuid NOT NULL, "tenant_subscription_id" uuid NOT NULL, PRIMARY KEY ("id"), CONSTRAINT "email_licenses_email_plans_licenses" FOREIGN KEY ("email_plan_id") REFERENCES "email_plans" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION, CONSTRAINT "email_licenses_product_subscriptions_email_licenses" FOREIGN KEY ("product_subscription_id") REFERENCES "product_subscriptions" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION, CONSTRAINT "email_licenses_tenant_subscriptions_email_licenses" FOREIGN KEY ("tenant_subscription_id") REFERENCES "tenant_subscriptions" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION);
-- Create index "emaillicense_product_subscription_id_assigned_to_email" to table: "email_licenses"
CREATE UNIQUE INDEX "emaillicense_product_subscription_id_assigned_to_email" ON "email_licenses" ("product_subscription_id", "assigned_to_email");
-- Create index "emaillicense_status" to table: "email_licenses"
CREATE INDEX "emaillicense_status" ON "email_licenses" ("status");
-- Create index "emaillicense_tenant_subscription_id_status" to table: "email_licenses"
CREATE INDEX "emaillicense_tenant_subscription_id_status" ON "email_licenses" ("tenant_subscription_id", "status");
