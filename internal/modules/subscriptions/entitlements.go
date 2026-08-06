package subscriptions

import (
	"context"
	"fmt"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/planfeature"
	"github.com/bengobox/subscription-service/internal/ent/productsubscription"
	"github.com/bengobox/subscription-service/internal/ent/subscriptionplan"
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
		var iv int
		switch t := v.(type) {
		case float64:
			iv = int(t)
		case int:
			iv = t
		default:
			continue
		}
		cur, ok := result.Limits[k]
		if !ok {
			result.Limits[k] = iv
			continue
		}
		if cur == -1 || iv == -1 {
			result.Limits[k] = -1
			continue
		}
		if iv > cur {
			result.Limits[k] = iv
		}
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

	plan, err := s.loadPlanWithFeatures(ctx, sub.PlanID)
	if err != nil {
		return nil, fmt.Errorf("lookup plan: %w", err)
	}

	result := s.buildResult(sub, plan)

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

	return result, nil
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
