package jobs

// exemptTenantSlug reports whether a tenant slug belongs to a subscription-exempt tenant
// (the demo sandbox or the platform owner). These tenants must never be invoiced, expired,
// or sent grace reminders. Kept in sync with subscriptions.exemptSlugs.
func exemptTenantSlug(slug string) bool {
	switch slug {
	case "codevertex", "codevertex-demo":
		return true
	default:
		return false
	}
}
