package jobs

import (
	"context"
	"fmt"
	"math"
	"time"

	"go.uber.org/zap"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/tenantsubscription"
	"github.com/bengobox/subscription-service/internal/modules/subscriptions"
)

// graceUntilOf returns the grace deadline stored in subscription metadata, if any.
func graceUntilOf(sub *ent.TenantSubscription) (time.Time, bool) {
	if sub.Metadata == nil {
		return time.Time{}, false
	}
	s, ok := sub.Metadata["grace_until"].(string)
	if !ok || s == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, false
	}
	return t.UTC(), true
}

// mergeMeta returns a copy of existing with updates applied.
func mergeMeta(existing, updates map[string]any) map[string]any {
	out := make(map[string]any, len(existing)+len(updates))
	for k, v := range existing {
		out[k] = v
	}
	for k, v := range updates {
		out[k] = v
	}
	return out
}

// daysRemaining returns the whole days (rounded up, min 0) until t.
func daysRemaining(now, t time.Time) int {
	d := t.Sub(now).Hours() / 24
	if d < 0 {
		return 0
	}
	return int(math.Ceil(d))
}

// graceEventPayload builds the payload for grace_started / grace_reminder events using
// the invoice markers recorded by the invoice generation job.
func graceEventPayload(sub *ent.TenantSubscription, graceUntil, now time.Time) map[string]any {
	payURL, _ := sub.Metadata["last_invoice_pay_url"].(string)
	invNo, _ := sub.Metadata["last_invoice_number"].(string)
	currency, _ := sub.Metadata["last_invoice_currency"].(string)
	total, _ := sub.Metadata["last_invoice_total"].(float64)
	amount := ""
	if total > 0 {
		amount = fmt.Sprintf("%s %.2f", currency, total)
	}
	return map[string]any{
		"tenant_id":      sub.TenantID.String(),
		"days_remaining": daysRemaining(now, graceUntil),
		"grace_ends_at":  graceUntil.Format("02 Jan 2006"),
		"amount":         amount,
		"invoice_number": invNo,
		"pay_link":       payURL,
		"notification": map[string]any{
			"target":          "tenant_admin",
			"recipient_email": stringFromMeta(sub.Metadata, "billing_email"),
		},
	}
}

func stringFromMeta(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	s, _ := m[key].(string)
	return s
}

// StartGraceReminderJob emits one grace_reminder per day (with a decrementing countdown)
// for every subscription currently in its grace window, until it is paid or blocked.
func StartGraceReminderJob(ctx context.Context, log *zap.Logger, orm *ent.Client, svc *subscriptions.Service) {
	log = log.Named("grace.reminder.job")
	ticker := time.NewTicker(6 * time.Hour) // check a few times a day; dedupe ensures one email/day
	defer ticker.Stop()

	sendGraceReminders(ctx, log, orm, svc)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sendGraceReminders(ctx, log, orm, svc)
		}
	}
}

func sendGraceReminders(ctx context.Context, log *zap.Logger, orm *ent.Client, svc *subscriptions.Service) {
	now := time.Now().UTC()
	today := now.Format("2006-01-02")

	// Candidates: ACTIVE subs past period end (in grace). Grace state lives in metadata,
	// so filter precisely in Go.
	subs, err := orm.TenantSubscription.Query().
		Where(
			tenantsubscription.StatusEQ(tenantsubscription.StatusACTIVE),
			tenantsubscription.CurrentPeriodEndLT(now),
		).
		WithTenant().
		All(ctx)
	if err != nil {
		log.Error("grace reminder: query failed", zap.Error(err))
		return
	}

	sent := 0
	for _, sub := range subs {
		// Defensive: never send grace reminders to exempt (demo/platform) tenants.
		if sub.Edges.Tenant != nil && exemptTenantSlug(sub.Edges.Tenant.Slug) {
			continue
		}
		graceUntil, inGrace := graceUntilOf(sub)
		if !inGrace || !now.Before(graceUntil) {
			continue // not in grace, or grace elapsed (expiry job handles blocking)
		}
		if last, _ := sub.Metadata["last_grace_reminder_date"].(string); last == today {
			continue // already reminded today
		}

		tx, err := orm.Tx(ctx)
		if err != nil {
			log.Error("grace reminder: tx start failed", zap.String("sub_id", sub.ID.String()), zap.Error(err))
			continue
		}
		meta := mergeMeta(sub.Metadata, map[string]any{"last_grace_reminder_date": today})
		if _, err := tx.TenantSubscription.UpdateOneID(sub.ID).SetMetadata(meta).Save(ctx); err != nil {
			_ = tx.Rollback()
			log.Error("grace reminder: update failed", zap.String("sub_id", sub.ID.String()), zap.Error(err))
			continue
		}
		svc.WriteOutboxEventPublic(ctx, tx, sub.TenantID, "subscription", sub.ID, "grace_reminder", graceEventPayload(sub, graceUntil, now))
		if err := tx.Commit(); err != nil {
			log.Error("grace reminder: commit failed", zap.String("sub_id", sub.ID.String()), zap.Error(err))
			continue
		}
		sent++
	}

	if sent > 0 {
		log.Info("grace reminder: dispatched", zap.Int("count", sent))
	}
}
