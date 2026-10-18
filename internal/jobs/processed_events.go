package jobs

import (
	"context"
	"time"

	sharedcache "github.com/Bengo-Hub/cache"
	eventslib "github.com/Bengo-Hub/shared-events"
	"go.uber.org/zap"
)

// StartProcessedEventsPruneJob deletes idempotency claims older than 30 days once a day. The
// processed_events ledger otherwise keeps a row for every event ever consumed: a pos.sale.finalized
// redelivery storm in August 2026 left 4.8M rows (about 1 GB, half of this service's database).
func StartProcessedEventsPruneJob(ctx context.Context, log *zap.Logger, store *eventslib.IdempotencyStore) {
	log = log.Named("processed_events.prune")
	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		runProcessedEventsPrune(ctx, log, store) // run on startup
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				runProcessedEventsPrune(ctx, log, store)
			}
		}
	}()
	log.Info("processed events prune job started")
}

func runProcessedEventsPrune(ctx context.Context, log *zap.Logger, store *eventslib.IdempotencyStore) {
	// Runs on every replica's ticker; only the first replica in each period does the work.
	if !sharedcache.ClaimPeriod(ctx, "subscriptions:processed-events-prune", 24*time.Hour) {
		return
	}
	n, err := store.Prune(ctx, eventslib.DefaultProcessedRetention, 0)
	if err != nil {
		log.Warn("processed events prune failed", zap.Error(err), zap.Int64("deleted", n))
		return
	}
	if n > 0 {
		log.Info("processed events pruned", zap.Int64("deleted", n))
	}
}
