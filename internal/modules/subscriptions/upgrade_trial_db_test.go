package subscriptions

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/outboxevent"
)

type trialFixture struct {
	svc        *Service
	client     *ent.Client
	tenant     uuid.UUID
	basic, pro *ent.SubscriptionPlan
	periodEnd  time.Time
}

func newTrialFixture(t *testing.T) *trialFixture {
	t.Helper()
	svc, client := newSupportTestService(t)
	ctx := context.Background()
	tn, err := client.Tenant.Create().SetID(uuid.New()).SetName("Trial Shop").SetSlug("trial-" + uuid.NewString()[:6]).Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	mk := func(code string, tier int, price float64, rpm int) *ent.SubscriptionPlan {
		p, err := client.SubscriptionPlan.Create().
			SetPlanCode(code).SetName(code).SetDescription(code).SetBillingCycle("MONTHLY").
			SetBasePrice(price).SetCurrency("KES").SetTierOrder(tier).SetIsPublic(true).
			SetTierLimitsJSON(map[string]any{"max_users": float64(rpm)}).
			Save(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	f := &trialFixture{svc: svc, client: client, tenant: tn.ID,
		basic: mk("POWERSUITE_DUKA_BASIC", 1, 3000, 3), pro: mk("POWERSUITE_DUKA_PRO", 2, 6000, 10)}
	now := time.Now().UTC()
	f.periodEnd = now.AddDate(0, 0, 20).Truncate(time.Second)
	if _, err := client.TenantSubscription.Create().
		SetTenantID(tn.ID).SetPlanID(f.basic.ID).SetStatus("ACTIVE").SetBillingCycle("MONTHLY").
		SetCurrentPeriodStart(now.AddDate(0, 0, -10)).SetCurrentPeriodEnd(f.periodEnd).Save(ctx); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *trialFixture) sub(t *testing.T) *ent.TenantSubscription {
	t.Helper()
	s, err := f.svc.getSubscription(context.Background(), f.tenant)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func (f *trialFixture) events(t *testing.T, eventType string) int {
	t.Helper()
	n, err := f.client.OutboxEvent.Query().Where(outboxevent.TenantIDEQ(f.tenant), outboxevent.EventTypeEQ(eventType)).Count(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// The whole upgrade trial: refused while off, started once on, tier 2 in force with tier 1 billed
// and the upgrade scheduled for the renewal date, back on tier 1 after the trial (no grace), and
// on tier 2 from the renewal date on.
func TestUpgradeTrialLifecycle(t *testing.T) {
	f := newTrialFixture(t)
	ctx := context.Background()
	in := StartUpgradeTrialInput{TenantID: f.tenant, PlanCode: f.pro.PlanCode}

	if _, err := f.svc.StartUpgradeTrial(ctx, in); err == nil {
		t.Fatal("trial started while upgrade trials are off")
	} else if te, ok := AsUpgradeTrialError(err); !ok || te.Reason != TrialReasonDisabled {
		t.Fatalf("want disabled, got %v", err)
	}

	if _, err := f.svc.SetPlatformUpgradeTrialPolicy(ctx, true, 14); err != nil {
		t.Fatal(err)
	}
	five := 5
	pol, err := f.svc.SetTenantUpgradeTrialPolicy(ctx, f.tenant, UpgradeTrialSetting{Days: &five})
	if err != nil {
		t.Fatal(err)
	}
	if !pol.Enabled || pol.Days != 5 || pol.DaysSource != PolicySourceTenant || pol.EnabledSource != PolicySourcePlatform {
		t.Fatalf("policy %+v", pol)
	}

	res, err := f.svc.StartUpgradeTrial(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if res.PlanCode != f.pro.PlanCode || res.TierOrder != 2 || res.UpgradeTrial == nil || res.UpgradeTrial.BasePlanCode != f.basic.PlanCode {
		t.Fatalf("trial result %+v", res)
	}
	if res.Limits["max_users"] != 10 {
		t.Fatalf("trial limits %v", res.Limits)
	}
	if res.ScheduledChange == nil || !res.ScheduledChange.EffectiveAt.Equal(f.periodEnd) {
		t.Fatalf("upgrade not scheduled for the renewal date: %+v", res.ScheduledChange)
	}
	sub := f.sub(t)
	if sub.PlanID != f.basic.ID {
		t.Fatal("plan_id moved during the trial; billing must stay on the paid plan")
	}
	if BillingPlanIDFor(sub) != f.pro.ID {
		t.Fatal("the renewal after the trial must bill the upgraded plan")
	}
	limits, err := EntitlementLimits(ctx, f.client, sub)
	if err != nil || limits["max_users"] != float64(10) {
		t.Fatalf("server-side limits %v %v", limits, err)
	}
	if f.events(t, "upgrade_trial_started") != 1 || f.events(t, "subscription.updated") != 1 {
		t.Fatal("trial start events missing")
	}

	if _, err := f.svc.StartUpgradeTrial(ctx, in); err == nil {
		t.Fatal("second trial started while one runs")
	}

	// Ending reminder, then the trial ends: tier 1 again, no grace, upgrade still scheduled.
	if st, err := f.svc.ProcessPlanSchedules(ctx, time.Now().UTC().AddDate(0, 0, 3)); err != nil || st.EndingNotices != 1 {
		t.Fatalf("ending notice: %+v %v", st, err)
	}
	afterTrial := time.Now().UTC().AddDate(0, 0, 6)
	if st, err := f.svc.ProcessPlanSchedules(ctx, afterTrial); err != nil || st.TrialsEnded != 1 {
		t.Fatalf("trial end: %+v %v", st, err)
	}
	sub = f.sub(t)
	if _, ok := upgradeTrialOf(sub); ok {
		t.Fatal("finished trial still stored")
	}
	if sub.Metadata["grace_until"] != nil {
		t.Fatal("a finished upgrade trial must not start grace")
	}
	if PlanIDInForce(sub, afterTrial) != f.basic.ID {
		t.Fatal("between trial end and renewal the paid plan applies")
	}
	if _, ok := PendingPlanChange(sub); !ok {
		t.Fatal("upgrade dropped when the trial completed")
	}
	if hist := upgradeTrialHistory(sub.Metadata); len(hist) != 1 || hist[0].Outcome != TrialOutcomeCompleted {
		t.Fatalf("history %+v", hist)
	}

	// The renewal date passes: plan_id moves to tier 2.
	if st, err := f.svc.ProcessPlanSchedules(ctx, f.periodEnd.Add(time.Hour)); err != nil || st.ChangesApplied != 1 {
		t.Fatalf("apply: %+v %v", st, err)
	}
	sub = f.sub(t)
	if sub.PlanID != f.pro.ID {
		t.Fatal("upgrade not applied on the renewal date")
	}
	if _, ok := scheduledChangeOf(sub); ok {
		t.Fatal("applied change still stored")
	}
	if f.events(t, "upgraded") != 1 {
		t.Fatal("upgraded event missing")
	}

	// A second trial of the same plan is refused for the tenant.
	if st, _ := f.svc.UpgradeTrialStatusFor(ctx, f.tenant, f.pro.PlanCode, false); st.Eligibility == nil || st.Eligibility.Eligible {
		t.Fatal("a tier already on the plan must not be trial-eligible")
	}
}

// Ending a trial early keeps the paid plan and cancels the upgrade it scheduled.
func TestUpgradeTrialEndedEarly(t *testing.T) {
	f := newTrialFixture(t)
	ctx := context.Background()
	if _, err := f.svc.StartUpgradeTrial(ctx, StartUpgradeTrialInput{TenantID: f.tenant, PlanCode: f.pro.PlanCode, ByPlatform: true, Days: 7}); err != nil {
		t.Fatal(err)
	}
	res, err := f.svc.EndUpgradeTrial(ctx, f.tenant, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.PlanCode != f.basic.PlanCode || res.UpgradeTrial != nil || res.ScheduledChange != nil {
		t.Fatalf("after ending early: %+v", res)
	}
	if f.events(t, "upgrade_trial_ended") != 1 {
		t.Fatal("ended event missing")
	}
	if _, err := f.svc.EndUpgradeTrial(ctx, f.tenant, false); err == nil {
		t.Fatal("ended a trial that is not running")
	}
}

// A tenant's own upgrade waits for the renewal date; a downgrade applies at once; the platform
// owner's change applies at once.
func TestChangePlanSchedulesTenantUpgrades(t *testing.T) {
	f := newTrialFixture(t)
	ctx := context.Background()

	res, err := f.svc.ChangePlan(ctx, ChangePlanInput{TenantID: f.tenant, NewPlanCode: f.pro.PlanCode})
	if err != nil {
		t.Fatal(err)
	}
	if res.PlanCode != f.basic.PlanCode || res.ScheduledChange == nil || res.ScheduledChange.PlanCode != f.pro.PlanCode {
		t.Fatalf("tenant upgrade not scheduled: %+v", res)
	}
	if f.sub(t).PlanID != f.basic.ID {
		t.Fatal("tenant upgrade applied before the renewal date")
	}
	if _, err := f.svc.CancelScheduledPlanChange(ctx, f.tenant); err != nil {
		t.Fatal(err)
	}

	res, err = f.svc.ChangePlan(ctx, ChangePlanInput{TenantID: f.tenant, NewPlanCode: f.pro.PlanCode, ByPlatform: true})
	if err != nil || res.PlanCode != f.pro.PlanCode || f.sub(t).PlanID != f.pro.ID {
		t.Fatalf("platform change not immediate: %+v %v", res, err)
	}

	res, err = f.svc.ChangePlan(ctx, ChangePlanInput{TenantID: f.tenant, NewPlanCode: f.basic.PlanCode})
	if err != nil || f.sub(t).PlanID != f.basic.ID {
		t.Fatalf("downgrade not immediate: %+v %v", res, err)
	}
}

// A plan paid for early at checkout takes effect on the renewal date, not on payment.
func TestRenewalWithEarlyPaidPlanSchedulesIt(t *testing.T) {
	f := newTrialFixture(t)
	ctx := context.Background()
	if _, err := f.svc.RenewSubscription(ctx, RenewInput{TenantID: f.tenant, PlanCode: f.pro.PlanCode, IntentID: uuid.NewString()}); err != nil {
		t.Fatal(err)
	}
	sub := f.sub(t)
	if sub.PlanID != f.basic.ID {
		t.Fatal("early payment switched the plan before the renewal date")
	}
	pc, ok := PendingPlanChange(sub)
	if !ok || pc.PlanID != f.pro.ID || !pc.EffectiveAt.Equal(f.periodEnd) || pc.Source != ChangeSourceCheckout {
		t.Fatalf("scheduled change %+v", pc)
	}
	if !sub.CurrentPeriodStart.Equal(f.periodEnd) {
		t.Fatalf("renewed period starts %s, want %s", sub.CurrentPeriodStart, f.periodEnd)
	}
}
