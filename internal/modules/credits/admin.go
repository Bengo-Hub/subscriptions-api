package credits

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/serviceconfig"
	"github.com/bengobox/subscription-service/internal/modules/billing"
	"github.com/bengobox/subscription-service/internal/modules/subscriptions"
)

// Platform-owner reporting over the credits ledger. Everything here is read with SQL aggregates
// so the cost does not grow with the number of ledger rows or tenants, and the tenant list is
// one paginated query with no per-row follow-ups.

// Tenant status values returned by the admin list and accepted by its status filter.
const (
	AdminStatusActive       = "active"
	AdminStatusCapped       = "capped"
	AdminStatusNotActivated = "not_activated"
	AdminStatusExempt       = "exempt"
)

const (
	adminDefaultLimit = 25
	adminMaxLimit     = 100
	// MaxOverageCapKES bounds the editable cap so a typo cannot push it to an absurd value.
	MaxOverageCapKES = 1_000_000.0
)

var adminStatuses = map[string]bool{
	AdminStatusActive: true, AdminStatusCapped: true, AdminStatusNotActivated: true, AdminStatusExempt: true,
}

// ErrInvalidAdminQuery marks a bad list parameter; handlers map it to 400.
var ErrInvalidAdminQuery = errors.New("invalid query")

// AdminListParams are the filters of the platform tenant list.
type AdminListParams struct {
	Service string
	Q       string
	Status  string
	Limit   int
	Offset  int
}

// normalise clamps paging, trims the search and rejects unknown status values.
func (p AdminListParams) normalise() (AdminListParams, error) {
	p.Q = strings.TrimSpace(p.Q)
	if len(p.Q) > 100 {
		p.Q = p.Q[:100]
	}
	p.Status = strings.ToLower(strings.TrimSpace(p.Status))
	if p.Status != "" && !adminStatuses[p.Status] {
		return p, fmt.Errorf("%w: status must be one of active, capped, not_activated, exempt", ErrInvalidAdminQuery)
	}
	if p.Limit <= 0 {
		p.Limit = adminDefaultLimit
	}
	if p.Limit > adminMaxLimit {
		p.Limit = adminMaxLimit
	}
	if p.Offset < 0 {
		p.Offset = 0
	}
	return p, nil
}

// likePattern turns user text into a contains pattern for ILIKE, escaping wildcards.
func likePattern(q string) string {
	if q == "" {
		return ""
	}
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + r.Replace(q) + "%"
}

