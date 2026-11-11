package main

import (
	"os"
	"slices"
	"strings"
	"testing"
)

// Five standalone MarketFlow tiers (owner, 2026-11-11): every feature and limit is in the catalog,
// the AI Studio outputs widen by tier, no plan includes AI credits, and the free chat turns rise.
func TestMarketflowPlans(t *testing.T) {
	wantPrices := map[string]float64{"MARKETFLOW_STARTER": 0, "MARKETFLOW_LITE": 1500, "MARKETFLOW_GROWTH": 3500, "MARKETFLOW_PRO": 8500, "MARKETFLOW_BUSINESS": 15000}
	if len(marketflowTiers) != len(wantPrices) {
		t.Fatalf("want %d tiers, got %d", len(wantPrices), len(marketflowTiers))
	}
	prevChat := -1
	for _, tier := range marketflowTiers {
		if p, ok := wantPrices[tier.code]; !ok || tier.monthly != p {
			t.Fatalf("%s: price %.0f, want %.0f", tier.code, tier.monthly, p)
		}
		feats := marketflowPlanFeatures(tier.tier)
		for _, f := range feats {
			if !isInCatalog(f) {
				t.Fatalf("%s: feature %q is not in the catalog", tier.code, f)
			}
		}
		limits := marketflowPlanLimits(tier.tier)
		for k := range limits {
			if _, ok := limitServiceTag(k); !ok {
				t.Fatalf("%s: limit %q is not in the catalog", tier.code, k)
			}
		}
		if _, ok := limits["ai_credits_monthly"]; ok {
			t.Fatalf("%s: no plan includes AI credits (owner, 2026-11-11)", tier.code)
		}
		chat := limits["chat_turns_daily"].(int)
		if chat <= prevChat {
			t.Fatalf("%s: free chat turns %d must rise with the tier", tier.code, chat)
		}
		prevChat = chat
		for k, v := range limits {
			if v.(int) == 0 {
				t.Fatalf("%s: limit %s is 0, which several services read as unlimited", tier.code, k)
			}
		}
		if got, want := slices.Contains(feats, "marketflow_custom_domain"), tier.tier >= 4; got != want {
			t.Fatalf("%s: custom domain included=%v, want %v", tier.code, got, want)
		}
		if !slices.Contains(feats, "ai_static_ads") || !slices.Contains(feats, "profile_pages") || (tier.tier == 1 && slices.Contains(feats, "basic_campaigns")) {
			t.Fatalf("%s: posters and the profile page are in every tier", tier.code)
		}
		if got, want := slices.Contains(feats, "ai_video_presenter"), tier.tier >= 2; got != want {
			t.Fatalf("%s: presenter videos included=%v, want %v", tier.code, got, want)
		}
		if got, want := slices.Contains(feats, "ai_video_presenter_premium"), tier.tier >= 4; got != want {
			t.Fatalf("%s: premium presenter included=%v, want %v", tier.code, got, want)
		}
	}
}

func TestMarketflowPlansSurviveTheRetirementMigration(t *testing.T) {
	src, err := os.ReadFile("migrate_usecase_powersuite.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(src), `strings.HasPrefix(code, "MARKETFLOW_")`) {
		t.Fatal("migrateUseCasePowerSuite still deletes every MARKETFLOW_* plan")
	}
	for _, tier := range marketflowTiers {
		if retiredMarketflowCodes[tier.code] {
			t.Fatalf("%s is seeded and retired at once", tier.code)
		}
	}
}

// A MarketFlow-only plan must not carry another product's feature: every feature's service tag
// goes into the token's active_service_tags, so one POS code would open POS.
func TestMarketflowPlansCarryNoOtherProduct(t *testing.T) {
	tags := map[string]map[string]bool{}
	for _, e := range featureCatalog {
		if tags[e.code] == nil {
			tags[e.code] = map[string]bool{}
		}
		tags[e.code][e.service] = true
	}
	for _, tier := range marketflowTiers {
		for _, f := range marketflowPlanFeatures(tier.tier) {
			owners := tags[canonicalFeatureCode(f)]
			if !owners["marketflow"] && !owners["platform"] && !owners[sharedServiceTag] {
				t.Errorf("%s: %q belongs to %v, not marketflow or the platform", tier.code, f, owners)
			}
		}
	}
}
