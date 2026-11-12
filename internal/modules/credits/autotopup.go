package credits

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	entsql "entgo.io/ent/dialect/sql"
	"entgo.io/ent/dialect/sql/sqljson"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/predicate"
	"github.com/bengobox/subscription-service/internal/ent/productsubscription"
	"github.com/bengobox/subscription-service/internal/ent/tenantsubscription"
	"github.com/bengobox/subscription-service/internal/modules/billing"
)

// Auto top-up buys a chosen bundle with the tenant's saved card when the bundle balance falls below
// a threshold the tenant sets. It is deliberately conservative because it moves money without the
// tenant present:
//   - one attempt per service per day (the Treasury reference carries the day, so even a replayed
//     attempt cannot charge twice),
//   - a monthly spend limit the tenant sets (default KES 5,000),
//   - a declined or refused card switches it off and tells the tenant admin,
//   - a charge whose outcome is unknown is never retried the same day.
//
// Settings live in the service's activation row metadata under "auto_topup". The four WhatsApp
// categories share one activation row, so each keeps its own key (autoTopupKey).

const (
	metaAutoTopup = "auto_topup"
	// DefaultAutoTopupLimitKES is the monthly spend limit when the tenant does not choose one.
	DefaultAutoTopupLimitKES = 5000.0
	// MaxAutoTopupLimitKES bounds the limit a tenant can set.
	MaxAutoTopupLimitKES = 100000.0
	// MaxAutoTopupThreshold bounds the trigger a tenant can set.
	MaxAutoTopupThreshold = 100000

	// EventAutoTopupFailed is the outbox event notifications turns into an email and a WhatsApp message.
	EventAutoTopupFailed = "credits_auto_topup_failed"
)

// ErrAutoTopup marks a rejected auto top-up setting; handlers map it to 400.
var ErrAutoTopup = errors.New("invalid auto top-up setting")

// AutoTopup is the stored and returned setting.
type AutoTopup struct {
	Enabled         bool       `json:"enabled"`
	Threshold       int64      `json:"threshold"`
	BundleCode      string     `json:"bundle_code"`
	MonthlyLimitKES float64    `json:"monthly_limit_kes"`
	SpentMonth      string     `json:"spent_month,omitempty"`
	SpentKES        float64    `json:"spent_kes"`
	LastAttemptDay  string     `json:"last_attempt_day,omitempty"`
	LastChargeAt    *time.Time `json:"last_charge_at,omitempty"`
	DisabledReason  string     `json:"disabled_reason,omitempty"`
}

// AutoTopupInput is what a tenant submits. A nil MonthlyLimitKES means the default.
type AutoTopupInput struct {
	Enabled         bool
	Threshold       int64
	BundleCode      string
	MonthlyLimitKES *float64
}

// autoTopupKey is the activation metadata key holding a service's setting. A service alone on its
// activation row uses "auto_topup". WhatsApp categories share the row: utility keeps "auto_topup"
// because it inherited the old single WhatsApp wallet (and with it the old setting), the others
// use "auto_topup_<service>".
func autoTopupKey(spec billing.MeteredService) string {
	if spec.Group == "" || spec.Code == billing.ServiceWhatsAppUtility {
		return metaAutoTopup
	}
	return metaAutoTopup + "_" + spec.Code
}

func readAutoTopup(meta map[string]any, key string) AutoTopup {
	raw, _ := meta[key].(map[string]any)
	if raw == nil {
		return AutoTopup{}
	}
	var a AutoTopup
	a.Enabled, _ = raw["enabled"].(bool)
	a.Threshold = int64(num(raw["threshold"]))
	a.BundleCode, _ = raw["bundle_code"].(string)
	a.MonthlyLimitKES = num(raw["monthly_limit_kes"])
	a.SpentMonth, _ = raw["spent_month"].(string)
	a.SpentKES = num(raw["spent_kes"])
	a.LastAttemptDay, _ = raw["last_attempt_day"].(string)
	a.DisabledReason, _ = raw["disabled_reason"].(string)
	if s, _ := raw["last_charge_at"].(string); s != "" {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			a.LastChargeAt = &t
		}
	}
	return a
}

func num(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	}
	return 0
}

func (a AutoTopup) toMeta() map[string]any {
	m := map[string]any{
		"enabled": a.Enabled, "threshold": a.Threshold, "bundle_code": a.BundleCode,
		"monthly_limit_kes": a.MonthlyLimitKES, "spent_month": a.SpentMonth, "spent_kes": a.SpentKES,
		"last_attempt_day": a.LastAttemptDay, "disabled_reason": a.DisabledReason,
	}
	if a.LastChargeAt != nil {
		m["last_charge_at"] = a.LastChargeAt.UTC().Format(time.RFC3339)
	}
	return m
}

