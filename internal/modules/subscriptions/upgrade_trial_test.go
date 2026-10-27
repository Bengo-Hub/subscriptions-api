package subscriptions

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/tenantsubscription"
)

func cfgRow(tenant *uuid.UUID, key, value string, updated time.Time) *ent.ServiceConfig {
	return &ent.ServiceConfig{TenantID: tenant, ConfigKey: key, ConfigValue: value, UpdatedAt: updated}
}

func TestResolveUpgradeTrialPolicy(t *testing.T) {
	now := time.Now()
	tid := uuid.New()

	p := resolveUpgradeTrialPolicy(nil)
	if p.Enabled || p.Days != DefaultUpgradeTrialDays || p.EnabledSource != PolicySourceDefault {
		t.Fatalf("no rows: got %+v", p)
	}

	p = resolveUpgradeTrialPolicy([]*ent.ServiceConfig{
		cfgRow(nil, UpgradeTrialEnabledConfigKey, "true", now),
		cfgRow(nil, UpgradeTrialDaysConfigKey, "7", now),
	})
	if !p.Enabled || p.Days != 7 || p.EnabledSource != PolicySourcePlatform || p.DaysSource != PolicySourcePlatform {
		t.Fatalf("platform rows: got %+v", p)
	}

	// The tenant row wins over the platform row, for each key independently.
	p = resolveUpgradeTrialPolicy([]*ent.ServiceConfig{
		cfgRow(nil, UpgradeTrialEnabledConfigKey, "true", now),
		cfgRow(nil, UpgradeTrialDaysConfigKey, "7", now),
		cfgRow(&tid, UpgradeTrialEnabledConfigKey, "false", now),
	})
	if p.Enabled || p.EnabledSource != PolicySourceTenant || p.Days != 7 || p.DaysSource != PolicySourcePlatform {
		t.Fatalf("tenant override: got %+v", p)
	}

	// Unparseable or out-of-range values are ignored; the newest of duplicate rows wins.
	p = resolveUpgradeTrialPolicy([]*ent.ServiceConfig{
		cfgRow(nil, UpgradeTrialEnabledConfigKey, "false", now.Add(-time.Hour)),
		cfgRow(nil, UpgradeTrialEnabledConfigKey, `"true"`, now),
		cfgRow(nil, UpgradeTrialDaysConfigKey, "500", now),
		cfgRow(&tid, UpgradeTrialDaysConfigKey, "soon", now),
	})
	if !p.Enabled || p.Days != DefaultUpgradeTrialDays || p.DaysSource != PolicySourceDefault {
		t.Fatalf("bad values: got %+v", p)
	}
}

func TestValidateUpgradeTrialDays(t *testing.T) {
	for _, d := range []int{0, -1, MaxUpgradeTrialDays + 1} {
		if ValidateUpgradeTrialDays(d) == nil {
			t.Fatalf("%d days accepted", d)
		}
	}
	for _, d := range []int{1, 14, MaxUpgradeTrialDays} {
		if ValidateUpgradeTrialDays(d) != nil {
			t.Fatalf("%d days rejected", d)
		}
	}
}

func trialPlan(code string, tier int, price float64, cycle string) *ent.SubscriptionPlan {
	return &ent.SubscriptionPlan{ID: uuid.New(), PlanCode: code, Name: code, TierOrder: tier, BasePrice: price,
		BillingCycle: cycle, IsActive: true, IsPublic: true}
}

