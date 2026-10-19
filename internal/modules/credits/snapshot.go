package credits

import (
	"context"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/bengobox/subscription-service/internal/ent/overagecharge"
	"github.com/bengobox/subscription-service/internal/modules/billing"
)

// ServiceSnapshot is the tenant-facing state of one credit service.
type ServiceSnapshot struct {
	Service       string                 `json:"service"`
	Label         string                 `json:"label"`
	Exempt        bool                   `json:"exempt"`
	Covered       bool                   `json:"covered"`
	Reason        string                 `json:"reason,omitempty"`
	BundleBalance int64                  `json:"bundle_balance"`
	QuotaBalance  int64                  `json:"quota_balance"`
	Unlimited     bool                   `json:"unlimited"`
	TierCode      string                 `json:"tier_code,omitempty"`
	TierPeriodEnd *time.Time             `json:"tier_period_end,omitempty"`
	AutoRenew     bool                   `json:"auto_renew"`
	OverageKES    float64                `json:"overage_pending_kes"`
	OverageCapKES float64                `json:"overage_cap_kes"`
	UnitPriceKES  float64                `json:"unit_price_kes"`
	Bundles       []billing.CreditBundle `json:"bundles"`
	Tiers         []billing.CreditTier   `json:"tiers"`
}

// Snapshots returns the state of every tenant-purchasable credit service (those with a product
// activation or plan allowance, so the eTIMS token wallet is excluded).
func (s *Service) Snapshots(ctx context.Context, tenantID uuid.UUID) ([]ServiceSnapshot, error) {
	var out []ServiceSnapshot
	for _, code := range billing.MeteredServiceCodes() {
		spec, _ := billing.LookupMeteredService(code)
		if spec.ProductCode == "" && spec.PlanQuotaKey == "" {
			continue
		}
		snap, err := s.Snapshot(ctx, tenantID, spec)
		if err != nil {
			return nil, err
		}
		out = append(out, snap)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Service < out[j].Service })
	return out, nil
}

// Snapshot returns the state of one credit service for a tenant.
func (s *Service) Snapshot(ctx context.Context, tenantID uuid.UUID, spec billing.MeteredService) (ServiceSnapshot, error) {
	cov, err := s.Coverage(ctx, tenantID, spec)
	if err != nil {
		return ServiceSnapshot{}, err
	}
	snap := ServiceSnapshot{
		Service: spec.Code, Label: spec.Label, Exempt: cov.Exempt, Covered: cov.Covered, Reason: cov.Reason,
		Unlimited: cov.Unlimited, UnitPriceKES: spec.UnitPriceKES, Bundles: spec.Bundles, Tiers: spec.Tiers,
	}
	if bal, err := s.wallets.GetBalance(ctx, tenantID, spec.BundleTag); err == nil {
		snap.BundleBalance = bal.Balance
	}
	if spec.QuotaTag != "" {
		if bal, err := s.wallets.GetBalance(ctx, tenantID, spec.QuotaTag); err == nil {
			snap.QuotaBalance = bal.Balance
		}
	}
	if cov.Product != nil {
		if tier, live := liveTier(spec, cov.Product, time.Now()); live {
			snap.TierCode = tier.Code
		}
		if end, _ := cov.Product.Metadata[MetaTierPeriodEnd].(string); end != "" {
			if t, err := time.Parse(time.RFC3339, end); err == nil {
				snap.TierPeriodEnd = &t
			}
		}
		snap.AutoRenew, _ = cov.Product.Metadata[MetaTierAutoRenew].(bool)
	}
	if spec.AllowsOverage() {
		snap.OverageCapKES = s.overageCap(ctx)
		kes, err := s.pendingOverageKES(ctx, tenantID, spec.OverageMetric)
		if err != nil {
			return ServiceSnapshot{}, err
		}
		snap.OverageKES = kes
	}
	return snap, nil
}

// pendingOverageKES sums the tenant's not yet invoiced overage for a metric. Messaging overage
// accumulates in one open row per subscription, so the set is tiny.
func (s *Service) pendingOverageKES(ctx context.Context, tenantID uuid.UUID, metric string) (float64, error) {
	rows, err := s.orm.OverageCharge.Query().
		Where(overagecharge.TenantIDEQ(tenantID), overagecharge.MetricTypeEQ(metric), overagecharge.StatusEQ(overagecharge.StatusPending)).
		All(ctx)
	if err != nil {
		return 0, err
	}
	var kes float64
	for _, r := range rows {
		kes += r.TotalChargeKes
	}
	return kes, nil
}
