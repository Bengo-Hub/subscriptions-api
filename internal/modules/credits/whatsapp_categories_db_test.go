package credits

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	entmigrate "github.com/bengobox/subscription-service/internal/ent/migrate"
	"github.com/bengobox/subscription-service/internal/ent/productsubscription"
	"github.com/bengobox/subscription-service/internal/modules/billing"
)

// Postgres-backed (skipped unless TEST_DATABASE_URL is set).

// A send spends only from its own category's wallet: utility credits never pay for a promotion.
func TestWhatsAppCategorySpendsOnlyItsOwnWallet(t *testing.T) {
	f := newFixture(t, map[string]any{})
	f.grant(t, billing.ServiceWhatsAppUtility, KindBundle, "bundle_100", "u1", 1)

	if d := f.spend(t, billing.ServiceWhatsAppMarketing, 1, "promo-1"); d.Allowed || d.Reason != billing.ReasonInsufficient {
		t.Fatalf("a promotion must not spend utility credits: %+v", d)
	}
	if d := f.spend(t, billing.ServiceWhatsAppService, 1, "reply-1"); d.Allowed || d.Reason != billing.ReasonInsufficient {
		t.Fatalf("a reply must not spend utility credits: %+v", d)
	}
	if d := f.spend(t, billing.ServiceWhatsAppUtility, 3, "order-1"); !d.Allowed || d.FromBundle != 3 || d.Overage != 0 {
		t.Fatalf("an order update spends utility credits: %+v", d)
	}
	if f.bal(t, billing.ServiceWhatsAppUtility) != 97 || f.bal(t, billing.ServiceWhatsAppMarketing) != 0 {
		t.Fatalf("balances: utility %d marketing %d", f.bal(t, billing.ServiceWhatsAppUtility), f.bal(t, billing.ServiceWhatsAppMarketing))
	}
	// Prepaid: with no live legacy tier there is no overage on any category.
	if d := f.spend(t, billing.ServiceWhatsAppUtility, 98, "order-2"); d.Allowed || d.Overage != 0 {
		t.Fatalf("no overage once credits run out: %+v", d)
	}
}

// Any category purchase activates the one whatsapp_messaging row, and only one row ever exists.
func TestAnyWhatsAppCategoryPurchaseActivatesWhatsApp(t *testing.T) {
	f := newFixture(t, map[string]any{})
	if d := f.spend(t, billing.ServiceWhatsAppUtility, 1, "before"); d.Reason != ReasonServiceNotActivated {
		t.Fatalf("nothing bought yet: %+v", d)
	}
	f.grant(t, billing.ServiceWhatsAppMarketing, KindBundle, "bundle_100", "m1", 1)
	f.grant(t, billing.ServiceWhatsAppService, KindBundle, "bundle_100", "s1", 1)
	n, err := f.orm.ProductSubscription.Query().Where(productsubscription.ProductCodeEQ("whatsapp_messaging")).Count(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("one activation row for every category, got %d %v", n, err)
	}
	if d := f.spend(t, billing.ServiceWhatsAppUtility, 1, "after"); d.Reason != billing.ReasonInsufficient {
		t.Fatalf("activated, but no utility credits: %+v", d)
	}
	if f.bal(t, billing.ServiceWhatsAppMarketing) != 100 || f.bal(t, billing.ServiceWhatsAppService) != 100 {
		t.Fatal("each purchase lands in its own wallet")
	}
}