// withAutoTopup returns a copy of meta with the setting under key replaced.
func withAutoTopup(meta map[string]any, key string, a AutoTopup) map[string]any {
	out := make(map[string]any, len(meta)+1)
	for k, v := range meta {
		out[k] = v
	}
	out[key] = a.toMeta()
	return out
}

// savedCardOf returns the tenant's saved card authorization, or "".
func savedCardOf(sub *ent.TenantSubscription) string {
	s, _ := sub.Metadata["paystack_auth_code"].(string)
	return s
}

// GetAutoTopup returns the tenant's setting for a service (zero value when never set).
func (s *Service) GetAutoTopup(ctx context.Context, tenantID uuid.UUID, service string) (AutoTopup, error) {
	spec, ok := billing.LookupMeteredService(service)
	if !ok || spec.ProductCode == "" || len(spec.Bundles) == 0 {
		return AutoTopup{}, fmt.Errorf("%w: %q has no bundles to auto top up", ErrAutoTopup, service)
	}
	cov, err := s.Coverage(ctx, tenantID, spec)
	if err != nil {
		return AutoTopup{}, err
	}
	if cov.Product == nil {
		return AutoTopup{}, nil
	}
	return readAutoTopup(cov.Product.Metadata, autoTopupKey(spec)), nil
}

// SetAutoTopup validates and stores the tenant's setting. Switching it on needs the service
// activated, a saved card, a threshold the tenant chose, a real bundle and a limit that covers at
// least one purchase. Switching it off only flips the flag.
func (s *Service) SetAutoTopup(ctx context.Context, tenantID uuid.UUID, service string, in AutoTopupInput) (AutoTopup, error) {
	spec, ok := billing.LookupMeteredService(service)
	if !ok || spec.ProductCode == "" || len(spec.Bundles) == 0 {
		return AutoTopup{}, fmt.Errorf("%w: %q has no bundles to auto top up", ErrAutoTopup, service)
	}
	if s.subs.IsExemptTenant(ctx, tenantID) {
		return AutoTopup{}, fmt.Errorf("%w: this account is never billed for %s", ErrAutoTopup, spec.Label)
	}
	cov, err := s.Coverage(ctx, tenantID, spec)
	if err != nil {
		return AutoTopup{}, err
	}
	if cov.Sub == nil {
		return AutoTopup{}, fmt.Errorf("%w: an active subscription is required", ErrAutoTopup)
	}
	if cov.Product == nil {
		return AutoTopup{}, fmt.Errorf("%w: buy a %s bundle first", ErrAutoTopup, spec.Label)
	}
	key := autoTopupKey(spec)
	cur := readAutoTopup(cov.Product.Metadata, key)

	if !in.Enabled {
		cur.Enabled = false
		cur.DisabledReason = "turned off by the tenant"
		return cur, s.saveAutoTopup(ctx, cov.Product, key, cur)
	}

	if in.Threshold < 1 || in.Threshold > MaxAutoTopupThreshold {
		return AutoTopup{}, fmt.Errorf("%w: choose a threshold between 1 and %d units", ErrAutoTopup, MaxAutoTopupThreshold)
	}
	bundle, ok := spec.Bundle(in.BundleCode)
	if !ok || bundle.Retired { // a retired bundle is not offered for new top-ups
		ok = false
	}
	if !ok {
		return AutoTopup{}, fmt.Errorf("%w: unknown bundle %q", ErrAutoTopup, in.BundleCode)
	}
	limit := DefaultAutoTopupLimitKES
	if in.MonthlyLimitKES != nil && *in.MonthlyLimitKES > 0 {
		limit = *in.MonthlyLimitKES
	}
	if limit > MaxAutoTopupLimitKES {
		return AutoTopup{}, fmt.Errorf("%w: the monthly limit cannot exceed KES %.0f", ErrAutoTopup, MaxAutoTopupLimitKES)
	}
	if limit < bundle.PriceKES {
		return AutoTopup{}, fmt.Errorf("%w: the monthly limit must cover at least one bundle (KES %.0f)", ErrAutoTopup, bundle.PriceKES)
	}
	if savedCardOf(cov.Sub) == "" {
		return AutoTopup{}, fmt.Errorf("%w: save a card on your billing page first", ErrAutoTopup)
	}

	cur.Enabled, cur.Threshold, cur.BundleCode, cur.MonthlyLimitKES, cur.DisabledReason = true, in.Threshold, bundle.Code, limit, ""
	return cur, s.saveAutoTopup(ctx, cov.Product, key, cur)
}

