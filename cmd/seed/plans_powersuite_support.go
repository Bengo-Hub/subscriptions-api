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

// seedUseCasePowerSuiteOneTimePlans seeds the per-family PERPETUAL (buy-outright) license
// tiers: POWERSUITE_{HOSP|DUKA|DAWA}_{BASIC|PRO|GOLD}_ONE_TIME. Each grants EXACTLY its
// recurring tier's features/limits (same builders) so gating is uniform between the monthly
// plan and the license. billing_cycle=ONE_TIME → resolved to billing_mode="one_time",
// is_perpetual, non-expiring; the renewal/expiry/invoice jobs exclude them via notPerpetual().
// The tier's setup/installation fee is charged ON TOP of the license price (inline here —
// setup_fees.go skips ONE_TIME plans). Ongoing support is sold separately via the SUPPORT_*
// annual plans below.
func seedUseCasePowerSuiteOneTimePlans(ctx context.Context, tx *ent.Tx) error {
	now := time.Now()
	for _, fam := range useCaseFamilies {
		for t := 0; t < 3; t++ {
			tier := t + 1
			planCode := fmt.Sprintf("POWERSUITE_%s_%s_ONE_TIME", fam.code, useCaseTierCodes[t])
			id := uuid.NewSHA1(uuid.NameSpaceOID, []byte("plan:"+planCode))
			name := fmt.Sprintf("PowerSuite %s — %s Perpetual License", fam.label, useCaseTierNames[t])
			desc := fmt.Sprintf("One-time buy-outright license for PowerSuite %s tier %d. Cloud sync, updates and support via the annual support plan.",
				fam.label, tier)
			feats := fam.features(tier)
			limits := useCaseSuiteLimits(fam.key, tier)

			existing, err := tx.SubscriptionPlan.Get(ctx, id)
			if err != nil && !ent.IsNotFound(err) {
				return fmt.Errorf("lookup one-time plan %s: %w", planCode, err)
			}
			if existing != nil {
				_, err = tx.SubscriptionPlan.UpdateOneID(id).
					SetPlanCode(planCode).SetName(name).SetDescription(desc).
					SetBillingCycle("ONE_TIME").SetPlanType(subscriptionplan.PlanTypeTIERED).
					SetBasePrice(fam.oneTime[t]).SetSetupFee(fam.setupFees[t]).SetCurrency("KES").
					SetIsActive(true).SetIsPublic(true).
					SetTierOrder(tier).SetFreeTrialDays(0).
					SetTierLimitsJSON(limits).SetServiceTag("pos").SetUpdatedAt(now).Save(ctx)
			} else {
				_, err = tx.SubscriptionPlan.Create().
					SetID(id).SetPlanCode(planCode).SetName(name).SetDescription(desc).
					SetBillingCycle("ONE_TIME").SetPlanType(subscriptionplan.PlanTypeTIERED).
					SetBasePrice(fam.oneTime[t]).SetSetupFee(fam.setupFees[t]).SetCurrency("KES").
					SetIsActive(true).SetIsPublic(true).
					SetTierOrder(tier).SetFreeTrialDays(0).
					SetTierLimitsJSON(limits).SetServiceTag("pos").
					SetCreatedAt(now).SetUpdatedAt(now).Save(ctx)
			}
			if err != nil {
				return fmt.Errorf("upsert one-time plan %s: %w", planCode, err)
			}
			if err := seedPlanFeatures(ctx, tx, id, feats); err != nil {
				return fmt.Errorf("seed features for %s: %w", planCode, err)
			}
			log.Printf("  use-case powersuite license: %s (ONE_TIME, KES %.0f + %.0f setup)", name, fam.oneTime[t], fam.setupFees[t])
		}
	}
	return nil
}

// seedUseCasePowerSuiteSupportPlans seeds the per-family ANNUAL support plans
// (SUPPORT_{HOSP|DUKA|DAWA}_{BASIC|PRO|GOLD}) that accompany the perpetual licenses:
// updates, cloud sync and support billed yearly via the existing invoicing job.
//
//   - Entitlement-only: NO gating features (universal custom_branding is auto-added by
//     seedPlanFeatures — harmless). Buying support never unlocks product features.
//   - is_public=false: sold/assigned by the platform owner alongside a license, not
//     self-served on the pricing page.
//   - metadata.support_plan=true marks them for retireAnnualPlanRows' exclusion and for
//     any UI that wants to badge them.
//   - service_tag=platform so setup_fees.go's per-service defaults never apply.
func seedUseCasePowerSuiteSupportPlans(ctx context.Context, tx *ent.Tx) error {
	now := time.Now()
	for _, fam := range useCaseFamilies {
		for t := 0; t < 3; t++ {
			tier := t + 1
			planCode := fmt.Sprintf("SUPPORT_%s_%s", fam.code, useCaseTierCodes[t])
			id := uuid.NewSHA1(uuid.NameSpaceOID, []byte("plan:"+planCode))
			name := fmt.Sprintf("Annual Support — PowerSuite %s %s", fam.label, useCaseTierNames[t])
			desc := fmt.Sprintf("Annual software support, updates and cloud sync for the PowerSuite %s %s perpetual license.",
				fam.label, useCaseTierNames[t])

			existing, err := tx.SubscriptionPlan.Get(ctx, id)
			if err != nil && !ent.IsNotFound(err) {
				return fmt.Errorf("lookup support plan %s: %w", planCode, err)
			}
			if existing != nil {
				_, err = tx.SubscriptionPlan.UpdateOneID(id).
					SetPlanCode(planCode).SetName(name).SetDescription(desc).
					SetBillingCycle("ANNUAL").SetPlanType(subscriptionplan.PlanTypeTIERED).
					SetBasePrice(fam.support[t]).SetSetupFee(0).SetCurrency("KES").
					SetIsActive(true).SetIsPublic(false).
					SetTierOrder(tier).SetFreeTrialDays(0).
					SetTierLimitsJSON(map[string]any{}).SetServiceTag("platform").
					SetMetadata(map[string]any{"support_plan": true}).
					SetUpdatedAt(now).Save(ctx)
			} else {
				_, err = tx.SubscriptionPlan.Create().
					SetID(id).SetPlanCode(planCode).SetName(name).SetDescription(desc).
					SetBillingCycle("ANNUAL").SetPlanType(subscriptionplan.PlanTypeTIERED).
					SetBasePrice(fam.support[t]).SetSetupFee(0).SetCurrency("KES").
					SetIsActive(true).SetIsPublic(false).
					SetTierOrder(tier).SetFreeTrialDays(0).
					SetTierLimitsJSON(map[string]any{}).SetServiceTag("platform").
					SetMetadata(map[string]any{"support_plan": true}).
					SetCreatedAt(now).SetUpdatedAt(now).Save(ctx)
			}
			if err != nil {
				return fmt.Errorf("upsert support plan %s: %w", planCode, err)
			}
			if err := seedPlanFeatures(ctx, tx, id, nil); err != nil {
				return fmt.Errorf("seed features for %s: %w", planCode, err)
			}
			log.Printf("  annual support plan: %s (ANNUAL, KES %.0f)", name, fam.support[t])
		}
	}
	return nil
}
