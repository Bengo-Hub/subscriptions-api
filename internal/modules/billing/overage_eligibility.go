package billing

// This file is the single source of truth for which plan limits are eligible for
// pay-as-you-go overage ("extra usage"). Overage applies ONLY to metered throughput
// limits — counters that reset every billing cycle. Structural caps (max_outlets,
// max_devices, max_cashiers, max_tables, max_riders, inventory_max_warehouses,
// inventory_max_sku, max_wallets, max_currencies, max_admins, max_staff, max_suppliers)
// hard-block and require a plan upgrade instead.
//
// Keep this list in sync with shared/auth-client (Claims.IsOverageEligible) and
// shared-ui-lib (feature-catalog). The metric keys here are the usage_event metric_type
// values; PlanLimitKey is the matching key in a plan's tierLimitsJSON; OveragePriceKey is
// the tierLimitsJSON key holding the KES unit price (empty = no price seeded yet → cannot
// be overaged even when allow_overage is on, so it hard-blocks).

// MeteredMetric describes one overage-eligible throughput limit.
type MeteredMetric struct {
	Metric          string  // usage_event metric_type, e.g. "orders"
	PlanLimitKey    string  // tierLimitsJSON key, e.g. "max_orders_per_month"
	OveragePriceKey string  // tierLimitsJSON key for KES unit price, e.g. "overage_orders_price_per_100_month"
	Unit            string  // human unit for UI, e.g. "per 100 orders / month"
	PriceQuantum    float64 // how many units one price covers (100 for "per 100"); 1 = per single unit
}

// meteredMetrics is the canonical metered-overage registry, keyed by metric_type.
var meteredMetrics = map[string]MeteredMetric{
	"orders": {
		Metric: "orders", PlanLimitKey: "max_orders_per_month",
		OveragePriceKey: "overage_orders_price_per_100_month", Unit: "per 100 orders", PriceQuantum: 100,
	},
	"transactions": {
		Metric: "transactions", PlanLimitKey: "max_transactions_per_month",
		OveragePriceKey: "overage_transactions_price_per_100_month", Unit: "per 100 transactions", PriceQuantum: 100,
	},
	"api_calls": {
		Metric: "api_calls", PlanLimitKey: "api_calls_per_month",
		OveragePriceKey: "overage_api_calls_price_per_1000_month", Unit: "per 1,000 API calls", PriceQuantum: 1000,
	},
	"sms_notifications": {
		Metric: "sms_notifications", PlanLimitKey: "sms_notifications_per_month",
		OveragePriceKey: "overage_sms_price_per_100", Unit: "per 100 SMS", PriceQuantum: 100,
	},
	"email_notifications": {
		Metric: "email_notifications", PlanLimitKey: "email_notifications_per_month",
		OveragePriceKey: "overage_email_price_per_1000", Unit: "per 1,000 emails", PriceQuantum: 1000,
	},
	"webhook_calls": {
		Metric: "webhook_calls", PlanLimitKey: "webhook_calls_per_month",
		OveragePriceKey: "overage_webhook_price_per_1000", Unit: "per 1,000 webhook calls", PriceQuantum: 1000,
	},
	"live_tracking_requests": {
		Metric: "live_tracking_requests", PlanLimitKey: "live_tracking_requests_per_month",
		OveragePriceKey: "overage_live_tracking_price_per_1000", Unit: "per 1,000 tracking requests", PriceQuantum: 1000,
	},
	"routing_requests": {
		Metric: "routing_requests", PlanLimitKey: "routing_requests_per_month",
		OveragePriceKey: "overage_routing_price_per_1000", Unit: "per 1,000 routing requests", PriceQuantum: 1000,
	},
	// etims_transactions is NOT registered here — it moved off this soft-cap/monthly-overage
	// pipeline onto the real-time prepaid ApiTokenWallet (see token_wallet.go, api_token_costs.go).
	// The external eTIMS API's included-transaction/overage figures are now expressed as
	// included_tokens/token_price_kes on the ETIMS_API_* plans and enforced by treasury-api's
	// ExternalAPIKeyAuth middleware calling POST /tenants/{id}/tokens/deduct BEFORE each KRA call,
	// not by this map + ReportUsage's Redis monthly counter. Do not re-add it here — a metric
	// reported through the old ReportUsage/evaluateUsage path would double-gate a call that the
	// wallet has already authorized.
}

// IsOverageEligibleMetric reports whether a usage metric_type may be overaged.
func IsOverageEligibleMetric(metric string) bool {
	_, ok := meteredMetrics[metric]
	return ok
}

// MeteredMetricByType returns the registry entry for a metric_type.
func MeteredMetricByType(metric string) (MeteredMetric, bool) {
	m, ok := meteredMetrics[metric]
	return m, ok
}

// MeteredMetricByLimitKey finds the registry entry whose PlanLimitKey matches a
// tierLimitsJSON key (e.g. "max_orders_per_month").
func MeteredMetricByLimitKey(limitKey string) (MeteredMetric, bool) {
	for _, m := range meteredMetrics {
		if m.PlanLimitKey == limitKey {
			return m, true
		}
	}
	return MeteredMetric{}, false
}

// OverageEligibleMetrics returns the canonical metered metric_type keys.
func OverageEligibleMetrics() []string {
	out := make([]string, 0, len(meteredMetrics))
	for k := range meteredMetrics {
		out = append(out, k)
	}
	return out
}
