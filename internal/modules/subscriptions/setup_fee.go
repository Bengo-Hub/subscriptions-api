package subscriptions

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/serviceconfig"
	"github.com/bengobox/subscription-service/internal/ent/subscriptionplan"
	"github.com/bengobox/subscription-service/internal/ent/tenantsubscription"
)

// Setup fee policy. The one-time setup fee is charged once, when a tenant first pays for a
// subscription, and only while the switch is on for that tenant. A tenant row wins over the
// platform row (tenant_id NULL); with neither, the fee is on.
const SetupFeeEnabledConfigKey = "subscriptions.setup_fee_enabled"

// paidPeriodMinAge separates a period_start stamped by a payment from the one set at creation.
const paidPeriodMinAge = time.Hour

// SetupFeePolicy is the switch in force for a tenant, with both levels it was built from.
type SetupFeePolicy struct {
	Enabled  bool   `json:"enabled"`
	Source   string `json:"source"`
	Platform *bool  `json:"platform"`
	Tenant   *bool  `json:"tenant"`
}

// SetupFeeQuote is what a checkout will charge for the setup fee, before any billing period waiver.
type SetupFeeQuote struct {
	SetupFee        float64 `json:"setup_fee"`
	HasSubscription bool    `json:"has_subscription"`
}

// SetupFeePolicyFor resolves the switch for tenantID.
func (s *Service) SetupFeePolicyFor(ctx context.Context, tenantID uuid.UUID) (SetupFeePolicy, error) {
	rows, err := s.client.ServiceConfig.Query().
		Where(
			serviceconfig.ConfigKeyEQ(SetupFeeEnabledConfigKey),
			serviceconfig.Or(serviceconfig.TenantIDIsNil(), serviceconfig.TenantIDEQ(tenantID)),
		).
		Order(ent.Asc(serviceconfig.FieldUpdatedAt)).
		All(ctx)
	if err != nil {
		return SetupFeePolicy{}, fmt.Errorf("read setup fee policy: %w", err)
	}
	return resolveSetupFeePolicy(rows), nil
}

// resolveSetupFeePolicy folds config rows (oldest first) into a policy. Values that do not parse
// are ignored.
func resolveSetupFeePolicy(rows []*ent.ServiceConfig) SetupFeePolicy {
	p := SetupFeePolicy{Enabled: true, Source: PolicySourceDefault}
	for _, row := range rows {
		v, ok := parseConfigBool(row.ConfigValue)
		if !ok {
			continue
		}
		if row.TenantID != nil {
			p.Tenant = &v
		} else {
			p.Platform = &v
		}
	}
	switch {
	case p.Tenant != nil:
		p.Enabled, p.Source = *p.Tenant, PolicySourceTenant
	case p.Platform != nil:
		p.Enabled, p.Source = *p.Platform, PolicySourcePlatform
	}
	return p
}

// SetTenantSetupFeePolicy stores a tenant's own switch. nil removes it, so the tenant follows the
// platform setting again.
func (s *Service) SetTenantSetupFeePolicy(ctx context.Context, tenantID uuid.UUID, enabled *bool) (SetupFeePolicy, error) {
	if tenantID == uuid.Nil {
		return SetupFeePolicy{}, fmt.Errorf("tenant_id is required")
	}
	if enabled == nil {
		if err := s.deleteConfig(ctx, tenantID, SetupFeeEnabledConfigKey); err != nil {
			return SetupFeePolicy{}, err
		}
		return s.SetupFeePolicyFor(ctx, tenantID)
	}
	tid := tenantID
	if err := s.putConfig(ctx, &tid, SetupFeeEnabledConfigKey, strconv.FormatBool(*enabled), "bool",
		"Tenant override: whether this tenant is charged the one-time setup fee when first subscribing"); err != nil {
		return SetupFeePolicy{}, err
	}
	return s.SetupFeePolicyFor(ctx, tenantID)
}

