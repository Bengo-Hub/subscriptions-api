package jobs

import (
	"context"
	"time"

	sharedcache "github.com/Bengo-Hub/cache"
	"go.uber.org/zap"

	"github.com/bengobox/subscription-service/internal/modules/credits"
)

// StartCreditsJob refills recurring credit allowances (WhatsApp tiers, plan AI credits) at the
// start of each calendar month and zeroes the allowance of lapsed tiers. RefillAllowances is
// idempotent per month, so the hourly cadence only bounds how late a new month's refill lands.
func StartCreditsJob(ctx context.Context, log *zap.Logger, svc *credits.Service) {
	log = log.Named("credits.job")
	go func() {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		runCredits(ctx, log, svc)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				runCredits(ctx, log, svc)
			}
		}
	}()
	log.Info("credits job started")
}

func runCredits(ctx context.Context, log *zap.Logger, svc *credits.Service) {
	if !sharedcache.ClaimPeriod(ctx, "subscriptions:credits-refill", 50*time.Minute) {
		return
	}
	n, err := svc.RefillAllowances(ctx, time.Now().UTC())
	if err != nil {
		log.Error("credit allowance refill failed", zap.Error(err))
		return
	}
	if n > 0 {
		log.Info("credit allowances refilled", zap.Int("wallets", n))
	}
}
