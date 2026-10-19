package credits

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/modules/billing"
)

// LegacyWallet is one tenant's balances from the retired notifications-api wallet tables.
type LegacyWallet struct {
	TenantID uuid.UUID `json:"tenant_id"`
	// SMSBalance is the whole number of SMS units left (KES balance at KES 1 per SMS).
	SMSBalance int64 `json:"sms_balance"`
	// WhatsAppTier is the mapped tier code (tier_1, tier_2, pro), empty when none.
	WhatsAppTier string `json:"whatsapp_tier"`
	// WhatsAppExpires is when the paid WhatsApp month ends.
	WhatsAppExpires *time.Time `json:"whatsapp_expires_at"`
	// WhatsAppUsed is how many messages the tenant already used in that month.
	WhatsAppUsed int64 `json:"whatsapp_used"`
}

// ImportResult reports what one import row did or would do.
type ImportResult struct {
	TenantID uuid.UUID `json:"tenant_id"`
	Applied  []string  `json:"applied,omitempty"`
	Skipped  string    `json:"skipped,omitempty"`
}

// ImportLegacy moves a tenant's old balances into the ledger. It is additive and idempotent: SMS
// units arrive as one adjustment keyed per tenant, the WhatsApp tier is activated until its old
// expiry with the new quota less what was already used, and a second run changes nothing. A
// tenant with no subscription here is skipped and reported, because it could not send anyway.
// With dryRun it only reports.
func (s *Service) ImportLegacy(ctx context.Context, w LegacyWallet, dryRun bool) (ImportResult, error) {
	res := ImportResult{TenantID: w.TenantID}
	sub, err := s.subs.SubscriptionByTenant(ctx, w.TenantID)
	if err != nil {
		if ent.IsNotFound(err) {
			res.Skipped = "no subscription in subscriptions-api"
			return res, nil
		}
		return res, err
	}
	_ = sub

	if w.SMSBalance > 0 {
		res.Applied = append(res.Applied, fmt.Sprintf("sms bundle +%d", w.SMSBalance))
		if !dryRun {
			spec, _ := billing.LookupMeteredService(billing.ServiceSMS)
			if err := s.ensureActivated(ctx, w.TenantID, spec, nil); err != nil {
				return res, err
			}
			if _, _, err := s.wallets.Credit(ctx, billing.CreditRequest{
				TenantID: w.TenantID, ServiceTag: spec.BundleTag, Units: w.SMSBalance, Action: "adjustment",
				Key: "migrate:notifications:sms", RefType: "migration", Description: "Balance moved from the notifications wallet",
			}); err != nil {
				return res, err
			}
		}
	}

	if w.WhatsAppTier != "" && w.WhatsAppExpires != nil && w.WhatsAppExpires.After(time.Now()) {
		spec, _ := billing.LookupMeteredService(billing.ServiceWhatsApp)
		tier, ok := spec.Tier(w.WhatsAppTier)
		if !ok {
			res.Skipped = "unknown whatsapp tier " + w.WhatsAppTier
			return res, nil
		}
		left := tier.MonthlyUnit - w.WhatsAppUsed
		if left < 0 {
			left = 0
		}
		res.Applied = append(res.Applied, fmt.Sprintf("whatsapp %s until %s, quota %d", tier.Code, w.WhatsAppExpires.Format("2006-01-02"), left))
		if !dryRun {
			meta := map[string]any{
				MetaTierCode: tier.Code, MetaTierPeriodEnd: w.WhatsAppExpires.UTC().Format(time.RFC3339),
				MetaTierAutoRenew: true, "last_grant_key": "migrate:notifications:whatsapp",
			}
			if err := s.ensureActivated(ctx, w.TenantID, spec, meta); err != nil {
				return res, err
			}
			if tier.MonthlyUnit >= 0 {
				if _, err := s.wallets.SetQuota(ctx, w.TenantID, spec.QuotaTag, left, "w:"+time.Now().UTC().Format("2006-01"), "Quota moved from the notifications plan"); err != nil {
					return res, err
				}
			}
		}
	}
	if len(res.Applied) == 0 && res.Skipped == "" {
		res.Skipped = "nothing to move"
	}
	return res, nil
}
