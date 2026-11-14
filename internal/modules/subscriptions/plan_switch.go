package subscriptions

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/productsubscription"
	"github.com/bengobox/subscription-service/internal/ent/tenantsubscription"
)

// Plan switching rules (owner, 2026-11-14):
//   - A cheaper plan starts at the next renewal: the tenant keeps what it paid for until then.
//   - A dearer plan applies at once and the rest of the current period is billed at the
//     difference (ProratedUpgradeCharge, invoiced through SetUpgradeInvoicer).
//   - Features, limits and included credits follow the plan in force: an upgrade tops the
//     included allowance up now, a downgrade changes it when the downgrade takes effect
//     (SetPlanAppliedHook).
//   - Product rows follow the plan (SyncPlanProducts): the old plan's products the new plan does
//     not include are switched off, admin overlay plans and paid add-ons are left alone.

// Product row markers written by SyncPlanProducts.
const (
	// MetaDeactivatedBy marks a product row switched off by a plan change, so a later move back
	// to a plan that includes the product switches it on again. A row the tenant or an admin
	// switched off carries no marker and is never switched on here.
	MetaDeactivatedBy = "deactivated_by"
	// DeactivatedByPlanChange is MetaDeactivatedBy's value for a plan change.
	DeactivatedByPlanChange = "plan_change"
	// MetaDeactivatedFromPlan is the plan the subscription left when the row was switched off.
	MetaDeactivatedFromPlan = "deactivated_from_plan"
)

// SetPlanAppliedHook registers what runs after a plan change took effect (the credits module
// moves the included allowance). Registered by the app so this package does not depend on credits.
func (s *Service) SetPlanAppliedHook(fn func(ctx context.Context, sub *ent.TenantSubscription, from, to *ent.SubscriptionPlan)) {
	s.planApplied = fn
}

// SetUpgradeInvoicer registers the billing hook that invoices the prorated rest of the period
// after an upgrade applied at once.
func (s *Service) SetUpgradeInvoicer(fn func(ctx context.Context, sub *ent.TenantSubscription, from, to *ent.SubscriptionPlan, charge ProratedCharge)) {
	s.invoiceUpgrade = fn
}

// NotifyPlanApplied runs the plan-applied hook for a move from one plan to another that is
// already committed. Paths outside this package (the platform admin handlers) call it too.
func (s *Service) NotifyPlanApplied(ctx context.Context, sub *ent.TenantSubscription, fromPlanID uuid.UUID) {
	if s.planApplied == nil || sub == nil || fromPlanID == sub.PlanID {
		return
	}
	from, err := s.client.SubscriptionPlan.Get(ctx, fromPlanID)
	if err != nil {
		s.log.Warn("plan applied: old plan not found", zap.String("tenant_id", sub.TenantID.String()), zap.Error(err))
		return
	}
	to, err := s.client.SubscriptionPlan.Get(ctx, sub.PlanID)
	if err != nil {
		s.log.Warn("plan applied: new plan not found", zap.String("tenant_id", sub.TenantID.String()), zap.Error(err))
		return
	}
	s.planApplied(ctx, sub, from, to)
}

// ProratedCharge is the price difference for the rest of the current period after an upgrade.
type ProratedCharge struct {
	// NetAmount is the amount before VAT, rounded to the cent.
	NetAmount float64
	// Fraction is the share of the period left (0 to 1).
	Fraction float64
	// MonthlyDiff is the monthly price difference between the plans.
	MonthlyDiff float64
	// Months is the billing period length in months.
	Months int
	// PeriodStart and PeriodEnd bound the period being charged; From is when the upgrade applied.
	PeriodStart, PeriodEnd, From time.Time
}

// ProratedUpgradeCharge prices an upgrade applied at now: the monthly price difference for every
// month of the billing period, times the share of the period still to run. Zero when the new plan
// is not dearer or no paid time is left.
func ProratedUpgradeCharge(sub *ent.TenantSubscription, oldPrice, newPrice float64, now time.Time) ProratedCharge {
	months := BillingCycleMonths(string(sub.BillingCycle))
	if months <= 0 {
		months = 1
	}
	c := ProratedCharge{Months: months, PeriodStart: sub.CurrentPeriodStart, PeriodEnd: sub.CurrentPeriodEnd, From: now}
	diff := newPrice - oldPrice
	total := sub.CurrentPeriodEnd.Sub(sub.CurrentPeriodStart)
	left := sub.CurrentPeriodEnd.Sub(now)
	if diff <= 0 || total <= 0 || left <= 0 {
		return c
	}
	if left > total {
		left = total
	}
	c.MonthlyDiff = diff
	c.Fraction = float64(left) / float64(total)
	c.NetAmount = math.Round(diff*float64(months)*c.Fraction*100) / 100
	return c
}

