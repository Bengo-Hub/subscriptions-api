package main

import "testing"

// The one ai_credits ledger also pays for video generation, so no PowerSuite tier may grant an
// unlimited (-1) or zero AI allowance, and Gold is capped at 600 a month.
func TestPowerSuiteAICreditAllowancesAreCapped(t *testing.T) {
	want := map[int]int{1: 5, 2: 100, 3: 600}
	for tier, expected := range want {
		got, ok := powerSuiteLimits(tier)["ai_credits_monthly"].(int)
		if !ok {
			t.Fatalf("tier %d: ai_credits_monthly missing or not an int", tier)
		}
		if got != expected {
			t.Fatalf("tier %d: ai_credits_monthly = %d, want %d", tier, got, expected)
		}
	}
	for _, fam := range useCaseFamilies {
		for tier := 1; tier <= 3; tier++ {
			got, _ := useCaseSuiteLimits(fam.key, tier)["ai_credits_monthly"].(int)
			if got <= 0 {
				t.Fatalf("%s tier %d: ai_credits_monthly = %d, must be a positive cap", fam.key, tier, got)
			}
		}
	}
}
