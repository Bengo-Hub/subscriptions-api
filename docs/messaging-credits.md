# Messaging and AI credits

subscriptions-api owns every prepaid unit sold on the platform: SMS, WhatsApp messages, AI credits and
the eTIMS API tokens. One engine, one ledger, one price list. Services that send or meter ask this
service whether a unit may be spent and report each spend. Nothing else keeps a wallet.

## What tenants can buy

| Service | Bundles | Monthly plans | Beyond plan and bundles |
|---|---|---|---|
| SMS | 100, 500, 1000, 5000 units at KES 1 each | none | KES 1 per SMS, billed on the next invoice |
| WhatsApp | 100, 500, 1000, 5000 units at KES 1 each | Tier 1: KES 500 for 400 messages. Tier 2: KES 1000 for 800. Pro: KES 2000, unlimited | KES 1 per message, billed on the next invoice |
| AI credits | 100 for KES 1000, 500 for KES 4000 | the plan's own monthly allowance (`ai_credits_monthly`) | not allowed, prepaid only |

Bundle units never expire. A plan allowance resets at the start of each calendar month.

The price list is public at `GET /api/v1/credits/catalog`. The source is `internal/modules/billing/credit_services.go`.
Adding a credit product means adding one entry there.

## How a send is paid for

1. The allowance of the current plan period.
2. The bundle balance.
3. Overage at KES 1 per unit, only for services that allow it and only while the tenant has a live plan or
   bundle activation. Overage accrues in one open `overage_charges` row per subscription and is added to the
   next subscription invoice. Sends pause when unpaid overage reaches the platform cap
   (`messaging.overage_cap_kes` in `service_configs`, default KES 500). Paying the invoice or buying a bundle
   lets sending continue.

Who may send:

* Every ordinary tenant needs an active or trial subscription and the service activated. The first bundle or plan
  bought activates it.
* Exempt tenants (the platform tenant `codevertex`, the demo tenant `codevertex-demo`, and any tenant flagged
  subscription exempt) are never charged and never refused.
* Platform scope sends (one time codes, platform invoices and reminders) are never charged to a tenant.

## Ledger

The ledger is the existing `api_token_wallets` and `api_token_transactions` pair, keyed by `service_tag`.

| service_tag | Holds |
|---|---|
| `sms`, `whatsapp`, `ai_credits` | purchased bundle units |
| `whatsapp_quota`, `ai_credits_quota` | the recurring allowance of the current period |
| `etims_api` | API tokens (prepaid only) |

Every movement runs in one database transaction with the wallet rows locked in tag order. A movement can carry
an idempotency key, enforced by the unique index on `(wallet_id, idempotency_key)`, so a redelivered event or a
retried send applies once. The code is `internal/modules/billing/credit_ledger.go`; the entitlement rules are in
`internal/modules/credits`.

Ledger actions: `topup` (paid bundle), `grant`, `deduction`, `overage` (a unit sent beyond quota and bundle, the
balance does not move), `refund`, `adjustment` (quota resets, admin grants, migration).

## Interfaces

Service to service, with the internal service key:

* `GET /api/v1/s2s/credits/{service}/check` asks without recording anything.
* `POST /api/v1/s2s/credits/{service}/consume` spends units. Always send a stable `key` for the send attempt. The answer
  is 200 with `allowed` true or false and a `reason`. A 5xx means unknown, and the caller must not send.
* `POST /api/v1/s2s/credits/{service}/refund` reverses a spend by `consume_key` when the provider rejected it.
* `POST /api/v1/s2s/credits/import` moves balances from the retired notifications wallet tables.

Tenant facing:

* `GET /api/v1/credits` balances, plan, owed overage and what can be bought.
* `GET /api/v1/credits/{service}/transactions` bundle history.
* `POST /api/v1/credits/{service}/purchase` creates a Treasury intent for a bundle or plan.
* `POST /api/v1/credits/{service}/auto-renew` turns plan renewal on or off.

Platform owner:

* `POST /api/v1/admin/tenants/{tenant_id}/credits/{service}/grant` adds units without a payment.
* `GET /api/v1/admin/credits/tenants` lists who holds what. See the next section.
* `GET /api/v1/admin/credits/overview` gives platform totals per service.
* `GET` and `PUT /api/v1/admin/credits/settings` read and change the messaging settings.

## Platform reporting and settings

These routes need the platform owner role and read the ledger with SQL aggregates, so they stay cheap as tenants and
ledger rows grow. Months are UTC calendar months.

`GET /api/v1/admin/credits/tenants?service=sms|whatsapp|ai_credits&q=&status=&limit=&offset=` returns one row per tenant
that has a wallet, an activation or overage for the service, newest activity first. `q` matches tenant name or slug,
`status` is one of `active`, `capped`, `not_activated`, `exempt`, `limit` defaults to 25 (max 100). The answer is
`{service, data, total, limit, offset}`. Each row has the tenant id, slug and name, the base plan name and status,
bundle balance, quota balance, tier code, name, period end and whether it is still live, `auto_renew`,
`overage_pending_kes`, `overage_cap_kes`, `capped`, `units_used_month`, `last_activity_at`, `exempt` and `status`.
A tenant is `capped` when its unpaid overage plus one more unit would pass the cap, which is the point where sends pause.
A lapsed tier shows no quota, matching what a send would see. `total` is zero when the offset is past the last row.

