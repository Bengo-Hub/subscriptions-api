-- Modify "subscription_plans" table
ALTER TABLE "subscription_plans" ADD COLUMN "free_trial_days" bigint NOT NULL DEFAULT 14;
