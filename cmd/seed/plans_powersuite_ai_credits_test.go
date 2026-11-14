package main

import "testing"

// AI credits come from MarketFlow plans or purchased bundles only, so no PowerSuite tier, use-case
// family or ERP tier built on them may carry an allowance (owner, 2026-11-14: PowerSuite tenants
// buy AI credits like SMS and WhatsApp credits).
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

// Free MarketFlow chat turns on PowerSuite (owner, 2026-11-14): Basic none (no CRM block), Pro the
// same as MarketFlow Growth (60), Gold the same as MarketFlow Business (300).
func TestPowerSuiteChatTurnsPerTier(t *testing.T) {
	want := map[int]int{1: 0, 2: 60, 3: 300}
	for tier, turns := range want {
		for _, fam := range useCaseFamilies {
			got, ok := useCaseSuiteLimits(fam.key, tier)["chat_turns_daily"]
			if turns == 0 {
				if ok {
					t.Fatalf("%s tier %d: chat_turns_daily = %v, Basic gets none (never a 0 limit)", fam.key, tier, got)
				}
				continue
			}
			if n, _ := got.(int); !ok || n != turns {
				t.Fatalf("%s tier %d: chat_turns_daily = %v, want %d", fam.key, tier, got, turns)
			}
		}
	}
	if powerSuiteChatTurnsDaily(2) != marketflowPlanLimits(3)["chat_turns_daily"] ||
		powerSuiteChatTurnsDaily(3) != marketflowPlanLimits(5)["chat_turns_daily"] {
		t.Fatal("PowerSuite Pro and Gold chat turns must follow MarketFlow Growth and Business")
	}
}