// A tier bought before the retirement keeps working for every category until its period ends, then
// it is gone: no renewal line, no renewal grant, and its allowance drops to zero.
func TestLegacyTierRunsToItsEndAndIsNeverRenewed(t *testing.T) {
	f := newFixture(t, map[string]any{})
	ctx := context.Background()
	f.grant(t, "whatsapp", KindTier, "tier_1", "legacy-tier", 1)

	if d := f.spend(t, billing.ServiceWhatsAppMarketing, 10, "promo"); !d.Allowed || d.FromQuota != 10 {
		t.Fatalf("a live tier pays for any category: %+v", d)
	}
	if d := f.spend(t, billing.ServiceWhatsAppUtility, 5, "order"); !d.Allowed || d.FromQuota != 5 {
		t.Fatalf("a live tier pays for any category: %+v", d)
	}
	if q := f.bal(t, "whatsapp_quota"); q != 385 {
		t.Fatalf("one shared allowance: %d", q)
	}

	ps, err := f.orm.ProductSubscription.Query().Where(productsubscription.ProductCodeEQ("whatsapp_messaging")).Only(ctx)
	if err != nil {
		t.Fatal(err)
	}
	endBefore, _ := ps.Metadata[MetaTierPeriodEnd].(string)
	if err := f.svc.RenewTiers(ctx, f.tenantID, "pay-1"); err != nil {
		t.Fatal(err)
	}
	ps, _ = f.orm.ProductSubscription.Get(ctx, ps.ID)
	if end, _ := ps.Metadata[MetaTierPeriodEnd].(string); end != endBefore {
		t.Fatalf("a retired tier must not renew: %s -> %s", endBefore, end)
	}

	if _, err := f.svc.InitiatePurchase(ctx, PurchaseInput{TenantID: f.tenantID, Service: billing.ServiceWhatsAppUtility, Kind: KindTier, Code: "tier_1"}); err == nil {
		t.Fatal("a retired tier cannot be bought again")
	}

	meta := ps.Metadata
	meta[MetaTierPeriodEnd] = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	if _, err := f.orm.ProductSubscription.UpdateOneID(ps.ID).SetMetadata(meta).Save(ctx); err != nil {
		t.Fatal(err)
	}
	if d := f.spend(t, billing.ServiceWhatsAppMarketing, 1, "late"); d.Allowed {
		t.Fatalf("after the period, credits only: %+v", d)
	}
	// Four categories share whatsapp_quota: the refill writes it once, not four times.
	n, err := f.svc.RefillAllowances(ctx, time.Now())
	if err != nil || n != 1 {
		t.Fatalf("refill: %d %v", n, err)
	}
	if f.bal(t, "whatsapp_quota") != 0 {
		t.Fatalf("lapsed allowance: %d", f.bal(t, "whatsapp_quota"))
	}
}

func TestAutoTopupIsSetPerWhatsAppCategory(t *testing.T) {
	f := newFixture(t, map[string]any{})
	ft := &fakeTreasury{}
	withTreasury(t, f, ft)
	ctx := context.Background()
	f.grant(t, billing.ServiceWhatsAppUtility, KindBundle, "bundle_100", "u", 1)
	f.grant(t, billing.ServiceWhatsAppMarketing, KindBundle, "bundle_100", "m", 1)
	f.saveCard(t)
	enableTopup(t, f, billing.ServiceWhatsAppMarketing, 50, "bundle_500", nil)

	if u, _ := f.svc.GetAutoTopup(ctx, f.tenantID, billing.ServiceWhatsAppUtility); u.Enabled {
		t.Fatalf("turning it on for promotions must not turn it on for order updates: %+v", u)
	}
	enableTopup(t, f, billing.ServiceWhatsAppUtility, 10, "bundle_100", nil)
	if m, _ := f.svc.GetAutoTopup(ctx, f.tenantID, billing.ServiceWhatsAppMarketing); !m.Enabled || m.Threshold != 50 || m.BundleCode != "bundle_500" {
		t.Fatalf("the promotions setting is kept: %+v", m)
	}

	f.spend(t, billing.ServiceWhatsAppMarketing, 60, "promo") // 40 left, below 50
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	r, err := f.svc.RunAutoTopups(ctx, now)
	if err != nil || r.Charged != 1 || ft.count() != 1 {
		t.Fatalf("only the low category is bought: %+v %v charges=%d", r, err, ft.count())
	}
	c := ft.charges[0]
	if c["amount"].(float64) != 2000 {
		t.Fatalf("500 promotion credits at KES 4: %+v", c)
	}
	if ref, _ := c["reference_id"].(string); !strings.HasSuffix(ref, "-"+billing.ServiceWhatsAppMarketing+"-20261112") {
		t.Fatalf("reference %q", ref)
	}
	// Both settings survive the run that saved one of them.
	if u, _ := f.svc.GetAutoTopup(ctx, f.tenantID, billing.ServiceWhatsAppUtility); !u.Enabled || u.Threshold != 10 {
		t.Fatalf("utility setting lost: %+v", u)
	}
	if m, _ := f.svc.GetAutoTopup(ctx, f.tenantID, billing.ServiceWhatsAppMarketing); m.LastAttemptDay != "20261112" {
		t.Fatalf("marketing attempt not recorded: %+v", m)
	}
}

// hadiaTenantID is the one tenant whose legacy balance is split by the owner's decision.
var hadiaTenantID = uuid.MustParse("1e32db34-b65d-4384-9239-a22ef6e35985")

const categoryMigration = "../../ent/migrate/migrations/20261112090000_whatsapp_category_credits.sql"

