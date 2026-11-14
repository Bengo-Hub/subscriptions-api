package main

import (
	"slices"
	"testing"
)

// PowerSuite plans that carry the CRM block get the Autopilot features and limit of the matching
// MarketFlow tier (owner, 2026-11-14): Basic none, Professional as MarketFlow Growth (3 agents, no
// auto-publish), Gold as MarketFlow Business (unlimited agents, auto-publish). Never AI credits.
func TestPowerSuiteAutopilotMatchesMarketflowTier(t *testing.T) {
	type want struct {
		autopilot, autoPublish bool
		agents                 any // nil means no key
	}
	wants := map[int]want{
		1: {false, false, nil},
		2: {true, false, 3},
		3: {true, true, -1},
	}
	for _, fam := range useCaseFamilies {
		for tier, w := range wants {
			feats := fam.features(tier)
			if got := slices.Contains(feats, "marketflow_autopilot"); got != w.autopilot {
				t.Errorf("%s tier %d: marketflow_autopilot=%v, want %v", fam.key, tier, got, w.autopilot)
			}
			if got := slices.Contains(feats, "autopilot_auto_publish"); got != w.autoPublish {
				t.Errorf("%s tier %d: autopilot_auto_publish=%v, want %v", fam.key, tier, got, w.autoPublish)
			}
			limits := useCaseSuiteLimits(fam.key, tier)
			if got := limits["max_autopilot_agents"]; got != w.agents {
				t.Errorf("%s tier %d: max_autopilot_agents=%v, want %v", fam.key, tier, got, w.agents)
			}
			if got, ok := limits["ai_credits_monthly"]; ok {
				t.Errorf("%s tier %d: ai_credits_monthly=%v, PowerSuite never includes AI credits", fam.key, tier, got)
			}
		}
	}
}

// The mapping follows the MarketFlow ladder: a PowerSuite tier's Autopilot equals its MarketFlow
// tier's, so a change to marketflowPlanFeatures or marketflowAutopilotAgents carries over.
func TestPowerSuiteAutopilotFollowsMarketflowLadder(t *testing.T) {
	for psTier, mfTier := range psMarketflowTier {
		mf := marketflowPlanFeatures(mfTier)
		ps := psAutopilotFeatures(psTier)
		for _, code := range []string{"marketflow_autopilot", "autopilot_auto_publish"} {
			if slices.Contains(mf, code) != slices.Contains(ps, code) {
				t.Errorf("PowerSuite tier %d vs MarketFlow tier %d disagree on %s", psTier, mfTier, code)
			}
		}
		if got, want := psAutopilotLimits(psTier)["max_autopilot_agents"], marketflowPlanLimits(mfTier)["max_autopilot_agents"]; got != want {
			t.Errorf("PowerSuite tier %d: max_autopilot_agents=%v, MarketFlow tier %d has %v", psTier, got, mfTier, want)
		}
	}
	// ERP plans reuse powerSuiteLimits but carry no CRM block, so no Autopilot limit leaks there.
	for tier := 1; tier <= 3; tier++ {
		if _, ok := powerSuiteLimits(tier)["max_autopilot_agents"]; ok {
			t.Errorf("powerSuiteLimits(%d) carries max_autopilot_agents; it belongs to the use-case suites only", tier)
		}
	}
}
