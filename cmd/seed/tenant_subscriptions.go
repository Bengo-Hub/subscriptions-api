package main

import (
	"context"
	"fmt"
	"log"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/productsubscription"
	"github.com/bengobox/subscription-service/internal/modules/tenant"
)

// seedAllTenantSubscriptions deletes all existing subscription records so the DB
// starts with a clean slate. No subscriptions are created by the seed — tenants
// subscribe themselves through the UI, or a platform admin assigns a plan.
//
// Excluded from seeding:
//   - codevertex (platform owner) — unrestricted access, no subscription needed
//   - codevertex-demo — bypassed in subscription.go via isDemoTenant()
//   - All other tenants — subscribe via /plans page or admin assignment
func seedAllTenantSubscriptions(ctx context.Context, tx *ent.Tx, _ *tenant.Syncer) error {
	existingSubs, err := tx.TenantSubscription.Query().All(ctx)
	if err != nil {
		return fmt.Errorf("query existing subscriptions for cleanup: %w", err)
	}

	for _, sub := range existingSubs {
		if _, err := tx.ProductSubscription.Delete().
			Where(productsubscription.TenantSubscriptionIDEQ(sub.ID)).
			Exec(ctx); err != nil {
			return fmt.Errorf("delete product subscriptions for %s: %w", sub.ID, err)
		}
		if err := tx.TenantSubscription.DeleteOne(sub).Exec(ctx); err != nil {
			return fmt.Errorf("delete subscription %s: %w", sub.ID, err)
		}
	}

	log.Printf("  tenant subscriptions: cleaned %d record(s) — no subscriptions seeded", len(existingSubs))
	return nil
}