// saveAutoTopup stores the setting under key and keeps ps.Metadata in step, so a later save for
// another service on the same row (the WhatsApp categories) does not overwrite this one.
func (s *Service) saveAutoTopup(ctx context.Context, ps *ent.ProductSubscription, key string, a AutoTopup) error {
	meta := withAutoTopup(ps.Metadata, key, a)
	if _, err := s.orm.ProductSubscription.UpdateOneID(ps.ID).SetMetadata(meta).Save(ctx); err != nil {
		return err
	}
	ps.Metadata = meta
	return nil
}

// AutoTopupResult counts what one run did, for logs and tests.
type AutoTopupResult struct {
	Checked, Charged, Failed, Skipped int
}

// RunAutoTopups buys bundles for every tenant whose balance is below its threshold. Safe to run on
// any number of replicas and any number of times a day: see the guarantees at the top of the file.
func (s *Service) RunAutoTopups(ctx context.Context, now time.Time) (AutoTopupResult, error) {
	var res AutoTopupResult
	// Every service that can be topped up, grouped by its activation product. Several services can
	// share one row (the WhatsApp categories), each with its own setting key.
	byProduct := map[string][]billing.MeteredService{}
	codes := []string{}
	keys := map[string]bool{}
	for _, code := range billing.MeteredServiceCodes() {
		spec, _ := billing.LookupMeteredService(code)
		if spec.ProductCode == "" || len(spec.Bundles) == 0 {
			continue
		}
		if len(byProduct[spec.ProductCode]) == 0 {
			codes = append(codes, spec.ProductCode)
		}
		byProduct[spec.ProductCode] = append(byProduct[spec.ProductCode], spec)
		keys[autoTopupKey(spec)] = true
	}
	enabled := predicate.ProductSubscription(func(sel *entsql.Selector) {
		var ors []*entsql.Predicate
		for key := range keys {
			ors = append(ors, sqljson.ValueEQ(productsubscription.FieldMetadata, true, sqljson.Path(key, "enabled")))
		}
		sel.Where(entsql.Or(ors...))
	})
	last := uuid.Nil
	const page = 200
	for {
		rows, err := s.orm.ProductSubscription.Query().
			Where(
				productsubscription.ProductCodeIn(codes...),
				productsubscription.StatusEQ(productsubscription.StatusActive),
				productsubscription.IDGT(last),
				enabled,
			).
			Order(ent.Asc(productsubscription.FieldID)).
			Limit(page).
			All(ctx)
		if err != nil {
			return res, err
		}
		if len(rows) == 0 {
			return res, nil
		}
		for _, ps := range rows {
			last = ps.ID
			for _, spec := range byProduct[ps.ProductCode] {
				if !readAutoTopup(ps.Metadata, autoTopupKey(spec)).Enabled {
					continue
				}
				res.Checked++
				outcome, err := s.autoTopupOne(ctx, ps, spec, now)
				if err != nil {
					s.log.Warn("auto top-up failed to run", zap.String("product_subscription", ps.ID.String()),
						zap.String("service", spec.Code), zap.Error(err))
					res.Skipped++
					continue
				}
				switch outcome {
				case "charged":
					res.Charged++
				case "failed":
					res.Failed++
				default:
					res.Skipped++
				}
			}
		}
	}
}

