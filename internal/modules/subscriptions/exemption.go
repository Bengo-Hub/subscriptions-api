package subscriptions

import (
	"context"
	"errors"

	enttenant "github.com/bengobox/subscription-service/internal/ent/tenant"
	"github.com/google/uuid"
)

// ErrExemptTenant is returned by lifecycle mutations when the target tenant is exempt
// from subscriptions (demo + platform-owner tenants). Handlers map it to a friendly
// no-op success so the demo/platform tenants never get a real subscription record.
var ErrExemptTenant = errors.New("tenant is exempt from subscriptions")

// exemptSlugs are tenants that bypass ALL subscription gating and must never own a
// subscription record: the demo sandbox and the platform owner. Kept in sync with the
// JWT-level exemption in auth-api (IsDemo / IsPlatformOwner) and the shared-auth-client
// IsGatingExempt funnel.
var exemptSlugs = map[string]bool{
	"codevertex":      true, // platform owner (operating tenant)
	"codevertex-demo": true, // demo sandbox — see project_demo_tenant memory
}

// IsExemptTenant reports whether the tenant bypasses subscriptions entirely. The local
// Tenant mirror only carries slug/status (auth-api owns metadata), so exemption is by
// slug or by the configured platform tenant id. Fail-closed to NOT exempt on lookup error
// so a transient DB issue never silently disables billing for a paying tenant.
func (s *Service) IsExemptTenant(ctx context.Context, tenantID uuid.UUID) bool {
	if s.platformTenantID != uuid.Nil && tenantID == s.platformTenantID {
		return true
	}
	t, err := s.client.Tenant.Query().
		Where(enttenant.IDEQ(tenantID)).
		Only(ctx)
	if err != nil {
		return false
	}
	return exemptSlugs[t.Slug]
}

// guardExempt returns ErrExemptTenant when the tenant must not own a subscription.
// Used by lifecycle mutations to block creating/changing real subscription records for
// demo/platform tenants.
func (s *Service) guardExempt(ctx context.Context, tenantID uuid.UUID) error {
	if s.IsExemptTenant(ctx, tenantID) {
		return ErrExemptTenant
	}
	return nil
}

// IsExemptErr reports whether err is the exempt-tenant sentinel, so handlers can branch
// on it without importing errors.
func IsExemptErr(err error) bool { return errors.Is(err, ErrExemptTenant) }
