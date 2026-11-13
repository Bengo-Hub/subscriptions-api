package credits

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/bengobox/subscription-service/internal/modules/billing"
)

// WhatsApp usage per tenant and category over a date range, read by notifications-api's
// platform cost report so Meta's charges can sit next to what each tenant paid us.

// WhatsAppUsageMaxDays bounds one report so a typo cannot scan the whole ledger.
const WhatsAppUsageMaxDays = 366

// whatsAppLegacyAllowance is the shared allowance wallet of the retired tiers. Its spends carry
// no category, so they are reported on their own line.
const whatsAppLegacyAllowance = "whatsapp_quota"

// WhatsAppUsageRange is a whole number of UTC days, From inclusive and To inclusive.
type WhatsAppUsageRange struct {
	From time.Time
	To   time.Time
}

// ParseWhatsAppUsageRange reads from and to as YYYY-MM-DD (UTC) and checks the order and length.
func ParseWhatsAppUsageRange(from, to string) (WhatsAppUsageRange, error) {
	f, err := time.Parse("2006-01-02", strings.TrimSpace(from))
	if err != nil {
		return WhatsAppUsageRange{}, fmt.Errorf("%w: from must be a date like 2026-11-01", ErrInvalidAdminQuery)
	}
	t, err := time.Parse("2006-01-02", strings.TrimSpace(to))
	if err != nil {
		return WhatsAppUsageRange{}, fmt.Errorf("%w: to must be a date like 2026-11-30", ErrInvalidAdminQuery)
	}
	if t.Before(f) {
		return WhatsAppUsageRange{}, fmt.Errorf("%w: to is before from", ErrInvalidAdminQuery)
	}
	if t.Sub(f) > WhatsAppUsageMaxDays*24*time.Hour {
		return WhatsAppUsageRange{}, fmt.Errorf("%w: the range is longer than %d days", ErrInvalidAdminQuery, WhatsAppUsageMaxDays)
	}
	return WhatsAppUsageRange{From: f.UTC(), To: t.UTC()}, nil
}

// WhatsAppUsageCategory is one tenant's movement in one WhatsApp credit wallet.
type WhatsAppUsageCategory struct {
	Service      string `json:"service"`
	MetaCategory string `json:"meta_category,omitempty"`
	// UnitsBought are credits bought (top-ups) in the range, KESPaid what they cost.
	UnitsBought int64   `json:"units_bought"`
	KESPaid     float64 `json:"kes_paid"`
	// UnitsConsumed are credits spent in the range net of refunds, overage included.
	UnitsConsumed int64   `json:"units_consumed"`
	OverageKES    float64 `json:"overage_kes"`
	UnitPriceKES  float64 `json:"unit_price_kes"`
	// ConsumedValueKES prices the bundle credits spent at today's unit price plus the overage
	// charged, an estimate of the revenue those messages earned.
	ConsumedValueKES float64 `json:"consumed_value_kes"`
}

// WhatsAppUsageTenant groups one tenant's categories.
type WhatsAppUsageTenant struct {
	TenantID   uuid.UUID               `json:"tenant_id"`
	Slug       string                  `json:"slug"`
	Name       string                  `json:"name"`
	Categories []WhatsAppUsageCategory `json:"categories"`
}

// WhatsAppUsage is the response of the usage endpoint.
type WhatsAppUsage struct {
	From     string                `json:"from"`
	To       string                `json:"to"`
	Currency string                `json:"currency"`
	Tenants  []WhatsAppUsageTenant `json:"tenants"`
}

