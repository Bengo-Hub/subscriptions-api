package jobs

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/bengobox/subscription-service/internal/modules/billing"
)

// StartOverageJob runs a daily sweep calculating accumulated overage charges for every
// active/trial subscription. CalculateDailyOverages upserts on (sub_id, metric_type,
// period_date), so re-running it is idempotent regardless of exact time-of-day.
func StartOverageJob(ctx context.Context, log *zap.Logger, svc *billing.OverageService) {
	log = log.Named("overage.job")
	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		runOverage(ctx, log, svc) // run on startup
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				runOverage(ctx, log, svc)
			}
		}
	}()
	log.Info("overage job started")
}

func runOverage(ctx context.Context, log *zap.Logger, svc *billing.OverageService) {
	if err := svc.CalculateDailyOverages(ctx, time.Now().UTC()); err != nil {
		log.Error("daily overage calculation failed", zap.Error(err))
	}
}
