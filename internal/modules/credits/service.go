// Package credits applies subscription entitlement and pricing rules on top of the shared
// credits ledger in billing/credit_ledger.go. It decides whether a tenant may spend a credit
// service (SMS, WhatsApp, AI credits), sells bundles and tiers through Treasury intents, and
// keeps recurring allowances in step with the subscription. Wallet arithmetic itself never
// lives here: every movement goes through billing.TokenWalletService.
package credits

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/productsubscription"
	"github.com/bengobox/subscription-service/internal/ent/serviceconfig"
	"github.com/bengobox/subscription-service/internal/ent/tenantsubscription"
	"github.com/bengobox/subscription-service/internal/modules/billing"
	"github.com/bengobox/subscription-service/internal/modules/subscriptions"
)

// Reason codes returned to callers when a spend is refused, in addition to the ledger's own
// billing.ReasonInsufficient and billing.ReasonOverageCap.
const (
	ReasonNoSubscription      = "no_subscription"
	ReasonServiceNotActivated = "service_not_activated"
	ReasonUnknownService      = "unknown_service"
)

// OverageCapConfigKey is the platform-level service_configs key holding the maximum pending
// (not yet invoiced) overage in KES before sends pause.
const OverageCapConfigKey = "messaging.overage_cap_kes"

// DefaultOverageCapKES applies when no platform config row exists.
const DefaultOverageCapKES = 500.0

// Service is the credits entitlement and fulfilment layer.
type Service struct {
	log     *zap.Logger
	orm     *ent.Client
	wallets *billing.TokenWalletService
	subs    *subscriptions.Service

	capMu    sync.RWMutex
	capValue float64
	capAt    time.Time
}

// New creates the credits service.
func New(log *zap.Logger, orm *ent.Client, wallets *billing.TokenWalletService, subs *subscriptions.Service) *Service {
	return &Service{log: log.Named("credits"), orm: orm, wallets: wallets, subs: subs}
}

// Wallets exposes the shared ledger for read endpoints.
func (s *Service) Wallets() *billing.TokenWalletService { return s.wallets }

// Coverage is a tenant's entitlement to one credit service right now.
type Coverage struct {
	Sub *ent.TenantSubscription
	// Exempt tenants (demo, platform, flagged) bypass entitlement and billing entirely.
	Exempt bool
	// Covered is true when the tenant may spend existing units.
	Covered bool
	Reason  string
	// OverageOK is true when spending beyond units is allowed and billed at the unit price.
	OverageOK bool
	// Unlimited is true when the recurring allowance has no cap (unlimited tier or plan).
	Unlimited bool
	// QuotaLive is true when the recurring allowance wallet applies to this spend.
	QuotaLive bool
	// Product is the activation row, when the service has one.
	Product *ent.ProductSubscription
}

// Coverage resolves the entitlement of tenantID to the service.
func (s *Service) Coverage(ctx context.Context, tenantID uuid.UUID, spec billing.MeteredService) (Coverage, error) {
	if s.subs.IsExemptTenant(ctx, tenantID) {
		return Coverage{Exempt: true, Covered: true}, nil
	}
	sub, err := s.subs.SubscriptionByTenant(ctx, tenantID)
	if err != nil {
		if ent.IsNotFound(err) {
			return Coverage{Reason: ReasonNoSubscription}, nil
		}
		return Coverage{}, err
	}
	cov := Coverage{Sub: sub}
	if sub.Status != tenantsubscription.StatusACTIVE && sub.Status != tenantsubscription.StatusTRIAL {
		cov.Reason = ReasonNoSubscription
		return cov, nil
	}

	if spec.ProductCode != "" {
		ps, err := s.orm.ProductSubscription.Query().
			Where(
				productsubscription.TenantSubscriptionIDEQ(sub.ID),
				productsubscription.ProductCodeEQ(spec.ProductCode),
				productsubscription.StatusEQ(productsubscription.StatusActive),
			).Only(ctx)
		if err != nil {
			if ent.IsNotFound(err) {
				cov.Reason = ReasonServiceNotActivated
				return cov, nil
			}
			return Coverage{}, err
		}
		cov.Product = ps
	}
	cov.Covered = true

	switch {
	case len(spec.Tiers) > 0:
		// Recurring tier: overage and the allowance apply only while the paid period is live.
		if tier, live := liveTier(spec, cov.Product, time.Now()); live {
			cov.QuotaLive = true
			cov.OverageOK = spec.AllowsOverage()
			cov.Unlimited = tier.MonthlyUnit < 0
		}
	case spec.PlanQuotaKey != "":
		limit := planLimit(sub, spec.PlanQuotaKey)
		cov.QuotaLive = limit != 0
		cov.Unlimited = limit < 0
	default:
		cov.OverageOK = spec.AllowsOverage()
	}
	return cov, nil
}