// hasPaidForSubscription reports whether the tenant has already paid for a period, so it is no
// longer subscribing for the first time. setup_fee_charged_at cannot answer this alone: it stays
// nil when the switch was off, when a checkout was overtaken by a newer one, and on subscriptions
// that paid before checkouts carried the fee. A trial keeps trial_ends_at until its first payment,
// and a first payment moves period_start past the creation time.
func hasPaidForSubscription(sub *ent.TenantSubscription) bool {
	if renewed, _ := sub.Metadata[MetaRenewedIntents].([]any); len(renewed) > 0 {
		return true
	}
	return sub.Status != tenantsubscription.StatusTRIAL &&
		sub.TrialEndsAt == nil &&
		sub.CurrentPeriodStart.After(sub.CreatedAt.Add(paidPeriodMinAge))
}

// setupFeeOwed reports the uncharged fee on a subscription that has not paid for a period yet. A
// plan change never reaches here with a fee once the first period is paid.
func setupFeeOwed(sub *ent.TenantSubscription) float64 {
	if sub.SetupFeeAmount <= 0 || sub.SetupFeeChargedAt != nil || hasPaidForSubscription(sub) {
		return 0
	}
	return sub.SetupFeeAmount
}

// setupFeeEnabled reads the switch. A read failure leaves the fee off: a missed fee can be billed
// by hand, an overcharge reaches the tenant.
func (s *Service) setupFeeEnabled(ctx context.Context, tenantID uuid.UUID) bool {
	p, err := s.SetupFeePolicyFor(ctx, tenantID)
	if err != nil {
		s.log.Warn("setup fee policy unavailable, not charging", zap.String("tenant_id", tenantID.String()), zap.Error(err))
		return false
	}
	return p.Enabled
}

// SetupFeeDue reports whether the next invoice must bill the setup fee. Shared by the invoice
// builder and its mark-charged step so the fee is only marked charged when it was billed.
func (s *Service) SetupFeeDue(ctx context.Context, sub *ent.TenantSubscription) bool {
	return setupFeeOwed(sub) > 0 &&
		!CycleWaivesSetupFee(string(sub.BillingCycle)) &&
		s.setupFeeEnabled(ctx, sub.TenantID)
}

// checkoutSetupFee is the fee a checkout for plan charges, before the billing period waiver. A
// tenant with no subscription yet pays the plan's fee; an existing one pays its own snapshot, so
// moving to a dearer plan never raises it.
func (s *Service) checkoutSetupFee(ctx context.Context, tenantID uuid.UUID, sub *ent.TenantSubscription, plan *ent.SubscriptionPlan) float64 {
	fee := plan.SetupFee
	if sub != nil {
		fee = setupFeeOwed(sub)
	}
	if fee <= 0 || !s.setupFeeEnabled(ctx, tenantID) {
		return 0
	}
	return fee
}

// QuoteSetupFee returns what a checkout for planCode will charge this tenant, so a checkout page
// shows the amount the payment will actually ask for.
func (s *Service) QuoteSetupFee(ctx context.Context, tenantID uuid.UUID, planCode string) (*SetupFeeQuote, error) {
	plan, err := s.client.SubscriptionPlan.Query().
		Where(subscriptionplan.PlanCodeEQ(planCode), subscriptionplan.IsActiveEQ(true)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("plan not found: %s", planCode)
		}
		return nil, fmt.Errorf("lookup plan: %w", err)
	}
	sub, err := s.client.TenantSubscription.Query().
		Where(tenantsubscription.TenantIDEQ(tenantID)).
		Only(ctx)
	if err != nil && !ent.IsNotFound(err) {
		return nil, fmt.Errorf("lookup subscription: %w", err)
	}
	return &SetupFeeQuote{
		SetupFee:        s.checkoutSetupFee(ctx, tenantID, sub, plan),
		HasSubscription: sub != nil,
	}, nil
}
