package billing

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	"go.uber.org/zap"

	"github.com/bengobox/subscription-service/internal/ent"
)

// Postgres-backed (skipped unless TEST_DATABASE_URL is set).
type ledgerFixture struct {
	svc      *TokenWalletService
	orm      *ent.Client
	db       *sql.DB
	tenantID uuid.UUID
	subID    uuid.UUID
}

func newLedgerFixture(t *testing.T) *ledgerFixture {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	schemaName := "l_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
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
	db.SetMaxOpenConns(30)
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })
	if err := client.Schema.Create(ctx); err != nil {
		t.Fatal(err)
	}
	tn, err := client.Tenant.Create().SetID(uuid.New()).SetName("Alpha").SetSlug("alpha-" + uuid.NewString()[:6]).Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := client.SubscriptionPlan.Create().
		SetPlanCode("POWERSUITE_DUKA_BASIC").SetName("Duka Basic").SetDescription("t").
		SetBillingCycle("MONTHLY").SetBasePrice(2500).SetCurrency("KES").SetTierOrder(1).
		SetTierLimitsJSON(map[string]any{}).Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	sub, err := client.TenantSubscription.Create().
		SetTenantID(tn.ID).SetPlanID(plan.ID).SetStatus("ACTIVE").
		SetCurrentPeriodStart(now.AddDate(0, 0, -10)).SetCurrentPeriodEnd(now.AddDate(0, 0, 20)).Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return &ledgerFixture{svc: NewTokenWalletService(zap.NewNop(), client, db), orm: client, db: db, tenantID: tn.ID, subID: sub.ID}
}

