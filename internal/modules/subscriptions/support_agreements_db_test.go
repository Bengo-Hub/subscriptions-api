package subscriptions

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	"go.uber.org/zap"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/subscriptionplan"
	"github.com/bengobox/subscription-service/internal/ent/supportagreement"
	"github.com/bengobox/subscription-service/internal/ent/supportfeecycle"
	"github.com/bengobox/subscription-service/internal/payref"
)

// DB-backed support agreement tests. They run against a real Postgres (partial unique indexes and
// the conditional updates matter) and are skipped unless TEST_DATABASE_URL points at a disposable
// database, for example:
//
//	TEST_DATABASE_URL=postgres://postgres:postgres@localhost:5432/subscriptions_test?sslmode=disable
//
// Each test runs in its own schema, dropped afterwards.

func newSupportTestService(t *testing.T) (*Service, *ent.Client) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	schemaName := "t_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	admin, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schemaName); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.ExecContext(context.Background(), "DROP SCHEMA "+schemaName+" CASCADE")
		_ = admin.Close()
	})
	sep := "?"
	if strings.Contains(url, "?") {
		sep = "&"
	}
	db, err := sql.Open("pgx", url+sep+"search_path="+schemaName)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(4)
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })
	if err := client.Schema.Create(ctx); err != nil {
		t.Fatal(err)
	}
	return New(client, zap.NewNop(), nil, "", uuid.Nil), client
}

type supportFixture struct {
	tenantID uuid.UUID
	sub      *ent.TenantSubscription
}

// oneTimeTenant creates a tenant on POWERSUITE_DUKA_GOLD_ONE_TIME onboarded at onboarded, plus the
// SUPPORT_DUKA_GOLD catalog row (annual 24,000).
func oneTimeTenant(t *testing.T, client *ent.Client, onboarded time.Time) supportFixture {
	t.Helper()
	ctx := context.Background()
	tn, err := client.Tenant.Create().SetID(uuid.New()).SetName("Acme Duka").SetSlug("acme-" + uuid.NewString()[:6]).Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	license := mustPlan(t, client, "POWERSUITE_DUKA_GOLD_ONE_TIME", "ONE_TIME", 150000)
	mustPlan(t, client, "SUPPORT_DUKA_GOLD", "ANNUAL", 24000)
	sub, err := client.TenantSubscription.Create().
		SetTenantID(tn.ID).
		SetPlanID(license.ID).
		SetCurrentPeriodStart(onboarded).
		SetCurrentPeriodEnd(onboarded.AddDate(100, 0, 0)).
		SetBillingCycle("ONE_TIME").
		SetStatus("ACTIVE").
		SetCreatedAt(onboarded).
		Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return supportFixture{tenantID: tn.ID, sub: sub}
}

