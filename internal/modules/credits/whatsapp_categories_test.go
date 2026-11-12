package credits

import (
	"testing"

	"github.com/bengobox/subscription-service/internal/modules/billing"
	"github.com/bengobox/subscription-service/internal/modules/subscriptions"
)

func TestResolveOfferPricesEachWhatsAppCategory(t *testing.T) {
	cases := []struct {
		service string
		units   int64
		price   float64
	}{
		{billing.ServiceWhatsAppMarketing, 100, 400},
		{billing.ServiceWhatsAppUtility, 100, 100},
		{billing.ServiceWhatsAppAuthentication, 100, 100},
		{billing.ServiceWhatsAppService, 100, 100},
	}
	for _, c := range cases {
		o, err := ResolveOffer(c.service, KindBundle, "bundle_100")
		if err != nil || o.Units != c.units || o.PriceKES != c.price || o.Service.Code != c.service {
			t.Errorf("%s: %+v %v", c.service, o, err)
		}
	}
	o, err := ResolveOffer(billing.ServiceWhatsAppMarketing, KindBundle, "bundle_1000")
	if err != nil || o.PriceKES != 4000 {
		t.Errorf("1000 promotion messages at KES 4: %+v %v", o, err)
	}
}

// A payment started for the old single WhatsApp bundle still settles, as utility credits.
func TestLegacyWhatsAppBundlePaymentSettlesAsUtility(t *testing.T) {
	o, err := ResolveOffer("whatsapp", KindBundle, "bundle_500")
	if err != nil || o.Service.Code != billing.ServiceWhatsAppUtility || o.Units != 500 || o.PriceKES != 500 {
		t.Fatalf("legacy bundle: %+v %v", o, err)
	}
	if o.Service.BundleTag != billing.ServiceWhatsAppUtility {
		t.Fatalf("units must land in the utility wallet, got %q", o.Service.BundleTag)
	}
}

// Tiers resolve (an in-flight tier payment still grants) but are marked retired so they are never sold.
func TestRetiredWhatsAppTierResolvesButIsNotForSale(t *testing.T) {
	o, err := ResolveOffer("whatsapp", KindTier, "tier_1")
	if err != nil || !o.Retired || o.Units != 400 || o.PriceKES != 500 {
		t.Fatalf("retired tier: %+v %v", o, err)
	}
	if _, err := AddonResolver()(subscriptions.AddonSelection{Service: "whatsapp", Kind: "tier", Code: "pro"}); err == nil {
		t.Fatal("a retired tier must not be sold as a checkout add-on")
	}
	if _, err := AddonResolver()(subscriptions.AddonSelection{Service: billing.ServiceWhatsAppMarketing, Kind: "bundle", Code: "bundle_100"}); err != nil {
		t.Fatalf("a category bundle is a valid add-on: %v", err)
	}
}

func TestCatalogListsWhatsAppCategoriesWithoutTiers(t *testing.T) {
	seen := map[string]CatalogEntry{}
	for _, e := range Catalog() {
		seen[e.Service] = e
		if len(e.Tiers) != 0 {
			t.Errorf("%s: no monthly tiers are sold any more", e.Service)
		}
	}
	if _, ok := seen["whatsapp"]; ok {
		t.Error("the legacy whatsapp code must not appear in the price list")
	}
	for _, code := range []string{billing.ServiceWhatsAppMarketing, billing.ServiceWhatsAppUtility, billing.ServiceWhatsAppAuthentication, billing.ServiceWhatsAppService} {
		e, ok := seen[code]
		if !ok {
			t.Errorf("%s missing from the catalog", code)
			continue
		}
		if e.Group != billing.GroupWhatsApp || e.Name == "" || e.Explainer == "" || e.MetaCategory == "" {
			t.Errorf("%s: the catalog must carry the plain name and explanation: %+v", code, e)
		}
	}
	if sms, ok := seen["sms"]; !ok || sms.UnitPriceKES != 1 || len(sms.Bundles) != 4 {
		t.Errorf("sms: %+v", sms)
	}
}

func TestAutoTopupKeyPerWhatsAppCategory(t *testing.T) {
	keys := map[string]bool{}
	for _, code := range []string{billing.ServiceWhatsAppMarketing, billing.ServiceWhatsAppUtility, billing.ServiceWhatsAppAuthentication, billing.ServiceWhatsAppService} {
		spec, _ := billing.LookupMeteredService(code)
		k := autoTopupKey(spec)
		if keys[k] {
			t.Errorf("%s shares auto top-up key %q with another category", code, k)
		}
		keys[k] = true
	}
	// Utility inherited the old whatsapp wallet, so it keeps the old setting.
	utility, _ := billing.LookupMeteredService(billing.ServiceWhatsAppUtility)
	if autoTopupKey(utility) != metaAutoTopup {
		t.Errorf("utility key %q", autoTopupKey(utility))
	}
	sms, _ := billing.LookupMeteredService(billing.ServiceSMS)
	if autoTopupKey(sms) != metaAutoTopup {
		t.Errorf("sms key %q", autoTopupKey(sms))
	}
}
