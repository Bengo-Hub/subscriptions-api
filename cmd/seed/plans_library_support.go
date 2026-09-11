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

// seedLibrarySupportPlans seeds the annual support plans that accompany the Library perpetual
// licenses (SUPPORT_LIBRARY_{STARTER,GROWTH,PROFESSIONAL}): updates and support billed yearly,
// on the same 12k/18k/25k schedule as the PowerSuite families (seedUseCasePowerSuiteSupportPlans).
// Mirrors that function exactly:
//   - Entitlement-only: NO gating features (universal custom_branding is auto-added by
//     seedPlanFeatures — harmless). Buying support never unlocks product features.
//   - is_public=false: sold/assigned by the platform owner alongside a license, not
//     self-served on the pricing page.
//   - metadata.support_plan=true marks them for retireAnnualPlanRows' exclusion and for
//     any UI that wants to badge them.
//   - service_tag=platform so setup_fees.go's per-service defaults never apply.
func seedLibrarySupportPlans(ctx context.Context, tx *ent.Tx) error {
	now := time.Now()
	tierCodes := [3]string{"STARTER", "GROWTH", "PROFESSIONAL"}
	tierNames := [3]string{"Starter", "Growth", "Professional"}
	prices := [3]float64{12000, 18000, 25000}

	for t := 0; t < 3; t++ {
		tier := t + 1
		planCode := "SUPPORT_LIBRARY_" + tierCodes[t]
		id := uuid.NewSHA1(uuid.NameSpaceOID, []byte("plan:"+planCode))
		name := "Annual Support — Library " + tierNames[t]
		desc := fmt.Sprintf("Annual software support and updates for the Library %s perpetual license.", tierNames[t])

		existing, err := tx.SubscriptionPlan.Get(ctx, id)
		if err != nil && !ent.IsNotFound(err) {
			return fmt.Errorf("lookup library support plan %s: %w", planCode, err)
		}
		if existing != nil {
			_, err = tx.SubscriptionPlan.UpdateOneID(id).
				SetPlanCode(planCode).SetName(name).SetDescription(desc).
				SetBillingCycle("ANNUAL").SetPlanType(subscriptionplan.PlanTypeTIERED).
				SetBasePrice(prices[t]).SetSetupFee(0).SetCurrency("KES").
				SetIsActive(true).SetIsPublic(false).
				SetTierOrder(tier).SetFreeTrialDays(0).
				SetTierLimitsJSON(map[string]any{}).SetServiceTag("platform").
				SetMetadata(map[string]any{"support_plan": true}).
				SetUpdatedAt(now).Save(ctx)
		} else {
			_, err = tx.SubscriptionPlan.Create().
				SetID(id).SetPlanCode(planCode).SetName(name).SetDescription(desc).
				SetBillingCycle("ANNUAL").SetPlanType(subscriptionplan.PlanTypeTIERED).
				SetBasePrice(prices[t]).SetSetupFee(0).SetCurrency("KES").
				SetIsActive(true).SetIsPublic(false).
				SetTierOrder(tier).SetFreeTrialDays(0).
				SetTierLimitsJSON(map[string]any{}).SetServiceTag("platform").
				SetMetadata(map[string]any{"support_plan": true}).
				SetCreatedAt(now).SetUpdatedAt(now).Save(ctx)
		}
		if err != nil {
			return fmt.Errorf("upsert library support plan %s: %w", planCode, err)
		}
		if err := seedPlanFeatures(ctx, tx, id, nil); err != nil {
			return fmt.Errorf("seed features for %s: %w", planCode, err)
		}
		log.Printf("  annual support plan: %s (ANNUAL, KES %.0f)", name, prices[t])
	}
	return nil
}