func mustPlan(t *testing.T, client *ent.Client, code, cycle string, price float64) *ent.SubscriptionPlan {
	t.Helper()
	ctx := context.Background()
	if p, err := client.SubscriptionPlan.Query().Where(subscriptionplan.PlanCodeEQ(code)).Only(ctx); err == nil {
		return p
	}
	p, err := client.SubscriptionPlan.Create().
		SetPlanCode(code).SetName(code).SetDescription(code).
		SetBillingCycle(cycle).SetBasePrice(price).SetCurrency("KES").SetTierOrder(3).
		Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func cyclesOf(t *testing.T, client *ent.Client, agreementID uuid.UUID) []*ent.SupportFeeCycle {
	t.Helper()
	cs, err := client.SupportFeeCycle.Query().
		Where(supportfeecycle.AgreementIDEQ(agreementID)).
		Order(ent.Asc(supportfeecycle.FieldCycleNumber)).
		All(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return cs
}

func TestStandardAgreementEnrollmentAndLegacyLink(t *testing.T) {
	svc, client := newSupportTestService(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	onboarded := now.AddDate(-1, -2, 0) // into its second annual period
	fx := oneTimeTenant(t, client, onboarded)
	plan := mustPlan(t, client, "SUPPORT_DUKA_GOLD", "ANNUAL", 24000)

	// A cycle created by the pre-agreement job (no agreement_id): cycle 2, due at the 2nd anniversary.
	legacy, err := client.SupportFeeCycle.Create().
		SetTenantID(fx.tenantID).SetTenantSubscriptionID(fx.sub.ID).SetSupportPlanID(plan.ID).
		SetAnchorDate(onboarded).SetCycleNumber(2).
		SetPeriodStart(onboarded.AddDate(1, 0, 0)).SetDueDate(onboarded.AddDate(2, 0, 0)).
		SetBasePrice(24000).Save(ctx)
	if err != nil {
		t.Fatal(err)
	}

	created, err := svc.EnsureStandardSupportAgreements(ctx, now)
	if err != nil || created != 1 {
		t.Fatalf("created=%d err=%v", created, err)
	}
	// Idempotent.
	if again, _ := svc.EnsureStandardSupportAgreements(ctx, now); again != 0 {
		t.Fatalf("second enrollment created %d", again)
	}
	a, err := client.SupportAgreement.Query().Where(supportagreement.TenantIDEQ(fx.tenantID)).Only(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if a.Kind != supportagreement.KindSTANDARD || a.CycleCount != 2 || !a.NextPeriodStart.Equal(onboarded.AddDate(2, 0, 0)) {
		t.Fatalf("agreement %+v", a)
	}
	got, _ := client.SupportFeeCycle.Get(ctx, legacy.ID)
	if got.AgreementID == nil || *got.AgreementID != a.ID || got.PeriodEnd == nil {
		t.Fatalf("legacy cycle not linked: %+v", got)
	}
	// Generation must not duplicate the current period.
	if n, err := svc.GenerateDueSupportCycles(ctx, now); err != nil || n != 0 {
		t.Fatalf("generated %d err %v", n, err)
	}
}

func TestStandardAgreementFreshTenantBillsCurrentPeriodOnly(t *testing.T) {
	svc, client := newSupportTestService(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	onboarded := now.AddDate(-2, -3, 0) // third annual period, no cycles ever created
	fx := oneTimeTenant(t, client, onboarded)

	if _, err := svc.EnsureStandardSupportAgreements(ctx, now); err != nil {
		t.Fatal(err)
	}
	if n, err := svc.GenerateDueSupportCycles(ctx, now); err != nil || n != 1 {
		t.Fatalf("generated %d err %v (want only the current period, no back-billing)", n, err)
	}
	a, _ := client.SupportAgreement.Query().Where(supportagreement.TenantIDEQ(fx.tenantID)).Only(ctx)
	cs := cyclesOf(t, client, a.ID)
	if len(cs) != 1 || cs[0].CycleNumber != 3 || cs[0].BasePrice != 24000 {
		t.Fatalf("cycles %+v", cs)
	}
	if !cs[0].DueDate.Equal(onboarded.AddDate(3, 0, 0)) {
		t.Fatalf("arrears due %s want %s", cs[0].DueDate, onboarded.AddDate(3, 0, 0))
	}
	// Claim: not yet due, so CURRENT.
	st, due, err := svc.currentSupportFeeStatus(ctx, fx.tenantID)
	if err != nil || st != "CURRENT" || due == nil || !due.Equal(cs[0].DueDate) {
		t.Fatalf("claim %s %v %v", st, due, err)
	}
}

// Regression: the old claim read the LATEST cycle, so an unpaid overdue cycle was masked as soon
// as the next period was generated and the gate never blocked.
func TestClaimUsesOldestUnpaidCycle(t *testing.T) {
	svc, client := newSupportTestService(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	fx := oneTimeTenant(t, client, now.AddDate(-1, 0, -20))
	if _, err := svc.EnsureStandardSupportAgreements(ctx, now); err != nil {
		t.Fatal(err)
	}
	a, _ := client.SupportAgreement.Query().Where(supportagreement.TenantIDEQ(fx.tenantID)).Only(ctx)
	overdueDue := now.AddDate(0, 0, -20)
	if _, err := client.SupportFeeCycle.Create().
		SetTenantID(fx.tenantID).SetTenantSubscriptionID(fx.sub.ID).SetAgreementID(a.ID).
		SetAnchorDate(a.StartsAt).SetCycleNumber(99).SetPeriodStart(overdueDue.AddDate(-1, 0, 0)).
		SetDueDate(overdueDue).SetBasePrice(24000).SetStatus(supportfeecycle.StatusOVERDUE).Save(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.GenerateDueSupportCycles(ctx, now); err != nil {
		t.Fatal(err)
	}
	st, due, err := svc.currentSupportFeeStatus(ctx, fx.tenantID)
	if err != nil || st != "OVERDUE" || due == nil || !due.Equal(overdueDue) {
		t.Fatalf("claim %s %v %v, want OVERDUE at %s", st, due, err, overdueDue)
	}
}

func TestSpecialAgreementAdvanceBillingAndPaymentResolution(t *testing.T) {
	svc, client := newSupportTestService(t)
	ctx := context.Background()
	fx := oneTimeTenant(t, client, time.Now().UTC().Truncate(time.Microsecond).AddDate(0, -1, 0))

	name := "Dedicated engineer"
	cycle := "MONTHLY"
	amount := 15000.0
	start := time.Now().UTC().Truncate(time.Microsecond).AddDate(0, 0, 3) // starts in 3 days, ADVANCE default for SPECIAL
	a, err := svc.CreateSupportAgreement(ctx, fx.tenantID, SupportAgreementInput{
		Name: &name, BillingCycle: &cycle, Amount: &amount, StartsAt: &start,
	}, uuid.Nil)
	if err != nil {
		t.Fatal(err)
	}
	if a.Kind != supportagreement.KindSPECIAL || a.BillingTiming != supportagreement.BillingTimingADVANCE {
		t.Fatalf("agreement %+v", a)
	}
	cs := cyclesOf(t, client, a.ID)
	if len(cs) != 1 || !cs[0].DueDate.Equal(start) || cs[0].BasePrice != 15000 || cs[0].SupportPlanID != nil {
		t.Fatalf("first advance cycle %+v", cs)
	}

	// A second agreement on the same subscription: the legacy unique index would have rejected
	// its cycle 1. Both coexist.
	name2, cycle2, count2, amount2 := "On-site visits", "CUSTOM", 2, 8000.0
	b, err := svc.CreateSupportAgreement(ctx, fx.tenantID, SupportAgreementInput{
		Name: &name2, BillingCycle: &cycle2, IntervalCount: &count2, Amount: &amount2,
	}, uuid.Nil)
	if err != nil {
		t.Fatal(err)
	}
	bc := cyclesOf(t, client, b.ID)
	if len(bc) != 1 || bc[0].CycleNumber != 1 {
		t.Fatalf("second agreement cycles %+v", bc)
	}

	// Pay agreement A's cycle by its payref; B's must stay open.
	ref := payref.Build("SUPFEE", "", fx.tenantID, cs[0].ID)
	paid, err := svc.MarkSupportCyclePaid(ctx, fx.tenantID, ref, "intent-1")
	if err != nil || paid == nil || paid.ID != cs[0].ID || paid.Status != supportfeecycle.StatusPAID {
		t.Fatalf("paid %+v err %v", paid, err)
	}
	// Idempotent redelivery.
	if again, err := svc.MarkSupportCyclePaid(ctx, fx.tenantID, ref, "intent-1"); err != nil || again.ID != cs[0].ID {
		t.Fatalf("redelivery %v %v", again, err)
	}
	open, _ := client.SupportFeeCycle.Get(ctx, bc[0].ID)
	if open.Status == supportfeecycle.StatusPAID {
		t.Fatal("payment for A settled B")
	}
	// Treasury manual payments reference the cycle UUID directly.
	if p, err := svc.MarkSupportCyclePaid(ctx, fx.tenantID, bc[0].ID.String(), ""); err != nil || p.ID != bc[0].ID {
		t.Fatalf("uuid reference %v %v", p, err)
	}

	// Validation.
	if _, err := svc.CreateSupportAgreement(ctx, fx.tenantID, SupportAgreementInput{Name: &name}, uuid.Nil); err == nil {
		t.Fatal("special agreement without amount accepted")
	}
	bad := 3
	unit := "DAY"
	if _, err := svc.CreateSupportAgreement(ctx, fx.tenantID, SupportAgreementInput{
		Name: &name, Amount: &amount, BillingCycle: &cycle2, IntervalCount: &bad, IntervalUnit: &unit,
	}, uuid.Nil); err == nil {
		t.Fatal("3-day custom cycle accepted")
	}
}

func TestRescheduleStandardToMonthlyNow(t *testing.T) {
	svc, client := newSupportTestService(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	fx := oneTimeTenant(t, client, now.AddDate(0, -5, 0))
	if _, err := svc.EnsureStandardSupportAgreements(ctx, now); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.GenerateDueSupportCycles(ctx, now); err != nil {
		t.Fatal(err)
	}
	a, _ := client.SupportAgreement.Query().Where(supportagreement.TenantIDEQ(fx.tenantID)).Only(ctx)
	annualCycle := cyclesOf(t, client, a.ID)[0]

	monthly := "MONTHLY"
	updated, err := svc.UpdateSupportAgreement(ctx, a.ID, SupportAgreementInput{BillingCycle: &monthly, RescheduleFrom: "now"}, uuid.Nil)
	if err != nil {
		t.Fatal(err)
	}
	if updated.BillingCycle != supportagreement.BillingCycleMONTHLY || updated.IntervalCount != 1 {
		t.Fatalf("updated %+v", updated)
	}
	waived, _ := client.SupportFeeCycle.Get(ctx, annualCycle.ID)
	if waived.Status != supportfeecycle.StatusWAIVED {
		t.Fatalf("pending annual cycle should be waived, is %s", waived.Status)
	}
	cs := cyclesOf(t, client, a.ID)
	last := cs[len(cs)-1]
	if last.CycleNumber != annualCycle.CycleNumber+1 || last.BasePrice != 2000 {
		t.Fatalf("monthly cycle %+v (want prorated 24000/12)", last)
	}
	if d := last.PeriodEnd.Sub(last.PeriodStart).Hours() / 24; d < 28 || d > 31 {
		t.Fatalf("monthly period length %v days", d)
	}

	// Pausing then ending never touches the issued monthly cycle's status beyond PENDING rules.
	paused := "PAUSED"
	if _, err := svc.UpdateSupportAgreement(ctx, a.ID, SupportAgreementInput{Status: &paused}, uuid.Nil); err != nil {
		t.Fatal(err)
	}
	if n, _ := svc.GenerateDueSupportCycles(ctx, now.AddDate(0, 3, 0)); n != 0 {
		t.Fatalf("paused agreement generated %d cycles", n)
	}
}
