-- Modify "subscription_plans" table
ALTER TABLE "subscription_plans" ADD COLUMN "service_tag" character varying NULL;
-- Create index "subscriptionplan_service_tag" to table: "subscription_plans"
CREATE INDEX "subscriptionplan_service_tag" ON "subscription_plans" ("service_tag");