func (f *ledgerFixture) balance(t *testing.T, tag string) int64 {
	t.Helper()
	var b int64
	err := f.db.QueryRow(`SELECT balance FROM api_token_wallets WHERE tenant_id = $1 AND service_tag = $2`, f.tenantID, tag).Scan(&b)
	if err == sql.ErrNoRows {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func (f *ledgerFixture) pendingOverage(t *testing.T, metric string) (units float64, kes float64) {
	t.Helper()
	if err := f.db.QueryRow(`SELECT COALESCE(SUM(units_over),0), COALESCE(SUM(total_charge_kes),0) FROM overage_charges
		WHERE tenant_id = $1 AND metric_type = $2 AND status = 'pending'`, f.tenantID, metric).Scan(&units, &kes); err != nil {
		t.Fatal(err)
	}
	return
}

func (f *ledgerFixture) credit(t *testing.T, tag string, units int64) {
	t.Helper()
	if _, _, err := f.svc.Credit(context.Background(), CreditRequest{TenantID: f.tenantID, ServiceTag: tag, Units: units, Action: ledgerTopup}); err != nil {
		t.Fatal(err)
	}
}

func sms() MeteredService { s, _ := LookupMeteredService(ServiceSMS); return s }

func wa() MeteredService { s, _ := LookupMeteredService(ServiceWhatsApp); return s }

func TestPrepaidOnlyServiceRefusesWhenEmptyAndNeverGoesNegative(t *testing.T) {
	f := newLedgerFixture(t)
	ctx := context.Background()
	f.credit(t, ServiceETIMS, 5)

	res, err := f.svc.DeductTokens(ctx, DeductInput{TenantID: f.tenantID, ServiceTag: ServiceETIMS, Tokens: 3})
	if err != nil || !res.Allowed || res.NewBalance != 2 {
		t.Fatalf("first deduct: %+v %v", res, err)
	}
	res, err = f.svc.DeductTokens(ctx, DeductInput{TenantID: f.tenantID, ServiceTag: ServiceETIMS, Tokens: 3})
	if err != nil || res.Allowed || res.NewBalance != 2 {
		t.Fatalf("deduct beyond balance must be refused untouched: %+v %v", res, err)
	}
}

func TestSMSSpendsBundleThenOverageAndRecordsKES(t *testing.T) {
	f := newLedgerFixture(t)
	ctx := context.Background()
	f.credit(t, ServiceSMS, 10)

	r1, err := f.svc.Consume(ctx, ConsumeRequest{TenantID: f.tenantID, Service: sms(), Units: 4, Key: "m1", AllowOverage: true, SubscriptionID: f.subID})
	if err != nil || !r1.Allowed || r1.FromBundle != 4 || r1.Overage != 0 {
		t.Fatalf("m1: %+v %v", r1, err)
	}
	r2, err := f.svc.Consume(ctx, ConsumeRequest{TenantID: f.tenantID, Service: sms(), Units: 9, Key: "m2", AllowOverage: true, SubscriptionID: f.subID})
	if err != nil || !r2.Allowed || r2.FromBundle != 6 || r2.Overage != 3 || r2.OverageKES != 3 {
		t.Fatalf("m2 must use 6 bundle units and 3 overage: %+v %v", r2, err)
	}
	if got := f.balance(t, ServiceSMS); got != 0 {
		t.Fatalf("bundle balance %d", got)
	}
	if u, k := f.pendingOverage(t, "messaging_sms"); u != 3 || k != 3 {
		t.Fatalf("pending overage units=%v kes=%v", u, k)
	}
	// Further overage accumulates in the same open row.
	if _, err := f.svc.Consume(ctx, ConsumeRequest{TenantID: f.tenantID, Service: sms(), Units: 2, Key: "m3", AllowOverage: true, SubscriptionID: f.subID}); err != nil {
		t.Fatal(err)
	}
	var rows int
	_ = f.db.QueryRow(`SELECT count(*) FROM overage_charges WHERE tenant_id = $1 AND status = 'pending'`, f.tenantID).Scan(&rows)
	if u, k := f.pendingOverage(t, "messaging_sms"); u != 5 || k != 5 || rows != 1 {
		t.Fatalf("overage must accumulate in one row: units=%v kes=%v rows=%d", u, k, rows)
	}
}

func TestConsumeIsIdempotentPerKey(t *testing.T) {
	f := newLedgerFixture(t)
	ctx := context.Background()
	f.credit(t, ServiceSMS, 10)
	req := ConsumeRequest{TenantID: f.tenantID, Service: sms(), Units: 12, Key: "msg-1", AllowOverage: true, SubscriptionID: f.subID}

	first, err := f.svc.Consume(ctx, req)
	if err != nil || !first.Allowed || first.Replayed {
		t.Fatalf("first: %+v %v", first, err)
	}
	second, err := f.svc.Consume(ctx, req)
	if err != nil || !second.Allowed || !second.Replayed || second.FromBundle != 10 || second.Overage != 2 {
		t.Fatalf("replay must report the original split: %+v %v", second, err)
	}
	if got := f.balance(t, ServiceSMS); got != 0 {
		t.Fatalf("balance changed by replay: %d", got)
	}
	if u, _ := f.pendingOverage(t, "messaging_sms"); u != 2 {
		t.Fatalf("overage double counted: %v", u)
	}
}

func TestOverageCapRefusesAndWritesNothing(t *testing.T) {
	f := newLedgerFixture(t)
	ctx := context.Background()
	f.credit(t, ServiceSMS, 2)

	res, err := f.svc.Consume(ctx, ConsumeRequest{TenantID: f.tenantID, Service: sms(), Units: 10, Key: "big", AllowOverage: true, SubscriptionID: f.subID, OverageCapKES: 5})
	if err != nil || res.Allowed || res.Reason != ReasonOverageCap {
		t.Fatalf("8 KES of overage over a 5 KES cap must be refused: %+v %v", res, err)
	}
	if got := f.balance(t, ServiceSMS); got != 2 {
		t.Fatalf("refused spend moved the bundle: %d", got)
	}
	if u, _ := f.pendingOverage(t, "messaging_sms"); u != 0 {
		t.Fatalf("refused spend recorded overage: %v", u)
	}
	// Exactly at the cap is allowed; the next unit is not.
	if r, _ := f.svc.Consume(ctx, ConsumeRequest{TenantID: f.tenantID, Service: sms(), Units: 7, Key: "fit", AllowOverage: true, SubscriptionID: f.subID, OverageCapKES: 5}); !r.Allowed || r.Overage != 5 {
		t.Fatalf("spend that lands exactly on the cap: %+v", r)
	}
	if r, _ := f.svc.Consume(ctx, ConsumeRequest{TenantID: f.tenantID, Service: sms(), Units: 1, Key: "over", AllowOverage: true, SubscriptionID: f.subID, OverageCapKES: 5}); r.Allowed || r.Reason != ReasonOverageCap {
		t.Fatalf("one more unit past the cap: %+v", r)
	}
}

func TestOverageNotAllowedForUncoveredTenantOrPrepaidService(t *testing.T) {
	f := newLedgerFixture(t)
	ctx := context.Background()
	if r, _ := f.svc.Consume(ctx, ConsumeRequest{TenantID: f.tenantID, Service: sms(), Units: 1, Key: "a", AllowOverage: false}); r.Allowed || r.Reason != ReasonInsufficient {
		t.Fatalf("no overage without entitlement: %+v", r)
	}
	ai, _ := LookupMeteredService(ServiceAI)
	if r, _ := f.svc.Consume(ctx, ConsumeRequest{TenantID: f.tenantID, Service: ai, Units: 1, Key: "b", AllowOverage: true, SubscriptionID: f.subID}); r.Allowed {
		t.Fatalf("AI credits are prepaid only: %+v", r)
	}
}

func TestWhatsAppSpendsQuotaBeforeBundleAndUnlimitedTier(t *testing.T) {
	f := newLedgerFixture(t)
	ctx := context.Background()
	if _, err := f.svc.SetQuota(ctx, f.tenantID, "whatsapp_quota", 5, "window-1", "reset"); err != nil {
		t.Fatal(err)
	}
	f.credit(t, ServiceWhatsApp, 10)

	r, err := f.svc.Consume(ctx, ConsumeRequest{TenantID: f.tenantID, Service: wa(), Units: 8, Key: "w1", AllowOverage: true, SubscriptionID: f.subID})
	if err != nil || r.FromQuota != 5 || r.FromBundle != 3 || r.Overage != 0 {
		t.Fatalf("quota must be spent first: %+v %v", r, err)
	}
	if f.balance(t, "whatsapp_quota") != 0 || f.balance(t, ServiceWhatsApp) != 7 {
		t.Fatalf("balances quota=%d bundle=%d", f.balance(t, "whatsapp_quota"), f.balance(t, ServiceWhatsApp))
	}

	// Unlimited tier: spend is recorded, nothing is reduced, never overage.
	u, err := f.svc.Consume(ctx, ConsumeRequest{TenantID: f.tenantID, Service: wa(), Units: 1000, Key: "w2", AllowOverage: true, SubscriptionID: f.subID, UnlimitedQuota: true})
	if err != nil || u.FromQuota != 1000 || u.FromBundle != 0 || u.Overage != 0 {
		t.Fatalf("unlimited: %+v %v", u, err)
	}
	if f.balance(t, ServiceWhatsApp) != 7 {
		t.Fatalf("unlimited spend touched the bundle: %d", f.balance(t, ServiceWhatsApp))
	}
}

func TestSetQuotaIsIdempotentPerWindowAndResets(t *testing.T) {
	f := newLedgerFixture(t)
	ctx := context.Background()
	if applied, err := f.svc.SetQuota(ctx, f.tenantID, "whatsapp_quota", 400, "w:2026-10", "reset"); err != nil || !applied {
		t.Fatalf("first reset: %v %v", applied, err)
	}
	if _, err := f.svc.Consume(ctx, ConsumeRequest{TenantID: f.tenantID, Service: wa(), Units: 100, Key: "x", AllowOverage: true, SubscriptionID: f.subID}); err != nil {
		t.Fatal(err)
	}
	if applied, _ := f.svc.SetQuota(ctx, f.tenantID, "whatsapp_quota", 400, "w:2026-10", "reset"); applied {
		t.Fatal("same window must not reset again")
	}
	if f.balance(t, "whatsapp_quota") != 300 {
		t.Fatalf("balance %d", f.balance(t, "whatsapp_quota"))
	}
	if applied, _ := f.svc.SetQuota(ctx, f.tenantID, "whatsapp_quota", 400, "w:2026-11", "reset"); !applied || f.balance(t, "whatsapp_quota") != 400 {
		t.Fatalf("next window resets to the allowance, got %d", f.balance(t, "whatsapp_quota"))
	}
}

func TestRefundConsumeRestoresEveryLegOnce(t *testing.T) {
	f := newLedgerFixture(t)
	ctx := context.Background()
	if _, err := f.svc.SetQuota(ctx, f.tenantID, "whatsapp_quota", 2, "w", "reset"); err != nil {
		t.Fatal(err)
	}
	f.credit(t, ServiceWhatsApp, 3)
	r, err := f.svc.Consume(ctx, ConsumeRequest{TenantID: f.tenantID, Service: wa(), Units: 8, Key: "send-1", AllowOverage: true, SubscriptionID: f.subID})
	if err != nil || r.FromQuota != 2 || r.FromBundle != 3 || r.Overage != 3 {
		t.Fatalf("setup consume: %+v %v", r, err)
	}
	if _, err := f.svc.RefundConsume(ctx, f.tenantID, wa(), f.subID, "send-1", "refund-1"); err != nil {
		t.Fatal(err)
	}
	if f.balance(t, "whatsapp_quota") != 2 || f.balance(t, ServiceWhatsApp) != 3 {
		t.Fatalf("legs not restored quota=%d bundle=%d", f.balance(t, "whatsapp_quota"), f.balance(t, ServiceWhatsApp))
	}
	if u, _ := f.pendingOverage(t, "messaging_whatsapp"); u != 0 {
		t.Fatalf("overage not reversed: %v", u)
	}
	again, err := f.svc.RefundConsume(ctx, f.tenantID, wa(), f.subID, "send-1", "refund-1")
	if err != nil || !again.Replayed {
		t.Fatalf("second refund must replay: %+v %v", again, err)
	}
	if f.balance(t, ServiceWhatsApp) != 3 {
		t.Fatalf("refund applied twice: %d", f.balance(t, ServiceWhatsApp))
	}
	// A refund for a spend that never happened changes nothing.
	none, err := f.svc.RefundConsume(ctx, f.tenantID, wa(), f.subID, "never", "refund-2")
	if err != nil || none.Allowed {
		t.Fatalf("unknown consume key: %+v %v", none, err)
	}
}

func TestCreditIsIdempotentPerKey(t *testing.T) {
	f := newLedgerFixture(t)
	ctx := context.Background()
	req := CreditRequest{TenantID: f.tenantID, ServiceTag: ServiceSMS, Units: 100, Action: ledgerTopup, Key: "topup:abc"}
	if _, applied, err := f.svc.Credit(ctx, req); err != nil || !applied {
		t.Fatalf("first credit: %v %v", applied, err)
	}
	if bal, applied, err := f.svc.Credit(ctx, req); err != nil || applied || bal != 100 {
		t.Fatalf("redelivered credit must not apply: bal=%d applied=%v err=%v", bal, applied, err)
	}
}

func TestConcurrentSpendNeverOversells(t *testing.T) {
	f := newLedgerFixture(t)
	ctx := context.Background()
	f.credit(t, ServiceAI, 10)
	ai, _ := LookupMeteredService(ServiceAI)

	var allowed, refused int32
	var wg sync.WaitGroup
	for i := 0; i < 25; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r, err := f.svc.Consume(ctx, ConsumeRequest{TenantID: f.tenantID, Service: ai, Units: 1, Key: uuid.NewString()})
			if err != nil {
				t.Error(err)
				return
			}
			if r.Allowed {
				atomic.AddInt32(&allowed, 1)
			} else {
				atomic.AddInt32(&refused, 1)
			}
		}(i)
	}
	wg.Wait()
	if allowed != 10 || refused != 15 || f.balance(t, ServiceAI) != 0 {
		t.Fatalf("allowed=%d refused=%d balance=%d", allowed, refused, f.balance(t, ServiceAI))
	}
}

func TestConcurrentSameKeyAppliesOnce(t *testing.T) {
	f := newLedgerFixture(t)
	ctx := context.Background()
	f.credit(t, ServiceSMS, 100)
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := f.svc.Consume(ctx, ConsumeRequest{TenantID: f.tenantID, Service: sms(), Units: 3, Key: "same", AllowOverage: true, SubscriptionID: f.subID}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if got := f.balance(t, ServiceSMS); got != 97 {
		t.Fatalf("one send charged %d times", (100-got)/3)
	}
}
