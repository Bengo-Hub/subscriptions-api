package main

import (
	"os"
	"slices"
	"strings"
	"testing"
)

func TestTreasuryPlans(t *testing.T) {
	wantPrices := map[string]float64{"TREASURY_STARTER": 4000, "TREASURY_GROWTH": 8000, "TREASURY_PROFESSIONAL": 15000}
	for _, tier := range treasuryTiers {
		if tier.monthly != wantPrices[tier.code] {
			t.Fatalf("%s: price %.0f, want %.0f", tier.code, tier.monthly, wantPrices[tier.code])
		}
		feats := treasuryPlanFeatures(tier.tier)
		for _, f := range feats {
			if !isInCatalog(f) {
				t.Fatalf("%s: feature %q is not in the catalog", tier.code, f)
			}
		}
		for k := range treasuryPlanLimits(tier.tier) {
			if _, ok := limitServiceTag(k); !ok {
				t.Fatalf("%s: limit %q is not in the catalog", tier.code, k)
			}
		}
		// Escrow from Growth: Hadia's plan.
		if got, want := slices.Contains(feats, "escrow_management"), tier.tier >= 2; got != want {
			t.Fatalf("%s: escrow_management included=%v, want %v", tier.code, got, want)
		}
		if !slices.Contains(feats, "invoice_generation") || !slices.Contains(feats, "mpesa_integration") {
			t.Fatalf("%s: invoicing and M-Pesa collection must be in every tier", tier.code)
		}
	}
}

func TestTreasuryPlansSurviveTheRetirementMigration(t *testing.T) {
	// migrateUseCasePowerSuite hard-deletes retired families on every seed; TREASURY_* must not
	// be among them any more.
	src, err := os.ReadFile("migrate_usecase_powersuite.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(src), `strings.HasPrefix(code, "TREASURY_"),`) {
		t.Fatal("migrateUseCasePowerSuite still deletes TREASURY_* plans")
	}
}

// A treasury-only plan must not carry another product's feature: subscriptions-api adds every
// feature's service tag to the token's active_service_tags, so one ERP code would open ERP.
func TestTreasuryPlansCarryNoOtherProduct(t *testing.T) {
	tags := map[string]map[string]bool{}
	for _, e := range featureCatalog {
		if tags[e.code] == nil {
			tags[e.code] = map[string]bool{}
		}
		tags[e.code][e.service] = true
	}
	for _, tier := range treasuryTiers {
		for _, f := range treasuryPlanFeatures(tier.tier) {
			owners := tags[canonicalFeatureCode(f)]
			if !owners["treasury"] && !owners["platform"] && !owners[sharedServiceTag] {
				t.Errorf("%s: %q belongs to %v, not treasury or the platform", tier.code, f, owners)
			}
		}
	}
	if !slices.Contains(treasuryPlanFeatures(2), "treasury_budgets") {
		t.Error("TREASURY_GROWTH must include treasury_budgets")
	}
}
