package main

import "testing"

// AI credits come from MarketFlow plans or purchased bundles only, so no PowerSuite tier, use-case
// family or ERP tier built on them may carry an allowance.
func TestPowerSuitePlansGrantNoAICredits(t *testing.T) {
	for tier := 1; tier <= 3; tier++ {
		if got, ok := powerSuiteLimits(tier)["ai_credits_monthly"]; ok {
			t.Fatalf("tier %d: ai_credits_monthly = %v, must not be set", tier, got)
		}
		for _, fam := range useCaseFamilies {
			if got, ok := useCaseSuiteLimits(fam.key, tier)["ai_credits_monthly"]; ok {
				t.Fatalf("%s tier %d: ai_credits_monthly = %v, must not be set", fam.key, tier, got)
			}
		}
	}
}