// The data migration moves every legacy whatsapp balance into category wallets with one ledger row
// per moved amount on each side, so every wallet's history sums to its balance, and running it
// again changes nothing.
func TestWhatsAppCategoryMigration(t *testing.T) {
	f := newFixture(t, map[string]any{})
	ctx := context.Background()
	sqlText, err := os.ReadFile(categoryMigration)
	if err != nil {
		t.Fatal(err)
	}
	other := uuid.New()
	empty := uuid.New()
	legacy := func(tenant uuid.UUID, units int64) {
		t.Helper()
		id := uuid.New()
		if _, err := f.db.ExecContext(ctx, `INSERT INTO api_token_wallets (id, tenant_id, service_tag, balance, lifetime_granted, low_balance_threshold, created_at, updated_at)
			VALUES ($1, $2, 'whatsapp', $3, $3, 50, now(), now())`, id, tenant, units); err != nil {
			t.Fatal(err)
		}
		if units > 0 {
			if _, err := f.db.ExecContext(ctx, `INSERT INTO api_token_transactions (id, tenant_id, wallet_id, service_tag, action, tokens, new_balance, idempotency_key, metadata, created_at)
				VALUES ($1, $2, $3, 'whatsapp', 'topup', $4, $4, $5, '{}', now())`, uuid.New(), tenant, id, units, "seed:"+tenant.String()); err != nil {
				t.Fatal(err)
			}
		}
	}
	legacy(hadiaTenantID, 100)
	legacy(other, 37)
	legacy(empty, 0)
	// The other tenant already holds a few utility credits: the move adds to them.
	if _, _, err := f.svc.Wallets().Credit(ctx, billing.CreditRequest{TenantID: other, ServiceTag: billing.ServiceWhatsAppUtility, Units: 5, Action: "adjustment", Key: "pre"}); err != nil {
		t.Fatal(err)
	}

	// The production path: the migrator runs the file through MoveWhatsAppToCategories (Schema.Create
	// never executes versioned .sql files), on every deploy, so it must be idempotent.
	if len(sqlText) == 0 {
		t.Fatal("empty migration file")
	}
	for run := 0; run < 2; run++ {
		if err := entmigrate.MoveWhatsAppToCategories(ctx, f.db); err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
	}

	bal := func(tenant uuid.UUID, tag string) int64 {
		var b int64
		_ = f.db.QueryRowContext(ctx, `SELECT COALESCE((SELECT balance FROM api_token_wallets WHERE tenant_id = $1 AND service_tag = $2), 0)`, tenant, tag).Scan(&b)
		return b
	}
	if bal(hadiaTenantID, billing.ServiceWhatsAppUtility) != 90 || bal(hadiaTenantID, billing.ServiceWhatsAppService) != 10 || bal(hadiaTenantID, "whatsapp") != 0 {
		t.Fatalf("hadia: utility %d service %d legacy %d", bal(hadiaTenantID, billing.ServiceWhatsAppUtility),
			bal(hadiaTenantID, billing.ServiceWhatsAppService), bal(hadiaTenantID, "whatsapp"))
	}
	if bal(other, billing.ServiceWhatsAppUtility) != 42 || bal(other, billing.ServiceWhatsAppService) != 0 || bal(other, "whatsapp") != 0 {
		t.Fatalf("other tenant converts 1:1 into utility: utility %d service %d legacy %d",
			bal(other, billing.ServiceWhatsAppUtility), bal(other, billing.ServiceWhatsAppService), bal(other, "whatsapp"))
	}
	var emptyWallets int
	_ = f.db.QueryRowContext(ctx, `SELECT count(*) FROM api_token_wallets WHERE tenant_id = $1 AND service_tag <> 'whatsapp'`, empty).Scan(&emptyWallets)
	if emptyWallets != 0 {
		t.Fatalf("an empty legacy wallet moves nothing and creates nothing, got %d wallets", emptyWallets)
	}

	// Every wallet's ledger sums to its balance.
	rows, err := f.db.QueryContext(ctx, `SELECT w.service_tag, w.balance, COALESCE(SUM(x.tokens), 0)
		FROM api_token_wallets w LEFT JOIN api_token_transactions x ON x.wallet_id = w.id
		GROUP BY w.id, w.service_tag, w.balance`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var tag string
		var balance, sum int64
		if err := rows.Scan(&tag, &balance, &sum); err != nil {
			t.Fatal(err)
		}
		if balance != sum {
			t.Errorf("wallet %s: balance %d but ledger sums to %d", tag, balance, sum)
		}
	}

	// One migration row per moved amount on each side: hadia 2 out and 2 in, the other tenant 1 and 1.
	var moves int
	_ = f.db.QueryRowContext(ctx, `SELECT count(*) FROM api_token_transactions WHERE ref_type = 'whatsapp_category_migration'`).Scan(&moves)
	if moves != 6 {
		t.Fatalf("migration ledger rows %d, want 6", moves)
	}
	var plain int
	_ = f.db.QueryRowContext(ctx, `SELECT count(*) FROM api_token_transactions
		WHERE ref_type = 'whatsapp_category_migration' AND description LIKE '%Order, payment and account updates%'`).Scan(&plain)
	if plain == 0 {
		t.Fatal("migration rows must name the category in plain words")
	}
}
