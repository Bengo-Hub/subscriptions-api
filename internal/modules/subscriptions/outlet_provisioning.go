package subscriptions

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// powerSuiteTierCodes maps a plan's numeric tier_order to its PowerSuite plan-code segment.
// Mirrors subscriptions-api's own cmd/seed/plans_powersuite_usecase.go useCaseTierCodes.
var powerSuiteTierCodes = map[int]string{1: "BASIC", 2: "PRO", 3: "GOLD"}

// useCaseToPowerSuiteFamily maps an outlet's use_case (auth-api's usecase.KnownUseCases) to the
// PowerSuite plan-family segment it needs. Mirrors pos-api's own use-case→feature-group gates
// (RequireUseCase("hospitality","quick_service") for KDS, RequireUseCase("retail","services") for
// commissions, etc. — see pos-service/pos-api/internal/http/router/router.go). Use cases with no
// PowerSuite family (warehouse, logistics, isp, hospital, ...) return "" — they have their own
// separate plan lines (TruLoad/ISP-billing/Codevertex Afya/...), not PowerSuite, so
// MaybeProvisionUseCaseFamily is a deliberate no-op for them.
func useCaseToPowerSuiteFamily(useCase string) string {
	switch useCase {
	case "hospitality", "quick_service":
		return "HOSP"
	case "retail", "services":
		return "DUKA"
	case "pharmacy":
		return "DAWA"
	default:
		return ""
	}
}

// MaybeProvisionUseCaseFamily auto-grants the PowerSuite family a newly-created outlet's use_case
// needs, the moment auth-api (the platform's SSO/tenant-identity service) creates it — see plan
// D:\Projects\Codevertex\.claude\plans\boi-multi-use-case-subscription-and-hospitality-audit-2026-08-18.md.
// This is the automatic counterpart of the one-off manual grant boi-enterprises needed by hand:
// a tenant whose Guest House outlet (use_case "hospitality") sat on a Retail-only plan had to be
// fixed via a platform-admin API call; new tenants adding a second-use-case outlet should never
// need that manual step again.
//
// Deliberately CONSERVATIVE — this only acts when the tenant ALREADY holds at least one active
// PowerSuite plan (their main subscription or an existing product-subscription overlay). A tenant
// with zero PowerSuite presence at all (e.g. a pure TruLoad/ISP-billing/Codevertex-Afya customer)
// adding a POS-family outlet does NOT get a free top-tier PowerSuite grant — extending a *new*
// product line to a tenant remains a deliberate sales/billing decision, never an automatic one.
// When the tenant DOES already have PowerSuite coverage, the new family is granted at the SAME
// tier + billing style (ONE_TIME vs recurring) as their highest existing PowerSuite plan — exactly
// mirroring what was done by hand for boi-enterprises (Duka Gold One-Time → also granted Hosp Gold
// One-Time). No-op, never an error the caller need act on, for: exempt tenants, use_cases with no
// PowerSuite family, tenants with no PowerSuite plan yet, tenants already covered for the target
// family, an unrecognized tier, or a missing/inactive target plan row — the caller (the
// auth.outlet.created consumer) Naks only on a genuine transient failure so JetStream redelivers.
func (s *Service) MaybeProvisionUseCaseFamily(ctx context.Context, tenantID uuid.UUID, useCase string) error {
	family := useCaseToPowerSuiteFamily(useCase)
	if family == "" {
		return nil
	}
	if s.IsExemptTenant(ctx, tenantID) {
		return nil
	}

	sub, err := s.getSubscription(ctx, tenantID)
	if err != nil {
		// No subscription yet (brand-new tenant whose first outlet event raced its subscription
		// provisioning) or a lookup failure — nothing to overlay onto; fail open like every other
		// subscription lookup on this platform (see [[subscription-gate-fail-open]]).
		return nil
	}

	mainPlan, err := s.client.SubscriptionPlan.Get(ctx, sub.PlanID)
	if err != nil {
		return nil //nolint:nilerr // fail open on a lookup failure, don't retry-storm the consumer
	}

	type psPlan struct {
		code      string
		tierOrder int
	}
	var psPlans []psPlan
	if strings.HasPrefix(mainPlan.PlanCode, "POWERSUITE_") {
		psPlans = append(psPlans, psPlan{mainPlan.PlanCode, mainPlan.TierOrder})
	}

	productPlans, perr := s.resolveProductPlans(ctx, sub.ID)
	if perr != nil {
		s.log.Warn("provision use-case family: failed to load product plans",
			zap.String("tenant_id", tenantID.String()), zap.Error(perr))
	}
	for _, pp := range productPlans {
		if strings.HasPrefix(pp.PlanCode, "POWERSUITE_") {
			psPlans = append(psPlans, psPlan{pp.PlanCode, pp.TierOrder})
		}
	}

	if len(psPlans) == 0 {
		s.log.Info("skipping use-case family auto-provision: tenant has no PowerSuite plan yet",
			zap.String("tenant_id", tenantID.String()), zap.String("use_case", useCase))
		return nil
	}

	// Already covered by an existing PowerSuite plan of this family (main or overlay)?
	prefix := "POWERSUITE_" + family + "_"
	best := psPlans[0]
	for _, p := range psPlans {
		if strings.HasPrefix(p.code, prefix) {
			return nil
		}
		if p.tierOrder > best.tierOrder {
			best = p
		}
	}

	tierCode, ok := powerSuiteTierCodes[best.tierOrder]
	if !ok {
		s.log.Warn("skipping use-case family auto-provision: unrecognized tier order on reference plan",
			zap.String("tenant_id", tenantID.String()), zap.String("reference_plan", best.code), zap.Int("tier_order", best.tierOrder))
		return nil
	}
	targetCode := prefix + tierCode
	if strings.HasSuffix(best.code, "_ONE_TIME") {
		targetCode += "_ONE_TIME"
	}

	// Reuses the exact same code path (transaction + tenant.subscription.updated outbox event +
	// cache invalidation via the caller) the platform-admin AssignProductToTenant endpoint uses —
	// "pos" is the product_code every tenant already has an active ProductSubscription row for
	// (see product_activation.go's DefaultActivatedProducts), so this always UPDATEs that existing
	// row's override_plan_id rather than inserting a second "pos" row (product_code is unique per
	// tenant_subscription).
	if err := s.AssignProductPlan(ctx, tenantID, "pos", targetCode); err != nil {
		return fmt.Errorf("assign %s overlay for tenant %s: %w", targetCode, tenantID, err)
	}
	s.log.Info("auto-provisioned PowerSuite family overlay for a new-use-case outlet",
		zap.String("tenant_id", tenantID.String()),
		zap.String("use_case", useCase),
		zap.String("reference_plan", best.code),
		zap.String("granted_plan", targetCode),
	)
	return nil
}
