package jobs

import (
	"context"
	"time"

	sharedcache "github.com/Bengo-Hub/cache"
	"go.uber.org/zap"

	"github.com/bengobox/subscription-service/internal/modules/subscriptions"
)

// StartPlanScheduleJob runs hourly: upgrade trial ending reminders, closing finished trials and
// applying plan changes whose renewal date has passed. Entitlements never wait for it, because
// the resolver checks trial ends and change dates itself (subscriptions.PlanIDInForce); the job
// moves plan_id, tidies metadata and sends the emails.
func StartPlanScheduleJob(ctx context.Context, log *zap.Logger, svc *subscriptions.Service) {
	log = log.Named("plan.schedule.job")
	// Offset from the expiry (0m), renewal (30m) and invoice (45m) jobs.
	select {
	case <-ctx.Done():
		return
	case <-time.After(15 * time.Minute):
	}
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()

	runPlanSchedules(ctx, log, svc)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runPlanSchedules(ctx, log, svc)
		}
	}
}

func runPlanSchedules(ctx context.Context, log *zap.Logger, svc *subscriptions.Service) {
	// Runs on every replica's ticker; only the first replica in each period does the work.
	if !sharedcache.ClaimPeriod(ctx, "subscriptions:plan-schedules", time.Hour) {
		return
	}
	st, err := svc.ProcessPlanSchedules(ctx, time.Now().UTC())
	if err != nil {
		log.Error("plan schedule job failed", zap.Error(err))
		return
	}
	if st.TrialsEnded+st.EndingNotices+st.ChangesApplied+st.Cleared+st.Failed > 0 {
		log.Info("plan schedule job: processed",
			zap.Int("trials_ended", st.TrialsEnded), zap.Int("ending_notices", st.EndingNotices),
			zap.Int("changes_applied", st.ChangesApplied), zap.Int("cleared", st.Cleared), zap.Int("failed", st.Failed))
	}
}
