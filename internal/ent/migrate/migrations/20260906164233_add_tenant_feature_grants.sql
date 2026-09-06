-- Create "tenant_feature_grants" table
CREATE TABLE "tenant_feature_grants" ("id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "feature_code" character varying NOT NULL, "granted_by" uuid NOT NULL, "granted_at" timestamptz NOT NULL, "revoked_at" timestamptz NULL, "revoked_by" uuid NULL, "notes" character varying NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, PRIMARY KEY ("id"));
-- Create index "tenantfeaturegrant_feature_code" to table: "tenant_feature_grants"
CREATE INDEX "tenantfeaturegrant_feature_code" ON "tenant_feature_grants" ("feature_code");
-- Create index "tenantfeaturegrant_tenant_id" to table: "tenant_feature_grants"
CREATE INDEX "tenantfeaturegrant_tenant_id" ON "tenant_feature_grants" ("tenant_id");
-- Create index "tenantfeaturegrant_tenant_id_feature_code" to table: "tenant_feature_grants"
CREATE UNIQUE INDEX "tenantfeaturegrant_tenant_id_feature_code" ON "tenant_feature_grants" ("tenant_id", "feature_code");
