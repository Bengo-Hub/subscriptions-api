package subscriptions

import (
	"context"
	"fmt"
	"time"

	"github.com/bengobox/subscription-service/internal/ent"
	entfeaturedefinition "github.com/bengobox/subscription-service/internal/ent/featuredefinition"
	"github.com/bengobox/subscription-service/internal/ent/planfeature"
	"github.com/bengobox/subscription-service/internal/ent/productsubscription"
	"github.com/bengobox/subscription-service/internal/ent/subscriptionplan"
	"github.com/bengobox/subscription-service/internal/ent/tenantsubscription"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// Composite entitlement resolution.
//
// A tenant has ONE main TenantSubscription (its primary plan) plus zero or more
// ProductSubscriptions for additional use cases (e.g. a supermarket on POS + a weighbridge
// on TruLoad under the same tenant). The effective entitlement is the COMPOSITE of all of
// them: the UNION of feature codes and the MAX of each tier limit. A product subscription
// only contributes when it carries an override_plan_id (there is no Product→plan link), so
// admin assignment sets that field when adding a product line.

// activeProductStatuses are the product-subscription statuses that grant entitlements.
var activeProductStatuses = []productsubscription.Status{
	productsubscription.StatusActive,
	productsubscription.StatusTrial,
}

// loadPlanWithFeatures loads a plan with only its included features.
func (s *Service) loadPlanWithFeatures(ctx context.Context, planID uuid.UUID) (*ent.SubscriptionPlan, error) {
	return s.client.SubscriptionPlan.Query().
		Where(subscriptionplan.IDEQ(planID)).
		WithFeatures(func(q *ent.PlanFeatureQuery) {
			q.Where(planfeature.IsIncludedEQ(true))
		}).
		Only(ctx)
}

// resolveProductPlans returns the override plans (with included features) of a tenant
// subscription's active/trial product subscriptions. Bare product subs (no override plan)
// contribute nothing and are skipped.
func (s *Service) resolveProductPlans(ctx context.Context, tenantSubID uuid.UUID) ([]*ent.SubscriptionPlan, error) {
	prods, err := s.client.ProductSubscription.Query().
		Where(
			productsubscription.TenantSubscriptionIDEQ(tenantSubID),
			productsubscription.StatusIn(activeProductStatuses...),
			productsubscription.OverridePlanIDNotNil(),
		).
		All(ctx)
	if err != nil {
		return nil, err
	}
	if len(prods) == 0 {
		return nil, nil
	}
	ids := make([]uuid.UUID, 0, len(prods))
	for _, p := range prods {
		if p.OverridePlanID != nil {
			ids = append(ids, *p.OverridePlanID)
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	return s.client.SubscriptionPlan.Query().
		Where(subscriptionplan.IDIn(ids...)).
		WithFeatures(func(q *ent.PlanFeatureQuery) {
			q.Where(planfeature.IsIncludedEQ(true))
		}).
		All(ctx)
}

// overlayPlanEntitlements merges a plan's features (union) and tier limits (max, with -1
// "unlimited" always winning) into an already-built result.
func overlayPlanEntitlements(result *SubscriptionResult, plan *ent.SubscriptionPlan) {
	if plan == nil {
		return
	}
	seen := make(map[string]bool, len(result.Features))
	for _, f := range result.Features {
		seen[f] = true
	}
	for _, f := range plan.Edges.Features {
		if !seen[f.FeatureCode] {
			result.Features = append(result.Features, f.FeatureCode)
			seen[f.FeatureCode] = true
		}
	}
	for k, v := range plan.TierLimitsJSON {
		iv, ok := limitValue(v)
		if !ok {
			continue
		}
		if cur, ok := result.Limits[k]; ok {
			result.Limits[k] = maxLimit(cur, iv)
		} else {
			result.Limits[k] = iv
		}
	}
}

// addFeatures appends the codes result does not hold yet (union).
func addFeatures(result *SubscriptionResult, codes []string) {
	seen := make(map[string]bool, len(result.Features))
	for _, f := range result.Features {
		seen[f] = true
	}
	for _, code := range codes {
		if !seen[code] {
			result.Features = append(result.Features, code)
			seen[code] = true
		}
	}
}

func planFeatureCodes(plan *ent.SubscriptionPlan) []string {
	out := make([]string, 0, len(plan.Edges.Features))
	for _, f := range plan.Edges.Features {
		out = append(out, f.FeatureCode)
	}
	return out
}

// attachSchedule reports a running upgrade trial and a waiting plan change on result.
func (s *Service) attachSchedule(result *SubscriptionResult, sub *ent.TenantSubscription, now time.Time) {
	if t, ok := ActiveUpgradeTrial(sub, now); ok {
		result.UpgradeTrial = t.info(now)
	}
	if pc, ok := PendingPlanChange(sub); ok {
		result.ScheduledChange = pc.info()
	}
}

// GetSubscriptionResult returns the COMPOSITE entitlement view for a tenant: the union of
// the main plan plus every active per-product subscription's override plan. This is the
// single resolution point used by both the UI (GET /subscription) and auth JWT enrichment
// (GET /tenants/{id}/subscription), so a multi-use-case tenant's token carries all of its
// features at once.
func (s *Service) GetSubscriptionResult(ctx context.Context, tenantID uuid.UUID) (*SubscriptionResult, error) {
	// Exempt tenants never own a subscription record. Return a synthetic, fully-entitled result
	// (status ACTIVE, every feature, no limits) marked Exempt so auth-api stamps the sub_exempt
	// JWT claim and every downstream gate bypasses cleanly.
	if s.IsExemptTenant(ctx, tenantID) {
		return s.exemptResult(ctx, tenantID), nil
	}

	sub, err := s.getSubscription(ctx, tenantID)
	if err != nil {
		return nil, err
	}

	// The plan in force is the paid plan, or an upgrade trial's plan while one runs (see
	// PlanIDInForce). Features, limits, tier and plan code come from it so tokens and gates see
	// the trial tier; the paid plan's features stay in the union so a trial never removes one.
	now := time.Now().UTC()
	paid, plan, err := s.loadPlanInForce(ctx, sub, now)
	if err != nil {
		return nil, err
	}

	result := s.buildResult(sub, plan)
	if plan != paid {
		addFeatures(result, planFeatureCodes(paid))
	}
	s.attachSchedule(result, sub, now)

	// Overlay active product subscriptions' plans (composite multi-use-case entitlements).
	productPlans, perr := s.resolveProductPlans(ctx, sub.ID)
	if perr != nil {
		s.log.Warn("composite entitlements: failed to load product plans",
			zap.String("tenant_id", tenantID.String()), zap.Error(perr))
	}
	for _, pp := range productPlans {
		overlayPlanEntitlements(result, pp)
	}

	if codes, cerr := s.activeProductCodesForSub(ctx, sub.ID); cerr != nil {
		s.log.Warn("composite entitlements: failed to load active product codes",
			zap.String("tenant_id", tenantID.String()), zap.Error(cerr))
	} else {
		result.ActiveProducts = codes
	}

	// Overlay platform-admin single-feature grants (add-ons like per-branch pricing / flash
	// sale that must NOT be bundled into a whole plan overlay -- see TenantFeatureGrant's
	// schema doc). Union only, same as plan/product-overlay features above.
	if grantCodes, gerr := s.activeTenantFeatureGrantCodes(ctx, tenantID); gerr != nil {
		s.log.Warn("composite entitlements: failed to load tenant feature grants",
			zap.String("tenant_id", tenantID.String()), zap.Error(gerr))
	} else if len(grantCodes) > 0 {
		addFeatures(result, grantCodes)
	}

	if tags, terr := s.resolveActiveServiceTags(ctx, result.Features, plan); terr != nil {
		s.log.Warn("composite entitlements: failed to resolve active service tags",
			zap.String("tenant_id", tenantID.String()), zap.Error(terr))
	} else {
		result.ActiveServiceTags = tags
	}

	// Support-fee obligation (perpetual/one-time-license tenants only — see SupportFeeCycle).
	// Fail-open like every other lookup here: a query failure must never break login/entitlement
	// resolution, it just means this token mints without the support-fee claim (same "absent =
	// pass" convention RequireSupportFeeCurrentForMutations already honors).
	if status, dueAt, serr := s.currentSupportFeeStatus(ctx, tenantID); serr != nil {
		s.log.Warn("composite entitlements: failed to resolve support fee status",
			zap.String("tenant_id", tenantID.String()), zap.Error(serr))
	} else {
		result.SupportFeeStatus = status
		result.SupportFeeDueAt = dueAt
	}

	return result, nil
}

// sharedServiceTag is the catalog tag of a code several services use (cmd/seed mergeSharedCodes).
const sharedServiceTag = "platform"

// resolveActiveServiceTags returns the set of FeatureDefinition.service_tag values the tenant
// currently has ANY entitlement in: the owning service_tag of every feature code in `features`
// (batch-queried once), plus the main plan's own primary service_tag (covers a standalone plan
// whose base tier grants zero features yet, e.g. bare wallet access). This is deliberately
// separate from activeServiceTags() above — that one only sees a plan's single service_tag field
// and can't see a PowerSuite bundle's per-feature-tagged modules (inventory/treasury/ordering/
// erp/logistics/marketflow all live under one "pos"-tagged plan row).
func (s *Service) resolveActiveServiceTags(ctx context.Context, features []string, mainPlan *ent.SubscriptionPlan) ([]string, error) {
	tags := map[string]bool{}
	if mainPlan != nil && mainPlan.ServiceTag != nil && *mainPlan.ServiceTag != "" {
		tags[*mainPlan.ServiceTag] = true
	}
	if len(features) > 0 {
		rows, err := s.client.FeatureDefinition.Query().
			Where(entfeaturedefinition.FeatureCodeIn(features...)).
			Select(entfeaturedefinition.FieldServiceTag).
			Strings(ctx)
		if err != nil {
			return nil, fmt.Errorf("query feature service tags: %w", err)
		}
		for _, t := range rows {
			// A shared code (analytics, branding, M-Pesa) belongs to no one service, so it opens none.
			if t != "" && t != sharedServiceTag {
				tags[t] = true
			}
		}
	}
	out := make([]string, 0, len(tags))
	for t := range tags {
		out = append(out, t)
	}
	return out, nil
}

// requireEtimsIntegrationEntitlement returns an error unless the tenant's CURRENT composite
// entitlements (main plan + already-active products, i.e. before whatever assignment is about to
// happen) already include etims_integration — the eligibility gate for the ETIMS_API_BUNDLED
// cross-sell plan (see AssignProductPlan). Kept eTIMS-specific and small on purpose: a generic
// "plan X requires feature Y" mechanism would be speculative machinery for a single case.
func (s *Service) requireEtimsIntegrationEntitlement(ctx context.Context, tenantID uuid.UUID) error {
	result, err := s.GetSubscriptionResult(ctx, tenantID)
	if err != nil {
		return fmt.Errorf("check etims_integration eligibility: %w", err)
	}
	for _, f := range result.Features {
		if f == "etims_integration" {
			return nil
		}
	}
	return fmt.Errorf("ETIMS_API_BUNDLED requires the tenant to already be entitled to etims_integration on their main plan or an active product")
}

// activeServiceTags returns the set of service tags a tenant is actively subscribed to,
// derived from the main plan plus active product subscriptions' override plans.
func (s *Service) activeServiceTags(ctx context.Context, sub *ent.TenantSubscription, mainPlan *ent.SubscriptionPlan) map[string]bool {
	tags := map[string]bool{}
	if mainPlan != nil && mainPlan.ServiceTag != nil {
		tags[*mainPlan.ServiceTag] = true
	}
	productPlans, err := s.resolveProductPlans(ctx, sub.ID)
	if err != nil {
		s.log.Warn("active service tags: failed to load product plans", zap.Error(err))
		return tags
	}
	for _, pp := range productPlans {
		if pp.ServiceTag != nil {
			tags[*pp.ServiceTag] = true
		}
	}
	return tags
}

// HasActiveServiceTagSubscription reports whether tenantID currently has an active or trial
// subscription — via its main plan or an active per-product overlay plan — tagged with
// serviceTag. Exempt tenants always pass. This is the gate for purchase/top-up flows on a
// service-tag-scoped external product (e.g. the prepaid API token wallet in
// billing/token_wallet.go), deliberately keyed by service_tag rather than Product.code: the
// wallet itself is generalized by service_tag, and a tenant whose MAIN plan IS a
// service-tag-scoped plan (e.g. ETIMS_API_BASIC) never gets a matching ProductSubscription row
// for that service tag (no "etims-api" product row maps to it) — only the plan default products
// (PlanDefaultProductCodes) and admin-assigned overlays (e.g.
// ETIMS_API_BUNDLED) do — so ActiveProducts/product_code would false-negative a legitimately
// subscribed standalone tenant. activeServiceTags already covers both enrollment paths (main
// plan + overlay), so it's reused here rather than introducing a second resolution mechanism.
func (s *Service) HasActiveServiceTagSubscription(ctx context.Context, tenantID uuid.UUID, serviceTag string) (bool, error) {
	if s.IsExemptTenant(ctx, tenantID) {
		return true, nil
	}
	sub, err := s.client.TenantSubscription.Query().
		Where(tenantsubscription.TenantIDEQ(tenantID)).
		WithPlan().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("query subscription: %w", err)
	}
	if sub.Status != tenantsubscription.StatusACTIVE && sub.Status != tenantsubscription.StatusTRIAL {
		return false, nil
	}
	return s.activeServiceTags(ctx, sub, sub.Edges.Plan)[serviceTag], nil
}
