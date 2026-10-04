package billing

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/redis/go-redis/v9"

	"github.com/bengobox/subscription-service/internal/ent"
)

// Postgres-backed (skipped unless TEST_DATABASE_URL is set, see the subscriptions package's
// support_agreements_db_test.go) with an in-memory Redis.
func newUsageFixture(t *testing.T, ordersLimit int) (*ent.Client, *redis.Client, uuid.UUID) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	schemaName := "u_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
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
		SetTierLimitsJSON(map[string]any{"max_orders_per_month": ordersLimit}).
		Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := client.TenantSubscription.Create().
		SetTenantID(tn.ID).SetPlanID(plan.ID).SetStatus("ACTIVE").
		SetCurrentPeriodStart(now.AddDate(0, 0, -10)).SetCurrentPeriodEnd(now.AddDate(0, 0, 20)).
		Save(ctx); err != nil {
		t.Fatal(err)
	}

	mr := miniredis.RunT(t)
	return client, redis.NewClient(&redis.Options{Addr: mr.Addr()}), tn.ID
}

// The pre-sale check must never count: only completed sales (pos.sale.finalized) increment.
func TestPeekUsageNeverCounts(t *testing.T) {
	orm, cache, tenantID := newUsageFixture(t, 3)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		res := PeekUsage(ctx, orm, cache, nil, tenantID, "orders", 1)
		if !res.Configured || res.Exceeded || res.Used != 1 {
			t.Fatalf("peek %d on an empty counter: %+v", i, res)
		}
	}
	for i := 1; i <= 3; i++ {
		if res := IncrementUsage(ctx, orm, cache, nil, tenantID, "orders", 1); res.Used != float64(i) || res.Exceeded {
			t.Fatalf("completed sale %d: %+v", i, res)
		}
	}
	// At 3/3 one more sale is refused, and asking again still does not move the counter.
	for i := 0; i < 3; i++ {
		res := PeekUsage(ctx, orm, cache, nil, tenantID, "orders", 1)
		if !res.Exceeded || res.Used != 4 || res.Limit != 3 {
			t.Fatalf("peek at the limit: %+v", res)
		}
	}
	if v, _ := cache.Get(ctx, UsageCounterKey(tenantID, "orders", time.Time{})).Result(); v != "" {
		// metered metrics are period keyed; the flat key must stay unused
		t.Fatalf("unexpected flat key value %q", v)
	}
	res := IncrementUsage(ctx, orm, cache, nil, tenantID, "orders", 0)
	if res.Used != 3 {
		t.Fatalf("counter moved by peeks: %+v", res)
	}
}

func TestPeekUsageUnlimitedPlanIsNotConfigured(t *testing.T) {
	orm, cache, tenantID := newUsageFixture(t, -1)
	if res := PeekUsage(context.Background(), orm, cache, nil, tenantID, "orders", 1); res.Configured {
		t.Fatalf("unlimited plan must fail open: %+v", res)
	}
}
