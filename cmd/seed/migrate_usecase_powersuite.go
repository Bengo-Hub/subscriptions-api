package main

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/google/uuid"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/planfeature"
	"github.com/bengobox/subscription-service/internal/ent/planpricinghistory"
	"github.com/bengobox/subscription-service/internal/ent/productsubscription"
	"github.com/bengobox/subscription-service/internal/ent/subscriptionplan"
	"github.com/bengobox/subscription-service/internal/ent/tenant"
	"github.com/bengobox/subscription-service/internal/ent/tenantsubscription"
)

// migrateUseCasePowerSuite migrates every tenant subscription off the SUPERSEDED plan rows
// onto their successors, then HARD-DELETES those rows (user-mandated 2026-07-16; supersedes
// the old retire-only convention for these sets). Runs LAST in runSeed, after every plan
// seed + validation, inside the same transaction. Idempotent: once the doomed rows are gone
// it is a no-op.
//
// Doomed sets → successors:
//   - Generic POWERSUITE_{STARTER,GROWTH,PROFESSIONAL}(_YEARLY|_ONE_TIME) and
//     POS_SUITE_{STARTER,GROWTH,PROFESSIONAL}(_YEARLY): same-tier row of the use-case
//     family picked from the subscriber tenant's use_case (hospitality→HOSP,
//     retail→DUKA, pharmacy→DAWA; unknown→HOSP). _ONE_TIME maps to the family's
//     _ONE_TIME license; _YEARLY subscribers keep billing_cycle=ANNUAL on the sub row.
//   - POS_DEVICE_{1,5,10}(_YEARLY): family tier 1/2/3 by seat count.
//   - POS_LICENSE_PER_DEVICE → family BASIC_ONE_TIME; POS_LICENSE_COMPLETE and the
//     POS_{HOSP,DUKA,DAWA}_LICENSE tier-11 rows → family GOLD_ONE_TIME.
//   - Legacy flat ERP_ONE_TIME (150k) → ERP_GROWTH_ONE_TIME (same price point).
//   - Every remaining *_YEARLY row platform-wide → the same code minus "_YEARLY"
//     (subscriber keeps billing_cycle=ANNUAL); ISP_* yearly rows → ISP_BILLING_STARTER
//     (their monthly codes were consolidated + retired too).
//
// Safety: a doomed plan that still has subscriptions whose successor cannot be resolved is
// LOGGED and SKIPPED (kept in the DB) rather than breaking the FK or the tenant. Every
// re-point is logged "MIGRATE sub <id> tenant <id> <old_code> -> <new_code>" — that log is
// the input for the treasury invoice-correction pass (void+regenerate PENDING invoices whose
// plan price changed; PAID invoices are never touched).
func migrateUseCasePowerSuite(ctx context.Context, tx *ent.Tx) error {
	plans, err := tx.SubscriptionPlan.Query().All(ctx)
	if err != nil {
		return fmt.Errorf("query plans for powersuite migration: %w", err)
	}
	byCode := make(map[string]*ent.SubscriptionPlan, len(plans))
	for _, p := range plans {
		byCode[p.PlanCode] = p
	}

	// familyForTenant resolves the use-case PowerSuite family segment for a tenant.
	familyForTenant := func(tenantID uuid.UUID) string {
		t, terr := tx.Tenant.Query().Where(tenant.IDEQ(tenantID)).Only(ctx)
		if terr != nil || t.UseCase == nil {
			return "HOSP"
		}
		switch strings.ToLower(strings.TrimSpace(*t.UseCase)) {
		case "retail", "grocery", "warehouse", "warehousing", "e_commerce", "ecommerce", "hardware":
			return "DUKA"
		case "pharmacy", "chemist", "agrovet":
			return "DAWA"
		default: // hospitality, quick_service, services, food_delivery, hotel, restaurant, …
			return "HOSP"
		}
	}

	tierSeg := map[string]string{"STARTER": "BASIC", "GROWTH": "PRO", "PROFESSIONAL": "GOLD", "1": "BASIC", "5": "PRO", "10": "GOLD"}

	// successorCode resolves the successor plan code for one subscription on a doomed plan.
	// Empty string = unresolvable.
	successorCode := func(oldCode string, tenantID uuid.UUID) string {
		oneTime := strings.HasSuffix(oldCode, "_ONE_TIME")
		yearly := strings.HasSuffix(oldCode, "_YEARLY")
		base := strings.TrimSuffix(strings.TrimSuffix(oldCode, "_ONE_TIME"), "_YEARLY")

		switch {
		case strings.HasPrefix(base, "POWERSUITE_") || strings.HasPrefix(base, "POS_SUITE_"):
			// Generic bundle → use-case family at the same tier.
			seg := tierSeg[strings.TrimPrefix(strings.TrimPrefix(base, "POWERSUITE_"), "POS_SUITE_")]
			if seg == "" {
				return ""
			}
			code := fmt.Sprintf("POWERSUITE_%s_%s", familyForTenant(tenantID), seg)
			if oneTime {
				code += "_ONE_TIME"
			}
			return code
		case strings.HasPrefix(base, "POS_DEVICE_"):
			seg := tierSeg[strings.TrimPrefix(base, "POS_DEVICE_")]
			if seg == "" {
				return ""
			}
			return fmt.Sprintf("POWERSUITE_%s_%s", familyForTenant(tenantID), seg)
		case base == "POS_LICENSE_PER_DEVICE":
			return fmt.Sprintf("POWERSUITE_%s_BASIC_ONE_TIME", familyForTenant(tenantID))
		case base == "POS_LICENSE_COMPLETE":
			return fmt.Sprintf("POWERSUITE_%s_GOLD_ONE_TIME", familyForTenant(tenantID))
		case base == "POS_HOSP_LICENSE":
			return "POWERSUITE_HOSP_GOLD_ONE_TIME"
		case base == "POS_DUKA_LICENSE":
			return "POWERSUITE_DUKA_GOLD_ONE_TIME"
		case base == "POS_DAWA_LICENSE":
			return "POWERSUITE_DAWA_GOLD_ONE_TIME"
		case oldCode == "ERP_ONE_TIME":
			return "ERP_GROWTH_ONE_TIME"
		case yearly && strings.HasPrefix(base, "ISP_"):
			return "ISP_BILLING_STARTER"
		case yearly:
			return base // e.g. ORDERING_STARTER_YEARLY → ORDERING_STARTER
		}
		return ""
	}

	// isDoomed decides whether a plan row is in the hard-delete scope.
	isDoomed := func(code string) bool {
		switch {
		case strings.HasSuffix(code, "_YEARLY"):
			return true
		case code == "POWERSUITE_STARTER" || code == "POWERSUITE_GROWTH" || code == "POWERSUITE_PROFESSIONAL",
			code == "POWERSUITE_STARTER_ONE_TIME" || code == "POWERSUITE_GROWTH_ONE_TIME" || code == "POWERSUITE_PROFESSIONAL_ONE_TIME",
			strings.HasPrefix(code, "POS_SUITE_"),
			strings.HasPrefix(code, "POS_DEVICE_"),
			strings.HasPrefix(code, "POS_LICENSE_"),
			code == "POS_HOSP_LICENSE" || code == "POS_DUKA_LICENSE" || code == "POS_DAWA_LICENSE",
			code == "ERP_ONE_TIME":
			return true
		}
		return false
	}

	var doomed []*ent.SubscriptionPlan
	for _, p := range plans {
		if isDoomed(p.PlanCode) {
			doomed = append(doomed, p)
		}
	}
	if len(doomed) == 0 {
		return nil
	}

	migrated, deleted, kept := 0, 0, 0
	for _, p := range doomed {
		subs, serr := tx.TenantSubscription.Query().
			Where(tenantsubscription.PlanIDEQ(p.ID)).
			All(ctx)
		if serr != nil {
			return fmt.Errorf("query subscriptions on plan %s: %w", p.PlanCode, serr)
		}

		blocked := false
		for _, s := range subs {
			succ := successorCode(p.PlanCode, s.TenantID)
			target := byCode[succ]
			if succ == "" || target == nil {
				log.Printf("  MIGRATE-SKIP plan %s: sub %s (tenant %s) has no resolvable successor (%q) — plan kept", p.PlanCode, s.ID, s.TenantID, succ)
				blocked = true
				continue
			}
			upd := tx.TenantSubscription.UpdateOneID(s.ID).SetPlanID(target.ID)
			// Annual-row subscribers keep their yearly cadence via the per-subscription cycle.
			if strings.HasSuffix(p.PlanCode, "_YEARLY") && s.BillingCycle != tenantsubscription.BillingCycleANNUAL {
				upd.SetBillingCycle(tenantsubscription.BillingCycleANNUAL)
			}
			if _, uerr := upd.Save(ctx); uerr != nil {
				return fmt.Errorf("re-point sub %s from %s to %s: %w", s.ID, p.PlanCode, succ, uerr)
			}
			log.Printf("  MIGRATE sub %s tenant %s %s -> %s", s.ID, s.TenantID, p.PlanCode, succ)
			migrated++
		}
		if blocked {
			kept++
			continue
		}

		// Detach product-subscription plan overrides pointing at the doomed row.
		if _, perr := tx.ProductSubscription.Update().
			Where(productsubscription.OverridePlanIDEQ(p.ID)).
			ClearOverridePlanID().
			Save(ctx); perr != nil {
			return fmt.Errorf("clear product-subscription overrides for %s: %w", p.PlanCode, perr)
		}

		// Children first (FKs), then the plan row itself.
		if _, ferr := tx.PlanFeature.Delete().Where(planfeature.PlanIDEQ(p.ID)).Exec(ctx); ferr != nil {
			return fmt.Errorf("delete features of %s: %w", p.PlanCode, ferr)
		}
		if _, herr := tx.PlanPricingHistory.Delete().Where(planpricinghistory.PlanIDEQ(p.ID)).Exec(ctx); herr != nil {
			return fmt.Errorf("delete pricing history of %s: %w", p.PlanCode, herr)
		}
		if derr := tx.SubscriptionPlan.DeleteOneID(p.ID).Exec(ctx); derr != nil {
			return fmt.Errorf("hard-delete plan %s: %w", p.PlanCode, derr)
		}
		if _, still := byCode[p.PlanCode]; still {
			delete(byCode, p.PlanCode)
		}
		log.Printf("  DELETED superseded plan %s (%s)", p.PlanCode, p.ID)
		deleted++
	}

	// Guard against a seed step above unknowingly re-creating a doomed row next run.
	if _, err := tx.SubscriptionPlan.Query().Where(subscriptionplan.PlanCodeHasSuffix("_YEARLY")).First(ctx); err == nil {
		log.Printf("  WARN: a *_YEARLY plan row survived migration (blocked by unresolvable subscribers above)")
	}

	log.Printf("  ✓ PowerSuite migration: %d subscriptions re-pointed, %d plans hard-deleted, %d kept (blocked)", migrated, deleted, kept)
	return nil
}
