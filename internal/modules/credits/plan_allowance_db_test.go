package credits

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/tenantsubscription"
)

// Postgres-backed (skipped unless TEST_DATABASE_URL is set). Owner rules of 2026-11-14: included
// AI credits follow the plan in force, a paid trial gets none, Check reports the real balances,
// and the one-time fix clears included credits on plans that grant none.

func (f *fixture) plan(t *testing.T, code string, limits map[string]any) *ent.SubscriptionPlan {
	t.Helper()
	p, err := f.orm.SubscriptionPlan.Create().
		SetPlanCode(code).SetName(code).SetDescription("t").
		SetBillingCycle("MONTHLY").SetBasePrice(1000).SetCurrency("KES").SetTierOrder(2).
		SetTierLimitsJSON(limits).Save(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func (f *fixture) sub(t *testing.T) *ent.TenantSubscription {
	t.Helper()
	s, err := f.orm.TenantSubscription.Query().Where(tenantsubscription.IDEQ(f.subID)).WithPlan().Only(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func (f *fixture) check(t *testing.T) Decision {
	t.Helper()
	d, err := f.svc.Check(context.Background(), f.tenantID, "ai_credits", 0, false)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// Check(units=0) carries the spendable balances marketflow-ai reads: the bundle and the live
// allowance; a stale allowance the plan no longer grants reads 0, an unlimited one -1.
func TestCheckReportsSpendableBalances(t *testing.T) {
	f := newFixture(t, map[string]any{"ai_credits_monthly": 5})
	ctx := context.Background()
	if _, err := f.svc.RefillAllowances(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	f.grant(t, "ai_credits", KindBundle, "bundle_60", "b60", 1)
	if d := f.check(t); !d.Allowed || d.QuotaBalance != 5 || d.BundleBalance != 60 {
		t.Fatalf("live allowance plus bundle: %+v", d)
	}

	none := f.plan(t, "POWERSUITE_DUKA_PRO", map[string]any{})
	if _, err := f.orm.TenantSubscription.UpdateOneID(f.subID).SetPlanID(none.ID).Save(ctx); err != nil {
		t.Fatal(err)
	}
	if d := f.check(t); d.QuotaBalance != 0 || d.BundleBalance != 60 {
		t.Fatalf("a stale allowance is not spendable and must read 0: %+v", d)
	}

	unl := f.plan(t, "AI_UNLIMITED", map[string]any{"ai_credits_monthly": -1})
	if _, err := f.orm.TenantSubscription.UpdateOneID(f.subID).SetPlanID(unl.ID).Save(ctx); err != nil {
		t.Fatal(err)
	}
	if d := f.check(t); !d.Allowed || d.QuotaBalance != -1 {
		t.Fatalf("unlimited allowance reads -1: %+v", d)
	}
}

// A paid trial gets no included allowance; the first payment (ACTIVE) gets the month's full grant
// even within the same month.
func TestTrialGetsNoIncludedAllowanceUntilActive(t *testing.T) {
	f := newFixture(t, map[string]any{"ai_credits_monthly": 50})
	ctx := context.Background()
	if _, err := f.orm.TenantSubscription.UpdateOneID(f.subID).SetStatus("TRIAL").Save(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.RefillAllowances(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	if b := f.bal(t, "ai_credits_quota"); b != 0 {
		t.Fatalf("trial allowance = %d, want 0", b)
	}
	if d := f.spend(t, "ai_credits", 1, "trial-chat"); d.Allowed {
		t.Fatalf("a trial cannot spend included credits: %+v", d)
	}
	if _, err := f.orm.TenantSubscription.UpdateOneID(f.subID).SetStatus("ACTIVE").Save(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.RefillAllowances(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	if b := f.bal(t, "ai_credits_quota"); b != 50 {
		t.Fatalf("allowance after the first payment = %d, want 50", b)
	}
}

// An upgrade tops up the difference now (used credits stay used), a retry changes nothing, a
// downgrade lowers the allowance, a plan with none clears it, and bought credits never move.
func TestSyncPlanAllowanceFollowsThePlan(t *testing.T) {
	f := newFixture(t, map[string]any{"ai_credits_monthly": 100})
	ctx := context.Background()
	if _, err := f.svc.RefillAllowances(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	f.grant(t, "ai_credits", KindBundle, "bundle_60", "b60", 1)
	f.spend(t, "ai_credits", 30, "used-30")
	if b := f.bal(t, "ai_credits_quota"); b != 70 {
		t.Fatalf("after spending 30: %d", b)
	}
	sub := f.sub(t)
	from := sub.Edges.Plan
	up := f.plan(t, "MARKETFLOW_PRO", map[string]any{"ai_credits_monthly": 300})
	for i := 0; i < 2; i++ {
		if err := f.svc.SyncPlanAllowance(ctx, sub, from, up); err != nil {
			t.Fatal(err)
		}
	}
	if b := f.bal(t, "ai_credits_quota"); b != 270 {
		t.Fatalf("upgrade top-up = %d, want 70 + 200 once", b)
	}
	down := f.plan(t, "MARKETFLOW_LITE", map[string]any{"ai_credits_monthly": 50})
	if err := f.svc.SyncPlanAllowance(ctx, sub, up, down); err != nil {
		t.Fatal(err)
	}
	if b := f.bal(t, "ai_credits_quota"); b != 50 {
		t.Fatalf("downgrade = %d, want 50", b)
	}
	ps := f.plan(t, "POWERSUITE_HOSP_GOLD", map[string]any{"chat_turns_daily": 300})
	if err := f.svc.SyncPlanAllowance(ctx, sub, down, ps); err != nil {
		t.Fatal(err)
	}
	if b := f.bal(t, "ai_credits_quota"); b != 0 {
		t.Fatalf("a plan with no AI credits clears the allowance, got %d", b)
	}
	// From a plan that granted none, a stale balance is replaced, not added to.
	if _, err := f.svc.wallets.SetQuota(ctx, f.tenantID, "ai_credits_quota", 600, "stale", "stale"); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.SyncPlanAllowance(ctx, sub, ps, up); err != nil {
		t.Fatal(err)
	}
	if b := f.bal(t, "ai_credits_quota"); b != 300 {
		t.Fatalf("PowerSuite to MarketFlow Pro = %d, want 300 (stale 600 replaced)", b)
	}
	if b := f.bal(t, "ai_credits"); b != 60 {
		t.Fatalf("bought credits moved: %d", b)
	}
}

// The one-time fix clears included credits only where the plan grants none, keeps bought credits,
// writes one ledger row per tenant and is idempotent; the dry run writes nothing.
func TestClearUngrantedAIAllowances(t *testing.T) {
	f := newFixture(t, map[string]any{}) // a PowerSuite plan: no ai_credits_monthly
	ctx := context.Background()
	wallets := f.svc.wallets
	if _, err := wallets.SetQuota(ctx, f.tenantID, "ai_credits_quota", 600, "w:2026-10", "old allowance"); err != nil {
		t.Fatal(err)
	}
	f.grant(t, "ai_credits", KindBundle, "bundle_60", "b60", 1)

	// A MarketFlow tenant whose plan grants an allowance is left alone.
	mf := f.plan(t, "MARKETFLOW_GROWTH", map[string]any{"ai_credits_monthly": 230})
	other, err := f.orm.Tenant.Create().SetID(uuid.New()).SetName("Beta").SetSlug("beta-" + uuid.NewString()[:6]).Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := f.orm.TenantSubscription.Create().SetTenantID(other.ID).SetPlanID(mf.ID).SetStatus("ACTIVE").
		SetCurrentPeriodStart(now).SetCurrentPeriodEnd(now.AddDate(0, 1, 0)).Save(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := wallets.SetQuota(ctx, other.ID, "ai_credits_quota", 230, "w:2026-11", "allowance"); err != nil {
		t.Fatal(err)
	}

	log := zap.NewNop()
	res, err := ClearUngrantedAIAllowances(ctx, f.orm, wallets, log, false)
	if err != nil || len(res.Found) != 1 || res.Found[0].TenantID != f.tenantID || res.Cleared != 0 {
		t.Fatalf("dry run: %+v %v", res, err)
	}
	if f.bal(t, "ai_credits_quota") != 600 {
		t.Fatal("the dry run must not write")
	}
	res, err = ClearUngrantedAIAllowances(ctx, f.orm, wallets, log, true)
	if err != nil || res.Cleared != 1 {
		t.Fatalf("apply: %+v %v", res, err)
	}
	if f.bal(t, "ai_credits_quota") != 0 || f.bal(t, "ai_credits") != 60 {
		t.Fatalf("included %d (want 0), bought %d (want 60)", f.bal(t, "ai_credits_quota"), f.bal(t, "ai_credits"))
	}
	if got, _ := wallets.GetBalance(ctx, other.ID, "ai_credits_quota"); got.Balance != 230 {
		t.Fatalf("a plan that grants credits was touched: %d", got.Balance)
	}
	var rows int
	if err := f.db.QueryRow(`SELECT count(*) FROM api_token_transactions WHERE tenant_id = $1 AND idempotency_key = $2`,
		f.tenantID, ClearIncludedAIAllowanceKey).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("ledger rows for the fix = %d (%v), want 1", rows, err)
	}
	res, err = ClearUngrantedAIAllowances(ctx, f.orm, wallets, log, true)
	if err != nil || len(res.Found) != 0 {
		t.Fatalf("a second run finds nothing: %+v %v", res, err)
	}
}
