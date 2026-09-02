package subscriptions

import (
	"context"
	"errors"
	"time"

	entfeaturedefinition "github.com/bengobox/subscription-service/internal/ent/featuredefinition"
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
	// Exempt if: the tenant slug is a built-in platform/demo slug, OR the platform explicitly
	// flagged this tenant subscription-exempt (configurable per-tenant grant).
	return t.SubscriptionExempt || exemptSlugs[t.Slug]
}

// SetTenantExemption flips a tenant's explicit subscription-exemption flag. Platform-admin only
// (the caller enforces authz). Returns the new value.
func (s *Service) SetTenantExemption(ctx context.Context, tenantID uuid.UUID, exempt bool) error {
	return s.client.Tenant.UpdateOneID(tenantID).SetSubscriptionExempt(exempt).Exec(ctx)
}

// exemptResult builds the synthetic, fully-entitled result returned for exempt tenants:
// status ACTIVE, every catalog feature, no limit caps, Exempt=true.
func (s *Service) exemptResult(ctx context.Context, tenantID uuid.UUID) *SubscriptionResult {
	now := time.Now()
	features := s.allFeatureCodes(ctx)
	return &SubscriptionResult{
		TenantID: tenantID,
		PlanCode: "EXEMPT",
		PlanName: "Platform Exempt",
		// FacilityType is deliberately left empty (2026-09-02 fix — was hardcoded "hospital").
		// Exemption is a LICENSING concept (bypass every feature/limit gate, enforced above via
		// Features/Limits/Exempt on this same struct) — facility_type is a PRESENTATION concept
		// (which facility tier's nav a consuming frontend like hospital-ui shows), and conflating
		// them meant an exempt tenant could never see its own real facility tier, always
		// collapsing to the richest one. guardExempt() means an exempt tenant structurally can
		// never own a real TenantSubscription row to read a real facility_type from anyway, so
		// there is nothing correct to assert here; consuming frontends already treat an empty/
		// unresolved facility_type as "show the full default view," so this is not a behavior
		// change for any tenant today — see hospital-ui's facility-nomenclature.ts.
		FacilityType:       "",
		Status:             "ACTIVE",
		AccessStatus:       "active",
		CurrentPeriodStart: now,
		CurrentPeriodEnd:   now.AddDate(100, 0, 0),
		Features:           features,
		Limits:             map[string]int{},
		BillingMode:        "exempt",
		IsPerpetual:        true,
		Exempt:             true,
	}
}

// allFeatureCodes returns every boolean FEATURE code in the catalog so an exempt tenant's token
// carries the full entitlement set (belt-and-braces for any service that reads features directly
// rather than going through the auth-client gating-exempt funnel).
func (s *Service) allFeatureCodes(ctx context.Context) []string {
	rows, err := s.client.FeatureDefinition.Query().
		Where(entfeaturedefinition.KindEQ(entfeaturedefinition.KindFEATURE)).
		Select(entfeaturedefinition.FieldFeatureCode).
		All(ctx)
	if err != nil {
		return []string{}
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.FeatureCode)
	}
	return out
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
