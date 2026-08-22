-- Modify "tenant_subscriptions" table: one-time Type-A referral bonus gate.
-- The referral bonus rewards BRINGING IN the referral, so it is paid once on the referred
-- tenant's first successful payment. Before this flag the payout keyed off the payment ref
-- only, so every renewal the referred tenant ever paid credited the referrer again.
-- Backfill: any tenant that already has a referrer AND already produced a referral_bonus
-- credit row is marked paid, so existing referrals do not get a second payout.
ALTER TABLE "tenant_subscriptions" ADD COLUMN "referral_bonus_paid" boolean NOT NULL DEFAULT false;

UPDATE "tenant_subscriptions" ts
SET "referral_bonus_paid" = true
WHERE ts."referred_by" IS NOT NULL
  AND EXISTS (
    SELECT 1
    FROM "subscription_credit_transactions" sct
    WHERE sct."tenant_id" = ts."referred_by"
      AND sct."type" = 'referral_bonus'
  );

-- Pre-flight for the unique index below. The event-driven earn paths previously used a fresh
-- random uuid as the credit ref whenever the event carried no intent_id, so legacy rows should
-- not collide; but a redelivered event that DID carry an intent_id could have produced two
-- 'earned' rows for the same (tenant, ref_id). Fail with an actionable message rather than a
-- bare unique-violation if so.
DO $$
DECLARE
  dup_count bigint;
BEGIN
  SELECT count(*) INTO dup_count FROM (
    SELECT 1
    FROM "subscription_credit_transactions"
    WHERE "type" IN ('earned', 'referral_bonus') AND "ref_id" IS NOT NULL
    GROUP BY "tenant_id", "type", "ref_id"
    HAVING count(*) > 1
  ) d;
  IF dup_count > 0 THEN
    RAISE EXCEPTION 'cannot create subscriptioncredittransaction_tenant_id_type_ref_id: % duplicate (tenant_id, type, ref_id) group(s) in subscription_credit_transactions. These are double-credited rows from the pre-fix idempotency bug. Reconcile them (keep the earliest row per group, adjust subscription_credits.balance_kes/lifetime_earned_kes by the removed amount_kes) before re-running this migration.', dup_count;
  END IF;
END $$;

-- Create index "subscriptioncredittransaction_tenant_id_type_ref_id" to table:
-- "subscription_credit_transactions". Last-resort double-credit guard for the two
-- event-driven earn paths, whose ref_id is the payment idempotency key. PARTIAL by design:
-- 'auto_applied' reuses the tenant's own id as ref_id on every renewal and 'gifted' reuses
-- the granting admin's user id on every gift, so a blanket unique index would break both.
CREATE UNIQUE INDEX "subscriptioncredittransaction_tenant_id_type_ref_id" ON "subscription_credit_transactions" ("tenant_id", "type", "ref_id") WHERE type IN ('earned', 'referral_bonus');
