package credits

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
	"github.com/bengobox/subscription-service/internal/ent/productsubscription"
	"github.com/bengobox/subscription-service/internal/modules/billing"
	"github.com/bengobox/subscription-service/internal/modules/subscriptions"
)

// Postgres-backed (skipped unless TEST_DATABASE_URL is set).
type fixture struct {
	svc      *Service
	orm      *ent.Client
	db       *sql.DB
	tenantID uuid.UUID
	subID    uuid.UUID
}

func newFixture(t *testing.T, planLimits map[string]any) *fixture {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	schemaName := "c_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
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
	db.SetMaxOpenConns(20)
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })
	if err := client.Schema.Create(ctx); err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{"sms_messaging", "whatsapp_messaging"} {
		if _, err := client.Product.Create().SetCode(code).SetName(code).SetCategory("add_on").Save(ctx); err != nil {
			t.Fatal(err)
		}
	}
	tn, err := client.Tenant.Create().SetID(uuid.New()).SetName("Alpha").SetSlug("alpha-" + uuid.NewString()[:6]).Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := client.SubscriptionPlan.Create().
		SetPlanCode("POWERSUITE_DUKA_BASIC").SetName("Duka Basic").SetDescription("t").
		SetBillingCycle("MONTHLY").SetBasePrice(2500).SetCurrency("KES").SetTierOrder(1).
		SetTierLimitsJSON(planLimits).Save(ctx)
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
	log := zap.NewNop()
	subs := subscriptions.New(client, log, nil, "", uuid.Nil)
	return &fixture{svc: New(log, client, billing.NewTokenWalletService(log, client, db), subs), orm: client, db: db, tenantID: tn.ID, subID: sub.ID}
}

func (f *fixture) bal(t *testing.T, tag string) int64 {
	t.Helper()
	var b int64
	err := f.db.QueryRow(`SELECT balance FROM api_token_wallets WHERE tenant_id = $1 AND service_tag = $2`, f.tenantID, tag).Scan(&b)
	if err != nil && err != sql.ErrNoRows {
		t.Fatal(err)
	}
	return b
}