`GET /api/v1/admin/credits/overview` returns `{month_start, overage_cap_kes, services}` with, per service, tenants
activated, units sold (paid bundles) and consumed this month, bundle revenue this month, overage accrued this month,
overage still unpaid, and tenants at or over the cap. Tier subscription revenue is billed on the subscription invoice and
is not part of the bundle revenue figure.

`GET /api/v1/admin/credits/settings` and `PUT` with `{"overage_cap_kes": 500}` manage the one platform setting that
the engine reads, `messaging.overage_cap_kes`. Zero means no cap, the upper bound is 1,000,000 and two decimal places are
allowed. The write stores the value in `service_configs`, clears the cache on the replica that served it and the other
replicas see it within a minute. There is no other messaging setting to edit: the low balance threshold on a wallet is
a fixed per-wallet value that no messaging rule reads.

Reasons a spend is refused: `no_subscription`, `service_not_activated`, `insufficient_credits`, `overage_cap`.

## Buying

* Standalone: the purchase endpoint creates a Treasury intent with `reference_type=credit_purchase`. The payment
  consumer grants it when `payment.succeeded` arrives, keyed by the intent id.
* With a plan: `POST /subscription/initiate` accepts `addons` (`service`, `kind`, `code`). The prices are added to the
  one payment, the selection is kept in the subscription metadata (`pending_credit_addons`) beside the pending
  intent, and it is granted when that intent pays. A free plan or a trial cannot carry add-ons.
* Recurring plans: the subscription invoice carries one line per tier the tenant keeps on auto renew, priced for every
  month of the billing period. Paying the invoice extends the tier by those months.
* Platform owner grants are adjustments with a reason, never top-ups, so revenue reports stay clean.

## Auto top-up

A tenant can have a bundle bought automatically with the saved card when the bundle balance falls below a number the tenant
chooses (`PUT /api/v1/credits/{service}/auto-topup`, read with `GET`, also shown as `auto_topup` in `GET /api/v1/credits`). The
tenant picks the bundle and a monthly spend limit (default KES 5,000, at most KES 100,000, at least one bundle). It needs the
service activated and a saved card.

Safety rules, all tested:

* At most one attempt per service per day. The Treasury reference carries the day (`AUTOTOP-<tenant>-<service>-<yyyymmdd>`),
  so even a replayed attempt cannot charge twice, and the attempt is recorded before the card is charged.
* The monthly limit counts what was charged; a month change resets it. At the limit nothing is bought and extra messages fall
  back to KES 1 overage on the invoice.
* A declined card or a refusal switches the setting off, records the reason and sends the tenant admin an email and a WhatsApp
  message (`credits_auto_topup_failed`, WhatsApp template `credits_auto_topup_failed_v1_btn`). Nothing is counted as spent.
* A Treasury server error means the outcome is unknown: it is not retried the same day, not counted, and the setting stays on.
* Exempt tenants, suspended subscriptions and tenants with no setting are never charged.

The hourly credits job runs it after the allowance refill. The units arrive through the normal `credit_purchase` payment event.
After deploying notifications-api, run the WhatsApp template sync so the new template is submitted to Meta; until it is approved
the tenant still gets the email.

## Background work

`jobs.StartCreditsJob` runs hourly. It sets each recurring allowance to the current month's value (live tier units,
zero for a lapsed tier, the plan's `ai_credits_monthly`). Each reset is keyed by month, so any number of runs on any
number of replicas change nothing after the first.

The daily overage job sums usage events in SQL and no longer applies to SMS or WhatsApp. Those run on the ledger.

## Moving old balances

notifications-api used to keep SMS balances and WhatsApp subscriptions in its own tables. They are moved with
`notifications-export-wallets` (dry run by default, `-apply` to write). The import is additive and idempotent:
SMS units become one adjustment per tenant, a live WhatsApp plan keeps its end date and gets the new quota less what was
already used that month. A tenant with no subscription here is reported and skipped. The old tables are not touched and
can be dropped in a later release once the totals have been checked.

## Notes for operators

* Changing a price or a bundle size is a code change in the registry plus a release. Existing tenants keep the units
  they already bought.
* The overage cap can be changed without a release through `PUT /api/v1/admin/credits/settings`.
* marketflow-ai spends one AI credit per chat message through the consume endpoint and refunds it when no answer was
  produced. The old one time `MARKETFLOW_AI_CREDITS_*` plans are switched off by the seed; the same two packs are
  catalog bundles. Only a tenant that resolves to a real tenant row is billed, and the platform tenant is exempt.
* The old `addon_purchase` path (`/api/v1/addons`) was removed: it had no payment consumer branch that settled it.
  The plan-feature add-on seed was removed with it.
