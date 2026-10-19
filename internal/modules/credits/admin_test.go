package credits

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/bengobox/subscription-service/internal/modules/billing"
)

func TestAdminListParamsNormalise(t *testing.T) {
	tests := []struct {
		name       string
		in         AdminListParams
		wantLimit  int
		wantOffset int
		wantStatus string
		wantErr    bool
	}{
		{"defaults", AdminListParams{}, 25, 0, "", false},
		{"limit clamped", AdminListParams{Limit: 5000}, 100, 0, "", false},
		{"negative offset", AdminListParams{Limit: 10, Offset: -4}, 10, 0, "", false},
		{"status folded", AdminListParams{Status: " Capped "}, 25, 0, "capped", false},
		{"unknown status", AdminListParams{Status: "paused"}, 0, 0, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.in.normalise()
			if tt.wantErr {
				if !errors.Is(err, ErrInvalidAdminQuery) {
					t.Fatalf("want ErrInvalidAdminQuery, got %v", err)
				}
				return
			}
			if err != nil || got.Limit != tt.wantLimit || got.Offset != tt.wantOffset || got.Status != tt.wantStatus {
				t.Fatalf("got %+v err=%v", got, err)
			}
		})
	}
}

func TestAdminSpecOnlyAcceptsTenantServices(t *testing.T) {
	for _, code := range []string{"sms", "whatsapp", "ai_credits"} {
		if _, err := adminSpec(code); err != nil {
			t.Errorf("%s should be accepted: %v", code, err)
		}
	}
	for _, code := range []string{"", "etims_api", "email"} {
		if _, err := adminSpec(code); !errors.Is(err, ErrInvalidAdminQuery) {
			t.Errorf("%q should be rejected, got %v", code, err)
		}
	}
}

