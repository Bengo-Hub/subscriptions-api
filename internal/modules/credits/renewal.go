package credits

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/productsubscription"
	"github.com/bengobox/subscription-service/internal/ent/tenantsubscription"
	"github.com/bengobox/subscription-service/internal/modules/billing"
	"github.com/bengobox/subscription-service/internal/modules/subscriptions"
)

// RenewTiers extends every auto-renewing tier of the tenant after a subscription invoice was
// paid. The invoice carried one line per tier for the whole billing period, so each tier is
// extended by that many months. A tier already granted by this same payment (a checkout add-on)
// is skipped, and each grant is keyed by payment and tier, so redeliveries change nothing.
func (s *Service) RenewTiers(ctx context.Context, tenantID uuid.UUID, paymentID string) error {
	sub, err := s.subs.SubscriptionByTenant(ctx, tenantID)
	if err != nil {
		return nil //nolint:nilerr // no subscription, nothing to renew
	}
	months := subscriptions.BillingCycleMonths(string(sub.BillingCycle))
	if months <= 0 {
		months = 1
	}
	for _, code := range billing.MeteredServiceCodes() {
		spec, _ := billing.LookupMeteredService(code)
		if len(spec.Tiers) == 0 || spec.ProductCode == "" {
			continue
		}
		ps, err := s.orm.ProductSubscription.Query().
			Where(
				productsubscription.TenantSubscriptionIDEQ(sub.ID),
				productsubscription.ProductCodeEQ(spec.ProductCode),
				productsubscription.StatusEQ(productsubscription.StatusActive),
			).Only(ctx)
		if err != nil {
			if ent.IsNotFound(err) {
				continue
			}
			return err
		}
		if renew, ok := ps.Metadata[MetaTierAutoRenew].(bool); ok && !renew {
			continue
		}
		tierCode, _ := ps.Metadata[MetaTierCode].(string)
		tier, ok := spec.Tier(tierCode)
		if !ok {
			continue
		}
		if last, _ := ps.Metadata["last_grant_key"].(string); strings.HasPrefix(last, "intent:"+paymentID) {
			continue
		}
		offer := PurchaseOffer{Service: spec, Kind: KindTier, Code: tier.Code, Units: tier.MonthlyUnit, PriceKES: tier.PriceKES,
			Label: fmt.Sprintf("%s %s monthly plan", spec.Label, tier.Name)}
		if err := s.Grant(ctx, tenantID, offer, months, "renew:"+paymentID+":"+tier.Code, uuid.Nil); err != nil {
			return err
		}
	}
	return nil
}

// SetTierAutoRenew switches renewal of a tiered service on or off. Access continues to the end
// of the paid period either way.
func (s *Service) SetTierAutoRenew(ctx context.Context, tenantID uuid.UUID, service string, on bool) error {
	spec, ok := billing.LookupMeteredService(service)
	if !ok || len(spec.Tiers) == 0 {
		return fmt.Errorf("%q has no recurring plan", service)
	}
	sub, err := s.subs.SubscriptionByTenant(ctx, tenantID)
	if err != nil {
		return err
	}
	ps, err := s.orm.ProductSubscription.Query().
		Where(productsubscription.TenantSubscriptionIDEQ(sub.ID), productsubscription.ProductCodeEQ(spec.ProductCode)).
		Only(ctx)
	if err != nil {
		return fmt.Errorf("no %s plan to change: %w", spec.Label, err)
	}
	meta := map[string]any{}
	for k, v := range ps.Metadata {
		meta[k] = v
	}
	meta[MetaTierAutoRenew] = on
	_, err = s.orm.ProductSubscription.UpdateOneID(ps.ID).SetMetadata(meta).Save(ctx)
	return err
}

// RefillAllowances brings every recurring allowance wallet in line with the current calendar
// month: live tiers get their monthly units, lapsed tiers drop to zero, and plans that grant a
// monthly allowance (AI credits) refill it. Each reset is keyed by month, so running the job
// any number of times a day, on any number of replicas, changes nothing after the first run.
func (s *Service) RefillAllowances(ctx context.Context, now time.Time) (int, error) {
	window := now.UTC().Format("2006-01")
	applied := 0
	const page = 500
	last := uuid.Nil
	for {
		subs, err := s.orm.TenantSubscription.Query().
			Where(
				tenantsubscription.StatusIn(tenantsubscription.StatusACTIVE, tenantsubscription.StatusTRIAL),
				tenantsubscription.IDGT(last),
			).
			WithPlan().
			Order(ent.Asc(tenantsubscription.FieldID)).
			Limit(page).
			All(ctx)
		if err != nil {
			return applied, err
		}
		if len(subs) == 0 {
			return applied, nil
		}
		for _, sub := range subs {
			last = sub.ID
			n, err := s.refillOne(ctx, sub, window, now)
			if err != nil {
				s.log.Warn("allowance refill failed", zap.String("tenant_id", sub.TenantID.String()), zap.Error(err))
				continue
			}
			applied += n
		}
	}
}

func (s *Service) refillOne(ctx context.Context, sub *ent.TenantSubscription, window string, now time.Time) (int, error) {
	n := 0
	for _, code := range billing.MeteredServiceCodes() {
		spec, _ := billing.LookupMeteredService(code)
		if spec.QuotaTag == "" {
			continue
		}
		target := int64(-1)
		switch {
		case len(spec.Tiers) > 0:
			ps, err := s.orm.ProductSubscription.Query().
				Where(productsubscription.TenantSubscriptionIDEQ(sub.ID), productsubscription.ProductCodeEQ(spec.ProductCode)).
				Only(ctx)
			if err != nil {
				if ent.IsNotFound(err) {
					continue
				}
				return n, err
			}
			target = 0
			if tier, live := liveTier(spec, ps, now); live && tier.MonthlyUnit > 0 {
				target = tier.MonthlyUnit
			}
		case spec.PlanQuotaKey != "":
			if limit := planLimit(sub, spec.PlanQuotaKey); limit > 0 {
				target = limit
			}
		}
		if target < 0 {
			continue
		}
		ok, err := s.wallets.SetQuota(ctx, sub.TenantID, spec.QuotaTag, target, "w:"+window, spec.Label+" monthly allowance")
		if err != nil {
			return n, err
		}
		if ok {
			n++
		}
	}
	return n, nil
}