// WhatsAppUsage reads every tenant's WhatsApp purchases and spends in the range with one
// aggregate query.
func (s *Service) WhatsAppUsage(ctx context.Context, rng WhatsAppUsageRange) (WhatsAppUsage, error) {
	db := s.wallets.DB()
	if db == nil {
		return WhatsAppUsage{}, errors.New("credit ledger database not configured")
	}
	prices := map[string]billing.WhatsAppCategory{}
	tags := []string{whatsAppLegacyAllowance}
	for _, c := range billing.WhatsAppCategories() {
		prices[c.Code] = c
		tags = append(tags, c.Code)
	}
	end := rng.To.AddDate(0, 0, 1)
	rows, err := db.QueryContext(ctx, whatsAppUsageSQL, strings.Join(tags, ","), rng.From, end)
	if err != nil {
		return WhatsAppUsage{}, fmt.Errorf("whatsapp usage: %w", err)
	}
	defer rows.Close()

	out := WhatsAppUsage{From: rng.From.Format("2006-01-02"), To: rng.To.Format("2006-01-02"), Currency: "KES", Tenants: []WhatsAppUsageTenant{}}
	index := map[uuid.UUID]int{}
	for rows.Next() {
		var (
			id         uuid.UUID
			slug, name string
			c          WhatsAppUsageCategory
		)
		if err := rows.Scan(&id, &slug, &name, &c.Service, &c.UnitsBought, &c.KESPaid, &c.UnitsConsumed, &c.OverageKES); err != nil {
			return WhatsAppUsage{}, fmt.Errorf("whatsapp usage scan: %w", err)
		}
		if p, ok := prices[c.Service]; ok {
			c.MetaCategory = p.MetaCategory
			c.UnitPriceKES = p.UnitPriceKES
		}
		c.ConsumedValueKES = whatsAppConsumedValue(c)
		i, ok := index[id]
		if !ok {
			i = len(out.Tenants)
			index[id] = i
			out.Tenants = append(out.Tenants, WhatsAppUsageTenant{TenantID: id, Slug: slug, Name: name})
		}
		out.Tenants[i].Categories = append(out.Tenants[i].Categories, c)
	}
	if err := rows.Err(); err != nil {
		return WhatsAppUsage{}, fmt.Errorf("whatsapp usage rows: %w", err)
	}
	return out, nil
}

// whatsAppConsumedValue prices the bundle credits spent (consumed less the overage units, whose
// KES is known exactly) at the category's unit price, plus the overage KES.
func whatsAppConsumedValue(c WhatsAppUsageCategory) float64 {
	if c.UnitPriceKES <= 0 {
		return c.OverageKES
	}
	overageUnits := int64(0)
	if c.OverageKES > 0 {
		overageUnits = int64(c.OverageKES/c.UnitPriceKES + 0.5)
	}
	bundle := c.UnitsConsumed - overageUnits
	if bundle < 0 {
		bundle = 0
	}
	return float64(bundle)*c.UnitPriceKES + c.OverageKES
}

// whatsAppUsageSQL: $1 comma separated service tags, $2 range start, $3 range end (exclusive).
// Spends are negative rows (deduction, overage), refunds positive.
const whatsAppUsageSQL = `
SELECT x.tenant_id, COALESCE(t.slug, ''), COALESCE(t.name, ''), x.service_tag,
	COALESCE(SUM(CASE WHEN x.action = 'topup' THEN x.tokens ELSE 0 END), 0)::bigint,
	COALESCE(SUM(CASE WHEN x.action = 'topup' THEN x.tokens * COALESCE(x.unit_cost_kes, 0) ELSE 0 END), 0)::float8,
	GREATEST(0, COALESCE(SUM(CASE WHEN x.action IN ('deduction', 'overage') THEN -x.tokens
		WHEN x.action = 'refund' THEN -x.tokens ELSE 0 END), 0))::bigint,
	COALESCE(SUM(CASE WHEN x.action = 'overage' THEN COALESCE((x.metadata ->> 'kes')::float8, 0) ELSE 0 END), 0)::float8
FROM api_token_transactions x
LEFT JOIN tenants t ON t.id = x.tenant_id
WHERE x.service_tag = ANY(string_to_array($1::text, ','))
	AND x.action IN ('topup', 'deduction', 'overage', 'refund')
	AND x.created_at >= $2::timestamptz AND x.created_at < $3::timestamptz
GROUP BY x.tenant_id, t.slug, t.name, x.service_tag
ORDER BY t.slug, x.service_tag`
