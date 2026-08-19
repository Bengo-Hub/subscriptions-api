package jobs

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/bengobox/subscription-service/internal/modules/subscriptions"
)

// StartEmailLicenseExpiryJob runs a daily background sweep that transitions
// any EmailLicense past its expires_at to EXPIRED — plan Part 5's T5.
// ExpireEmailLicense (the HTTP handler) only ever ran on an explicit admin
// call; without this job a license with a real expires_at set never
// actually expired on its own, matching every other lifecycle sweep in this
// package's own convention (dormancy.go, grace.go).
func StartEmailLicenseExpiryJob(ctx context.Context, log *zap.Logger, svc *subscriptions.Service) {
	log = log.Named("email_license_expiry.job")
	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		runEmailLicenseExpirySweep(ctx, log, svc) // run on startup
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				runEmailLicenseExpirySweep(ctx, log, svc)
			}
		}
	}()
	log.Info("email license expiry job started")
}

func runEmailLicenseExpirySweep(ctx context.Context, log *zap.Logger, svc *subscriptions.Service) {
	n, err := svc.ExpireDueLicenses(ctx, log)
	if err != nil {
		log.Warn("email license expiry sweep failed", zap.Error(err))
		return
	}
	if n > 0 {
		log.Info("email license expiry sweep completed", zap.Int("expired", n))
	}
}