func TestCheckUpgradeTrial(t *testing.T) {
	now := time.Now().UTC()
	basic := trialPlan("POWERSUITE_DUKA_BASIC", 1, 3000, "MONTHLY")
	pro := trialPlan("POWERSUITE_DUKA_PRO", 2, 6000, "MONTHLY")
	on := UpgradeTrialPolicy{Enabled: true, Days: 14}
	newSub := func() *ent.TenantSubscription {
		return &ent.TenantSubscription{ID: uuid.New(), PlanID: basic.ID, Status: tenantsubscription.StatusACTIVE,
			BillingCycle: "MONTHLY", CurrentPeriodStart: now.AddDate(0, 0, -10), CurrentPeriodEnd: now.AddDate(0, 0, 20),
			Metadata: map[string]any{}}
	}
	check := func(mut func(c *upgradeTrialCheck)) string {
		c := upgradeTrialCheck{sub: newSub(), paid: basic, target: pro, policy: on, now: now}
		if mut != nil {
			mut(&c)
		}
		if e := checkUpgradeTrial(c); e != nil {
			return e.Reason
		}
		return ""
	}

	cases := []struct {
		name string
		mut  func(c *upgradeTrialCheck)
		want string
	}{
		{"eligible", nil, ""},
		{"policy off", func(c *upgradeTrialCheck) { c.policy.Enabled = false }, TrialReasonDisabled},
		{"policy off, platform grants", func(c *upgradeTrialCheck) { c.policy.Enabled = false; c.byPlatform = true }, ""},
		{"expired", func(c *upgradeTrialCheck) { c.sub.Status = tenantsubscription.StatusEXPIRED }, TrialReasonStatus},
		{"past due", func(c *upgradeTrialCheck) { c.sub.CurrentPeriodEnd = now.Add(-time.Hour) }, TrialReasonPastDue},
		{"trial running", func(c *upgradeTrialCheck) {
			c.sub.Metadata[MetaUpgradeTrial] = encodeMeta(&UpgradeTrial{PlanID: pro.ID, BasePlanID: basic.ID, EndsAt: now.Add(time.Hour)})
		}, TrialReasonTrialActive},
		{"change scheduled", func(c *upgradeTrialCheck) {
			c.sub.Metadata[MetaScheduledPlanChange] = encodeMeta(&ScheduledPlanChange{PlanID: pro.ID, FromPlanID: basic.ID, EffectiveAt: c.sub.CurrentPeriodEnd})
		}, TrialReasonChangeExists},
		{"hidden plan", func(c *upgradeTrialCheck) { p := *pro; p.IsPublic = false; c.target = &p }, TrialReasonPlanNotFound},
		{"hidden plan, platform", func(c *upgradeTrialCheck) { p := *pro; p.IsPublic = false; c.target = &p; c.byPlatform = true }, ""},
		{"missing plan", func(c *upgradeTrialCheck) { c.target = nil }, TrialReasonPlanNotFound},
		{"one-time", func(c *upgradeTrialCheck) { p := *pro; p.BillingCycle = "ONE_TIME"; c.target = &p }, TrialReasonOneTime},
		{"other family", func(c *upgradeTrialCheck) { c.target = trialPlan("ERP_GROWTH", 2, 6000, "MONTHLY") }, TrialReasonOtherFamily},
		{"same tier", func(c *upgradeTrialCheck) { c.target = trialPlan("POWERSUITE_DUKA_PLUS", 1, 4000, "MONTHLY") }, TrialReasonNotUpgrade},
		{"free", func(c *upgradeTrialCheck) { p := *pro; p.BasePrice = 0; c.target = &p }, TrialReasonFreePlan},
		{"used before", func(c *upgradeTrialCheck) { c.sub.Metadata[MetaUpgradeTrialsUsed] = []any{pro.PlanCode} }, TrialReasonAlreadyUsed},
		{"used before, platform", func(c *upgradeTrialCheck) {
			c.sub.Metadata[MetaUpgradeTrialsUsed] = []any{pro.PlanCode}
			c.byPlatform = true
		}, ""},
	}
	for _, tc := range cases {
		if got := check(tc.mut); got != tc.want {
			t.Errorf("%s: reason %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestPlanInForceAndBillingPlan(t *testing.T) {
	now := time.Now().UTC()
	basic, pro := uuid.New(), uuid.New()
	end := now.AddDate(0, 0, 20)
	sub := &ent.TenantSubscription{PlanID: basic, Status: tenantsubscription.StatusACTIVE, CurrentPeriodEnd: end, Metadata: map[string]any{}}

	if PlanIDInForce(sub, now) != basic || BillingPlanIDFor(sub) != basic {
		t.Fatal("no trial or schedule: paid plan expected")
	}

	sub.Metadata[MetaUpgradeTrial] = encodeMeta(&UpgradeTrial{PlanID: pro, BasePlanID: basic, EndsAt: now.AddDate(0, 0, 5)})
	sub.Metadata[MetaScheduledPlanChange] = encodeMeta(&ScheduledPlanChange{PlanID: pro, FromPlanID: basic, EffectiveAt: end})
	if PlanIDInForce(sub, now) != pro {
		t.Fatal("running trial: trial plan expected in force")
	}
	if BillingPlanIDFor(sub) != pro {
		t.Fatal("change due at the renewal: the renewal invoice must bill the new plan")
	}
	// After the trial ends and before the renewal date the paid plan is back in force.
	if PlanIDInForce(sub, now.AddDate(0, 0, 6)) != basic {
		t.Fatal("between trial end and renewal: paid plan expected")
	}
	// From the renewal date the scheduled plan applies even before the job moves plan_id.
	if PlanIDInForce(sub, end.Add(time.Minute)) != pro {
		t.Fatal("after the renewal date: scheduled plan expected")
	}
	// A suspended subscription gets no trial overlay.
	sub.Status = tenantsubscription.StatusSUSPENDED
	if _, ok := ActiveUpgradeTrial(sub, now); ok {
		t.Fatal("trial in force on a suspended subscription")
	}
	// A stale trial or schedule (plan changed by an admin since) is inert.
	sub.Status = tenantsubscription.StatusACTIVE
	sub.PlanID = uuid.New()
	if BillingPlanIDFor(sub) != sub.PlanID || PlanIDInForce(sub, now) != sub.PlanID {
		t.Fatal("stale trial or schedule must be inert")
	}
}

func TestNextRenewalAtOrAfter(t *testing.T) {
	end := time.Date(2026, 11, 30, 0, 0, 0, 0, time.UTC)
	sub := &ent.TenantSubscription{CurrentPeriodEnd: end, BillingCycle: "MONTHLY"}
	if got := nextRenewalAtOrAfter(sub, end.AddDate(0, 0, -5)); !got.Equal(end) {
		t.Fatalf("trial ends before renewal: %s", got)
	}
	if got := nextRenewalAtOrAfter(sub, end.AddDate(0, 0, 3)); !got.Equal(end.AddDate(0, 1, 0)) {
		t.Fatalf("trial ends after renewal: %s", got)
	}
	sub.BillingCycle = "SEMI_ANNUAL"
	if got := nextRenewalAtOrAfter(sub, end.AddDate(0, 0, 3)); !got.Equal(end.AddDate(0, 6, 0)) {
		t.Fatalf("semi-annual: %s", got)
	}
}

func TestScheduledChangeMovesWithPeriodEnd(t *testing.T) {
	end := time.Date(2026, 11, 30, 0, 0, 0, 0, time.UTC)
	basic, pro := uuid.New(), uuid.New()
	sub := &ent.TenantSubscription{PlanID: basic, CurrentPeriodEnd: end, Metadata: map[string]any{
		MetaScheduledPlanChange: encodeMeta(&ScheduledPlanChange{PlanID: pro, FromPlanID: basic, EffectiveAt: end}),
	}}
	later := end.AddDate(0, 0, 10)
	meta, moved := WithScheduledChangeMoved(sub, later)
	if !moved {
		t.Fatal("change due on the old period end was not moved")
	}
	sub.Metadata, sub.CurrentPeriodEnd = meta, later
	if pc, _ := PendingPlanChange(sub); !pc.EffectiveAt.Equal(later) {
		t.Fatalf("moved to %s", pc.EffectiveAt)
	}
	if _, moved := WithScheduledChangeMoved(sub, later); moved {
		t.Fatal("moved with no change in the period end")
	}
}

func TestMaxLimitFamilyDays(t *testing.T) {
	if maxLimit(5, -1) != -1 || maxLimit(-1, 5) != -1 || maxLimit(5, 9) != 9 || maxLimit(9, 5) != 9 {
		t.Fatal("maxLimit")
	}
	if PlanFamily("POWERSUITE_DUKA_GOLD") != "POWERSUITE_DUKA" || PlanFamily("ERP_STARTER") != "ERP" {
		t.Fatal("PlanFamily")
	}
	if daysUntil(time.Unix(0, 0), time.Unix(0, 0).Add(25*time.Hour)) != 2 || daysUntil(time.Unix(100, 0), time.Unix(0, 0)) != 0 {
		t.Fatal("daysUntil")
	}
	refs := AppendPlanChangePaymentRefs(map[string]any{MetaPlanChangePaymentRefs: []any{"a"}}, "a", "b", "")
	if len(refs) != 2 {
		t.Fatalf("payment refs: %v", refs)
	}
}
