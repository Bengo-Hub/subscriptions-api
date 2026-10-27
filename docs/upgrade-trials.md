# Upgrade trials and scheduled plan changes

Updated 2026-10-27.

A tenant on a paid plan can try a higher tier of the same product free for a set number of days.
The platform owner turns this on for every tenant or for chosen tenants, and sets the length.
Upgrades made before the current period ends, including the one that follows a trial, take effect on
the next renewal date, and that renewal is billed at the new plan's price. There is no grace period
at the end of an upgrade trial: the tenant goes back to the plan it pays for until the renewal date.

## Rules

| Situation | What happens |
|---|---|
| Tenant starts a trial of a higher tier | The trial plan's features, limits and tier apply now, until `ends_at`. `plan_id` stays on the paid plan, so every invoice, renewal and revenue report keeps billing it. The upgrade is scheduled for the first renewal date on or after the trial end. |
| Trial ends | The trial plan stops applying. The paid plan applies until the scheduled renewal date. No grace, no invoice of its own. |
| Renewal date passes | `plan_id` moves to the upgraded plan. The renewal invoice for that period already billed it. |
| Tenant ends the trial early | The paid plan applies again and the scheduled upgrade is cancelled. |
| Tenant upgrades without a trial while paid time remains | Scheduled for the next renewal date (`PUT /subscription/plan`). Nothing is due now. |
| Tenant pays early at checkout for another plan | The plan is scheduled for the start of the paid period (the old period end), not switched on payment. |
| Tenant has no paid time left (expired, past due, no plan) | Checkout as before: pay, and the plan applies at once. |
| Tenant moves to a plan costing the same or less | Applies at once (`PUT /subscription/plan`). |
| Platform owner changes a plan, or presses "Apply now" | Applies at once. |
| An admin moves `current_period_end` | A change due on the old renewal date moves to the new one. |
| An admin assigns another plan | A trial or schedule made for the old plan becomes inert and the schedule job clears it. |

Who can start a trial: the subscription is ACTIVE or TRIAL and not past due; no trial is running and no
plan change is scheduled; the target is a public, active, recurring (not one-time) plan of the same
family (`PlanFamily`, the same rule as the in-family upgrade suggestions) with a higher `tier_order`
and a price above zero; the tenant has not tried that plan before. The platform owner can grant a
trial outside the switch, for a hidden plan, a second time, or with its own length.

Signup trials (status TRIAL with no paid plan behind them) are separate and keep the normal 7-day
grace: see section 20 of `subscription-workflows.md`.

## Configuration

Two `service_configs` keys. A row with `tenant_id` NULL is the platform setting; a row with a
tenant's id overrides it for that tenant. With neither, trials are off and last 14 days.

| Key | Type | Seeded | Meaning |
|---|---|---|---|
| `subscriptions.upgrade_trial_enabled` | bool | `false` | Whether tenants can start a trial |
| `subscriptions.upgrade_trial_days` | int, 1 to 90 | `14` | Trial length in days |

Both keys at both levels are read in one query. A value that does not parse is ignored (a bad edit
through the generic config screen falls back cleanly), and the newest of duplicate platform rows
wins. Admin screens: Platform > Configs (the "Upgrade trials" card) and Platform > Tenants > a tenant
(the "Upgrade trials" panel).

## State kept on the subscription

No schema change: everything lives in `tenant_subscriptions.metadata`.

| Key | Content |
|---|---|
| `upgrade_trial` | Running trial: `plan_id`, `plan_code`, `plan_name`, `tier_order`, `base_plan_id`, `base_plan_code`, `base_plan_name`, `days`, `started_at`, `ends_at`, `source` (tenant or platform), `started_by`, `ending_notice_sent` |
| `scheduled_plan_change` | Waiting change: `plan_id`, `plan_code`, `plan_name`, `from_plan_id`, `from_plan_code`, `from_plan_name`, `effective_at`, `source` (tenant, platform, upgrade_trial, checkout), `requested_at`, `requested_by` |
| `upgrade_trials_used` | Plan codes the tenant has tried |
| `upgrade_trial_history` | Last 10 trials: plan, base plan, start, end, outcome (completed, ended_by_tenant, ended_by_platform, paid, plan_changed, cancelled) |
| `last_invoice_plan_id` | The plan the last renewal invoice billed |
| `plan_change_invoice_*` | The plan-change invoice of the current cycle (see Billing) |
| `plan_change_payment_refs` | Invoice, intent and pay-reference ids of plan-change invoices (last 24) |
| `trial_lapsed_at` | Set when a signup trial expires unpaid |

