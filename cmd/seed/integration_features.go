package main

import (
	"context"
	"fmt"
	"log"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/planfeature"
)

// serviceIntegrationFeatures is the BASIC integration scope map: for each service tag, the
// CORE features of the OTHER services it integrates with that every plan of that service
// should grant. This is the cross-service entitlement the user asked for — e.g. a POS plan
// unlocks basic treasury (payments) + inventory (BOM/stock) access — WITHOUT handing over
// those services' premium/standalone features (which need their own subscription/product line).
//
// Only "basic_*_access" grant flags live here; the target service's gate honors them for the
// integration paths. Keep these codes in sync with the feature catalog (validated by
// validateCatalogLinkage).
var serviceIntegrationFeatures = map[string][]string{
	"pos":         {"basic_inventory_access", "basic_treasury_access"},
	"inventory":   {"basic_treasury_access"},
	"ordering":    {"basic_inventory_access", "basic_logistics_access", "basic_treasury_access"},
	"truload":     {"basic_treasury_access"},
	"isp_billing": {"basic_treasury_access"},
	"logistics":   {"basic_treasury_access"},
	"marketflow":  {"basic_treasury_access"},
	"projects":    {"basic_treasury_access"},
	"erp":         {"basic_treasury_access", "basic_inventory_access"},
}

// injectIntegrationFeatures runs after all plans are seeded and ensures every plan carries
// the basic integration-scope features for its service tag. It is additive + idempotent: an
// integration feature already attached to the plan is skipped, and bundle/platform plans
// (no single service tag) are left untouched. Runs before validateCatalogLinkage so the
// injected codes are validated against the catalog.
func injectIntegrationFeatures(ctx context.Context, tx *ent.Tx) error {
	plans, err := tx.SubscriptionPlan.Query().All(ctx)
	if err != nil {
		return fmt.Errorf("query plans for integration features: %w", err)
	}
	injected := 0
	for _, p := range plans {
		if p.ServiceTag == nil {
			continue // bundles / platform-wide plans
		}
		extras, ok := serviceIntegrationFeatures[*p.ServiceTag]
		if !ok || len(extras) == 0 {
			continue
		}

		existing, err := tx.PlanFeature.Query().
			Where(planfeature.PlanIDEQ(p.ID)).
			All(ctx)
		if err != nil {
			return fmt.Errorf("query plan features for %s: %w", p.PlanCode, err)
		}
		have := make(map[string]bool, len(existing))
		for _, ef := range existing {
			have[ef.FeatureCode] = true
		}

		for _, code := range extras {
			canon := canonicalFeatureCode(code)
			if have[canon] {
				continue
			}
			if _, err := tx.PlanFeature.Create().
				SetPlanID(p.ID).
				SetFeatureCode(canon).
				SetIsIncluded(true).
				Save(ctx); err != nil {
				return fmt.Errorf("attach integration feature %s to %s: %w", canon, p.PlanCode, err)
			}
			have[canon] = true
			injected++
		}
	}
	if injected > 0 {
		log.Printf("  injected %d cross-service integration features", injected)
	}
	return nil
}
