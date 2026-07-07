package main

import (
	"context"
	"fmt"
	"log"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/subscriptionplan"
)

// retireAnnualPlanRows deactivates every recurring ANNUAL plan row (the *_YEARLY /
// *_ANNUAL duplicates of the monthly tiers).
//
// Billing periods are no longer modeled as separate plan rows: each tier has ONE
// monthly-priced plan and the tenant chooses the period (MONTHLY / SEMI_ANNUAL / ANNUAL)
// at subscribe/renew time — price = months × base_price, and a 6+ month period waives the
// one-time setup fee (the only incentive; no automatic discount). See
// internal/modules/subscriptions/billing_cycle.go.
//
// Deactivated (not deleted) so existing annual subscriptions keep resolving their plan;
// they simply can't be newly subscribed to. ONE_TIME perpetual licences are untouched.
// Runs AFTER all plan seeds (which upsert the legacy annual rows back to active), so the
// end state of every seed run is: annual rows present but retired.
func retireAnnualPlanRows(ctx context.Context, tx *ent.Tx) error {
	n, err := tx.SubscriptionPlan.Update().
		Where(
			subscriptionplan.BillingCycleEQ("ANNUAL"),
			subscriptionplan.IsActiveEQ(true),
		).
		SetIsActive(false).
		SetIsPublic(false).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("retire annual plan rows: %w", err)
	}
	if n > 0 {
		log.Printf("  ✓ Retired %d ANNUAL plan rows (billing period is now chosen per subscription; 6+ months waives the setup fee)", n)
	}
	return nil
}