// Decision is the outcome of a spend request.
type Decision struct {
	Allowed bool   `json:"allowed"`
	Exempt  bool   `json:"exempt,omitempty"`
	Reason  string `json:"reason,omitempty"`
	billing.ConsumeResult
}

// ConsumeInput is one spend request from a service that sends or meters.
type ConsumeInput struct {
	TenantID uuid.UUID
	Service  string
	Units    int64
	// Key makes the spend idempotent, for example the message id.
	Key         string
	Description string
	// PlatformScope marks platform-owned sends (OTP, platform invoices) that are never billed.
	PlatformScope bool
}

// Consume decides and records a spend. Exempt tenants and platform-scope sends are allowed and
// write nothing.
func (s *Service) Consume(ctx context.Context, in ConsumeInput) (Decision, error) {
	spec, ok := billing.LookupMeteredService(in.Service)
	if !ok || spec.ProductCode == "" && spec.PlanQuotaKey == "" {
		return Decision{Reason: ReasonUnknownService}, nil
	}
	if in.Key == "" {
		return Decision{}, errors.New("idempotency key is required")
	}
	if in.PlatformScope {
		return Decision{Allowed: true, Exempt: true}, nil
	}
	cov, err := s.Coverage(ctx, in.TenantID, spec)
	if err != nil {
		return Decision{}, err
	}
	if cov.Exempt {
		return Decision{Allowed: true, Exempt: true}, nil
	}
	if !cov.Covered {
		return Decision{Reason: cov.Reason}, nil
	}
	req := billing.ConsumeRequest{
		TenantID: in.TenantID, Service: spec, Units: in.Units, Key: in.Key, Description: in.Description,
		SubscriptionID: cov.Sub.ID, AllowOverage: cov.OverageOK, UnlimitedQuota: cov.Unlimited && cov.QuotaLive,
	}
	if !cov.QuotaLive {
		// An expired tier must not keep spending its stale allowance between the expiry and the
		// next quota job run.
		req.Service.QuotaTag = ""
	}
	if cov.OverageOK {
		req.OverageCapKES = s.overageCap(ctx)
	}
	res, err := s.wallets.Consume(ctx, req)
	if err != nil {
		return Decision{}, err
	}
	return Decision{Allowed: res.Allowed, Reason: res.Reason, ConsumeResult: res}, nil
}

// Refund reverses a spend, for example when the provider rejected the message.
func (s *Service) Refund(ctx context.Context, tenantID uuid.UUID, service, consumeKey, refundKey string) (billing.ConsumeResult, error) {
	spec, ok := billing.LookupMeteredService(service)
	if !ok {
		return billing.ConsumeResult{}, fmt.Errorf("unknown credit service %q", service)
	}
	var subID uuid.UUID
	if sub, err := s.subs.SubscriptionByTenant(ctx, tenantID); err == nil {
		subID = sub.ID
	}
	return s.wallets.RefundConsume(ctx, tenantID, spec, subID, consumeKey, refundKey)
}

// overageCap returns the platform pause threshold, cached for a minute.
func (s *Service) overageCap(ctx context.Context) float64 {
	s.capMu.RLock()
	if !s.capAt.IsZero() && time.Since(s.capAt) < time.Minute {
		v := s.capValue
		s.capMu.RUnlock()
		return v
	}
	s.capMu.RUnlock()

	v := DefaultOverageCapKES
	cfg, err := s.orm.ServiceConfig.Query().
		Where(serviceconfig.ConfigKeyEQ(OverageCapConfigKey), serviceconfig.TenantIDIsNil()).
		Only(ctx)
	if err == nil {
		if f, perr := strconv.ParseFloat(cfg.ConfigValue, 64); perr == nil && f >= 0 {
			v = f
		}
	}
	s.capMu.Lock()
	s.capValue, s.capAt = v, time.Now()
	s.capMu.Unlock()
	return v
}

// OverageCap returns the current platform overage cap in KES (zero means uncapped).
func (s *Service) OverageCap(ctx context.Context) float64 { return s.overageCap(ctx) }

func planLimit(sub *ent.TenantSubscription, key string) int64 {
	if sub.Edges.Plan == nil {
		return 0
	}
	switch v := sub.Edges.Plan.TierLimitsJSON[key].(type) {
	case float64:
		return int64(v)
	case int:
		return int64(v)
	case int64:
		return v
	}
	return 0
}