func (s *Service) autoTopupOne(ctx context.Context, ps *ent.ProductSubscription, spec billing.MeteredService, now time.Time) (string, error) {
	key := autoTopupKey(spec)
	cfg := readAutoTopup(ps.Metadata, key)
	if !cfg.Enabled || cfg.Threshold < 1 {
		return "skipped", nil
	}
	sub, err := s.orm.TenantSubscription.Query().Where(tenantsubscription.IDEQ(ps.TenantSubscriptionID)).Only(ctx)
	if err != nil {
		return "", err
	}
	if sub.Status != tenantsubscription.StatusACTIVE && sub.Status != tenantsubscription.StatusTRIAL {
		return "skipped", nil
	}
	if s.subs.IsExemptTenant(ctx, sub.TenantID) {
		return "skipped", nil
	}
	bal, err := s.wallets.GetBalance(ctx, sub.TenantID, spec.BundleTag)
	if err != nil {
		return "", err
	}
	if bal.Balance >= cfg.Threshold {
		return "skipped", nil
	}
	bundle, ok := spec.Bundle(cfg.BundleCode)
	if !ok {
		return s.failAutoTopup(ctx, ps, sub, spec, key, cfg, fmt.Sprintf("the chosen bundle %q no longer exists", cfg.BundleCode), 0, 0)
	}

	day := now.UTC().Format("20060102")
	month := now.UTC().Format("2006-01")
	if cfg.SpentMonth != month {
		cfg.SpentMonth, cfg.SpentKES = month, 0
	}
	if cfg.LastAttemptDay == day {
		return "skipped", nil // one attempt per service per day
	}
	if cfg.SpentKES+bundle.PriceKES > cfg.MonthlyLimitKES {
		return "skipped", nil // monthly limit reached: nothing more is bought this month
	}
	authCode := savedCardOf(sub)
	if authCode == "" {
		return s.failAutoTopup(ctx, ps, sub, spec, key, cfg, "there is no saved card on the account", bundle.Units, bundle.PriceKES)
	}

	// Record the attempt BEFORE charging: if anything below is interrupted the next run, same day,
	// stands down instead of risking a second charge.
	cfg.LastAttemptDay = day
	if err := s.saveAutoTopup(ctx, ps, key, cfg); err != nil {
		return "", err
	}
	offer := PurchaseOffer{Service: spec, Kind: KindBundle, Code: bundle.Code, Units: bundle.Units, PriceKES: bundle.PriceKES,
		Label: fmt.Sprintf("%s bundle: %d units (auto top-up)", spec.Label, bundle.Units)}
	meta := offer.IntentMetadata(sub.TenantID)
	meta["auto_topup"], meta["collection"] = true, "saved_card"
	// The same VAT rule as a manual purchase (platform VAT active and eTIMS activated).
	rate := 0.0
	if s.vatRate != nil {
		rate = s.vatRate(ctx)
	}
	total, vat := withVAT(offer.PriceKES, rate)
	meta["net_kes"], meta["vat_rate"], meta["vat_kes"] = offer.PriceKES, rate, vat
	email, _ := sub.Metadata["billing_email"].(string)
	req := map[string]any{
		"amount": total, "currency": "KES", "payment_method": "pending",
		"reference_id":   fmt.Sprintf("AUTOTOP-%s-%s-%s", strings.ToUpper(sub.TenantID.String()[:8]), spec.Code, day),
		"reference_type": RefTypeCreditPurchase, "source_service": "subscriptions",
		"description": offer.Label, "customer_email": email, "metadata": meta,
	}
	charge, err := s.subs.ChargeSavedCard(ctx, sub.TenantID, authCode, req)
	if err != nil {
		// Outcome unknown: do not retry today and do not switch it off on a guess.
		s.log.Error("auto top-up charge outcome unknown", zap.String("tenant_id", sub.TenantID.String()), zap.String("service", spec.Code), zap.Error(err))
		return "", err
	}
	if charge.Failure != "" || charge.Status == "failed" {
		return s.failAutoTopup(ctx, ps, sub, spec, key, cfg, charge.Failure, bundle.Units, bundle.PriceKES)
	}
	// Accepted (succeeded or pending). The payment event grants the units; the spend is counted now
	// so the limit holds even while the payment is still settling.
	t := now.UTC()
	cfg.SpentKES += bundle.PriceKES
	cfg.LastChargeAt = &t
	if err := s.saveAutoTopup(ctx, ps, key, cfg); err != nil {
		return "", err
	}
	s.log.Info("auto top-up charged", zap.String("tenant_id", sub.TenantID.String()), zap.String("service", spec.Code),
		zap.String("bundle", bundle.Code), zap.String("status", charge.Status))
	return "charged", nil
}

// failAutoTopup switches the setting off and queues the notification, in one transaction.
func (s *Service) failAutoTopup(ctx context.Context, ps *ent.ProductSubscription, sub *ent.TenantSubscription, spec billing.MeteredService,
	key string, cfg AutoTopup, reason string, units int64, price float64) (string, error) {
	if reason == "" {
		reason = "the card was declined"
	}
	cfg.Enabled, cfg.DisabledReason = false, reason
	tx, err := s.orm.Tx(ctx)
	if err != nil {
		return "", err
	}
	meta := withAutoTopup(ps.Metadata, key, cfg)
	if _, err := tx.ProductSubscription.UpdateOneID(ps.ID).SetMetadata(meta).Save(ctx); err != nil {
		_ = tx.Rollback()
		return "", err
	}
	s.subs.WriteOutboxEventPublic(ctx, tx, sub.TenantID, "subscription", sub.ID, EventAutoTopupFailed, map[string]any{
		"tenant_id": sub.TenantID.String(), "service": spec.Code, "service_label": spec.Label,
		"units": units, "amount": price, "reason": reason,
		"notification": map[string]any{"target": "tenant_admin"},
	})
	if err := tx.Commit(); err != nil {
		return "", err
	}
	ps.Metadata = meta
	s.log.Warn("auto top-up switched off", zap.String("tenant_id", sub.TenantID.String()), zap.String("service", spec.Code), zap.String("reason", reason))
	return "failed", nil
}