Writes that own these keys use an optimistic lock on `updated_at`, so two requests cannot both start
a trial. `ErrConcurrentUpdate` comes back as HTTP 409.

## Which plan applies

`PlanIDInForce(sub, now)` is the one rule every reader uses:

1. a running trial (status ACTIVE or TRIAL, `now < ends_at`, `base_plan_id == plan_id`) gives the trial plan;
2. else a scheduled change whose `effective_at` has passed (before the job moves `plan_id`) gives the scheduled plan;
3. else `plan_id`.

`GetSubscriptionResult` (the `GET /subscription` and auth-api JWT source) builds features, limits,
`plan_code`, `tier_order` and `facility_type` from that plan, keeps the paid plan's features in the
union, and adds `upgrade_trial` and `scheduled_change` to the response. A trial therefore reaches
every service through the token with no change on their side. `tenant.subscription.updated` is
published when a trial starts or ends and when `plan_id` moves, so auth-api and cached gates refresh.

`EntitlementLimits` gives the same composite limits (plan in force, raised by active product overlay
plans) to the server-side checks that used to read only the paid plan's limits: `/usage/check` and
usage counting, the daily overage sweep, the usage dashboards and admin usage view, and the eTIMS
rate limit. Overage is no longer accrued for usage a trial or a product overlay allows.

Prepaid credit allowances (AI credits per month) stay on the paid plan: a trial does not hand out a
higher tier's monthly credit refill.

## Billing

The renewal invoice for the period starting at `current_period_end` bills `BillingPlanIDFor(sub)`:
the scheduled plan when its date is on or before that renewal, else `plan_id`.

When a change is scheduled (or cancelled) after the renewal invoice for that period already went
out, the renewal invoice is not voided and raised again, because it may already carry wallet
credit, overage lines and the setup fee, and its emailed pay link stays payable. Instead
`ReconcileOpenRenewalInvoice` raises a separate plan-change invoice for the price difference
(months of the billing period times the monthly difference, plus VAT), due on the renewal date.
Paying it settles that invoice only: the payment consumer recognises its ids and does not renew the
subscription a second time. Cancelling the change voids the plan-change invoice through treasury's
S2S void route, unless money has already been paid on it (then it is kept and logged for a refund
or credit). A change to a cheaper plan after the renewal invoice went out leaves the invoice as it
is. Custom-priced tenants (`custom_base_price`) are billed their custom price on any plan, so their
difference is zero.

## Jobs

`StartPlanScheduleJob` runs hourly (claimed by one replica). It sends the ending reminder 3 days
before a trial ends, closes finished trials (completed, no grace), applies scheduled changes whose
date has passed (moving `plan_id` and emitting `upgraded`, `downgraded` or `changed`), and clears
stale entries. It reads only rows that carry one of the two keys, in id pages of 200, so its cost
follows the number of open trials and schedules. Entitlements never wait for it: the resolver checks
the dates itself.

## Events and emails

| Event (`subscription.*`) | Email template | When |
|---|---|---|
| `upgrade_trial_started` | `subscription/upgrade_trial_started` | Trial starts |
| `upgrade_trial_ending` | `subscription/upgrade_trial_ending` | 3 days before the end |
| `upgrade_trial_ended` | `subscription/upgrade_trial_ended` | Trial completes or is ended early |
| `plan_change_scheduled` | `subscription/plan_change_scheduled` | An upgrade is scheduled |
| `upgraded` / `downgraded` | existing templates, now with plan names instead of ids | `plan_id` moves |
| `invoice_generated` | `finance/invoice_sent` | Plan-change invoice raised |
| `trial_ended` | `subscription/grace_reminder` | A signup trial ends unpaid and enters grace |

