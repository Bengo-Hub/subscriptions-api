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

// seedAICreditsAddonPlans seeds the MarketFlow AI-credit one-time top-up packs as standalone
// SubscriptionPlan rows. Relocated here 2026-09-11 from plans_marketflow.go (now retired — its
// STARTER/GROWTH/PROFESSIONAL tiers were folded into every PowerSuite tier's psCRMBlock, since
// ai_chat_agent and the rest of MarketFlow's catalog are now baked into PowerSuite itself). These
// two packs are consumable purchases, not tier features — every PowerSuite tenant now has
// ai_chat_agent already, but still needs a way to buy extra credits, so the top-up capability
// survives the family's retirement under its original plan codes/deterministic IDs (nothing else
// in the codebase references them, confirmed via repo-wide grep, so the codes didn't need renaming).
func seedAICreditsAddonPlans(ctx context.Context, tx *ent.Tx) error {
	now := time.Now()
	serviceTag := "marketflow"

	type planDef struct {
		id          uuid.UUID
		planCode    string
		name        string
		description string
		price       float64
		tierOrder   int
		tierLimits  map[string]any
	}

	plans := []planDef{
		{
			id:          uuid.NewSHA1(uuid.NameSpaceOID, []byte("marketflow:AI_CREDITS_100")),
			planCode:    "MARKETFLOW_AI_CREDITS_100",
			name:        "MarketFlow AI Credits — 100 pack",
			description: "Top-up pack of 100 AI chat credits for MarketFlow. Each credit = 1 user question + AI response.",
			price:       1000.0,
			tierOrder:   10,
			tierLimits:  map[string]any{"ai_credits_monthly": 100},
		},
		{
			id:          uuid.NewSHA1(uuid.NameSpaceOID, []byte("marketflow:AI_CREDITS_500")),
			planCode:    "MARKETFLOW_AI_CREDITS_500",
			name:        "MarketFlow AI Credits — 500 pack",
			description: "Top-up pack of 500 AI chat credits for MarketFlow. Best value for high-volume teams.",
			price:       4000.0,
			tierOrder:   11,
			tierLimits:  map[string]any{"ai_credits_monthly": 500},
		},
	}

	for _, p := range plans {
		existing, err := tx.SubscriptionPlan.Get(ctx, p.id)
		if err != nil && !ent.IsNotFound(err) {
			return fmt.Errorf("lookup ai-credits addon plan %s: %w", p.planCode, err)
		}
		if existing != nil {
			_, err = tx.SubscriptionPlan.UpdateOneID(p.id).
				SetPlanCode(p.planCode).SetName(p.name).SetDescription(p.description).
				SetBillingCycle("ONE_TIME").SetPlanType(subscriptionplan.PlanTypeSTANDALONE_SERVICE).
				SetBasePrice(p.price).SetCurrency("KES").SetIsActive(true).SetIsPublic(true).
				SetTierOrder(p.tierOrder).SetTierLimitsJSON(p.tierLimits).SetServiceTag(serviceTag).SetUpdatedAt(now).Save(ctx)
		} else {
			_, err = tx.SubscriptionPlan.Create().
				SetID(p.id).SetPlanCode(p.planCode).SetName(p.name).SetDescription(p.description).
				SetBillingCycle("ONE_TIME").SetPlanType(subscriptionplan.PlanTypeSTANDALONE_SERVICE).
				SetBasePrice(p.price).SetCurrency("KES").SetIsActive(true).SetIsPublic(true).
				SetTierOrder(p.tierOrder).SetTierLimitsJSON(p.tierLimits).SetServiceTag(serviceTag).
				SetCreatedAt(now).SetUpdatedAt(now).Save(ctx)
		}
		if err != nil {
			return fmt.Errorf("upsert ai-credits addon plan %s: %w", p.planCode, err)
		}
		if err := seedPlanFeatures(ctx, tx, p.id, []string{"ai_chat_agent"}); err != nil {
			return fmt.Errorf("seed features for ai-credits addon plan %s: %w", p.planCode, err)
		}
		log.Printf("  ai-credits addon plan: %s (ONE_TIME, KES %.0f)", p.name, p.price)
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
