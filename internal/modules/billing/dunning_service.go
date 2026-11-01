package billing

import (
	"context"
	sharedcache "github.com/Bengo-Hub/cache"
	"time"

	"go.uber.org/zap"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/tenantsubscription"
	"github.com/bengobox/subscription-service/internal/modules/subscriptions"
)

// dunningSchedule lists the days after suspension on which a payment retry is attempted.
var dunningSchedule = []int{1, 3, 7}

// metaDunning holds the dunning state of a suspended subscription: when dunning started, the
// period it is chasing and how many retries ran. The clock lives here, not on updated_at, because
// every metadata write (this job's own included) moves updated_at and would restart the count.
const metaDunning = "dunning"

// StartDunningJob runs a daily background job that retries payment for SUSPENDED subscriptions.
// After the last retry day (day 7), the subscription is cancelled.
func StartDunningJob(ctx context.Context, log *zap.Logger, orm *ent.Client, svc *subscriptions.Service, invSvc *InvoiceService) {
	log = log.Named("dunning.job")

	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()

	runDunning(ctx, log, orm, svc, invSvc)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runDunning(ctx, log, orm, svc, invSvc)
		}
	}
}

func runDunning(ctx context.Context, log *zap.Logger, orm *ent.Client, svc *subscriptions.Service, invSvc *InvoiceService) {
	// Runs on every replica's ticker; only the first replica in each period does the work.
	if !sharedcache.ClaimPeriod(ctx, "subscriptions:dunning", 24*time.Hour) {
		return
	}
	// A dormant account awaiting purge is suspended too, but the dormancy job owns it.
	suspended, err := orm.TenantSubscription.Query().
		Where(
			tenantsubscription.StatusEQ(tenantsubscription.StatusSUSPENDED),
			tenantsubscription.PendingPurge(false),
		).
		WithPlan().
		All(ctx)
	if err != nil {
		log.Error("dunning job: failed to query suspended subscriptions", zap.Error(err))
		return
	}

	now := time.Now().UTC()
	for _, sub := range suspended {
		if svc.IsExemptTenant(ctx, sub.TenantID) {
			continue
		}
		processDunning(ctx, log, orm, svc, invSvc, sub, now)
	}

	if len(suspended) > 0 {
		log.Info("dunning job: processed suspended subscriptions", zap.Int("count", len(suspended)))
	}
}

// dunningState is the decoded metaDunning entry.
type dunningState struct {
	StartedAt time.Time
	PeriodEnd string
	Attempts  int
}

// readDunningState returns the dunning state for the subscription's current suspension. A state
// left from an earlier suspension (another period end) starts over, as does a missing one.
func readDunningState(sub *ent.TenantSubscription, now time.Time) (dunningState, bool) {
	periodEnd := sub.CurrentPeriodEnd.UTC().Format(time.RFC3339)
	raw, _ := sub.Metadata[metaDunning].(map[string]any)
	if raw != nil && raw["period_end"] == periodEnd {
		if started, err := time.Parse(time.RFC3339, stringMeta(raw, "started_at")); err == nil {
			attempts, _ := raw["attempts"].(float64)
			return dunningState{StartedAt: started, PeriodEnd: periodEnd, Attempts: int(attempts)}, true
		}
	}
	return dunningState{StartedAt: now, PeriodEnd: periodEnd}, false
}

// nextDunningStep says whether a retry is due today and whether dunning is exhausted.
func nextDunningStep(st dunningState, now time.Time) (retry, cancel bool) {
	days := int(now.Sub(st.StartedAt).Hours() / 24)
	if st.Attempts < len(dunningSchedule) {
		return days >= dunningSchedule[st.Attempts], false
	}
	return false, days > dunningSchedule[len(dunningSchedule)-1]
}

func processDunning(ctx context.Context, log *zap.Logger, orm *ent.Client, svc *subscriptions.Service, invSvc *InvoiceService, sub *ent.TenantSubscription, now time.Time) {
	st, known := readDunningState(sub, now)
	retry, cancel := nextDunningStep(st, now)

	if cancel {
		if _, err := svc.CancelSubscription(ctx, subscriptions.CancelInput{
			TenantID: sub.TenantID,
			Reason:   "dunning_exhausted",
		}); err != nil {
			log.Error("dunning job: failed to cancel subscription", zap.String("tenant_id", sub.TenantID.String()), zap.Error(err))
		} else {
			log.Info("dunning job: subscription cancelled after exhausted retries", zap.String("tenant_id", sub.TenantID.String()))
		}
		return
	}
	if !retry && known {
		return
	}
	if retry {
		st.Attempts++
	}

	meta := make(map[string]any, len(sub.Metadata)+1)
	for k, v := range sub.Metadata {
		if k != "dunning_attempt" { // the old counter, replaced by metaDunning
			meta[k] = v
		}
	}
	meta[metaDunning] = map[string]any{
		"started_at": st.StartedAt.Format(time.RFC3339),
		"period_end": st.PeriodEnd,
		"attempts":   st.Attempts,
	}
	if _, err := orm.TenantSubscription.UpdateOneID(sub.ID).SetMetadata(meta).Save(ctx); err != nil {
		log.Error("dunning job: failed to save dunning state", zap.String("tenant_id", sub.TenantID.String()), zap.Error(err))
		return
	}
	if !retry {
		return
	}

	// Collect the open invoice from the saved card, the same charge the renewal job makes.
	// Without a saved card nothing is attempted; the tenant pays from the emailed invoice link.
	if invSvc == nil || sub.Edges.Plan == nil {
		log.Info("dunning job: no invoice service or plan, skipping charge attempt",
			zap.String("tenant_id", sub.TenantID.String()), zap.Int("attempt", st.Attempts))
		return
	}
	res, err := invSvc.ChargeSavedCard(ctx, sub)
	if err != nil {
		log.Warn("dunning job: saved-card charge failed",
			zap.String("tenant_id", sub.TenantID.String()), zap.Int("attempt", st.Attempts), zap.Error(err))
		return
	}
	log.Info("dunning job: saved-card charge",
		zap.String("tenant_id", sub.TenantID.String()), zap.Int("attempt", st.Attempts),
		zap.Bool("attempted", res.Attempted), zap.String("status", res.Status), zap.String("reason", res.Reason))
}
