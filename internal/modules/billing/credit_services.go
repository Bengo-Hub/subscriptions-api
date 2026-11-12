package billing

import (
	"fmt"
	"sort"
)

// This file is the single registry of prepaid credit services. Every product that sells units
// (SMS, WhatsApp messages, AI credits, external API tokens) is one MeteredService entry here and
// all of them run through the same ledger in token_wallet.go. Adding a credit product means
// registering a spec, never writing a second wallet. WhatsApp is four category services built from
// whatsapp_categories.json (see whatsapp_categories.go).
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
	// Tiers are recurring monthly subscriptions that grant a quota. Only the retired WhatsApp tiers
	// remain; they resolve for tenants inside a paid period and are never sold or renewed.
	Tiers []CreditTier
	// PlanQuotaKey is a tier_limits key on the tenant's base plan that grants a free monthly
	// allowance (AI credits), reset every period. Empty when the plan grants none.
	PlanQuotaKey string
	// Label is the display name.
	Label string
	// Unit names what one unit is (credits, tokens, messages), for balance alerts.
	Unit string
	// Group ties several services sold together (the WhatsApp categories); empty for the rest.
	Group string
	// Name is the plain category name a tenant reads, e.g. "Promotions and broadcasts".
	Name string
	// MetaCategory is the Meta pricing category the service pays for (WhatsApp only).
	MetaCategory string
	// Explainer tells a tenant in plain words which messages fall in the service.
	Explainer string
}

// CreditBundle is a purchasable pack of units.
type CreditBundle struct {
	Code     string  `json:"code"`
	Units    int64   `json:"units"`
	PriceKES float64 `json:"price_kes"`
	// Retired bundles are no longer sold but still resolve, so a payment started before they were
	// retired is still granted.
	Retired bool `json:"-"`
}

// CreditTier is a recurring subscription that grants Quota units per month. Quota -1 is unlimited.
type CreditTier struct {
	Code        string  `json:"code"`
	Name        string  `json:"name"`
	PriceKES    float64 `json:"price_kes"`
	MonthlyUnit int64   `json:"monthly_units"`
	// Retired tiers are never sold or renewed but still resolve, so a tenant keeps a tier it paid
	// for until the period ends and a payment started before the retirement still grants.
	Retired bool `json:"-"`
}

// Service codes and wallet tags. The tags are stored in api_token_wallets.service_tag.
const (
	ServiceETIMS = "etims_api"
	ServiceSMS   = "sms"
	ServiceAI    = "ai_credits"
	// ServiceWhatsApp is the retired single WhatsApp service. It is an alias of
	// ServiceWhatsAppUtility so payments, sends and refunds still naming it keep working, and its
	// old wallet was moved into the category wallets by a data migration.
	ServiceWhatsApp = "whatsapp"
)

// MessagingOverageUnitKES is the price of one SMS beyond bundles, and the lowest price of any message.
const MessagingOverageUnitKES = 1.0

// smsBundleSizes are the SMS top-up sizes, at MessagingOverageUnitKES a unit.
var smsBundleSizes = []int64{100, 500, 1000, 5000}

func smsBundles() []CreditBundle {
	out := make([]CreditBundle, 0, len(smsBundleSizes))
	for _, n := range smsBundleSizes {
		out = append(out, CreditBundle{Code: fmt.Sprintf("bundle_%d", n), Units: n, PriceKES: float64(n) * MessagingOverageUnitKES})
	}
	return out
}

// legacyServiceAliases map retired service codes to the service that now holds their units.
var legacyServiceAliases = map[string]string{
	ServiceWhatsApp: ServiceWhatsAppUtility,
}

// creditServices is the registry, keyed by Code.
var creditServices = buildCreditServices()

func buildCreditServices() map[string]MeteredService {
	out := map[string]MeteredService{
		ServiceETIMS: {
			Code: ServiceETIMS, BundleTag: ServiceETIMS, Label: "eTIMS API tokens", Unit: "tokens",
		},
		// SMS is credits only (owner, 2026-11-12): no overage metric, so a send that does not fit
		// the bundle is refused. SMS overage already on an invoice stays there.
		ServiceSMS: {
			Code: ServiceSMS, BundleTag: ServiceSMS, ProductCode: "sms_messaging",
			UnitPriceKES: MessagingOverageUnitKES, Bundles: smsBundles(), Label: "SMS", Unit: "messages",
		},
		ServiceAI: aiCreditsService(),
	}
	for code, spec := range whatsAppServices(whatsAppConfig) {
		out[code] = spec
	}
	return out
}

func aiCreditsService() MeteredService {
	return MeteredService{
		Code: ServiceAI, BundleTag: ServiceAI, QuotaTag: "ai_credits_quota",
		PlanQuotaKey: "ai_credits_monthly",
		Bundles: []CreditBundle{
			// Owner, 2026-11-11 (.claude/plans/marketflow-ai-studio-bundle-pricing-2026-11-11.md): four
			// bundles for every budget, a 60% down to 51% media margin. No plan includes AI credits.
			{Code: "bundle_60", Units: 60, PriceKES: 750},
			{Code: "bundle_150", Units: 150, PriceKES: 1750},
			{Code: "bundle_400", Units: 400, PriceKES: 4400},
			{Code: "bundle_1000", Units: 1000, PriceKES: 10000},
			// Retired 2026-11-11: still resolve for payments already started.
			{Code: "bundle_100", Units: 100, PriceKES: 1000, Retired: true},
			{Code: "bundle_500", Units: 500, PriceKES: 4000, Retired: true},
		},
		Label: "AI credits", Unit: "credits",
	}
}

// LookupMeteredService returns the spec for a service code. A retired code resolves to the
// service that now holds its units (whatsapp is utility credits).
func LookupMeteredService(code string) (MeteredService, bool) {
	if alias, ok := legacyServiceAliases[code]; ok {
		code = alias
	}
	s, ok := creditServices[code]
	return s, ok
}

// serviceByQuotaTag finds the service whose allowance wallet is quotaTag. The WhatsApp
// categories share one allowance wallet; utility, which inherited the old WhatsApp wallet, is
// returned for it so the answer does not depend on map order.
func serviceByQuotaTag(quotaTag string) (MeteredService, bool) {
	if quotaTag == whatsAppQuotaTag {
		if s, ok := creditServices[ServiceWhatsAppUtility]; ok {
			return s, true
		}
	}
	for _, code := range MeteredServiceCodes() {
		if s := creditServices[code]; s.QuotaTag != "" && s.QuotaTag == quotaTag {
			return s, true
		}
	}
	return MeteredService{}, false
}

// MeteredServiceCodes lists the registered codes, sorted. Retired aliases are not listed.
func MeteredServiceCodes() []string {
	out := make([]string, 0, len(creditServices))
	for k := range creditServices {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// OfferedTiers are the tiers on sale (retired ones left out).
func (s MeteredService) OfferedTiers() []CreditTier {
	out := make([]CreditTier, 0, len(s.Tiers))
	for _, t := range s.Tiers {
		if !t.Retired {
			out = append(out, t)
		}
	}
	return out
}

// OfferedBundles are the bundles on sale (retired ones left out).
func (s MeteredService) OfferedBundles() []CreditBundle {
	out := make([]CreditBundle, 0, len(s.Bundles))
	for _, b := range s.Bundles {
		if !b.Retired {
			out = append(out, b)
		}
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

// Metadata keys on the activation ProductSubscription of a tiered credit service.
const (
	MetaTierCode      = "tier_code"
	MetaTierPeriodEnd = "tier_period_end"
	MetaTierAutoRenew = "tier_auto_renew"
)
