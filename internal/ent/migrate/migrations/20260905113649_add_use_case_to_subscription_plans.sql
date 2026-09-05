-- Modify "subscription_plans" table
ALTER TABLE "subscription_plans" ADD COLUMN "use_case" character varying NULL;
-- Create index "subscriptionplan_use_case" to table: "subscription_plans"
CREATE INDEX "subscriptionplan_use_case" ON "subscription_plans" ("use_case");