// hasPaidTimeLeft reports whether the subscription is a paid, recurring, ACTIVE one whose current
// period has not ended: the only case where a switch is timed against the renewal date. A trial,
// a free plan, a one-time licence or a lapsed period switches at once.
func hasPaidTimeLeft(sub *ent.TenantSubscription, current *ent.SubscriptionPlan, now time.Time) bool {
	return sub.Status == tenantsubscription.StatusACTIVE &&
		sub.CurrentPeriodEnd.After(now) &&
		current.BillingCycle != "ONE_TIME" &&
		EffectivePrice(sub, current) > 0
}

// SyncPlanProducts brings a subscription's product rows in line with its plan after a move from
// oldPlanID (pass uuid.Nil when unknown, which only adds rows):
//   - rows the new plan adds are inserted (EnsureMissingPlanDefaultProducts);
//   - rows an earlier plan change switched off are switched on again when the new plan, or an
//     admin overlay plan, includes them;
//   - active rows the OLD plan gave that neither the new plan, an overlay plan nor the core
//     platform products include are switched off and marked, so a move back restores them.
//
// Rows that carry an overlay plan, add-ons the old plan did not give (sms_messaging,
// whatsapp_messaging) and rows a tenant or admin switched off are never touched. client may be a
// transactional client. Subscriptions that are not ACTIVE or TRIAL are skipped.
func SyncPlanProducts(ctx context.Context, client *ent.Client, tenantSubscriptionID, oldPlanID uuid.UUID) (deactivated, reactivated []string, err error) {
	sub, err := client.TenantSubscription.Get(ctx, tenantSubscriptionID)
	if err != nil {
		return nil, nil, fmt.Errorf("load tenant subscription %s: %w", tenantSubscriptionID, err)
	}
	if !grantsPlanProducts(sub.Status) {
		return nil, nil, nil
	}
	if _, err := EnsureMissingPlanDefaultProducts(ctx, client, sub.ID); err != nil {
		return nil, nil, err
	}

	keep := map[string]bool{}
	for _, c := range CorePlatformProducts {
		keep[c] = true
	}
	newCodes, err := PlanDefaultProductCodes(ctx, client, sub.PlanID)
	if err != nil {
		return nil, nil, err
	}
	for _, c := range newCodes {
		keep[c] = true
	}
	rows, err := client.ProductSubscription.Query().
		Where(productsubscription.TenantSubscriptionIDEQ(sub.ID)).
		All(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("query product rows: %w", err)
	}
	// Admin overlay plans keep their own row and every product they bring.
	for _, r := range rows {
		if r.OverridePlanID == nil || !isActiveProductStatus(r.Status) {
			continue
		}
		keep[r.ProductCode] = true
		codes, err := PlanDefaultProductCodes(ctx, client, *r.OverridePlanID)
		if err != nil {
			return nil, nil, err
		}
		for _, c := range codes {
			keep[c] = true
		}
	}
	oldGave := map[string]bool{}
	if oldPlanID != uuid.Nil && oldPlanID != sub.PlanID {
		codes, err := PlanDefaultProductCodes(ctx, client, oldPlanID)
		if err != nil {
			return nil, nil, err
		}
		for _, c := range codes {
			oldGave[c] = true
		}
	}

	now := time.Now().UTC()
	for _, r := range rows {
		if r.OverridePlanID != nil {
			continue
		}
		marked, _ := r.Metadata[MetaDeactivatedBy].(string)
		switch {
		case keep[r.ProductCode] && r.Status == productsubscription.StatusInactive && marked == DeactivatedByPlanChange:
			meta := cloneMetadata(r.Metadata)
			delete(meta, MetaDeactivatedBy)
			delete(meta, MetaDeactivatedFromPlan)
			if _, err := client.ProductSubscription.UpdateOneID(r.ID).
				SetStatus(productsubscription.StatusActive).SetActivatedAt(now).ClearDeactivatedAt().
				SetMetadata(meta).SetUpdatedAt(now).Save(ctx); err != nil {
				return deactivated, reactivated, fmt.Errorf("switch %s back on: %w", r.ProductCode, err)
			}
			reactivated = append(reactivated, r.ProductCode)
		case !keep[r.ProductCode] && oldGave[r.ProductCode] && isActiveProductStatus(r.Status):
			meta := cloneMetadata(r.Metadata)
			meta[MetaDeactivatedBy] = DeactivatedByPlanChange
			meta[MetaDeactivatedFromPlan] = oldPlanID.String()
			if _, err := client.ProductSubscription.UpdateOneID(r.ID).
				SetStatus(productsubscription.StatusInactive).SetDeactivatedAt(now).
				SetMetadata(meta).SetUpdatedAt(now).Save(ctx); err != nil {
				return deactivated, reactivated, fmt.Errorf("switch %s off: %w", r.ProductCode, err)
			}
			deactivated = append(deactivated, r.ProductCode)
		}
	}
	return deactivated, reactivated, nil
}

func isActiveProductStatus(st productsubscription.Status) bool {
	for _, a := range activeProductStatuses {
		if st == a {
			return true
		}
	}
	return false
}
