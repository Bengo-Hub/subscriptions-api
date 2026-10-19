package credits

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/product"
	"github.com/bengobox/subscription-service/internal/ent/productsubscription"
	"github.com/bengobox/subscription-service/internal/modules/billing"
	"github.com/bengobox/subscription-service/internal/modules/subscriptions"
)

// Treasury reference type for every credit purchase (bundle or tier).
const RefTypeCreditPurchase = "credit_purchase"

// PurchaseKind is what a tenant is buying.
type PurchaseKind string

const (
	KindBundle PurchaseKind = "bundle"
	KindTier   PurchaseKind = "tier"
)

// PurchaseInput describes a standalone credit purchase.
type PurchaseInput struct {
	TenantID  uuid.UUID
	Service   string
	Kind      PurchaseKind
	Code      string // bundle code or tier code
	ReturnURL string
}

// PurchaseOffer is the priced result of resolving a purchase.
type PurchaseOffer struct {
	Service  billing.MeteredService
	Kind     PurchaseKind
	Code     string
	Units    int64
	PriceKES float64
	Label    string
}

// ResolveOffer prices a purchase from the registry. Prices never come from the client.
func ResolveOffer(service string, kind PurchaseKind, code string) (PurchaseOffer, error) {
	spec, ok := billing.LookupMeteredService(service)
	if !ok {
		return PurchaseOffer{}, fmt.Errorf("unknown credit service %q", service)
	}
	switch kind {
	case KindBundle:
		b, ok := spec.Bundle(code)
		if !ok {
			return PurchaseOffer{}, fmt.Errorf("unknown %s bundle %q", spec.Label, code)
		}
		return PurchaseOffer{Service: spec, Kind: kind, Code: code, Units: b.Units, PriceKES: b.PriceKES,
			Label: fmt.Sprintf("%s bundle: %d units", spec.Label, b.Units)}, nil
	case KindTier:
		t, ok := spec.Tier(code)
		if !ok {
			return PurchaseOffer{}, fmt.Errorf("unknown %s tier %q", spec.Label, code)
		}
		return PurchaseOffer{Service: spec, Kind: kind, Code: code, Units: t.MonthlyUnit, PriceKES: t.PriceKES,
			Label: fmt.Sprintf("%s %s monthly plan", spec.Label, t.Name)}, nil
	}
	return PurchaseOffer{}, fmt.Errorf("unknown purchase kind %q", kind)
}

// IntentMetadata is stored on the Treasury intent and read back on payment.
func (o PurchaseOffer) IntentMetadata(tenantID uuid.UUID) map[string]any {
	return map[string]any{
		"tenant_id": tenantID.String(), "credit_service": o.Service.Code,
		"purchase_kind": string(o.Kind), "purchase_code": o.Code, "units": o.Units, "price_kes": o.PriceKES,
	}
}

// InitiatePurchase creates the Treasury intent. Units are granted only once payment succeeds.
// Buying anything needs an active base subscription; the first bundle or tier activates the
// service for the tenant.
func (s *Service) InitiatePurchase(ctx context.Context, in PurchaseInput) (map[string]any, error) {
	offer, err := ResolveOffer(in.Service, in.Kind, in.Code)
	if err != nil {
		return nil, err
	}
	if s.subs.IsExemptTenant(ctx, in.TenantID) {
		return nil, fmt.Errorf("tenant is exempt from subscriptions and needs no purchase")
	}
	cov, err := s.Coverage(ctx, in.TenantID, offer.Service)
	if err != nil {
		return nil, err
	}
	if cov.Sub == nil {
		return nil, fmt.Errorf("an active subscription is required")
	}
	req := map[string]any{
		"amount": offer.PriceKES, "currency": "KES", "payment_method": "pending",
		"reference_id":   fmt.Sprintf("CRED-%s-%s-%d", in.TenantID.String()[:8], offer.Service.Code, time.Now().UnixNano()),
		"reference_type": RefTypeCreditPurchase, "source_service": "subscriptions",
		"description": offer.Label, "callback_url": in.ReturnURL,
		"metadata": offer.IntentMetadata(in.TenantID),
	}
	return s.subs.CreatePaymentIntent(ctx, in.TenantID, req)
}

