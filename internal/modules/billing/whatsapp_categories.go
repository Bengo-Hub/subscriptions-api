package billing

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
)

// WhatsApp is sold as one prepaid wallet per Meta pricing category (owner decision 2026-11-12):
// promotions (MARKETING), order, payment and account updates (UTILITY), login codes
// (AUTHENTICATION) and replies inside the 24 hour window (SERVICE). 1 credit = 1 message, and a
// send spends only from its own category's wallet. Names, explanations and prices live in
// whatsapp_categories.json so they are config, never typed into a handler.
//
// All four share one activation row (product whatsapp_messaging): buying any category activates
// WhatsApp. The monthly tiers (tier_1, tier_2, pro) are retired: they still resolve, so a tenant on
// a paid tier keeps its shared allowance (whatsapp_quota) until the period ends, but they are never
// sold or renewed again.

// GroupWhatsApp groups the WhatsApp category services for display.
const GroupWhatsApp = "whatsapp"

// WhatsApp category service codes, also their wallet service tags.
const (
	ServiceWhatsAppMarketing      = "whatsapp_marketing"
	ServiceWhatsAppUtility        = "whatsapp_utility"
	ServiceWhatsAppAuthentication = "whatsapp_authentication"
	ServiceWhatsAppService        = "whatsapp_service"
)

// whatsAppQuotaTag is the allowance wallet of a legacy tier, shared by every category while the
// tier's paid period runs.
const whatsAppQuotaTag = "whatsapp_quota"

//go:embed whatsapp_categories.json
var whatsAppCategoriesJSON []byte

// WhatsAppCategory is one entry of whatsapp_categories.json.
type WhatsAppCategory struct {
	Code         string  `json:"code"`
	MetaCategory string  `json:"meta_category"`
	Name         string  `json:"name"`
	Explainer    string  `json:"explainer"`
	UnitPriceKES float64 `json:"unit_price_kes"`
}

type whatsAppCategoryConfig struct {
	ProductCode string             `json:"product_code"`
	BundleSizes []int64            `json:"bundle_sizes"`
	Categories  []WhatsAppCategory `json:"categories"`
}

var whatsAppConfig = mustLoadWhatsAppCategories(whatsAppCategoriesJSON)

func mustLoadWhatsAppCategories(raw []byte) whatsAppCategoryConfig {
	var cfg whatsAppCategoryConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		panic(fmt.Sprintf("whatsapp_categories.json: %v", err))
	}
	if cfg.ProductCode == "" || len(cfg.BundleSizes) == 0 || len(cfg.Categories) == 0 {
		panic("whatsapp_categories.json: product_code, bundle_sizes and categories are required")
	}
	for _, c := range cfg.Categories {
		if c.Code == "" || c.MetaCategory == "" || c.Name == "" || c.Explainer == "" {
			panic(fmt.Sprintf("whatsapp_categories.json: category %q is incomplete", c.Code))
		}
		if c.UnitPriceKES < MessagingOverageUnitKES {
			panic(fmt.Sprintf("whatsapp_categories.json: %s costs KES %v, the lowest charge is KES %v", c.Code, c.UnitPriceKES, MessagingOverageUnitKES))
		}
	}
	return cfg
}

// WhatsAppCategories returns the configured categories in display order.
func WhatsAppCategories() []WhatsAppCategory {
	return append([]WhatsAppCategory(nil), whatsAppConfig.Categories...)
}

// WhatsAppServiceForMetaCategory maps a Meta template category (MARKETING, UTILITY,
// AUTHENTICATION, SERVICE; any case) to its credit service code.
func WhatsAppServiceForMetaCategory(meta string) (string, bool) {
	for _, c := range whatsAppConfig.Categories {
		if strings.EqualFold(c.MetaCategory, meta) {
			return c.Code, true
		}
	}
	return "", false
}

// legacyWhatsAppTiers are the retired monthly tiers. They resolve for tenants still inside a paid
// period and for payments already started, and are never offered.
func legacyWhatsAppTiers() []CreditTier {
	return []CreditTier{
		{Code: "tier_1", Name: "Tier 1", PriceKES: 500, MonthlyUnit: 400, Retired: true},
		{Code: "tier_2", Name: "Tier 2", PriceKES: 1000, MonthlyUnit: 800, Retired: true},
		{Code: "pro", Name: "Pro", PriceKES: 2000, MonthlyUnit: -1, Retired: true},
	}
}

// whatsAppLabel is the display label used on payments, invoices and alerts, e.g.
// "WhatsApp promotions and broadcasts".
func whatsAppLabel(name string) string {
	if name == "" {
		return "WhatsApp"
	}
	return "WhatsApp " + strings.ToLower(name[:1]) + name[1:]
}

// whatsAppServices builds one MeteredService per configured category.
func whatsAppServices(cfg whatsAppCategoryConfig) map[string]MeteredService {
	out := make(map[string]MeteredService, len(cfg.Categories))
	for _, c := range cfg.Categories {
		bundles := make([]CreditBundle, 0, len(cfg.BundleSizes))
		for _, n := range cfg.BundleSizes {
			bundles = append(bundles, CreditBundle{Code: fmt.Sprintf("bundle_%d", n), Units: n, PriceKES: float64(n) * c.UnitPriceKES})
		}
		out[c.Code] = MeteredService{
			Code: c.Code, BundleTag: c.Code, QuotaTag: whatsAppQuotaTag, ProductCode: cfg.ProductCode,
			// Overage and the shared allowance apply only while a legacy tier's paid period runs
			// (credits.Coverage); once every tier has lapsed WhatsApp is prepaid only.
			OverageMetric: "messaging_whatsapp", UnitPriceKES: c.UnitPriceKES,
			Bundles: bundles, Tiers: legacyWhatsAppTiers(),
			Label: whatsAppLabel(c.Name), Unit: "messages",
			Group: GroupWhatsApp, Name: c.Name, MetaCategory: c.MetaCategory, Explainer: c.Explainer,
		}
	}
	return out
}
