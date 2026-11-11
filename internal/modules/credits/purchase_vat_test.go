package credits

import (
	"testing"

	"github.com/bengobox/subscription-service/internal/modules/billing"
)

func TestWithVAT(t *testing.T) {
	if total, vat := withVAT(750, 0); total != 750 || vat != 0 {
		t.Fatalf("no VAT: %v %v", total, vat)
	}
	if total, vat := withVAT(750, 16); total != 870 || vat != 120 {
		t.Fatalf("16%% VAT: %v %v", total, vat)
	}
	if total, vat := withVAT(1750, 16); total != 2030 || vat != 280 {
		t.Fatalf("16%% VAT: %v %v", total, vat)
	}
}

// The four AI bundles are on sale; the two retired ones are not offered but still resolve, so a
// payment started before 2026-11-11 is granted.
func TestAIBundles(t *testing.T) {
	spec, ok := billing.LookupMeteredService(billing.ServiceAI)
	if !ok {
		t.Fatal("ai_credits missing")
	}
	want := map[string]float64{"bundle_60": 750, "bundle_150": 1750, "bundle_400": 4400, "bundle_1000": 10000}
	offered := spec.OfferedBundles()
	if len(offered) != len(want) {
		t.Fatalf("offered: %+v", offered)
	}
	for _, b := range offered {
		if want[b.Code] != b.PriceKES {
			t.Fatalf("%s: KES %v", b.Code, b.PriceKES)
		}
	}
	o, err := ResolveOffer(billing.ServiceAI, KindBundle, "bundle_100")
	if err != nil || !o.Retired || o.PriceKES != 1000 {
		t.Fatalf("a retired bundle still resolves: %+v %v", o, err)
	}
}
