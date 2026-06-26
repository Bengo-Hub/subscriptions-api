package billing

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/bengobox/subscription-service/internal/domain"
	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/usageevent"
)

// ISP Billing usage-charge model (ISP_BILLING_STARTER / service_tag "isp_billing").
//
// On top of the plan base_price (added by the generic invoice line builder), an ISP
// tenant is billed each cycle:
//
//	+ (monthly_hotspot_sales > service_charge_threshold ?
//	       service_charge_percentage% × monthly_hotspot_sales : 0)   ← threshold-gated, FULL sales
//	+ (active_pppoe_subscribers × pppoe_per_subscriber_fee)          ← always
//
// e.g. 12,000 hotspot sales, 3% / 10,000 threshold → 0.03 × 12,000 = KES 360 service charge
// (so 500 base + 360 = KES 860), plus KES 35 per active PPPoE subscriber.
//
// All three rates are read from the plan's tier_limits_json (seeded by
// cmd/seed/plans_isp_billing.go). Usage is sourced from usage_events:
//   - hotspot_sales      → SUM(value) of "isp.subscriber.created" events (value = sale amount)
//   - pppoe_subscribers  → COUNT via SUM(value) of "isp.subscription.renewed" events
//
// ACTIVE PPPoE count (exact via S2S, renewals-approximation as fallback): the per-subscriber
// fee multiplies the EXACT point-in-time active PPPoE count fetched from isp-billing over S2S
// (GET /api/v1/s2s/tenants/{tenant_id}/pppoe/active-count). The ISP service publishes no PPPoE
// subscriber create/deactivate events — only a renewal event — so when the S2S call is
// unavailable (client not configured, non-2xx, parse/network error) we gracefully FALL BACK to
// the pppoe_subscribers metric summed from usage_events, which reflects PPPoE *renewals in the
// billing period*: an approximation of, not identical to, the true active count. The exact path
// is preferred; the fallback never fails the invoice. The deterministic base + threshold-gated
// % are exact regardless.

// tier_limits keys (seeded in cmd/seed/plans_isp_billing.go).
const (
	ispKeyServiceChargePct       = "service_charge_percentage"
	ispKeyServiceChargeThreshold = "service_charge_threshold"
	ispKeyPPPoEPerSubscriberFee  = "pppoe_per_subscriber_fee"
)

// usage_events metric_type values populated by the usage consumer for ISP tenants.
const (
	ispMetricHotspotSales     = "hotspot_sales"
	ispMetricPPPoESubscribers = "pppoe_subscribers"
)

// IsISPBillingPlan reports whether a plan uses the ISP usage-billing model. Primary signal
// is the canonical service_tag; a tier_limits fallback covers plans tagged differently but
// carrying the ISP billing params (so the model still applies if the tag is ever omitted).
func IsISPBillingPlan(plan *ent.SubscriptionPlan) bool {
	if plan == nil {
		return false
	}
	if plan.ServiceTag != nil && *plan.ServiceTag == domain.ServiceTagISPBilling {
		return true
	}
	if plan.TierLimitsJSON != nil {
		_, hasPct := plan.TierLimitsJSON[ispKeyServiceChargePct]
		_, hasFee := plan.TierLimitsJSON[ispKeyPPPoEPerSubscriberFee]
		return hasPct || hasFee
	}
	return false
}

// ispNum reads a numeric tier_limits value (JSON numbers decode as float64; ints tolerated).
func ispNum(limits map[string]any, key string) float64 {
	switch v := limits[key].(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case int64:
		return float64(v)
	}
	return 0
}

// ispUsageLine is one computed ISP usage charge ready to become an invoice line.
type ispUsageLine struct {
	Description string
	Quantity    float64
	UnitPrice   float64
	Subtotal    float64
}

// ispBillingPeriod returns the [start, end) window whose usage this invoice bills.
//
// Invoices are generated InvoiceLeadDays (~7) BEFORE current_period_end (in advance — see
// internal/jobs/invoicing.go), and the subscription period is NOT advanced at buildLines
// time, so at billing time CurrentPeriodStart/End describe the current, not-yet-complete
// cycle. To charge each cycle's usage exactly once and in FULL, ISP usage is billed in
// ARREARS: we aggregate the most-recently COMPLETED cycle — the same-length window
// immediately before the current period, [current_period_start − cycle_len,
// current_period_start). That window always ends at/before "now", so its hotspot sales and
// PPPoE counts are final. (Billing the current period instead would silently drop the last
// ~7 days of every cycle, or bill zero for calendar-aligned subscriptions.)
func ispBillingPeriod(sub *ent.TenantSubscription) (time.Time, time.Time) {
	end := sub.CurrentPeriodStart.UTC()
	if end.IsZero() {
		now := time.Now().UTC()
		return now.AddDate(0, -1, 0), now
	}
	start := end.AddDate(0, -1, 0) // default: monthly cycle
	if !sub.CurrentPeriodEnd.IsZero() && sub.CurrentPeriodEnd.After(sub.CurrentPeriodStart) {
		start = end.Add(-sub.CurrentPeriodEnd.Sub(sub.CurrentPeriodStart))
	}
	return start, end
}

// ispMonthlyHotspotSales sums the tenant's hotspot sale amounts over the billing period.
func ispMonthlyHotspotSales(ctx context.Context, orm *ent.Client, tenantID uuid.UUID, start, end time.Time) (float64, error) {
	events, err := orm.UsageEvent.Query().
		Where(
			usageevent.TenantIDEQ(tenantID),
			usageevent.MetricTypeEQ(ispMetricHotspotSales),
			usageevent.CreatedAtGTE(start),
			usageevent.CreatedAtLT(end),
		).
		All(ctx)
	if err != nil {
		return 0, fmt.Errorf("query hotspot_sales usage: %w", err)
	}
	var total float64
	for _, e := range events {
		total += e.Value
	}
	return total, nil
}

