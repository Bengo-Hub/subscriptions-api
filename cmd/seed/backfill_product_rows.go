package main

import (
	"context"
	"log"

	"go.uber.org/zap"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/modules/subscriptions"
)

// backfillZeroProductRows gives every ACTIVE/TRIAL tenant subscription with zero product rows its
// plan-derived default products (subscriptions.BackfillZeroProductSubscriptions). It runs after
// the seed transaction has committed, so the products and plan features it reads are current, and
// a failure here only logs: it never rolls back the catalog seed.
func backfillZeroProductRows(ctx context.Context, client *ent.Client) {
	logger, err := zap.NewProduction()
	if err != nil {
		logger = zap.NewNop()
	}
	defer func() { _ = logger.Sync() }()
	res, err := subscriptions.BackfillZeroProductSubscriptions(ctx, client, logger)
	if err != nil {
		log.Printf("zero-row product backfill skipped: %v", err)
		return
	}
	log.Printf("zero-row product backfill: %d subscriptions without product rows, %d filled, %d rows inserted, %d failed",
		res.Subscriptions, res.Filled, res.RowsInserted, res.Failed)
}
