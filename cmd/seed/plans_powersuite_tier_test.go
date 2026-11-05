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

// suiteServices works out the services a feature list opens the way the live resolver does: each
// code's catalog tag, leaving out the shared tag, plus the plan's own "pos" tag.
func suiteServices(feats []string) map[string]bool {
	tags := map[string]string{}
	for _, e := range featureCatalog {
		tags[e.code] = e.service
	}
	out := map[string]bool{"pos": true}
	for _, code := range append(feats, serviceIntegrationFeatures["pos"]...) {
		if tag := tags[code]; tag != "" && tag != sharedServiceTag {
			out[tag] = true
		}
	}
	return out
}

// What each PowerSuite tier opens, worked out from the real catalog. Tier 1 is POS, Inventory and
// Books and nothing else, in all three families.
func TestPowerSuiteTierServices(t *testing.T) {
	want := map[int][]string{
		1: {"pos", "inventory", "treasury"},
		2: {"pos", "inventory", "treasury", "ordering", "logistics", "marketflow"},
		3: {"pos", "inventory", "treasury", "ordering", "logistics", "marketflow", "erp", "projects"},
	}
	for _, fam := range useCaseFamilies {
		for tier, services := range want {
			got := suiteServices(fam.features(tier))
			wanted := map[string]bool{}
			for _, s := range services {
				wanted[s] = true
				if !got[s] {
					t.Errorf("%s tier %d does not open %s", fam.key, tier, s)
				}
			}
			for s := range got {
				if !wanted[s] {
					t.Errorf("%s tier %d must not open %s", fam.key, tier, s)
				}
			}
		}
	}
}

// Every tier keeps everything the tier below has, which the tier-aware unlock logic relies on.
func TestPowerSuiteTiersAreSupersets(t *testing.T) {
	for _, fam := range useCaseFamilies {
		for tier := 2; tier <= 3; tier++ {
			higher := fam.features(tier)
			for _, code := range fam.features(tier - 1) {
				if !contains(higher, code) {
					t.Errorf("%s tier %d lost %s from the tier below", fam.key, tier, code)
				}
			}
		}
	}
}

func TestPowerSuiteTier1DropsPaidModules(t *testing.T) {
	for _, fam := range useCaseFamilies {
		feats := fam.features(1)
		for _, code := range []string{"online_ordering", "loyalty_program", "paystack_integration", "rider_management", "contact_management", "basic_logistics_access"} {
			if contains(feats, code) {
				t.Errorf("%s tier 1 must not include %s", fam.key, code)
			}
		}
		limits := useCaseSuiteLimits(fam.key, 1)
		for _, key := range []string{"max_riders", "max_contacts", "max_campaigns"} {
			if _, ok := limits[key]; ok {
				t.Errorf("%s tier 1 must not carry the %s limit", fam.key, key)
			}
		}
		if _, ok := limits["inventory_max_sku"]; !ok {
			t.Errorf("%s tier 1 lost its product limit", fam.key)
		}
	}
}

// Treasury refuses Paystack without the code, so every plan keeps it except tier 1 PowerSuite, and
// holding it opens no service.
func TestPaystackOnEveryPlanExceptPowerSuiteTier1(t *testing.T) {
	pos, library := "pos", "library"
	for _, code := range []string{"POWERSUITE_DUKA_BASIC", "POWERSUITE_HOSP_BASIC_ONE_TIME", "POWERSUITE_DAWA_BASIC"} {
		if contains(planIntegrationFeatures(code, &pos), paystackFeature) {
			t.Errorf("%s must not get Paystack", code)
		}
	}
	if !contains(planIntegrationFeatures("POWERSUITE_DUKA_PRO", &pos), paystackFeature) ||
		!contains(planIntegrationFeatures("LIBRARY_STARTER", &library), paystackFeature) ||
		!contains(planIntegrationFeatures("BUNDLE_X", nil), paystackFeature) {
		t.Error("other plans must keep Paystack")
	}
	for _, e := range featureCatalog {
		if e.code == paystackFeature && e.service != sharedServiceTag {
			t.Errorf("%s is tagged %s; it would open that service on every plan", e.code, e.service)
		}
	}
}

// One entry per code, and a code two services list grants neither.
func TestCatalogHasOneEntryPerCode(t *testing.T) {
	seen := map[string]bool{}
	for _, e := range featureCatalog {
		if seen[e.code] {
			t.Errorf("%s is listed twice", e.code)
		}
		seen[e.code] = true
		if e.code == "customer_management" && e.service != sharedServiceTag {
			t.Errorf("customer_management is tagged %s, the bug that showed ISP billing on every plan", e.service)
		}
	}
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
