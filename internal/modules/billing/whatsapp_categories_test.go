package billing

import (
	"strings"
	"testing"
)

// Owner decision 2026-11-12: WhatsApp is sold as four separate wallets, one per Meta pricing
// category, 1 credit = 1 message. Names are plain words and prices come from the config file.
func TestWhatsAppCategoriesComeFromConfig(t *testing.T) {
	want := []struct {
		code, meta, name string
		price            float64
	}{
		{ServiceWhatsAppMarketing, "MARKETING", "Promotions and broadcasts", 4},
		{ServiceWhatsAppUtility, "UTILITY", "Order, payment and account updates", 1},
		{ServiceWhatsAppAuthentication, "AUTHENTICATION", "Login and verification codes", 1},
		{ServiceWhatsAppService, "SERVICE", "Replies to customers", 1},
	}
	if got := WhatsAppCategories(); len(got) != len(want) {
		t.Fatalf("want %d WhatsApp categories, got %d", len(want), len(got))
	}
	for _, w := range want {
		spec, ok := LookupMeteredService(w.code)
		if !ok {
			t.Fatalf("%s is not registered", w.code)
		}
		if spec.Code != w.code || spec.BundleTag != w.code {
			t.Errorf("%s: code %q tag %q", w.code, spec.Code, spec.BundleTag)
		}
		if spec.MetaCategory != w.meta || spec.Name != w.name {
			t.Errorf("%s: meta %q name %q", w.code, spec.MetaCategory, spec.Name)
		}
		if spec.UnitPriceKES != w.price {
			t.Errorf("%s: price %v, want KES %v", w.code, spec.UnitPriceKES, w.price)
		}
		if spec.UnitPriceKES < 1 {
			t.Errorf("%s: the lowest charge is KES 1 a message", w.code)
		}
		// Any category purchase activates the one WhatsApp activation row.
		if spec.ProductCode != "whatsapp_messaging" || spec.Group != GroupWhatsApp {
			t.Errorf("%s: product %q group %q", w.code, spec.ProductCode, spec.Group)
		}
		if spec.Explainer == "" {
			t.Errorf("%s: tenants need a plain explanation of what falls in the category", w.code)
		}
		// Never bare Meta jargon in what a tenant reads.
		for _, text := range []string{spec.Label, spec.Name, spec.Explainer} {
			if strings.Contains(text, w.meta) || strings.Contains(strings.ToLower(text), "utility") {
				t.Errorf("%s: %q shows Meta jargon", w.code, text)
			}
		}
		if !strings.Contains(spec.Label, "WhatsApp") {
			t.Errorf("%s: label %q must say WhatsApp", w.code, spec.Label)
		}
		if len(spec.OfferedBundles()) == 0 {
			t.Errorf("%s: no bundles on sale", w.code)
		}
		for _, b := range spec.Bundles {
			if b.PriceKES != float64(b.Units)*w.price {
				t.Errorf("%s %s: price %v for %d units at KES %v", w.code, b.Code, b.PriceKES, b.Units, w.price)
			}
		}
	}
}

func TestLegacyWhatsAppCodeResolvesToUtility(t *testing.T) {
	spec, ok := LookupMeteredService(ServiceWhatsApp)
	if !ok || spec.Code != ServiceWhatsAppUtility {
		t.Fatalf("payments and sends still naming %q must land on utility credits, got %q", ServiceWhatsApp, spec.Code)
	}
	for _, code := range MeteredServiceCodes() {
		if code == ServiceWhatsApp {
			t.Fatal("the legacy code is an alias, never listed as a service of its own")
		}
	}
}

func TestWhatsAppTiersAreRetiredButStillResolve(t *testing.T) {
	for _, code := range []string{ServiceWhatsAppMarketing, ServiceWhatsAppUtility, ServiceWhatsAppAuthentication, ServiceWhatsAppService} {
		spec, _ := LookupMeteredService(code)
		if len(spec.OfferedTiers()) != 0 {
			t.Errorf("%s: WhatsApp monthly tiers are off sale", code)
		}
		// A tenant already on a paid tier keeps it to the end of its period, whatever it sends.
		for _, tc := range []string{"tier_1", "tier_2", "pro"} {
			tier, ok := spec.Tier(tc)
			if !ok || !tier.Retired {
				t.Errorf("%s: tier %s must still resolve, retired: %+v", code, tc, tier)
			}
		}
		if spec.QuotaTag != "whatsapp_quota" {
			t.Errorf("%s: a live legacy tier allowance is shared through whatsapp_quota", code)
		}
	}
	for _, code := range MeteredServiceCodes() {
		spec, _ := LookupMeteredService(code)
		for _, tier := range spec.OfferedTiers() {
			t.Errorf("%s still sells tier %s", code, tier.Code)
		}
	}
}

func TestWhatsAppServiceForMetaCategory(t *testing.T) {
	cases := map[string]string{
		"MARKETING":      ServiceWhatsAppMarketing,
		"marketing":      ServiceWhatsAppMarketing,
		"UTILITY":        ServiceWhatsAppUtility,
		"AUTHENTICATION": ServiceWhatsAppAuthentication,
		"SERVICE":        ServiceWhatsAppService,
	}
	for meta, want := range cases {
		if got, ok := WhatsAppServiceForMetaCategory(meta); !ok || got != want {
			t.Errorf("%s: got %q %v, want %q", meta, got, ok, want)
		}
	}
	if _, ok := WhatsAppServiceForMetaCategory("SOMETHING"); ok {
		t.Error("an unknown category must not resolve")
	}
}

func TestSMSStaysOneCategoryAtOneShilling(t *testing.T) {
	spec, ok := LookupMeteredService(ServiceSMS)
	if !ok || spec.UnitPriceKES != 1 || len(spec.Tiers) != 0 || spec.QuotaTag != "" || spec.Group != "" || spec.AllowsOverage() {
		t.Fatalf("SMS: %+v", spec)
	}
}
