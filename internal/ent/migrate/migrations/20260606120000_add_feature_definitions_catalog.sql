-- Create "feature_definitions" table
CREATE TABLE "feature_definitions" ("id" uuid NOT NULL, "feature_code" character varying NOT NULL, "service_tag" character varying NOT NULL, "category" character varying NOT NULL DEFAULT 'General', "label" character varying NOT NULL, "description" text NULL, "kind" character varying NOT NULL DEFAULT 'FEATURE', "value_type" character varying NOT NULL DEFAULT 'bool', "default_limit" bigint NULL, "is_rate_limited" boolean NOT NULL DEFAULT false, "unit" character varying NULL, "nats_event" character varying NULL, "sort_order" bigint NOT NULL DEFAULT 0, "is_active" boolean NOT NULL DEFAULT true, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, PRIMARY KEY ("id"));
-- Create index "feature_definitions_feature_code_key" to table: "feature_definitions"
CREATE UNIQUE INDEX "feature_definitions_feature_code_key" ON "feature_definitions" ("feature_code");
-- Create index "featuredefinition_feature_code" to table: "feature_definitions"
CREATE UNIQUE INDEX "featuredefinition_feature_code" ON "feature_definitions" ("feature_code");
-- Create index "featuredefinition_kind" to table: "feature_definitions"
CREATE INDEX "featuredefinition_kind" ON "feature_definitions" ("kind");
-- Create index "featuredefinition_service_tag" to table: "feature_definitions"
CREATE INDEX "featuredefinition_service_tag" ON "feature_definitions" ("service_tag");
