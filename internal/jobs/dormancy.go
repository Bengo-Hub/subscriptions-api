package jobs

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/tenantsubscription"
	"github.com/bengobox/subscription-service/internal/modules/subscriptions"
)

const (
	// DormancyDays is the idle window (no billable activity, unpaid) after which an account
	// is flagged dormant.
	DormancyDays = 60
	// DormancyGraceDays is the notified grace window before a dormant account is suspended
	// and queued for an admin-confirmed data purge.
	DormancyGraceDays = 7
)

// StartDormancyJob runs a daily background sweep implementing the account-dormancy lifecycle:
//   - Flag + notify accounts with no billable activity for > DormancyDays (status -> DORMANT).
//   - After DormancyGraceDays with no reactivation, suspend + queue for purge (pending_purge).
//
// Reactivation is automatic: any billable usage clears a DORMANT flag (usage_consumer), and a
// successful payment restores the account. Exempt tenants (demo / platform owner) are skipped.
// The actual data deletion is NOT done here — it requires a platform-owner-confirmed purge
// (see PlatformHandler.ConfirmDormancyPurge), which emits tenant.purge for services to consume.
func StartDormancyJob(ctx context.Context, log *zap.Logger, orm *ent.Client, svc *subscriptions.Service) {
	log = log.Named("dormancy.job")
	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		runDormancySweep(ctx, log, orm, svc) // run on startup
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				runDormancySweep(ctx, log, orm, svc)
			}
		}
	}()
	log.Info("dormancy job started", zap.Int("dormancy_days", DormancyDays), zap.Int("grace_days", DormancyGraceDays))
}

func runDormancySweep(ctx context.Context, log *zap.Logger, orm *ent.Client, svc *subscriptions.Service) {
	now := time.Now().UTC()
	threshold := now.Add(-time.Duration(DormancyDays) * 24 * time.Hour)
	flagDormant(ctx, log, orm, svc, now, threshold)
	suspendDormant(ctx, log, orm, svc, now)
}

// flagDormant is the first pass: warn accounts idle past the threshold and start the grace clock.
func flagDormant(ctx context.Context, log *zap.Logger, orm *ent.Client, svc *subscriptions.Service, now, threshold time.Time) {
	subs, err := orm.TenantSubscription.Query().
		Where(
			tenantsubscription.DormantAtIsNil(),
			tenantsubscription.PendingPurgeEQ(false),
			tenantsubscription.StatusIn(
				tenantsubscription.StatusACTIVE,
				tenantsubscription.StatusTRIAL,
				tenantsubscription.StatusEXPIRED,
			),
			tenantsubscription.Or(
				tenantsubscription.LastActivityAtLT(threshold),
				tenantsubscription.And(
					tenantsubscription.LastActivityAtIsNil(),
					tenantsubscription.CreatedAtLT(threshold),
				),
			),
		).
		All(ctx)
	if err != nil {
		log.Warn("dormancy: query dormant candidates failed", zap.Error(err))
		return
	}
	graceEnds := now.Add(time.Duration(DormancyGraceDays) * 24 * time.Hour)
	for _, sub := range subs {
		if svc.IsExemptTenant(ctx, sub.TenantID) {
			continue
		}
		tx, err := orm.Tx(ctx)
		if err != nil {
			continue
		}
		if _, err := tx.TenantSubscription.UpdateOneID(sub.ID).
			SetStatus(tenantsubscription.StatusDORMANT).
			SetDormantAt(now).
			SetPurgeGraceEndsAt(graceEnds).
			Save(ctx); err != nil {
			_ = tx.Rollback()
			continue
		}
		svc.WriteOutboxEventPublic(ctx, tx, sub.TenantID, "subscription", sub.ID, "dormancy.warning", map[string]any{
			"tenant_id":     sub.TenantID.String(),
			"dormant_days":  DormancyDays,
			"grace_days":    DormancyGraceDays,
			"grace_ends_at": graceEnds.Format(time.RFC3339),
			"notification": map[string]any{
				"target":  "tenant_admin",
				"subject": "Your Codevertex account is inactive",
				"message": "Your account has had no activity for over 60 days. Reactivate within 7 days (record a sale or pay your plan) to avoid suspension and permanent data deletion.",
			},
		})
		if err := tx.Commit(); err != nil {
			log.Warn("dormancy: commit warning failed", zap.String("tenant_id", sub.TenantID.String()), zap.Error(err))
			continue
		}
		log.Info("dormancy: account flagged dormant + warned", zap.String("tenant_id", sub.TenantID.String()))
	}
}

// suspendDormant is the second pass: grace window elapsed with no reactivation → suspend the
// account and queue it for an admin-confirmed purge.
func suspendDormant(ctx context.Context, log *zap.Logger, orm *ent.Client, svc *subscriptions.Service, now time.Time) {
	subs, err := orm.TenantSubscription.Query().
		Where(
			tenantsubscription.DormantAtNotNil(),
			tenantsubscription.PendingPurgeEQ(false),
			tenantsubscription.PurgeGraceEndsAtLT(now),
			tenantsubscription.StatusNotIn(
				tenantsubscription.StatusSUSPENDED,
				tenantsubscription.StatusCANCELLED,
			),
		).
		All(ctx)
	if err != nil {
		log.Warn("dormancy: query grace-elapsed failed", zap.Error(err))
		return
	}
	for _, sub := range subs {
		if svc.IsExemptTenant(ctx, sub.TenantID) {
			continue
		}
		tx, err := orm.Tx(ctx)
		if err != nil {
			continue
		}
		if _, err := tx.TenantSubscription.UpdateOneID(sub.ID).
			SetStatus(tenantsubscription.StatusSUSPENDED).
			SetPendingPurge(true).
			Save(ctx); err != nil {
			_ = tx.Rollback()
			continue
		}
		svc.WriteOutboxEventPublic(ctx, tx, sub.TenantID, "subscription", sub.ID, "dormancy.suspended", map[string]any{
			"tenant_id":     sub.TenantID.String(),
			"pending_purge": true,
			"notification": map[string]any{
				"target":  "tenant_admin",
				"subject": "Your Codevertex account has been suspended",
				"message": "Your account stayed inactive through the grace period and is now suspended and queued for data deletion. Contact support immediately to restore it before your data is removed.",
			},
		})
		if err := tx.Commit(); err != nil {
			log.Warn("dormancy: commit suspend failed", zap.String("tenant_id", sub.TenantID.String()), zap.Error(err))
			continue
		}
		log.Info("dormancy: account suspended + queued for purge", zap.String("tenant_id", sub.TenantID.String()))
	}
}
