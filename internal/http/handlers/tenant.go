package handlers

import (
	"net/http"

	"github.com/Bengo-Hub/httpware"
)

// resolveTenantID extracts the target tenant ID string from the request.
//
// Platform owners: X-Tenant-ID header / ?tenantId= param are OPTIONAL.
// When present they select which tenant to manage; when absent the request is
// treated as platform-wide and "" is returned.
//
// Regular tenants: MUST have a resolvable tenant ID. The function checks the
// X-Tenant-ID header first (apiClient always sends it), then falls back to
// the tenant ID embedded in the JWT claims.
func resolveTenantID(r *http.Request) string {
	ctx := r.Context()

	if httpware.IsPlatformOwner(ctx) {
		// Header override (subscriptions-ui apiClient injects X-Tenant-ID when a
		// tenant is selected in the platform admin top-nav filter)
		if h := r.Header.Get("X-Tenant-ID"); h != "" {
			return h
		}
		// Legacy query-param override
		if q := r.URL.Query().Get("tenantId"); q != "" {
			return q
		}
		return ""
	}

	// Regular tenant: check explicit header first, then JWT claims (both should
	// be the same — apiClient sends X-Tenant-ID from localStorage; JWT is authoritative)
	if h := r.Header.Get("X-Tenant-ID"); h != "" {
		return h
	}
	return httpware.GetTenantID(ctx)
}
