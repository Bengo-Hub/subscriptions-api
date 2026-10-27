package subscriptions

import "strings"

// PlanFamily mirrors shared-ui-lib's feature-gate.tsx planFamily() exactly: a plan code's
// family, upper-cased. PowerSuite plans return their first TWO segments (POWERSUITE_DUKA_GOLD
// -> "POWERSUITE_DUKA") because the PowerSuite line encodes a further disjoint use-case
// sub-family (Hospitality/Retail/Pharmacy) as its second segment; every other product line
// (ERP_*, LIBRARY_*, TRULOAD_*, ...) has no such split, so the first segment is correct there.
// Upgrade suggestions and upgrade trials only move a tenant up its own family.
func PlanFamily(code string) string {
	upper := strings.ToUpper(code)
	if strings.HasPrefix(upper, "POWERSUITE_") {
		parts := strings.SplitN(upper, "_", 3)
		if len(parts) >= 2 {
			return parts[0] + "_" + parts[1]
		}
		return upper
	}
	if i := strings.Index(upper, "_"); i != -1 {
		return upper[:i]
	}
	return upper
}
