package subscriptions

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/tenantsubscription"
)

func TestSetupFeeOwedOnlyBeforeTheFirstPaidPeriod(t *testing.T) {
	created := time.Now().Add(-90 * 24 * time.Hour)
	trialEnd := created.Add(14 * 24 * time.Hour)
	charged := created.Add(14 * 24 * time.Hour)

	cases := []struct {
		name string
		sub  ent.TenantSubscription
		want float64
	}{
		{
			name: "trial on first checkout",
			sub:  ent.TenantSubscription{Status: tenantsubscription.StatusTRIAL, TrialEndsAt: &trialEnd, SetupFeeAmount: 2500, CreatedAt: created, CurrentPeriodStart: created},
			want: 2500,
		},
		{
			name: "lapsed trial that never paid",
			sub:  ent.TenantSubscription{Status: tenantsubscription.StatusEXPIRED, TrialEndsAt: &trialEnd, SetupFeeAmount: 2500, CreatedAt: created, CurrentPeriodStart: created},
			want: 2500,
		},
		{
			name: "created active, not paid yet",
			sub:  ent.TenantSubscription{Status: tenantsubscription.StatusACTIVE, SetupFeeAmount: 2500, CreatedAt: created, CurrentPeriodStart: created},
			want: 2500,
		},
		{
			name: "fee already charged, upgrading",
			sub:  ent.TenantSubscription{Status: tenantsubscription.StatusACTIVE, SetupFeeAmount: 2500, SetupFeeChargedAt: &charged, CreatedAt: created, CurrentPeriodStart: charged},
			want: 0,
		},
		{
			name: "paid before but never marked charged, upgrading",
			sub:  ent.TenantSubscription{Status: tenantsubscription.StatusACTIVE, SetupFeeAmount: 2500, CreatedAt: created, CurrentPeriodStart: charged},
			want: 0,
		},
		{
			name: "payment recorded, marker lost",
			sub: ent.TenantSubscription{Status: tenantsubscription.StatusACTIVE, SetupFeeAmount: 2500, CreatedAt: created, CurrentPeriodStart: created,
				Metadata: map[string]any{MetaRenewedIntents: []any{uuid.NewString()}}},
			want: 0,
		},
		{
			name: "paid, cancelled, coming back",
			sub:  ent.TenantSubscription{Status: tenantsubscription.StatusCANCELLED, SetupFeeAmount: 2500, CreatedAt: created, CurrentPeriodStart: charged},
			want: 0,
		},
		{
			name: "no fee on the subscription",
			sub:  ent.TenantSubscription{Status: tenantsubscription.StatusTRIAL, TrialEndsAt: &trialEnd, CreatedAt: created, CurrentPeriodStart: created},
			want: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := setupFeeOwed(&tc.sub); got != tc.want {
				t.Fatalf("setupFeeOwed = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestResolveSetupFeePolicy(t *testing.T) {
	tenant := uuid.New()
	platformRow := func(v string) *ent.ServiceConfig { return &ent.ServiceConfig{ConfigValue: v} }
	tenantRow := func(v string) *ent.ServiceConfig { return &ent.ServiceConfig{ConfigValue: v, TenantID: &tenant} }

	cases := []struct {
		name    string
		rows    []*ent.ServiceConfig
		enabled bool
		source  string
	}{
		{"no rows defaults on", nil, true, PolicySourceDefault},
		{"platform off", []*ent.ServiceConfig{platformRow("false")}, false, PolicySourcePlatform},
		{"tenant off beats platform on", []*ent.ServiceConfig{platformRow("true"), tenantRow("false")}, false, PolicySourceTenant},
		{"tenant on beats platform off", []*ent.ServiceConfig{platformRow("false"), tenantRow("true")}, true, PolicySourceTenant},
		{"unparseable tenant row is ignored", []*ent.ServiceConfig{platformRow("false"), tenantRow("maybe")}, false, PolicySourcePlatform},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := resolveSetupFeePolicy(tc.rows)
			if p.Enabled != tc.enabled || p.Source != tc.source {
				t.Fatalf("got enabled=%v source=%s, want enabled=%v source=%s", p.Enabled, p.Source, tc.enabled, tc.source)
			}
		})
	}
}