// monthStart is the first instant of the UTC calendar month containing now.
func monthStart(now time.Time) time.Time {
	n := now.UTC()
	return time.Date(n.Year(), n.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// adminSpec resolves a service code to a tenant-purchasable credit service.
func adminSpec(code string) (billing.MeteredService, error) {
	spec, ok := billing.LookupMeteredService(code)
	if !ok || (spec.ProductCode == "" && spec.PlanQuotaKey == "") {
		return billing.MeteredService{}, fmt.Errorf("%w: service must be one of sms, whatsapp, ai_credits", ErrInvalidAdminQuery)
	}
	return spec, nil
}

// AdminTenantRow is one tenant's position in one credit service.
type AdminTenantRow struct {
	TenantID       uuid.UUID  `json:"tenant_id"`
	Slug           string     `json:"slug"`
	Name           string     `json:"name"`
	Exempt         bool       `json:"exempt"`
	PlanName       string     `json:"plan_name,omitempty"`
	PlanStatus     string     `json:"plan_status,omitempty"`
	Status         string     `json:"status"`
	Activated      bool       `json:"activated"`
	BundleBalance  int64      `json:"bundle_balance"`
	QuotaBalance   int64      `json:"quota_balance"`
	Unlimited      bool       `json:"unlimited"`
	TierCode       string     `json:"tier_code,omitempty"`
	TierName       string     `json:"tier_name,omitempty"`
	TierActive     bool       `json:"tier_active"`
	TierPeriodEnd  *time.Time `json:"tier_period_end,omitempty"`
	AutoRenew      bool       `json:"auto_renew"`
	OverageKES     float64    `json:"overage_pending_kes"`
	OverageCapKES  float64    `json:"overage_cap_kes"`
	Capped         bool       `json:"capped"`
	UnitsUsedMonth int64      `json:"units_used_month"`
	LastActivityAt *time.Time `json:"last_activity_at,omitempty"`
}

// AdminTenantPage is one page of the tenant list.
type AdminTenantPage struct {
	Service string           `json:"service"`
	Data    []AdminTenantRow `json:"data"`
	Total   int              `json:"total"`
	Limit   int              `json:"limit"`
	Offset  int              `json:"offset"`
}

// rawTenantRow carries the columns as SQL returns them, before Go-side interpretation.
type rawTenantRow struct {
	row       AdminTenantRow
	tierCode  sql.NullString
	tierEnd   sql.NullString
	tierRenew sql.NullString
}

// finishTenantRow applies the tier and quota interpretation that depends on the registry and the
// clock. A lapsed tier contributes neither its stale allowance nor overage, matching Coverage.
func finishTenantRow(spec billing.MeteredService, raw rawTenantRow, capKES float64, now time.Time) AdminTenantRow {
	row := raw.row
	row.OverageCapKES = capKES
	if spec.AllowsOverage() {
		row.Capped = capKES > 0 && row.OverageKES+spec.UnitPriceKES > capKES
	}
	if len(spec.Tiers) > 0 {
		if tier, ok := spec.Tier(raw.tierCode.String); ok {
			row.TierCode, row.TierName = tier.Code, tier.Name
			if end, err := time.Parse(time.RFC3339, raw.tierEnd.String); err == nil {
				row.TierPeriodEnd = &end
				row.TierActive = end.After(now)
			}
			row.Unlimited = row.TierActive && tier.MonthlyUnit < 0
		}
		row.AutoRenew = raw.tierRenew.String == "true"
		if !row.TierActive {
			row.QuotaBalance = 0
		}
	}
	return row
}

// ListTenants returns one row per tenant that holds a wallet, an activation or overage for the
// service. Search, status filter, paging and the total come from one SQL statement.
func (s *Service) ListTenants(ctx context.Context, params AdminListParams) (AdminTenantPage, error) {
	p, err := params.normalise()
	if err != nil {
		return AdminTenantPage{}, err
	}
	spec, err := adminSpec(p.Service)
	if err != nil {
		return AdminTenantPage{}, err
	}
	db := s.wallets.DB()
	if db == nil {
		return AdminTenantPage{}, errors.New("credit ledger database not configured")
	}
	now := time.Now()
	capKES := s.overageCap(ctx)

	rows, err := db.QueryContext(ctx, adminListSQL,
		spec.BundleTag, spec.QuotaTag, spec.ProductCode, spec.OverageMetric, monthStart(now),
		capKES, spec.UnitPriceKES, likePattern(p.Q), p.Status, p.Limit, p.Offset,
		strings.Join(subscriptions.ExemptSlugs(), ","),
	)
	if err != nil {
		return AdminTenantPage{}, fmt.Errorf("list credit tenants: %w", err)
	}
	defer rows.Close()

	page := AdminTenantPage{Service: spec.Code, Data: []AdminTenantRow{}, Limit: p.Limit, Offset: p.Offset}
	for rows.Next() {
		var raw rawTenantRow
		r := &raw.row
		var lastAt sql.NullTime
		if err := rows.Scan(
			&r.TenantID, &r.Name, &r.Slug, &r.Exempt, &r.PlanName, &r.PlanStatus, &r.Status, &r.Activated,
			&r.BundleBalance, &r.QuotaBalance, &r.OverageKES, &r.UnitsUsedMonth, &lastAt,
			&raw.tierCode, &raw.tierEnd, &raw.tierRenew, &page.Total,
		); err != nil {
			return AdminTenantPage{}, fmt.Errorf("scan credit tenant: %w", err)
		}
		if lastAt.Valid {
			t := lastAt.Time
			r.LastActivityAt = &t
		}
		page.Data = append(page.Data, finishTenantRow(spec, raw, capKES, now))
	}
	if err := rows.Err(); err != nil {
		return AdminTenantPage{}, fmt.Errorf("list credit tenants: %w", err)
	}
	return page, nil
}

// adminListSQL parameters: $1 bundle tag, $2 quota tag, $3 activation product code, $4 overage
// metric, $5 month start, $6 cap KES, $7 unit price KES, $8 ILIKE pattern, $9 status filter,
// $10 limit, $11 offset, $12 comma separated exempt slugs. An empty tag, code or metric matches
// nothing, which is how services without a quota wallet or activation product fall out.
const adminListSQL = `
WITH scope AS (
	SELECT tenant_id FROM api_token_wallets WHERE service_tag IN ($1::text, $2::text)
	UNION
	SELECT ts.tenant_id FROM product_subscriptions ps
		JOIN tenant_subscriptions ts ON ts.id = ps.tenant_subscription_id
		WHERE ps.product_code = $3::text
	UNION
	SELECT tenant_id FROM overage_charges WHERE metric_type = $4::text
),
cand AS (
	SELECT t.id, t.name, t.slug,
		(t.subscription_exempt OR t.slug = ANY(string_to_array($12::text, ','))) AS exempt
	FROM tenants t
	WHERE t.id IN (SELECT tenant_id FROM scope)
		AND ($8::text = '' OR t.name ILIKE $8::text OR t.slug ILIKE $8::text)
),
wal AS (
	SELECT tenant_id,
		COALESCE(SUM(balance) FILTER (WHERE service_tag = $1::text), 0)::bigint AS bundle,
		COALESCE(SUM(balance) FILTER (WHERE service_tag = $2::text), 0)::bigint AS quota,
		COALESCE(SUM(lifetime_granted) FILTER (WHERE service_tag = $1::text), 0)::bigint AS lifetime
	FROM api_token_wallets
	WHERE service_tag IN ($1::text, $2::text) AND tenant_id IN (SELECT id FROM cand)
	GROUP BY tenant_id
),
ovg AS (
	SELECT tenant_id, COALESCE(SUM(total_charge_kes), 0)::float8 AS pending
	FROM overage_charges
	WHERE metric_type = $4::text AND status = 'pending' AND tenant_id IN (SELECT id FROM cand)
	GROUP BY tenant_id
),
usage AS (
	SELECT tenant_id,
		GREATEST(0,
			COALESCE(SUM(-tokens) FILTER (WHERE action IN ('deduction', 'overage') AND created_at >= $5::timestamptz), 0)
			+ COALESCE(SUM(tokens) FILTER (WHERE action = 'refund' AND created_at >= $5::timestamptz), 0)
		)::bigint AS used,
		MAX(created_at) FILTER (WHERE action IN ('deduction', 'overage', 'topup')) AS last_at
	FROM api_token_transactions
	WHERE service_tag IN ($1::text, $2::text) AND tenant_id IN (SELECT id FROM cand)
	GROUP BY tenant_id
),
act AS (
	SELECT ts.tenant_id, ps.status, ps.metadata
	FROM product_subscriptions ps
		JOIN tenant_subscriptions ts ON ts.id = ps.tenant_subscription_id
	WHERE ps.product_code = $3::text AND ts.tenant_id IN (SELECT id FROM cand)
),
joined AS (
	SELECT c.id, c.name, c.slug, c.exempt,
		COALESCE(pl.name, '') AS plan_name,
		COALESCE(sub.status::text, '') AS plan_status,
		COALESCE(w.bundle, 0) AS bundle, COALESCE(w.quota, 0) AS quota,
		COALESCE(o.pending, 0) AS pending,
		COALESCE(u.used, 0) AS used, u.last_at,
		act.metadata ->> 'tier_code' AS tier_code,
		act.metadata ->> 'tier_period_end' AS tier_end,
		act.metadata ->> 'tier_auto_renew' AS tier_renew,
		CASE WHEN $3::text <> '' THEN COALESCE(act.status::text = 'active', false)
			ELSE (COALESCE(w.bundle, 0) > 0 OR COALESCE(w.quota, 0) > 0 OR COALESCE(w.lifetime, 0) > 0) END AS activated
	FROM cand c
		LEFT JOIN tenant_subscriptions sub ON sub.tenant_id = c.id
		LEFT JOIN subscription_plans pl ON pl.id = sub.plan_id
		LEFT JOIN wal w ON w.tenant_id = c.id
		LEFT JOIN ovg o ON o.tenant_id = c.id
		LEFT JOIN usage u ON u.tenant_id = c.id
		LEFT JOIN act ON act.tenant_id = c.id
),
statused AS (
	SELECT j.*,
		CASE
			WHEN j.exempt THEN 'exempt'
			WHEN $4::text <> '' AND $6::float8 > 0 AND j.pending + $7::float8 > $6::float8 THEN 'capped'
			WHEN j.activated THEN 'active'
			ELSE 'not_activated'
		END AS status
	FROM joined j
)
SELECT id, name, slug, exempt, plan_name, plan_status, status, activated, bundle, quota, pending, used, last_at,
	tier_code, tier_end, tier_renew, count(*) OVER ()
FROM statused
WHERE $9::text = '' OR status = $9::text
ORDER BY last_at DESC NULLS LAST, name ASC, id ASC
LIMIT $10 OFFSET $11`

// AdminServiceOverview is the platform-wide position of one credit service.
type AdminServiceOverview struct {
	Service            string  `json:"service"`
	Label              string  `json:"label"`
	AllowsOverage      bool    `json:"allows_overage"`
	TenantsActivated   int64   `json:"tenants_activated"`
	UnitsSoldMonth     int64   `json:"units_sold_month"`
	UnitsConsumedMonth int64   `json:"units_consumed_month"`
	BundleRevenueKES   float64 `json:"bundle_revenue_month_kes"`
	OverageAccruedKES  float64 `json:"overage_accrued_month_kes"`
	OverageOutstanding float64 `json:"overage_outstanding_kes"`
	TenantsAtCap       int64   `json:"tenants_at_cap"`
}

// AdminOverview is the response of the overview endpoint.
type AdminOverview struct {
	MonthStart    time.Time              `json:"month_start"`
	OverageCapKES float64                `json:"overage_cap_kes"`
	Services      []AdminServiceOverview `json:"services"`
}

// Overview totals every tenant-purchasable credit service for the current UTC month.
func (s *Service) Overview(ctx context.Context) (AdminOverview, error) {
	db := s.wallets.DB()
	if db == nil {
		return AdminOverview{}, errors.New("credit ledger database not configured")
	}
	now := time.Now()
	capKES := s.overageCap(ctx)
	out := AdminOverview{MonthStart: monthStart(now), OverageCapKES: capKES, Services: []AdminServiceOverview{}}
	for _, c := range Catalog() {
		spec, _ := billing.LookupMeteredService(c.Service)
		o := AdminServiceOverview{Service: spec.Code, Label: spec.Label, AllowsOverage: spec.AllowsOverage()}
		err := db.QueryRowContext(ctx, adminOverviewSQL,
			spec.BundleTag, spec.QuotaTag, spec.ProductCode, spec.OverageMetric, out.MonthStart, capKES, spec.UnitPriceKES,
		).Scan(&o.TenantsActivated, &o.UnitsSoldMonth, &o.UnitsConsumedMonth, &o.BundleRevenueKES,
			&o.OverageAccruedKES, &o.OverageOutstanding, &o.TenantsAtCap)
		if err != nil {
			return AdminOverview{}, fmt.Errorf("credits overview %s: %w", spec.Code, err)
		}
		out.Services = append(out.Services, o)
	}
	return out, nil
}

// adminOverviewSQL shares its parameter numbering with adminListSQL ($1 to $7).
const adminOverviewSQL = `
SELECT
	CASE WHEN $3::text <> '' THEN
		(SELECT count(DISTINCT ts.tenant_id) FROM product_subscriptions ps
			JOIN tenant_subscriptions ts ON ts.id = ps.tenant_subscription_id
			WHERE ps.product_code = $3::text AND ps.status = 'active')
	ELSE
		(SELECT count(*) FROM api_token_wallets WHERE service_tag = $1::text AND (balance > 0 OR lifetime_granted > 0))
	END::bigint,
	COALESCE((SELECT SUM(tokens) FROM api_token_transactions
		WHERE service_tag = $1::text AND action = 'topup' AND created_at >= $5::timestamptz), 0)::bigint,
	GREATEST(0, COALESCE((SELECT SUM(-tokens) FROM api_token_transactions
		WHERE service_tag IN ($1::text, $2::text) AND action IN ('deduction', 'overage') AND created_at >= $5::timestamptz), 0)
		+ COALESCE((SELECT SUM(tokens) FROM api_token_transactions
		WHERE service_tag IN ($1::text, $2::text) AND action = 'refund' AND created_at >= $5::timestamptz), 0))::bigint,
	COALESCE((SELECT SUM(tokens * unit_cost_kes) FROM api_token_transactions
		WHERE service_tag = $1::text AND action = 'topup' AND unit_cost_kes IS NOT NULL AND created_at >= $5::timestamptz), 0)::float8,
	COALESCE((SELECT SUM((metadata ->> 'kes')::float8) FROM api_token_transactions
		WHERE service_tag = $1::text AND action = 'overage' AND created_at >= $5::timestamptz), 0)::float8,
	COALESCE((SELECT SUM(total_charge_kes) FROM overage_charges
		WHERE metric_type = $4::text AND status = 'pending'), 0)::float8,
	(SELECT count(*) FROM (
		SELECT tenant_id FROM overage_charges
		WHERE $6::float8 > 0 AND metric_type = $4::text AND status = 'pending'
		GROUP BY tenant_id HAVING SUM(total_charge_kes) + $7::float8 > $6::float8) capped)::bigint`

// AdminSettings are the platform messaging settings an operator may change.
type AdminSettings struct {
	OverageCapKES        float64    `json:"overage_cap_kes"`
	DefaultOverageCapKES float64    `json:"default_overage_cap_kes"`
	MaxOverageCapKES     float64    `json:"max_overage_cap_kes"`
	UpdatedAt            *time.Time `json:"updated_at,omitempty"`
}

// ValidateOverageCap accepts zero (no cap) up to MaxOverageCapKES and rejects NaN, infinity,
// negatives and more than two decimal places.
func ValidateOverageCap(v float64) error {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return errors.New("overage_cap_kes must be a number")
	}
	if v < 0 {
		return errors.New("overage_cap_kes cannot be negative")
	}
	if v > MaxOverageCapKES {
		return fmt.Errorf("overage_cap_kes cannot exceed %.0f", MaxOverageCapKES)
	}
	if math.Abs(v*100-math.Round(v*100)) > 1e-6 {
		return errors.New("overage_cap_kes allows at most two decimal places")
	}
	return nil
}

// Settings returns the current platform messaging settings.
func (s *Service) Settings(ctx context.Context) (AdminSettings, error) {
	out := AdminSettings{OverageCapKES: s.overageCap(ctx), DefaultOverageCapKES: DefaultOverageCapKES, MaxOverageCapKES: MaxOverageCapKES}
	cfg, err := s.orm.ServiceConfig.Query().
		Where(serviceconfig.ConfigKeyEQ(OverageCapConfigKey), serviceconfig.TenantIDIsNil()).Only(ctx)
	switch {
	case err == nil:
		t := cfg.UpdatedAt
		out.UpdatedAt = &t
	case !ent.IsNotFound(err):
		return AdminSettings{}, fmt.Errorf("read messaging settings: %w", err)
	}
	return out, nil
}

// SetOverageCap validates and stores messaging.overage_cap_kes at platform level, then drops this
// replica's cached copy. Other replicas pick the change up when their one minute cache expires.
func (s *Service) SetOverageCap(ctx context.Context, kes float64) (AdminSettings, error) {
	if err := ValidateOverageCap(kes); err != nil {
		return AdminSettings{}, err
	}
	value := fmt.Sprintf("%.2f", kes)
	cfg, err := s.orm.ServiceConfig.Query().
		Where(serviceconfig.ConfigKeyEQ(OverageCapConfigKey), serviceconfig.TenantIDIsNil()).Only(ctx)
	switch {
	case err == nil:
		err = s.orm.ServiceConfig.UpdateOne(cfg).SetConfigValue(value).SetConfigType("float").Exec(ctx)
	case ent.IsNotFound(err):
		err = s.orm.ServiceConfig.Create().SetConfigKey(OverageCapConfigKey).SetConfigValue(value).
			SetConfigType("float").
			SetDescription("Most unpaid messaging overage in KES a tenant can run up before sends pause. 0 means no cap.").
			Exec(ctx)
	}
	if err != nil {
		return AdminSettings{}, fmt.Errorf("store overage cap: %w", err)
	}
	s.capMu.Lock()
	s.capAt = time.Time{}
	s.capMu.Unlock()
	return s.Settings(ctx)
}
