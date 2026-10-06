package handlers

import "testing"

// Badges show the tier as people say it, never another family's full plan name.
func TestShortTierLabel(t *testing.T) {
	cases := map[string]string{
		"POWERSUITE_DUKA_BASIC":         "Basic",
		"POWERSUITE_DAWA_PROFESSIONAL":  "Pro",
		"POWERSUITE_HOSP_PRO":           "Pro",
		"POWERSUITE_DUKA_GOLD_ONE_TIME": "Gold",
		"ERP_STARTER":                   "Starter",
		"LIBRARY":                       "Library",
	}
	for code, want := range cases {
		if got := shortTierLabel(code); got != want {
			t.Errorf("shortTierLabel(%q) = %q, want %q", code, got, want)
		}
	}
}
