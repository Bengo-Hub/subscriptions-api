package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/planfeature"
	"github.com/bengobox/subscription-service/internal/ent/subscriptionplan"
)

// retireAICreditsAddonPlans switches off the old MarketFlow AI-credit one-time packs. They were
// plan rows with a one-time price; the same two packs (100 for KES 1000, 500 for KES 4000) are now
// bundles in the credits catalog and are bought through POST /credits/ai_credits/purchase. The rows
// are kept, inactive and private, so nothing that references them breaks. Idempotent.
func retireAICreditsAddonPlans(ctx context.Context, tx *ent.Tx) error {
	n, err := tx.SubscriptionPlan.Update().
		Where(
			subscriptionplan.PlanCodeIn("MARKETFLOW_AI_CREDITS_100", "MARKETFLOW_AI_CREDITS_500"),
			subscriptionplan.Or(subscriptionplan.IsActive(true), subscriptionplan.IsPublic(true)),
		).
		SetIsActive(false).
		SetIsPublic(false).
		SetUpdatedAt(time.Now()).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("retire ai-credits addon plans: %w", err)
	}
	if n > 0 {
		log.Printf("  retired %d legacy ai-credits addon plan(s); AI credit bundles now live in the credits catalog", n)
	}
	return nil
}

// seedPlanAddonFeatures seeds purchasable addon PlanFeature records (is_included=false)
// for each plan. These are shown in the Addons section of the billing page and can be
// purchased individually via POST /addons/{feature_code}/purchase.
func seedPlanAddonFeatures(ctx context.Context, tx *ent.Tx) error {
	type addonDef struct {
		featureCode      string
		overageUnitPrice float64
		limitValue       int // 0 = unlimited / not applicable
	}

	type planAddon struct {
		planID uuid.UUID
		addons []addonDef
	}

	plans := []planAddon{
		// ORDERING_STARTER/GROWTH/PROFESSIONAL addon blocks removed 2026-09-11: those plan
		// rows were hard-deleted by migrateUseCasePowerSuite's 2026-09-11 Ordering/Inventory/
		// Treasury consolidation (superseded by PowerSuite), but this file kept referencing
		// their old plan IDs — every seed run since has hit a foreign-key violation here
		// ("insert or update on table plan_features violates foreign key constraint
		// plan_features_subscription_plans_features"), which rolled back the ENTIRE seed
		// transaction (entrypoint.sh's `|| echo ... non-fatal` swallowed the failure at the
		// container-startup log level, masking that nothing after this point in runSeed had
		// been committing). PowerSuite has no equivalent per-tenant Ordering overage-addon
		// model, so these are dropped rather than redirected to a successor plan.
		// *_YEARLY addon blocks removed — annual plan rows are hard-deleted (billing
		// period is now a per-subscription choice on the monthly rows).
		// ── TRULOAD_STARTER addons ─────────────────────────────────────────────
		{
			planID: uuid.NewSHA1(uuid.NameSpaceOID, []byte("truload:org:STARTER")),
			addons: []addonDef{
				{featureCode: "extra_transporter_portal", overageUnitPrice: 1500.0},
				{featureCode: "advanced_analytics_addon", overageUnitPrice: 500.0},
				{featureCode: "route_optimization_addon", overageUnitPrice: 1000.0},
			},
		},
		// ── TRULOAD_GROWTH addons ──────────────────────────────────────────────
		{
			planID: uuid.NewSHA1(uuid.NameSpaceOID, []byte("truload:org:GROWTH")),
			addons: []addonDef{
				{featureCode: "extra_transporter_portal", overageUnitPrice: 1200.0},
				{featureCode: "dedicated_support_addon", overageUnitPrice: 3000.0},
			},
		},
	}

	total := 0
	for _, p := range plans {
		// Remove existing addon features for this plan before re-seeding
		existing, err := tx.PlanFeature.Query().
			Where(
				planfeature.PlanIDEQ(p.planID),
				planfeature.IsIncludedEQ(false),
			).
			All(ctx)
		if err != nil {
			return fmt.Errorf("query addon features for plan %s: %w", p.planID, err)
		}
		for _, ef := range existing {
			if err := tx.PlanFeature.DeleteOne(ef).Exec(ctx); err != nil {
				return fmt.Errorf("delete addon feature: %w", err)
			}
		}

		for _, a := range p.addons {
			code := canonicalFeatureCode(a.featureCode)
			if !isInCatalog(code) {
				log.Printf("WARN seed: addon feature %q is not in the feature catalog — add it to featureCatalog in feature_catalog.go", code)
			}
			b := tx.PlanFeature.Create().
				SetPlanID(p.planID).
				SetFeatureCode(code).
				SetIsIncluded(false).
				SetOverageUnitPrice(a.overageUnitPrice)
			if a.limitValue > 0 {
				b = b.SetLimitValue(a.limitValue)
			}
			if _, err := b.Save(ctx); err != nil {
				return fmt.Errorf("create addon feature %s for plan %s: %w", a.featureCode, p.planID, err)
			}
			total++
		}
	}

	log.Printf("  addon features seeded: %d", total)
	return nil
}