func TestLikePatternEscapesWildcards(t *testing.T) {
	tests := map[string]string{
		"":        "",
		"acme":    "%acme%",
		"50%_off": `%50\%\_off%`,
		`back\sl`: `%back\\sl%`,
	}
	for in, want := range tests {
		if got := likePattern(in); got != want {
			t.Errorf("likePattern(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMonthStartIsUTCFirstOfMonth(t *testing.T) {
	nairobi := time.FixedZone("EAT", 3*3600)
	// 00:30 on 1 Nov in Nairobi is still 31 Oct in UTC.
	got := monthStart(time.Date(2026, 11, 1, 0, 30, 0, 0, nairobi))
	want := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestValidateOverageCap(t *testing.T) {
	tests := []struct {
		name    string
		v       float64
		wantErr bool
	}{
		{"zero means uncapped", 0, false},
		{"default", 500, false},
		{"two decimals", 99.95, false},
		{"upper bound", MaxOverageCapKES, false},
		{"negative", -1, true},
		{"too large", MaxOverageCapKES + 1, true},
		{"three decimals", 10.001, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := ValidateOverageCap(tt.v); (err != nil) != tt.wantErr {
				t.Fatalf("ValidateOverageCap(%v) err=%v wantErr=%v", tt.v, err, tt.wantErr)
			}
		})
	}
}

func TestFinishTenantRow(t *testing.T) {
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	wa, _ := billing.LookupMeteredService("whatsapp")
	sms, _ := billing.LookupMeteredService("sms")
	ai, _ := billing.LookupMeteredService("ai_credits")
	future := now.Add(48 * time.Hour).Format(time.RFC3339)
	past := now.Add(-48 * time.Hour).Format(time.RFC3339)

	raw := func(pending float64, quota int64, code, end, renew string) rawTenantRow {
		r := rawTenantRow{}
		r.row.OverageKES = pending
		r.row.QuotaBalance = quota
		r.tierCode = sql.NullString{String: code, Valid: code != ""}
		r.tierEnd = sql.NullString{String: end, Valid: end != ""}
		r.tierRenew = sql.NullString{String: renew, Valid: renew != ""}
		return r
	}

	tests := []struct {
		name          string
		spec          billing.MeteredService
		raw           rawTenantRow
		cap           float64
		wantCapped    bool
		wantTierOn    bool
		wantUnlimited bool
		wantQuota     int64
		wantRenew     bool
	}{
		{"live tier keeps quota", wa, raw(0, 120, "tier_1", future, "true"), 500, false, true, false, 120, true},
		{"lapsed tier drops stale quota", wa, raw(0, 120, "tier_1", past, "false"), 500, false, false, false, 0, false},
		{"pro is unlimited while live", wa, raw(0, 0, "pro", future, "true"), 500, false, true, true, 0, true},
		{"pro lapsed is not unlimited", wa, raw(0, 0, "pro", past, ""), 500, false, false, false, 0, false},
		{"unknown tier code ignored", wa, raw(0, 5, "legacy", future, ""), 500, false, false, false, 0, false},
		{"one unit short of cap still sends", sms, raw(498, 0, "", "", ""), 500, false, false, false, 0, false},
		{"at cap boundary pauses", sms, raw(500, 0, "", "", ""), 500, true, false, false, 0, false},
		{"cap zero is uncapped", sms, raw(9999, 0, "", "", ""), 0, false, false, false, 0, false},
		{"prepaid only service is never capped", ai, raw(9999, 40, "", "", ""), 500, false, false, false, 40, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := finishTenantRow(tt.spec, tt.raw, tt.cap, now)
			if got.Capped != tt.wantCapped || got.TierActive != tt.wantTierOn || got.Unlimited != tt.wantUnlimited ||
				got.QuotaBalance != tt.wantQuota || got.AutoRenew != tt.wantRenew {
				t.Fatalf("got %+v", got)
			}
			if got.OverageCapKES != tt.cap {
				t.Fatalf("cap not carried: %v", got.OverageCapKES)
			}
		})
	}
}

// Postgres-backed (skipped unless TEST_DATABASE_URL is set).
func TestAdminListOverviewAndSettings(t *testing.T) {
	f := newFixture(t, map[string]any{})
	ctx := context.Background()
	f.grant(t, "sms", KindBundle, "bundle_100", "g1", 1)
	f.grant(t, "whatsapp", KindTier, "tier_1", "t1", 1)
	f.spend(t, "sms", 130, "big") // 100 from the bundle, 30 overage
	f.spend(t, "whatsapp", 50, "w1")

	// A tenant that never touched messaging must not appear.
	if _, err := f.orm.Tenant.Create().SetID(uuid.New()).SetName("Idle Co").SetSlug("idle-co").Save(ctx); err != nil {
		t.Fatal(err)
	}
	// The platform tenant holds a wallet but is exempt.
	plat, err := f.orm.Tenant.Create().SetID(uuid.New()).SetName("Platform").SetSlug("codevertex").Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.svc.wallets.Credit(ctx, billing.CreditRequest{TenantID: plat.ID, ServiceTag: "sms", Units: 10, Action: "adjustment", Key: "p1"}); err != nil {
		t.Fatal(err)
	}

	page, err := f.svc.ListTenants(ctx, AdminListParams{Service: "sms"})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 2 || len(page.Data) != 2 {
		t.Fatalf("sms tenants: total=%d rows=%d", page.Total, len(page.Data))
	}
	var alpha, platform *AdminTenantRow
	for i := range page.Data {
		switch page.Data[i].TenantID {
		case f.tenantID:
			alpha = &page.Data[i]
		case plat.ID:
			platform = &page.Data[i]
		}
	}
	if alpha == nil || platform == nil {
		t.Fatalf("missing rows: %+v", page.Data)
	}
	if alpha.Status != AdminStatusActive || alpha.BundleBalance != 0 || alpha.OverageKES != 30 ||
		alpha.UnitsUsedMonth != 130 || alpha.PlanName != "Duka Basic" || alpha.PlanStatus != "ACTIVE" || alpha.LastActivityAt == nil {
		t.Fatalf("alpha row: %+v", alpha)
	}
	if platform.Status != AdminStatusExempt || !platform.Exempt {
		t.Fatalf("platform row: %+v", platform)
	}

	// Filters and paging.
	if p, _ := f.svc.ListTenants(ctx, AdminListParams{Service: "sms", Status: "exempt"}); p.Total != 1 || p.Data[0].TenantID != plat.ID {
		t.Fatalf("exempt filter: %+v", p)
	}
	if p, _ := f.svc.ListTenants(ctx, AdminListParams{Service: "sms", Q: "ALPH"}); p.Total != 1 || p.Data[0].TenantID != f.tenantID {
		t.Fatalf("search: %+v", p)
	}
	if p, _ := f.svc.ListTenants(ctx, AdminListParams{Service: "sms", Limit: 1}); len(p.Data) != 1 || p.Total != 2 {
		t.Fatalf("paging: %+v", p)
	}

	// WhatsApp row carries tier and quota left.
	wp, err := f.svc.ListTenants(ctx, AdminListParams{Service: "whatsapp"})
	if err != nil || wp.Total != 1 {
		t.Fatalf("whatsapp list: %+v %v", wp, err)
	}
	if r := wp.Data[0]; r.TierCode != "tier_1" || !r.TierActive || r.QuotaBalance != 350 || r.UnitsUsedMonth != 50 {
		t.Fatalf("whatsapp row: %+v", r)
	}

	// Push alpha to the cap and see it flagged.
	f.spend(t, "sms", 470, "fill")
	if p, _ := f.svc.ListTenants(ctx, AdminListParams{Service: "sms", Status: "capped"}); p.Total != 1 || !p.Data[0].Capped {
		t.Fatalf("capped filter: %+v", p)
	}

	ov, err := f.svc.Overview(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var smsOv AdminServiceOverview
	for _, s := range ov.Services {
		if s.Service == "sms" {
			smsOv = s
		}
	}
	if smsOv.UnitsConsumedMonth != 600 || smsOv.OverageAccruedKES != 500 || smsOv.OverageOutstanding != 500 || smsOv.TenantsAtCap != 1 {
		t.Fatalf("sms overview: %+v", smsOv)
	}

	// Settings round trip busts the cache so the new cap applies at once.
	if _, err := f.svc.SetOverageCap(ctx, 1000); err != nil {
		t.Fatal(err)
	}
	st, err := f.svc.Settings(ctx)
	if err != nil || st.OverageCapKES != 1000 || st.UpdatedAt == nil {
		t.Fatalf("settings: %+v %v", st, err)
	}
	if d := f.spend(t, "sms", 1, "after-raise"); !d.Allowed {
		t.Fatalf("raised cap should allow more: %+v", d)
	}
	if _, err := f.svc.SetOverageCap(ctx, -5); err == nil {
		t.Fatal("negative cap accepted")
	}
}