// ispActivePPPoEResponse is the isp-billing S2S active-count payload.
type ispActivePPPoEResponse struct {
	TenantID               string `json:"tenant_id"`
	ActivePPPoESubscribers int    `json:"active_pppoe_subscribers"`
}

// ispActivePPPoESubscribers returns the active PPPoE subscriber count used for the per-
// subscriber fee. Primary source is the EXACT point-in-time count from isp-billing over S2S;
// on ANY failure it falls back to ispRenewalsApproxPPPoE (the usage_events renewals sum). See
// the package-level note. The (start, end) window is used only by the fallback path.
func (s *InvoiceService) ispActivePPPoESubscribers(ctx context.Context, tenantID uuid.UUID, start, end time.Time) (float64, error) {
	if s != nil && s.ispBillingClient != nil {
		count, err := s.ispExactActivePPPoE(ctx, tenantID)
		if err == nil {
			return count, nil
		}
		// Graceful degradation: never fail the invoice over the exact-count lookup — log and
		// fall back to the renewals approximation below.
		if s.log != nil {
			s.log.Debug("isp: exact active PPPoE count unavailable; falling back to renewals approximation",
				zap.String("tenant_id", tenantID.String()), zap.Error(err))
		}
	}
	return s.ispRenewalsApproxPPPoE(ctx, tenantID, start, end)
}

// ispExactActivePPPoE fetches the exact active PPPoE subscriber count from isp-billing via S2S.
func (s *InvoiceService) ispExactActivePPPoE(ctx context.Context, tenantID uuid.UUID) (float64, error) {
	path := fmt.Sprintf("/api/v1/s2s/tenants/%s/pppoe/active-count", tenantID)
	resp, err := s.ispBillingClient.Get(ctx, path, s.headers())
	if err != nil {
		return 0, fmt.Errorf("isp-billing active-count request: %w", err)
	}
	if !resp.IsSuccess() {
		return 0, fmt.Errorf("isp-billing active-count returned status %d", resp.StatusCode)
	}
	var parsed ispActivePPPoEResponse
	if err := json.Unmarshal(resp.Body, &parsed); err != nil {
		return 0, fmt.Errorf("isp-billing active-count decode: %w", err)
	}
	count := float64(parsed.ActivePPPoESubscribers)
	if count < 0 {
		count = 0
	}
	return count, nil
}

// ispRenewalsApproxPPPoE returns the fallback active-count approximation: the count of PPPoE
// renewals in the period (best available offline signal), summed from usage_events.
func (s *InvoiceService) ispRenewalsApproxPPPoE(ctx context.Context, tenantID uuid.UUID, start, end time.Time) (float64, error) {
	events, err := s.orm.UsageEvent.Query().
		Where(
			usageevent.TenantIDEQ(tenantID),
			usageevent.MetricTypeEQ(ispMetricPPPoESubscribers),
			usageevent.CreatedAtGTE(start),
			usageevent.CreatedAtLT(end),
		).
		All(ctx)
	if err != nil {
		return 0, fmt.Errorf("query pppoe_subscribers usage: %w", err)
	}
	var total float64
	for _, e := range events {
		total += e.Value
	}
	if total < 0 {
		total = 0
	}
	return total, nil
}

// computeISPUsageLines assembles the ISP usage charges (threshold-gated service charge +
// per-PPPoE-subscriber fee) for the subscription's current cycle. It does NOT include the
// base_price — the generic line builder already adds that. Returns an empty slice (no
// error) when the plan is not ISP or there is nothing to charge.
func (s *InvoiceService) computeISPUsageLines(ctx context.Context, sub *ent.TenantSubscription, plan *ent.SubscriptionPlan) ([]ispUsageLine, error) {
	if !IsISPBillingPlan(plan) || plan.TierLimitsJSON == nil {
		return nil, nil
	}
	limits := plan.TierLimitsJSON
	start, end := ispBillingPeriod(sub)
	period := fmt.Sprintf("%s–%s", start.Format("2006-01-02"), end.Format("2006-01-02"))

	var lines []ispUsageLine

	// 1. Threshold-gated service charge on FULL monthly hotspot sales.
	pct := ispNum(limits, ispKeyServiceChargePct)
	threshold := ispNum(limits, ispKeyServiceChargeThreshold)
	if pct > 0 {
		sales, err := ispMonthlyHotspotSales(ctx, s.orm, sub.TenantID, start, end)
		if err != nil {
			return nil, err
		}
		if sales > threshold {
			charge := sales * pct / 100.0
			if charge > 0 {
				lines = append(lines, ispUsageLine{
					Description: fmt.Sprintf("Hotspot service charge %s (%.3g%% of KES %.2f sales)", period, pct, sales),
					Quantity:    1,
					UnitPrice:   charge,
					Subtotal:    charge,
				})
			}
		}
	}

	// 2. Per-active-PPPoE-subscriber fee (always, threshold-independent).
	fee := ispNum(limits, ispKeyPPPoEPerSubscriberFee)
	if fee > 0 {
		count, err := s.ispActivePPPoESubscribers(ctx, sub.TenantID, start, end)
		if err != nil {
			return nil, err
		}
		if count > 0 {
			subtotal := count * fee
			lines = append(lines, ispUsageLine{
				Description: fmt.Sprintf("PPPoE subscribers %s (%.0f × KES %.2f)", period, count, fee),
				Quantity:    count,
				UnitPrice:   fee,
				Subtotal:    subtotal,
			})
		}
	}

	return lines, nil
}
