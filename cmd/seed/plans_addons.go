package main

import (
	"context"
	"fmt"
	"log"

	"github.com/google/uuid"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/planfeature"
)

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
		// ── ORDERING_STARTER addons ────────────────────────────────────────────
		{
			planID: uuid.NewSHA1(uuid.NameSpaceOID, []byte("plan:ORDERING_STARTER")),
			addons: []addonDef{
				{featureCode: "extra_rider_slot", overageUnitPrice: 250.0},
				{featureCode: "extra_outlet", overageUnitPrice: 500.0},
				{featureCode: "advanced_analytics_addon", overageUnitPrice: 500.0},
				{featureCode: "sms_bundle_100", overageUnitPrice: 200.0, limitValue: 100},
			},
		},
		// ── ORDERING_GROWTH addons ─────────────────────────────────────────────
		{
			planID: uuid.NewSHA1(uuid.NameSpaceOID, []byte("plan:ORDERING_GROWTH")),
			addons: []addonDef{
				{featureCode: "extra_rider_slot", overageUnitPrice: 250.0},
				{featureCode: "extra_outlet", overageUnitPrice: 400.0},
				{featureCode: "hotel_module_addon", overageUnitPrice: 2000.0},
				{featureCode: "route_optimization_addon", overageUnitPrice: 1000.0},
				{featureCode: "sms_bundle_500", overageUnitPrice: 800.0, limitValue: 500},
			},
		},
		// ── ORDERING_PROFESSIONAL addons ───────────────────────────────────────
		{
			planID: uuid.NewSHA1(uuid.NameSpaceOID, []byte("plan:ORDERING_PROFESSIONAL")),
			addons: []addonDef{
				{featureCode: "extra_api_quota_bundle", overageUnitPrice: 500.0},
				{featureCode: "dedicated_support_addon", overageUnitPrice: 5000.0},
			},
		},
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
