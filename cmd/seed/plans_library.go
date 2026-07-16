package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/subscriptionplan"
)

// ── Library Plans ─────────────────────────────────────────────────────────────
// Standalone Library Management plans (catalog/OPAC, circulation, members, fines,
// e-books). Monthly + annual recurring tiers plus 3 one-time perpetual licenses.
// New tenants with use_case == "library" are auto-assigned LIBRARY_STARTER (14-day trial)
// by auth-api's defaultTrialPlan.

func seedLibraryPlans(ctx context.Context, tx *ent.Tx) error {
	now := time.Now()
	serviceTag := "library"

	starterFeatures := []string{"library_catalog", "library_circulation", "library_members", "library_fines"}
	growthFeatures := append(append([]string{}, starterFeatures...), "library_holds", "library_ebooks")
	proFeatures := append(append([]string{}, growthFeatures...), "priority_support")

	starterLimits := map[string]any{"max_library_titles": 5000, "max_library_members": 1000, "max_library_branches": 1}
	growthLimits := map[string]any{"max_library_titles": 50000, "max_library_members": 10000, "max_library_branches": 5}
	proLimits := map[string]any{"max_library_titles": -1, "max_library_members": -1, "max_library_branches": -1}

	type planDef struct {
		key          string
		planCode     string
		name         string
		description  string
		billingCycle string
		price        float64
		tierOrder    int
		tierLimits   map[string]any
		features     []string
	}

	plans := []planDef{
		// Monthly
		{"STARTER", "LIBRARY_STARTER", "Library Starter", "Single-branch library: catalog/OPAC, circulation, members, and fines. Up to 5,000 titles.", "MONTHLY", 1500, 1, starterLimits, starterFeatures},
		{"GROWTH", "LIBRARY_GROWTH", "Library Growth", "Multi-branch with holds and e-book lending. Up to 50,000 titles, 5 branches.", "MONTHLY", 4000, 2, growthLimits, growthFeatures},
		{"PROFESSIONAL", "LIBRARY_PROFESSIONAL", "Library Professional", "Unlimited branches, titles, and members; all features + priority support.", "MONTHLY", 9000, 3, proLimits, proFeatures},
		// Annual rows are no longer seeded — billing period is a per-subscription choice
		// (MONTHLY/SEMI_ANNUAL/ANNUAL on the monthly row); legacy *_YEARLY rows are
		// hard-deleted by migrateUseCasePowerSuite.
		// One-time perpetual licenses (include priority support; setup fee applies)
		{"STARTER_ONE_TIME", "LIBRARY_STARTER_ONE_TIME", "Library Starter — Perpetual", "One-time perpetual license for a single-branch library. Excludes ongoing hosting.", "ONE_TIME", 150000, 1, starterLimits, append(append([]string{}, starterFeatures...), "priority_support")},
		{"GROWTH_ONE_TIME", "LIBRARY_GROWTH_ONE_TIME", "Library Growth — Perpetual", "One-time perpetual license: multi-branch + holds + e-books. Excludes ongoing hosting.", "ONE_TIME", 200000, 2, growthLimits, append(append([]string{}, growthFeatures...), "priority_support")},
		{"PROFESSIONAL_ONE_TIME", "LIBRARY_PROFESSIONAL_ONE_TIME", "Library Professional — Perpetual", "One-time perpetual license: unlimited, all features + priority support.", "ONE_TIME", 250000, 3, proLimits, proFeatures},
	}

	for _, p := range plans {
		id := uuid.NewSHA1(uuid.NameSpaceOID, []byte("library:"+p.key))
		existing, err := tx.SubscriptionPlan.Get(ctx, id)
		if err != nil && !ent.IsNotFound(err) {
			return fmt.Errorf("lookup library plan %s: %w", p.planCode, err)
		}
		if existing != nil {
			_, err = tx.SubscriptionPlan.UpdateOneID(id).
				SetPlanCode(p.planCode).SetName(p.name).SetDescription(p.description).
				SetBillingCycle(p.billingCycle).SetPlanType(subscriptionplan.PlanTypeSTANDALONE_SERVICE).
				SetBasePrice(p.price).SetCurrency("KES").SetIsActive(true).SetIsPublic(true).
				SetTierOrder(p.tierOrder).SetTierLimitsJSON(p.tierLimits).SetServiceTag(serviceTag).SetUpdatedAt(now).Save(ctx)
		} else {
			_, err = tx.SubscriptionPlan.Create().
				SetID(id).SetPlanCode(p.planCode).SetName(p.name).SetDescription(p.description).
				SetBillingCycle(p.billingCycle).SetPlanType(subscriptionplan.PlanTypeSTANDALONE_SERVICE).
				SetBasePrice(p.price).SetCurrency("KES").SetIsActive(true).SetIsPublic(true).
				SetTierOrder(p.tierOrder).SetTierLimitsJSON(p.tierLimits).SetServiceTag(serviceTag).
				SetCreatedAt(now).SetUpdatedAt(now).Save(ctx)
		}
		if err != nil {
			return fmt.Errorf("upsert library plan %s: %w", p.planCode, err)
		}
		if err := seedPlanFeatures(ctx, tx, id, p.features); err != nil {
			return fmt.Errorf("seed features for library plan %s: %w", p.planCode, err)
		}
		log.Printf("  library plan: %s (%s, KES %.0f)", p.name, p.billingCycle, p.price)
	}
	return nil
}
