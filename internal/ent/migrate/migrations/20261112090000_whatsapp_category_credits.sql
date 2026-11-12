-- WhatsApp credits are sold per Meta category since 2026-11-12 (whatsapp_categories.json). Every
-- legacy "whatsapp" bundle balance moves into the category wallets at the same KES value (1 unit at
-- KES 1 becomes 1 credit at KES 1):
--   * hadia (1e32db34-b65d-4384-9239-a22ef6e35985), whose templates are all order, payment and
--     account updates, keeps up to 10 units as replies to customers and the rest as utility
--     (100 units today: 90 utility and 10 service);
--   * every other tenant converts 1:1 into utility credits.
-- Each moved amount writes one ledger row on each side, so every wallet's history still sums to its
-- balance. Safe to run again: an emptied legacy wallet is skipped and each row has its own key.

-- 1. Destination wallets.
INSERT INTO "api_token_wallets" ("id", "tenant_id", "service_tag", "balance", "lifetime_granted", "low_balance_threshold", "created_at", "updated_at")
SELECT gen_random_uuid(), w."tenant_id", d.tag, 0, 0, 50, now(), now()
FROM "api_token_wallets" w
CROSS JOIN (VALUES ('whatsapp_utility'), ('whatsapp_service')) AS d(tag)
WHERE w."service_tag" = 'whatsapp' AND w."balance" > 0
  AND (d.tag = 'whatsapp_utility' OR w."tenant_id" = '1e32db34-b65d-4384-9239-a22ef6e35985'::uuid)
ON CONFLICT ("tenant_id", "service_tag") DO NOTHING;

-- 2. The move, in one statement.
WITH legacy AS (
  SELECT w."id", w."tenant_id", w."balance",
         CASE WHEN w."tenant_id" = '1e32db34-b65d-4384-9239-a22ef6e35985'::uuid THEN LEAST(10, w."balance") ELSE 0 END AS to_service
  FROM "api_token_wallets" w
  WHERE w."service_tag" = 'whatsapp' AND w."balance" > 0
  FOR UPDATE
),
moves AS (
  SELECT l."id" AS legacy_id, l."tenant_id", 'whatsapp_utility'::text AS tag,
         l."balance" - l.to_service AS units, l.to_service AS legacy_after,
         'Order, payment and account updates'::text AS name
  FROM legacy l
  UNION ALL
  SELECT l."id", l."tenant_id", 'whatsapp_service'::text, l.to_service, 0::bigint, 'Replies to customers'::text
  FROM legacy l
),
moved AS (
  SELECT m.*, d."id" AS dest_id, d."balance" AS dest_balance
  FROM moves m
  JOIN "api_token_wallets" d ON d."tenant_id" = m."tenant_id" AND d."service_tag" = m.tag
  WHERE m.units > 0
),
dest_update AS (
  UPDATE "api_token_wallets" d SET "balance" = d."balance" + mv.units, "updated_at" = now()
  FROM moved mv
  WHERE d."id" = mv.dest_id
  RETURNING d."id"
),
dest_rows AS (
  INSERT INTO "api_token_transactions" ("id", "tenant_id", "wallet_id", "service_tag", "action", "tokens", "new_balance",
    "ref_type", "description", "idempotency_key", "metadata", "created_at")
  SELECT gen_random_uuid(), mv."tenant_id", mv.dest_id, mv.tag, 'adjustment', mv.units, mv.dest_balance + mv.units,
    'whatsapp_category_migration', 'Moved from WhatsApp credits to ' || mv.name,
    'migrate:whatsapp-categories:in', jsonb_build_object('from', 'whatsapp', 'to', mv.tag, 'legacy_wallet_id', mv.legacy_id), now()
  FROM moved mv
  ON CONFLICT ("wallet_id", "idempotency_key") DO NOTHING
  RETURNING "id"
),
legacy_rows AS (
  INSERT INTO "api_token_transactions" ("id", "tenant_id", "wallet_id", "service_tag", "action", "tokens", "new_balance",
    "ref_type", "description", "idempotency_key", "metadata", "created_at")
  SELECT gen_random_uuid(), mv."tenant_id", mv.legacy_id, 'whatsapp', 'adjustment', -mv.units, mv.legacy_after,
    'whatsapp_category_migration', 'Moved to WhatsApp ' || mv.name,
    'migrate:whatsapp-categories:out:' || mv.tag, jsonb_build_object('from', 'whatsapp', 'to', mv.tag, 'dest_wallet_id', mv.dest_id), now()
  FROM moved mv
  ON CONFLICT ("wallet_id", "idempotency_key") DO NOTHING
  RETURNING "id"
)
UPDATE "api_token_wallets" w SET "balance" = 0, "updated_at" = now()
FROM legacy l
WHERE w."id" = l."id";