// FulfilIntent grants what a settled intent bought. Safe to call twice for the same intent.
func (s *Service) FulfilIntent(ctx context.Context, tenantID, intentID uuid.UUID) error {
	meta, err := s.subs.GetPaymentIntentMetadata(ctx, tenantID, intentID)
	if err != nil {
		return err
	}
	service, _ := meta["credit_service"].(string)
	kind, _ := meta["purchase_kind"].(string)
	code, _ := meta["purchase_code"].(string)
	offer, err := ResolveOffer(service, PurchaseKind(kind), code)
	if err != nil {
		return err
	}
	return s.Grant(ctx, tenantID, offer, 1, "intent:"+intentID.String(), intentID)
}

// Grant applies an offer to a tenant: bundles add units, tiers activate the service for months
// (extending a running period) and refill the allowance. key makes it apply once.
func (s *Service) Grant(ctx context.Context, tenantID uuid.UUID, offer PurchaseOffer, months int, key string, refID uuid.UUID) error {
	if months <= 0 {
		months = 1
	}
	switch offer.Kind {
	case KindBundle:
		if err := s.ensureActivated(ctx, tenantID, offer.Service, nil); err != nil {
			return err
		}
		unit := offer.PriceKES / float64(offer.Units)
		_, _, err := s.wallets.Credit(ctx, billing.CreditRequest{
			TenantID: tenantID, ServiceTag: offer.Service.BundleTag, Units: offer.Units, Action: "topup",
			Key: key, RefID: refID, RefType: "payment", Description: offer.Label, UnitCostKES: &unit,
		})
		return err
	case KindTier:
		return s.activateTier(ctx, tenantID, offer, months, key)
	}
	return fmt.Errorf("unknown purchase kind %q", offer.Kind)
}

// ensureActivated makes sure the tenant has an active activation row for the service, merging
// extra metadata into it. A service without a product code needs none.
func (s *Service) ensureActivated(ctx context.Context, tenantID uuid.UUID, spec billing.MeteredService, extra map[string]any) error {
	if spec.ProductCode == "" {
		return nil
	}
	sub, err := s.subs.SubscriptionByTenant(ctx, tenantID)
	if err != nil {
		return fmt.Errorf("an active subscription is required: %w", err)
	}
	prod, err := s.orm.Product.Query().Where(product.CodeEQ(spec.ProductCode)).Only(ctx)
	if err != nil {
		return fmt.Errorf("product %s: %w", spec.ProductCode, err)
	}
	existing, err := s.orm.ProductSubscription.Query().
		Where(productsubscription.TenantSubscriptionIDEQ(sub.ID), productsubscription.ProductCodeEQ(spec.ProductCode)).
		Only(ctx)
	if err != nil && !ent.IsNotFound(err) {
		return err
	}
	if existing == nil {
		_, err = s.orm.ProductSubscription.Create().
			SetTenantSubscriptionID(sub.ID).SetProductID(prod.ID).SetProductCode(spec.ProductCode).
			SetStatus(productsubscription.StatusActive).SetActivatedAt(time.Now().UTC()).
			SetMetadata(nonNilMeta(extra)).Save(ctx)
		return err
	}
	if existing.Status == productsubscription.StatusActive && len(extra) == 0 {
		return nil
	}
	meta := map[string]any{}
	for k, v := range existing.Metadata {
		meta[k] = v
	}
	for k, v := range extra {
		meta[k] = v
	}
	_, err = s.orm.ProductSubscription.UpdateOneID(existing.ID).
		SetStatus(productsubscription.StatusActive).ClearDeactivatedAt().SetMetadata(meta).Save(ctx)
	return err
}

