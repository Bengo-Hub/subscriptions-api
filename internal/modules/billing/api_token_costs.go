package billing

import "strings"

// This file is the single source of truth for how many tokens one call to a metered external
// API endpoint costs. Weight tiers follow the general industry pattern (Stripe metered billing,
// Twilio per-channel pricing, OpenAI input/output token pricing): a cheap, cacheable read costs
// far less than an operation that performs real compute/writes, which in turn costs far less
// than an operation carrying real external/compliance weight (here: a call that actually reaches
// KRA's live signing infrastructure and produces legally significant fiscal data).
//
// TOKEN SCALE IS NORMALIZED TO PRESERVE EXISTING PRICING for the one call type that was already
// metered 1:1 (sales/credit-note/stock-io transmission, ApiTokenCostTransmit = 10 tokens): the
// ETIMS_API_* plans' seeded "included transactions" and "per-100 overage" figures are unchanged
// in KES terms once scaled by this weight (see cmd/seed/etims_api.go's included_tokens/
// token_price_kes derivation) — a subscriber's sales-transmission capacity does not shrink. Every
// other endpoint (lookups, item/device saves) was effectively unmetered/unbilled before this
// registry existed, so metering them at a low weight is a strict improvement for callers, not a
// new cost.
const (
	ApiTokenCostLookup   int64 = 1  // cacheable reads: code lists, item classification, branch/notice/PIN lookups
	ApiTokenCostWrite    int64 = 3  // device/item/customer "save" operations that don't transmit to KRA's signing path
	ApiTokenCostTransmit int64 = 10 // sales/credit-note transmission, stock in/out — hits KRA's live signing infra
)

// apiTokenCostRules maps a (service_tag, endpoint substring) to a token cost. Matched by
// substring against the request's route pattern (not full regex — the external API surface is
// small and stable enough that this stays readable; see TokenCostForEndpoint). Ordered most-
// specific-first: the first substring match wins.
type apiTokenCostRule struct {
	ServiceTag string
	Contains   string
	Tokens     int64
}

// Ordered MOST-SPECIFIC-FIRST — this matters, not just style: several endpoint substrings are
// prefixes of others (e.g. "/sales" is a substring of both "/sales-transactions" and
// "/sandbox/sales"), so a shorter/more-general rule listed before its more specific sibling
// would silently steal the match. Exempt/free prefixes are listed first for the same reason
// ("/sandbox/sales" must hit the "/sandbox/" free rule, never the "/sales" transmit rule).
var apiTokenCostRules = []apiTokenCostRule{
	// eTIMS external API (treasury-api /api/v1/external/etims/*) ----------------------------
	// Free/exempt prefixes FIRST — must win over any operation-specific rule below.
	{ServiceTag: "etims_api", Contains: "/sandbox/", Tokens: 0}, // never touches KRA or persists real data
	{ServiceTag: "etims_api", Contains: "/tokens/", Tokens: 0},  // wallet self-service, not a billable operation
	{ServiceTag: "etims_api", Contains: "/certification-status", Tokens: 0},
	{ServiceTag: "etims_api", Contains: "/request-go-live", Tokens: 0},
	// Specific-before-general for the rest.
	{ServiceTag: "etims_api", Contains: "/sales-transactions", Tokens: ApiTokenCostLookup},
	{ServiceTag: "etims_api", Contains: "/sales", Tokens: ApiTokenCostTransmit},
	{ServiceTag: "etims_api", Contains: "/credit-note", Tokens: ApiTokenCostTransmit},
	{ServiceTag: "etims_api", Contains: "/stock-io", Tokens: ApiTokenCostTransmit},
	{ServiceTag: "etims_api", Contains: "/devices/", Tokens: ApiTokenCostWrite}, // register/init a device
	{ServiceTag: "etims_api", Contains: "/devices", Tokens: ApiTokenCostWrite},
	{ServiceTag: "etims_api", Contains: "/items/bulk", Tokens: ApiTokenCostWrite},
	{ServiceTag: "etims_api", Contains: "/items/reconcile-stock", Tokens: ApiTokenCostWrite},
	{ServiceTag: "etims_api", Contains: "/items", Tokens: ApiTokenCostWrite},
	{ServiceTag: "etims_api", Contains: "/code-lists/refresh", Tokens: ApiTokenCostWrite},
	{ServiceTag: "etims_api", Contains: "/code-lists", Tokens: ApiTokenCostLookup},
	{ServiceTag: "etims_api", Contains: "/invoice-detail", Tokens: ApiTokenCostLookup},
	{ServiceTag: "etims_api", Contains: "/transmissions", Tokens: ApiTokenCostLookup},
	{ServiceTag: "etims_api", Contains: "/retry/", Tokens: ApiTokenCostWrite},
	{ServiceTag: "etims_api", Contains: "/status", Tokens: ApiTokenCostLookup},
}

// TokenCostForEndpoint resolves the token weight for one call to serviceTag+endpointPattern.
// Returns (cost, matched) — an unmatched endpoint defaults to ApiTokenCostWrite (fail toward
// metering something rather than silently giving an unrecognized route away for free), and
// matched=false so callers can log the gap for the registry to be extended.
func TokenCostForEndpoint(serviceTag, endpointPattern string) (int64, bool) {
	for _, r := range apiTokenCostRules {
		if r.ServiceTag != serviceTag {
			continue
		}
		if strings.Contains(endpointPattern, r.Contains) {
			return r.Tokens, true
		}
	}
	return ApiTokenCostWrite, false
}
