-- Rename the "Complete" bundle plan family to "PowerSuite" (data migration).
--
-- Run against the subscription-service database.
--
-- Background: the seed (cmd/seed/plans_bundles.go) keeps the deterministic plan
-- UUIDs stable across the rename (its id namespace strings are unchanged), so
-- re-running the seed already updates subscription_plans.plan_code / name in place
-- and tenant_subscriptions.plan_id stays valid (it FKs by id, not by code).
--
-- This script is the idempotent fallback for environments that are NOT re-seeded,
-- plus it fixes the bundle_code carried on existing tenant subscriptions. Safe to
-- run multiple times.

BEGIN;

-- 1. Plan catalog rows: COMPLETE_* -> POWERSUITE_*, "Complete ..." -> "PowerSuite ..."
UPDATE subscription_plans
SET plan_code = replace(plan_code, 'COMPLETE_', 'POWERSUITE_'),
    name      = replace(name, 'Complete', 'PowerSuite'),
    updated_at = now()
WHERE plan_code LIKE 'COMPLETE\_%';

-- 2. Bundle row code/name (bundles table) if present.
UPDATE bundles
SET code = 'powersuite',
    name = replace(name, 'Complete', 'PowerSuite')
WHERE code = 'complete';

-- 3. Tenant subscriptions carry a denormalized bundle_code (plan link is by
--    plan_id and needs no change). Flip any 'complete' bundle reference.
UPDATE tenant_subscriptions
SET bundle_code = 'powersuite'
WHERE bundle_code = 'complete';

COMMIT;

-- Companion (run against the AUTH-service DB — see auth-api/scripts):
--   the auth `tenants` table denormalizes subscription_plan (plan_code string).
--   Any tenant on a COMPLETE_* plan must be flipped to POWERSUITE_* there too.
