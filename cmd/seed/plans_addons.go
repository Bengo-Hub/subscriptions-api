package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/bengobox/subscription-service/internal/ent"
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
