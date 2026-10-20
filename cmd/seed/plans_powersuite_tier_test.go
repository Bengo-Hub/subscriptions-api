package main

import "testing"

func contains(list []string, code string) bool {
	for _, c := range list {
		if c == code {
			return true
		}
	}
	return false
}

// PowerSuite plans open ERP and Projects only at tier 3; the standalone ERP family keeps its own
// tiers.
func TestPowerSuiteErpAndProjectsStartAtTier3(t *testing.T) {
	erpCodes := []string{"hr_management", "leave_management", "attendance", "project_management", "task_tracking"}
	suites := map[string]func(int) []string{
		"hospitality": hospSuiteFeatures,
		"retail":      dukaSuiteFeatures,
		"pharmacy":    dawaSuiteFeatures,
	}
	for name, suite := range suites {
		for _, tier := range []int{1, 2} {
			feats := suite(tier)
			for _, code := range erpCodes {
				if contains(feats, code) {
					t.Errorf("%s tier %d must not include %s", name, tier, code)
				}
			}
		}
		feats := suite(3)
		for _, code := range []string{"hr_management", "payroll", "project_management", "gantt_chart"} {
			if !contains(feats, code) {
				t.Errorf("%s tier 3 must include %s", name, code)
			}
		}
	}
	if !contains(erpFeatures(2), "project_management") {
		t.Error("standalone ERP tier 2 keeps projects")
	}
}
