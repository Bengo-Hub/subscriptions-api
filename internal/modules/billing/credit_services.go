package billing

// This file is the single registry of prepaid credit services. Every product that sells units
// (SMS, WhatsApp messages, AI credits, external API tokens) is one MeteredService entry here and
// all of them run through the same ledger in token_wallet.go. Adding a credit product means
// registering a spec, never writing a second wallet.
//
// Wallet layout per tenant and service:
//   - BundleTag wallet: purchased units. They never expire.
//   - QuotaTag wallet (optional): the recurring allowance of the current period. It is reset to
//     the allowance by SetQuota at every period roll and is spent before the bundle wallet.
//
// Spend order for one consume call is quota, then bundle, then overage (only for services that
// declare an overage metric). Overage is billed in KES on the next invoice.

// MeteredService describes one credit product.
type MeteredService struct {
	// Code is the public service code used in API paths, e.g. "sms".
	Code string
	// BundleTag is the api_token_wallets.service_tag holding purchased units.
	BundleTag string
	// QuotaTag is the service_tag of the recurring allowance wallet, or "" when there is none.
	QuotaTag string
	// ProductCode is the add_on Product a tenant must have active to use the service. Empty means
	// no activation is required beyond holding a balance (the eTIMS API gates on its own plan).
	ProductCode string
	// OverageMetric is the overage_charges.metric_type used when the service may run past its
	// quota and bundle at UnitPriceKES per unit. Empty means prepaid only: a spend that does not
	// fit is refused.
	OverageMetric string
	// UnitPriceKES is the price of one unit for bundles and overage.
	UnitPriceKES float64
	// Bundles are the purchasable top-up sizes (units). Price is units times UnitPriceKES unless
	// a bundle sets its own PriceKES.
	Bundles []CreditBundle
	// Tiers are recurring monthly subscriptions that grant a quota (WhatsApp).
	Tiers []CreditTier
	// PlanQuotaKey is a tier_limits key on the tenant's base plan that grants a free monthly
	// allowance (AI credits), reset every period. Empty when the plan grants none.
	PlanQuotaKey string
	// Label is the display name.
	Label string
}

// CreditBundle is a purchasable pack of units.
type CreditBundle struct {
	Code     string  `json:"code"`
	Units    int64   `json:"units"`
	PriceKES float64 `json:"price_kes"`
}

// CreditTier is a recurring subscription that grants Quota units per month. Quota -1 is unlimited.
type CreditTier struct {
	Code        string  `json:"code"`
	Name        string  `json:"name"`
	PriceKES    float64 `json:"price_kes"`
	MonthlyUnit int64   `json:"monthly_units"`
}

// Service codes and wallet tags. The tags are stored in api_token_wallets.service_tag.
const (
	ServiceETIMS    = "etims_api"
	ServiceSMS      = "sms"
	ServiceWhatsApp = "whatsapp"
	ServiceAI       = "ai_credits"
)

// MessagingOverageUnitKES is the price of one SMS or WhatsApp message beyond quota and bundles.
const MessagingOverageUnitKES = 1.0

func messagingBundles() []CreditBundle {
	sizes := []int64{100, 500, 1000, 5000}
	out := make([]CreditBundle, 0, len(sizes))
	for _, n := range sizes {
		out = append(out, CreditBundle{Code: bundleCode(n), Units: n, PriceKES: float64(n) * MessagingOverageUnitKES})
	}
	return out
}

func bundleCode(units int64) string {
	switch units {
	case 100:
		return "bundle_100"
	case 500:
		return "bundle_500"
	case 1000:
		return "bundle_1000"
	case 5000:
		return "bundle_5000"
	}
	return ""
}

// creditServices is the registry, keyed by Code.
var creditServices = map[string]MeteredService{
	ServiceETIMS: {
		Code: ServiceETIMS, BundleTag: ServiceETIMS, Label: "eTIMS API tokens",
	},
	ServiceSMS: {
		Code: ServiceSMS, BundleTag: ServiceSMS, ProductCode: "sms_messaging",
		OverageMetric: "messaging_sms", UnitPriceKES: MessagingOverageUnitKES,
		Bundles: messagingBundles(), Label: "SMS",
	},
	ServiceWhatsApp: {
		Code: ServiceWhatsApp, BundleTag: ServiceWhatsApp, QuotaTag: "whatsapp_quota", ProductCode: "whatsapp_messaging",
		OverageMetric: "messaging_whatsapp", UnitPriceKES: MessagingOverageUnitKES,
		Bundles: messagingBundles(),
		Tiers: []CreditTier{
			{Code: "tier_1", Name: "Tier 1", PriceKES: 500, MonthlyUnit: 400},
			{Code: "tier_2", Name: "Tier 2", PriceKES: 1000, MonthlyUnit: 800},
			{Code: "pro", Name: "Pro", PriceKES: 2000, MonthlyUnit: -1},
		},
		Label: "WhatsApp",
	},
	ServiceAI: {
		Code: ServiceAI, BundleTag: ServiceAI, QuotaTag: "ai_credits_quota", ProductCode: "marketflow_ai_credits",
		PlanQuotaKey: "ai_credits_monthly",
		Bundles: []CreditBundle{
			{Code: "bundle_100", Units: 100, PriceKES: 1000},
			{Code: "bundle_500", Units: 500, PriceKES: 4000},
		},
		UnitPriceKES: 10, Label: "AI credits",
	},
}

// LookupMeteredService returns the spec for a service code.
func LookupMeteredService(code string) (MeteredService, bool) {
	s, ok := creditServices[code]
	return s, ok
}

// MeteredServiceCodes lists the registered codes.
func MeteredServiceCodes() []string {
	out := make([]string, 0, len(creditServices))
	for k := range creditServices {
		out = append(out, k)
	}
	return out
}

// Bundle returns the bundle with the given code.
func (s MeteredService) Bundle(code string) (CreditBundle, bool) {
	for _, b := range s.Bundles {
		if b.Code == code {
			return b, true
		}
	}
	return CreditBundle{}, false
}

// Tier returns the tier with the given code.
func (s MeteredService) Tier(code string) (CreditTier, bool) {
	for _, t := range s.Tiers {
		if t.Code == code {
			return t, true
		}
	}
	return CreditTier{}, false
}

// AllowsOverage reports whether the service may run past its prepaid units.
func (s MeteredService) AllowsOverage() bool { return s.OverageMetric != "" }
