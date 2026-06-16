package main

import (
	"context"
	"log"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/subscriptionplan"
)

// seedSetupFees applies one-time onboarding/setup fees (KES) to recurring plans by service.
// Runs AFTER all plans are seeded. Idempotent (sets an absolute value each run).
//
// Rules:
//   - Applied only to recurring plans (billing_cycle != ONE_TIME) so perpetual licenses
//     — which already carry a large one-off price — never get an extra setup fee.
//   - Keyed by service_tag; transporter plans (which share the "truload" tag) are
//     overridden by plan_code prefix.
//
// Amounts are platform defaults — editable per-plan in the plan builder UI afterwards.
func seedSetupFees(ctx context.Context, tx *ent.Tx) error {
	byServiceTag := map[string]float64{
		"pos":         2500,  // POS device onboarding
		"truload":     5000,  // TruLoad org setup
		"erp":         10000, // ERP implementation
		"inventory":   1500,  // Inventory standalone setup
		"ordering":    1500,  // Ordering standalone setup
		"isp_billing": 3000,  // ISP setup
	}

	for tag, fee := range byServiceTag {
		n, err := tx.SubscriptionPlan.Update().
			Where(
				subscriptionplan.ServiceTagEQ(tag),
				subscriptionplan.BillingCycleNEQ("ONE_TIME"),
			).
			SetSetupFee(fee).
			Save(ctx)
		if err != nil {
			return err
		}
		log.Printf("  setup fee: %s plans → KES %.0f (%d plans)", tag, fee, n)
	}

	// Transporter plans share the "truload" service_tag but warrant a lower setup fee.
	if n, err := tx.SubscriptionPlan.Update().
		Where(
			subscriptionplan.PlanCodeHasPrefix("TRANSPORTER_"),
			subscriptionplan.BillingCycleNEQ("ONE_TIME"),
		).
		SetSetupFee(3000).
		Save(ctx); err != nil {
		return err
	} else {
		log.Printf("  setup fee: TRANSPORTER_* plans → KES 3000 (%d plans)", n)
	}

	return nil
}