func nonNilMeta(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

// activateTier extends the tier period by months from the later of now and the running period
// end (same tier), activates the service and refills the allowance.
func (s *Service) activateTier(ctx context.Context, tenantID uuid.UUID, offer PurchaseOffer, months int, key string) error {
	sub, err := s.subs.SubscriptionByTenant(ctx, tenantID)
	if err != nil {
		return fmt.Errorf("an active subscription is required: %w", err)
	}
	now := time.Now().UTC()
	base := now
	var prevMeta map[string]any
	if ps, perr := s.orm.ProductSubscription.Query().
		Where(productsubscription.TenantSubscriptionIDEQ(sub.ID), productsubscription.ProductCodeEQ(offer.Service.ProductCode)).
		Only(ctx); perr == nil {
		prevMeta = ps.Metadata
	} else if !ent.IsNotFound(perr) {
		return perr
	}
	if applied, _ := prevMeta["last_grant_key"].(string); applied == key {
		return nil
	}
	if prev, ok := prevMeta[MetaTierPeriodEnd].(string); ok && prevMeta[MetaTierCode] == offer.Code {
		if t, perr := time.Parse(time.RFC3339, prev); perr == nil && t.After(now) {
			base = t
		}
	}
	autoRenew := true
	if v, ok := prevMeta[MetaTierAutoRenew].(bool); ok {
		autoRenew = v
	}
	end := base.AddDate(0, months, 0)
	if err := s.ensureActivated(ctx, tenantID, offer.Service, map[string]any{
		MetaTierCode: offer.Code, MetaTierPeriodEnd: end.Format(time.RFC3339),
		MetaTierAutoRenew: autoRenew, "last_grant_key": key,
	}); err != nil {
		return err
	}
	if offer.Service.QuotaTag != "" && offer.Units >= 0 {
		if _, err := s.wallets.SetQuota(ctx, tenantID, offer.Service.QuotaTag, offer.Units, key, offer.Label); err != nil {
			return err
		}
	}
	return nil
}

// AddonResolver returns the checkout pricer to register on the subscriptions service.
func AddonResolver() subscriptions.AddonResolver {
	return func(sel subscriptions.AddonSelection) (subscriptions.AddonOffer, error) {
		offer, err := ResolveOffer(sel.Service, PurchaseKind(sel.Kind), sel.Code)
		if err != nil {
			return subscriptions.AddonOffer{}, err
		}
		return subscriptions.AddonOffer{PriceKES: offer.PriceKES, Label: offer.Label, Recurring: offer.Kind == KindTier}, nil
	}
}

// FulfilCheckoutAddons grants the credit add-ons bought with a plan checkout. The selection is
// read from the subscription metadata bound to the intent at checkout, and applies only when the
// paying intent matches, so invoice payments and stale checkouts never grant anything. Each
// grant is keyed by intent and position, so a redelivered payment event grants nothing twice.
func (s *Service) FulfilCheckoutAddons(ctx context.Context, tenantID, intentID uuid.UUID) error {
	sub, err := s.subs.SubscriptionByTenant(ctx, tenantID)
	if err != nil {
		return err
	}
	pending, _ := sub.Metadata[subscriptions.MetaPendingCreditAddons].(map[string]any)
	if pending == nil || pending["intent_id"] != intentID.String() {
		return nil
	}
	raw, _ := pending["addons"].([]any)
	for i, item := range raw {
		m, _ := item.(map[string]any)
		service, _ := m["service"].(string)
		kind, _ := m["kind"].(string)
		code, _ := m["code"].(string)
		months := 1
		if f, ok := m["months"].(float64); ok && f >= 1 {
			months = int(f)
		}
		offer, err := ResolveOffer(service, PurchaseKind(kind), code)
		if err != nil {
			return err
		}
		if err := s.Grant(ctx, tenantID, offer, months, fmt.Sprintf("intent:%s:%d", intentID, i), intentID); err != nil {
			return err
		}
	}
	meta := make(map[string]any, len(sub.Metadata))
	for k, v := range sub.Metadata {
		if k != subscriptions.MetaPendingCreditAddons {
			meta[k] = v
		}
	}
	_, err = s.orm.TenantSubscription.UpdateOneID(sub.ID).SetMetadata(meta).Save(ctx)
	return err
}

// AdminGrant lets the platform team add units to a tenant's bundle wallet without a payment, for
// goodwill or manual settlement. It is an adjustment row naming the reason, never a top-up, so
// revenue reports keep working. key makes a repeated call apply once.
func (s *Service) AdminGrant(ctx context.Context, tenantID uuid.UUID, service string, units int64, reason, key string) (int64, bool, error) {
	spec, ok := billing.LookupMeteredService(service)
	if !ok {
		return 0, false, fmt.Errorf("unknown credit service %q", service)
	}
	if err := s.ensureActivated(ctx, tenantID, spec, nil); err != nil {
		return 0, false, err
	}
	return s.wallets.Credit(ctx, billing.CreditRequest{
		TenantID: tenantID, ServiceTag: spec.BundleTag, Units: units, Action: "adjustment",
		Key: key, RefType: "admin_grant", Description: reason,
	})
}
