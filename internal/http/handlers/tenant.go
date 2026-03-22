package handlers

import (
	"net/http"

	"github.com/Bengo-Hub/httpware"
	"github.com/google/uuid"
)

// resolveTenantID extracts the target tenant ID string from the request,
// applying the platform-owner ?tenantId= query param override.
// Returns the tenant ID string (may be empty for platform owners requesting "all").
func resolveTenantID(r *http.Request) string {
	ctx := r.Context()
	tenantIDStr := httpware.GetTenantID(ctx)

	// Platform owners can override via query param
	if httpware.IsPlatformOwner(ctx) {
		if q := r.URL.Query().Get("tenantId"); q != "" {
			return q
		}
	}

	return tenantIDStr
}

// resolveTenantUUID is a convenience wrapper that parses the resolved tenant
// string into a UUID. Returns (uuid.Nil, false) if resolution or parsing fails.
func resolveTenantUUID(r *http.Request) (uuid.UUID, bool) {
	s := resolveTenantID(r)
	if s == "" {
		return uuid.Nil, false
	}
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil, false
	}
	return id, true
}
