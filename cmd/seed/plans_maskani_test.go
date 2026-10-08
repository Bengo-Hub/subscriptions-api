package main

import "testing"

func TestMaskaniPlans(t *testing.T) {
	catalog := map[string]bool{}
	for _, e := range featureCatalog {
		catalog[e.code] = true
	}
	var prev map[string]bool
	for _, tier := range maskaniTiers {
		feats := maskaniTierFeatures(tier.tier)
		set := map[string]bool{}
		for _, f := range feats {
			if !catalog[f] {
				t.Errorf("%s: feature %q is not in the catalog", tier.code, f)
			}
			set[f] = true
		}
		// shared-ui-lib's tier-aware unlocking needs every tier to contain the tier below.
		for f := range prev {
			if !set[f] {
				t.Errorf("%s: drops %q from the tier below", tier.code, f)
			}
		}
		prev = set
		for _, must := range []string{"maskani_billing", "maskani_gate", "invoice_generation", "payroll", "mpesa_integration"} {
			if !set[must] {
				t.Errorf("%s: missing %q", tier.code, must)
			}
		}
	}

	// SRDD 25.3: units, ERP staff and staff users per tier; Enterprise unlimited.
	want := map[int][3]int{1: {150, 25, 10}, 2: {400, 75, 30}, 3: {1000, 200, 100}, 4: {-1, -1, -1}}
	for tier, w := range want {
		l := maskaniTierLimits(tier)
		if l["max_units"] != w[0] || l["max_employees"] != w[1] || l["max_users"] != w[2] {
			t.Errorf("tier %d limits = %v, want units %d, employees %d, users %d", tier, l, w[0], w[1], w[2])
		}
	}

	growth := map[string]bool{}
	for _, f := range maskaniTierFeatures(2) {
		growth[f] = true
	}
	starter := map[string]bool{}
	for _, f := range maskaniTierFeatures(1) {
		starter[f] = true
	}
	for _, f := range []string{"budgeting", "approval_workflows", "bi_reports", "asset_management"} {
		if starter[f] || !growth[f] {
			t.Errorf("%q should start at Growth", f)
		}
	}
}
