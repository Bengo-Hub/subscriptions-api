package subscriptions

import (
	"context"
	"fmt"
	"time"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/featuredefinition"
	"github.com/bengobox/subscription-service/internal/ent/productsubscription"
	"github.com/bengobox/subscription-service/internal/ent/subscriptionplan"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// DefaultActivatedProducts are auto-activated (status=active, no tenant action needed) the
// moment a tenant gets its first TenantSubscription — every tenant is assumed to need identity,
// point-of-sale, stock, and money-movement from day one, plus the ability to manage its own
// subscription. Deliberately NOT the same as Product.category (existing seed data has "pos" as
// an add_on and "treasury"/"inventory" as product-category rows for billing-line purposes
// unrelated to this default-vs-opt-in distinction) — an explicit list here is safer than
// repurposing that field and risking an unrelated billing-line side effect. Everything else
// (ordering, logistics, marketflow, erp, truload, ...) requires explicit self-activation via
// ActivateMyProduct.
var DefaultActivatedProducts = []string{"auth", "pos", "inventory", "treasury", "subscription"}

// activateDefaultProductsTx creates active ProductSubscription rows for every code in
// DefaultActivatedProducts, within the caller's transaction, for a brand-new subscription.
// Idempotent by construction (only ever called once, right after the TenantSubscription itself
// is created) — no existing-row check needed, unlike AssignProductPlan which can be called
// repeatedly against an existing subscription.
func activateDefaultProductsTx(ctx context.Context, tx *ent.Tx, tenantSubscriptionID uuid.UUID, now time.Time) error {
	for _, code := range DefaultActivatedProducts {
		if err := tx.ProductSubscription.Create().
			SetTenantSubscriptionID(tenantSubscriptionID).
			SetProductCode(code).
			SetStatus(productsubscription.StatusActive).
			SetActivatedAt(now).
			Exec(ctx); err != nil {
			return fmt.Errorf("activate default product %s: %w", code, err)
		}
	}
	return nil
}

// EntitlementCheck is the result of checking whether a tenant may self-activate a product.
type EntitlementCheck struct {
	Entitled bool `json:"entitled"`
	// MinPlanCode/MinPlanName/MinPlanTier name the cheapest plan that would unlock this
	// product, when NOT entitled — lets the UI prompt a specific upgrade rather than a
	// generic "upgrade your plan" message. Nil when Entitled is true or no catalogued plan
	// grants the product (nothing to recommend).
	MinPlanCode *string `json:"min_plan_code,omitempty"`
	MinPlanName *string `json:"min_plan_name,omitempty"`
	MinPlanTier *int    `json:"min_plan_tier,omitempty"`
}

// IsEntitledToProduct reports whether tenantID's current composite entitlements (base plan +
// already-active product overlays) already grant at least one feature tagged with productCode's
// service_tag — i.e. whether self-activating this product would be turning on something they're
// already paying for, not granting something new. A product with no catalogued
// FeatureDefinition rows at all (nothing gates it) fails OPEN, matching this platform's
// consistent "uncatalogued → don't block" entitlement posture (see isFeatureUnlocked in
// shared-ui-lib's feature-gate.tsx for the client-side twin of this rule).
func (s *Service) IsEntitledToProduct(ctx context.Context, tenantID uuid.UUID, productCode string) (EntitlementCheck, error) {
	defs, err := s.client.FeatureDefinition.Query().
		Where(featuredefinition.ServiceTagEQ(productCode)).
		All(ctx)
	if err != nil {
		return EntitlementCheck{}, fmt.Errorf("query feature definitions for product %s: %w", productCode, err)
	}
	if len(defs) == 0 {
		return EntitlementCheck{Entitled: true}, nil
	}

	result, err := s.GetSubscriptionResult(ctx, tenantID)
	if err != nil {
		return EntitlementCheck{}, err
	}
	if result.Exempt {
		return EntitlementCheck{Entitled: true}, nil
	}

	have := make(map[string]struct{}, len(result.Features))
	for _, f := range result.Features {
		have[f] = struct{}{}
	}
	for _, d := range defs {
		if _, ok := have[d.FeatureCode]; ok {
			return EntitlementCheck{Entitled: true}, nil
		}
	}

	// Not entitled: find the cheapest active plan that includes any of this product's features,
	// reusing the same minUnlockingPlans computation the /features/catalog endpoint uses for its
	// "upgrade to X" prompts, so the two surfaces never disagree about which plan to recommend.
	min, minErr := s.minUnlockingPlanForFeatures(ctx, defs)
	if minErr != nil {
		s.log.Warn("entitlement check: failed to resolve min unlocking plan", zap.Error(minErr))
		return EntitlementCheck{Entitled: false}, nil
	}
	if min == nil {
		return EntitlementCheck{Entitled: false}, nil
	}
	return EntitlementCheck{
		Entitled:    false,
		MinPlanCode: &min.PlanCode,
		MinPlanName: &min.Name,
		MinPlanTier: &min.TierOrder,
	}, nil
}

// ActivateMyProduct is the tenant-self-service entry point: activates productCode within the
// tenant's OWN current plan (never an override plan — self-service can only turn on what's
// already entitled, never grant a new plan) after confirming entitlement. Returns the same
// EntitlementCheck the caller would have gotten from IsEntitledToProduct so a 403 response can
// carry the upgrade recommendation without a second lookup.
func (s *Service) ActivateMyProduct(ctx context.Context, tenantID uuid.UUID, productCode string) (EntitlementCheck, error) {
	check, err := s.IsEntitledToProduct(ctx, tenantID, productCode)
	if err != nil {
		return EntitlementCheck{}, err
	}
	if !check.Entitled {
		return check, nil
	}
	if err := s.AssignProductPlan(ctx, tenantID, productCode, ""); err != nil {
		return EntitlementCheck{}, err
	}
	return check, nil
}

// minUnlockingPlan is the cheapest active plan (by tier, then price) that includes at least one
// of the given feature definitions.
type minUnlockingPlan struct {
	PlanCode  string
	Name      string
	TierOrder int
}

func (s *Service) minUnlockingPlanForFeatures(ctx context.Context, defs []*ent.FeatureDefinition) (*minUnlockingPlan, error) {
	codes := make(map[string]struct{}, len(defs))
	for _, d := range defs {
		codes[d.FeatureCode] = struct{}{}
	}

	plans, err := s.client.SubscriptionPlan.Query().
		Where(subscriptionplan.IsActive(true)).
		WithFeatures().
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("query plans: %w", err)
	}

	var best *minUnlockingPlan
	var bestPrice = float64(0)
	for _, p := range plans {
		for _, pf := range p.Edges.Features {
			if !pf.IsIncluded {
				continue
			}
			if _, ok := codes[pf.FeatureCode]; !ok {
				continue
			}
			if best == nil || p.TierOrder < best.TierOrder || (p.TierOrder == best.TierOrder && p.BasePrice < bestPrice) {
				best = &minUnlockingPlan{PlanCode: p.PlanCode, Name: p.Name, TierOrder: p.TierOrder}
				bestPrice = p.BasePrice
			}
			break
		}
	}
	return best, nil
}

// ActiveProductCodes returns the product_code of every currently-active ProductSubscription for
// tenantID, for JWT claim enrichment (auth-api mints these into the access token so the
// app-switcher can show only activated apps without an extra network call per page load).
func (s *Service) ActiveProductCodes(ctx context.Context, tenantID uuid.UUID) ([]string, error) {
	sub, err := s.getSubscription(ctx, tenantID)
	if err != nil {
		// No subscription (exempt tenant, or none provisioned yet) → no product claims, not an error.
		return nil, nil //nolint:nilerr
	}
	rows, err := s.client.ProductSubscription.Query().
		Where(
			productsubscription.TenantSubscriptionIDEQ(sub.ID),
			productsubscription.StatusEQ(productsubscription.StatusActive),
		).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("query active product subscriptions: %w", err)
	}
	codes := make([]string, 0, len(rows))
	for _, r := range rows {
		codes = append(codes, r.ProductCode)
	}
	return codes, nil
}