func (f *fixture) grant(t *testing.T, service string, kind PurchaseKind, code, key string, months int) {
	t.Helper()
	offer, err := ResolveOffer(service, kind, code)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Grant(context.Background(), f.tenantID, offer, months, key, uuid.Nil); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) spend(t *testing.T, service string, units int64, key string) Decision {
	t.Helper()
	d, err := f.svc.Consume(context.Background(), ConsumeInput{TenantID: f.tenantID, Service: service, Units: units, Key: key})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestNoSubscriptionIsRefused(t *testing.T) {
	f := newFixture(t, map[string]any{})
	d, err := f.svc.Consume(context.Background(), ConsumeInput{TenantID: uuid.New(), Service: "whatsapp", Units: 1, Key: "k"})
	if err != nil || d.Allowed || d.Reason != ReasonNoSubscription {
		t.Fatalf("tenant without a subscription: %+v %v", d, err)
	}
}

func TestInactiveSubscriptionIsRefused(t *testing.T) {
	f := newFixture(t, map[string]any{})
	f.grant(t, "sms", KindBundle, "bundle_100", "g1", 1)
	if _, err := f.orm.TenantSubscription.UpdateOneID(f.subID).SetStatus("SUSPENDED").Save(context.Background()); err != nil {
		t.Fatal(err)
	}
	if d := f.spend(t, "sms", 1, "m1"); d.Allowed || d.Reason != ReasonNoSubscription {
		t.Fatalf("suspended subscription must not send: %+v", d)
	}
}

func TestServiceMustBeActivatedAndFirstBundleActivatesIt(t *testing.T) {
	f := newFixture(t, map[string]any{})
	if d := f.spend(t, "sms", 1, "m0"); d.Allowed || d.Reason != ReasonServiceNotActivated {
		t.Fatalf("SMS not bought yet: %+v", d)
	}
	f.grant(t, "sms", KindBundle, "bundle_100", "g1", 1)
	if f.bal(t, "sms") != 100 {
		t.Fatalf("bundle balance %d", f.bal(t, "sms"))
	}
	d := f.spend(t, "sms", 30, "m1")
	if !d.Allowed || d.FromBundle != 30 || f.bal(t, "sms") != 70 {
		t.Fatalf("after buying a bundle: %+v bal=%d", d, f.bal(t, "sms"))
	}
}

func TestSMSOverageBilledAtOneShillingAndPausesAtCap(t *testing.T) {
	f := newFixture(t, map[string]any{})
	f.grant(t, "sms", KindBundle, "bundle_100", "g1", 1)

	d := f.spend(t, "sms", 110, "big")
	if !d.Allowed || d.FromBundle != 100 || d.Overage != 10 || d.OverageKES != 10 {
		t.Fatalf("overage split: %+v", d)
	}
	// Default cap is KES 500: 490 more KES of overage fits, one more KES does not.
	if d := f.spend(t, "sms", 490, "fill"); !d.Allowed {
		t.Fatalf("up to the cap: %+v", d)
	}
	if d := f.spend(t, "sms", 1, "past"); d.Allowed || d.Reason != billing.ReasonOverageCap {
		t.Fatalf("past the cap: %+v", d)
	}
	// Buying a bundle does not clear unpaid overage, but units are spent first so sending resumes.
	f.grant(t, "sms", KindBundle, "bundle_500", "g2", 1)
	if d := f.spend(t, "sms", 5, "resume"); !d.Allowed || d.FromBundle != 5 {
		t.Fatalf("sending resumes from the new bundle: %+v", d)
	}
}

func TestWhatsAppTierQuotaThenBundleThenOverage(t *testing.T) {
	f := newFixture(t, map[string]any{})
	f.grant(t, "whatsapp", KindTier, "tier_1", "t1", 1) // KES 500 for 400 messages
	if q := f.bal(t, "whatsapp_quota"); q != 400 {
		t.Fatalf("tier 1 quota %d", q)
	}
	f.grant(t, "whatsapp", KindBundle, "bundle_100", "b1", 1)

	d := f.spend(t, "whatsapp", 450, "a")
	if !d.Allowed || d.FromQuota != 400 || d.FromBundle != 50 || d.Overage != 0 {
		t.Fatalf("quota then bundle: %+v", d)
	}
	d = f.spend(t, "whatsapp", 60, "b")
	if !d.Allowed || d.FromBundle != 50 || d.Overage != 10 || d.OverageKES != 10 {
		t.Fatalf("then overage at KES 1: %+v", d)
	}
}

func TestTier2Gives800AndProIsUnlimited(t *testing.T) {
	f := newFixture(t, map[string]any{})
	f.grant(t, "whatsapp", KindTier, "tier_2", "t2", 1)
	if q := f.bal(t, "whatsapp_quota"); q != 800 {
		t.Fatalf("tier 2 quota %d", q)
	}
	f.grant(t, "whatsapp", KindTier, "pro", "p1", 1)
	d := f.spend(t, "whatsapp", 5000, "bulk")
	if !d.Allowed || d.Overage != 0 || d.FromQuota != 5000 {
		t.Fatalf("pro tier is unlimited and never bills overage: %+v", d)
	}
}

func TestExpiredTierStopsOverageAndStaleQuota(t *testing.T) {
	f := newFixture(t, map[string]any{})
	f.grant(t, "whatsapp", KindTier, "tier_1", "t1", 1)
	ps, err := f.orm.ProductSubscription.Query().Only(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	meta := ps.Metadata
	meta[MetaTierPeriodEnd] = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	if _, err := f.orm.ProductSubscription.UpdateOneID(ps.ID).SetMetadata(meta).Save(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Quota left over from the lapsed period must not be spendable and there is no overage.
	d := f.spend(t, "whatsapp", 1, "late")
	if d.Allowed {
		t.Fatalf("lapsed tier with no bundle must refuse: %+v", d)
	}
	n, err := f.svc.RefillAllowances(context.Background(), time.Now())
	if err != nil || n == 0 {
		t.Fatalf("refill: %d %v", n, err)
	}
	if f.bal(t, "whatsapp_quota") != 0 {
		t.Fatalf("lapsed tier allowance must drop to zero, got %d", f.bal(t, "whatsapp_quota"))
	}
}

func TestExemptTenantAndPlatformScopeAreNeverBilled(t *testing.T) {
	f := newFixture(t, map[string]any{})
	if _, err := f.orm.Tenant.UpdateOneID(f.tenantID).SetSubscriptionExempt(true).Save(context.Background()); err != nil {
		t.Fatal(err)
	}
	d := f.spend(t, "whatsapp", 1000, "x")
	if !d.Allowed || !d.Exempt {
		t.Fatalf("exempt tenant: %+v", d)
	}
	var rows int
	_ = f.db.QueryRow(`SELECT count(*) FROM api_token_transactions`).Scan(&rows)
	if rows != 0 {
		t.Fatalf("exempt spend wrote %d ledger rows", rows)
	}
	other := newFixture(t, map[string]any{})
	pd, err := other.svc.Consume(context.Background(), ConsumeInput{TenantID: other.tenantID, Service: "sms", Units: 5, Key: "otp", PlatformScope: true})
	if err != nil || !pd.Allowed || !pd.Exempt {
		t.Fatalf("platform scope: %+v %v", pd, err)
	}
}

func TestGrantIsIdempotentAndTierRenewalStacks(t *testing.T) {
	f := newFixture(t, map[string]any{})
	f.grant(t, "sms", KindBundle, "bundle_500", "same", 1)
	f.grant(t, "sms", KindBundle, "bundle_500", "same", 1)
	if f.bal(t, "sms") != 500 {
		t.Fatalf("redelivered grant applied twice: %d", f.bal(t, "sms"))
	}
	f.grant(t, "whatsapp", KindTier, "tier_1", "w1", 1)
	f.grant(t, "whatsapp", KindTier, "tier_1", "w2", 3) // renewal of a 3 month billing period
	ps, err := f.orm.ProductSubscription.Query().Where(productsubscription.ProductCodeEQ("whatsapp_messaging")).Only(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	end, _ := time.Parse(time.RFC3339, ps.Metadata[MetaTierPeriodEnd].(string))
	wantMin := time.Now().AddDate(0, 3, 25)
	if end.Before(wantMin) {
		t.Fatalf("renewal must extend from the running period end, got %s", end)
	}
}

func TestAIPlanAllowanceThenBundleAndNoOverage(t *testing.T) {
	f := newFixture(t, map[string]any{"ai_credits_monthly": 5})
	if n, err := f.svc.RefillAllowances(context.Background(), time.Now()); err != nil || n != 1 {
		t.Fatalf("refill: %d %v", n, err)
	}
	if n, _ := f.svc.RefillAllowances(context.Background(), time.Now()); n != 0 {
		t.Fatalf("refill must be idempotent within the month, wrote %d", n)
	}
	f.grant(t, "ai_credits", KindBundle, "bundle_100", "ai1", 1)
	d := f.spend(t, "ai_credits", 8, "chat1")
	if !d.Allowed || d.FromQuota != 5 || d.FromBundle != 3 {
		t.Fatalf("plan allowance first: %+v", d)
	}
	if d := f.spend(t, "ai_credits", 98, "chat2"); d.Allowed {
		t.Fatalf("AI credits are prepaid: %+v", d)
	}
}

func TestRefundRestoresAfterProviderFailure(t *testing.T) {
	f := newFixture(t, map[string]any{})
	f.grant(t, "sms", KindBundle, "bundle_100", "g", 1)
	f.spend(t, "sms", 10, "send-1")
	if _, err := f.svc.Refund(context.Background(), f.tenantID, "sms", "send-1", "send-1:refund"); err != nil {
		t.Fatal(err)
	}
	if f.bal(t, "sms") != 100 {
		t.Fatalf("balance after refund %d", f.bal(t, "sms"))
	}
}

func TestResolveOfferPricesComeFromTheRegistry(t *testing.T) {
	o, err := ResolveOffer("sms", KindBundle, "bundle_1000")
	if err != nil || o.Units != 1000 || o.PriceKES != 1000 {
		t.Fatalf("sms bundle: %+v %v", o, err)
	}
	o, err = ResolveOffer("whatsapp", KindTier, "tier_1")
	if err != nil || o.PriceKES != 500 || o.Units != 400 {
		t.Fatalf("tier 1: %+v %v", o, err)
	}
	o, _ = ResolveOffer("whatsapp", KindTier, "tier_2")
	if o.PriceKES != 1000 || o.Units != 800 {
		t.Fatalf("tier 2: %+v", o)
	}
	o, _ = ResolveOffer("whatsapp", KindTier, "pro")
	if o.PriceKES != 2000 || o.Units != -1 {
		t.Fatalf("pro: %+v", o)
	}
	if _, err := ResolveOffer("sms", KindBundle, "free"); err == nil {
		t.Fatal("unknown bundle must fail")
	}
	if _, err := ResolveOffer("sms", KindTier, "tier_1"); err == nil {
		t.Fatal("SMS has no tiers")
	}
}

func TestCheckMirrorsConsumeWithoutRecording(t *testing.T) {
	f := newFixture(t, map[string]any{})
	ctx := context.Background()
	chk := func(service string, units int64) Decision {
		d, err := f.svc.Check(ctx, f.tenantID, service, units, false)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	if d := chk("sms", 1); d.Allowed || d.Reason != ReasonServiceNotActivated {
		t.Fatalf("not activated: %+v", d)
	}
	f.grant(t, "sms", KindBundle, "bundle_100", "g", 1)
	if d := chk("sms", 100); !d.Allowed {
		t.Fatalf("exactly the bundle: %+v", d)
	}
	if d := chk("sms", 400); !d.Allowed {
		t.Fatalf("300 KES of overage is under the cap: %+v", d)
	}
	if d := chk("sms", 700); d.Allowed || d.Reason != billing.ReasonOverageCap {
		t.Fatalf("600 KES of overage is over the cap: %+v", d)
	}
	if f.bal(t, "sms") != 100 {
		t.Fatalf("check must not spend: %d", f.bal(t, "sms"))
	}
	if d := chk("whatsapp", 1); d.Allowed {
		t.Fatalf("whatsapp not bought: %+v", d)
	}
	pd, _ := f.svc.Check(ctx, f.tenantID, "whatsapp", 1, true)
	if !pd.Allowed {
		t.Fatalf("platform scope is always allowed: %+v", pd)
	}
}

func TestImportLegacyIsAdditiveIdempotentAndKeepsUsage(t *testing.T) {
	f := newFixture(t, map[string]any{})
	ctx := context.Background()
	exp := time.Now().Add(10 * 24 * time.Hour).UTC()
	w := LegacyWallet{TenantID: f.tenantID, SMSBalance: 250, WhatsAppTier: "tier_2", WhatsAppExpires: &exp, WhatsAppUsed: 300}

	dry, err := f.svc.ImportLegacy(ctx, w, true)
	if err != nil || len(dry.Applied) != 2 || f.bal(t, "sms") != 0 {
		t.Fatalf("dry run must report without writing: %+v bal=%d %v", dry, f.bal(t, "sms"), err)
	}
	for i := 0; i < 2; i++ {
		if _, err := f.svc.ImportLegacy(ctx, w, false); err != nil {
			t.Fatal(err)
		}
	}
	if f.bal(t, "sms") != 250 {
		t.Fatalf("sms balance after two runs: %d", f.bal(t, "sms"))
	}
	if q := f.bal(t, "whatsapp_quota"); q != 500 {
		t.Fatalf("tier 2 has 800, 300 used, 500 must remain: %d", q)
	}
	// The monthly job must not hand the used allowance back during the migrated month.
	if _, err := f.svc.RefillAllowances(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	if q := f.bal(t, "whatsapp_quota"); q != 500 {
		t.Fatalf("refill job reset a migrated quota: %d", q)
	}
	if d := f.spend(t, "whatsapp", 1, "after-migration"); !d.Allowed || d.FromQuota != 1 {
		t.Fatalf("migrated tenant can send: %+v", d)
	}

	none, err := f.svc.ImportLegacy(ctx, LegacyWallet{TenantID: uuid.New(), SMSBalance: 5}, false)
	if err != nil || none.Skipped == "" {
		t.Fatalf("tenant without a subscription is skipped and reported: %+v %v", none, err)
	}
}

func TestCatalogListsPublicPrices(t *testing.T) {
	cat := Catalog()
	var sms, wa *CatalogEntry
	for i := range cat {
		switch cat[i].Service {
		case "sms":
			sms = &cat[i]
		case "whatsapp":
			wa = &cat[i]
		}
	}
	if sms == nil || wa == nil || len(sms.Bundles) != 4 || len(wa.Tiers) != 3 {
		t.Fatalf("catalog %+v", cat)
	}
	if sms.UnitPriceKES != 1 || wa.UnitPriceKES != 1 {
		t.Fatalf("messages cost KES 1 each: %v %v", sms.UnitPriceKES, wa.UnitPriceKES)
	}
	for _, e := range cat {
		if e.Service == "etims_api" {
			t.Fatal("the eTIMS token wallet is not a tenant catalog item")
		}
	}
}