Links in these emails point at `SUBSCRIPTIONS_UI_URL` + `/billing` (default
`https://pricing.codevertexafrica.com`).

## Endpoints

Tenant (acts on the signed-in tenant):

| Method | Path | Purpose |
|---|---|---|
| GET | `/api/v1/subscription/upgrade-trial?plan_code=` | Policy outcome, running trial, scheduled change, history, eligibility for a plan |
| POST | `/api/v1/subscription/upgrade-trial` `{plan_code}` | Start a trial |
| DELETE | `/api/v1/subscription/upgrade-trial` | End the trial now, keep the paid plan |
| PUT | `/api/v1/subscription/plan` `{new_plan_code}` | Dearer plan: scheduled for renewal. Same or cheaper: at once |
| DELETE | `/api/v1/subscription/plan-change` | Cancel a scheduled change |

Platform owner:

| Method | Path | Purpose |
|---|---|---|
| GET / PUT | `/api/v1/admin/upgrade-trial/settings` `{enabled, days}` | Platform switch and length |
| GET | `/api/v1/admin/tenants/{tenant_id}/upgrade-trial?plan_code=` | Tenant view with both policy levels |
| PUT | `/api/v1/admin/tenants/{tenant_id}/upgrade-trial/policy` `{enabled, days}` | Tenant override; `null` follows the platform |
| POST | `/api/v1/admin/tenants/{tenant_id}/upgrade-trial` `{plan_code, days}` | Grant a trial |
| DELETE | `/api/v1/admin/tenants/{tenant_id}/upgrade-trial` | End a trial |
| POST | `/api/v1/admin/tenants/{tenant_id}/plan-change/apply` | Apply a scheduled change today |
| DELETE | `/api/v1/admin/tenants/{tenant_id}/plan-change` | Cancel a scheduled change |

Errors refusing a trial come back as HTTP 409 `{error, reason}` with `reason` one of `disabled`,
`status`, `past_due`, `trial_active`, `change_scheduled`, `plan_not_found`, `one_time_plan`,
`different_family`, `not_an_upgrade`, `free_plan`, `already_used`, `no_trial`, `no_change`.

## Fixed in the same change

- `PUT /subscription/plan` let any tenant move to any plan, including a dearer one, at once and with
  no payment, and set the status to ACTIVE. Tenant upgrades are now scheduled and billed at renewal;
  hidden plans and one-time licences are refused for tenants; the status is kept.
- `tenant_id` in the JSON body of `PUT /subscription/plan`, `POST /subscription/initiate` and
  `POST /subscription` was trusted, so a tenant could act on another tenant. Only service callers
  and the platform owner may now name another tenant.
- `PUT /subscriptions/{id}/switch-plan` had no tenant check at all. It is platform owner only.
- A renewal that switched plans published nothing, so auth-api kept the old plan on its tenant row.
- The upgrade emails printed the old plan's UUID as its name; renewal emails had no plan name.
- `proration_credit` was written on upgrades and never read anywhere; removed.
- `AssignPlanToTenant` with `startTrial` set the period end a month out while the trial ended
  earlier; the period now ends with the trial, as in every other trial path.
- The upgrade page charged the full price at once under a "prorated" label and listed Ordering-only
  limits for every product.

## Open items

- `preferences/registry.go` in notifications-api has no entries yet for the four new templates.
  Unregistered templates are delivered (fail open), so the emails go out; tenants just cannot switch
  them off. Add them as ClassEssential once the parallel session's uncommitted change to that file
  is committed.
- The `auth.tenant.created` consumer (`consumers/tenant_created.go`) asks for a bare `STARTER` plan
  that no longer exists and passes no T&C acceptance, so it never creates anything (signups go
  through `POST /subscription` instead), and `subscriptions.trial_days` is seeded but never read.
  Decide whether to remove the consumer or point it at a real plan.
- Downgrades still apply at once with no credit for the unused part of the period.
- An M-Pesa standing order keeps the amount it was registered with; after a scheduled upgrade the
  tenant has to register it again for the new amount.
