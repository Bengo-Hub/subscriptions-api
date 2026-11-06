# Public feature catalog and ISP plan copy (Nov 2026)

**Status:** Code done; the live ISP description needs a seed run

## Delivered
- [x] `GET /api/v1/catalog/features` serves the active feature and limit names without sign-in:
  code, service tag, category, label, description, kind, unit and sort order. It leaves out the
  Cross-Service Access category, event subjects, default limits and plan mappings, and sends
  `Cache-Control: public, max-age=300`. The signed-in `/features/catalog` is unchanged.
- [x] accounts.codevertexafrica.com/pricing reads it to list each tier's features by category.
- [x] The `ISP_BILLING_STARTER` description no longer repeats the usage charges. The pricing page
  prints them from `tier_limits` (`pppoe_per_subscriber_fee`, `service_charge_percentage`,
  `service_charge_threshold`), so the copy cannot drift from the rates billed.

## Open items
- Seeds run by hand (`seed.enabled: false` in devops-k8s), so the shorter ISP description reaches
  production only after `cmd/seed` runs against the production database or the plan is edited in
  the platform plans admin.
