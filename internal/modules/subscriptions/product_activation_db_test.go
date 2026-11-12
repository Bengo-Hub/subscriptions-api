package subscriptions

import (
	"context"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/productsubscription"
)

// Postgres-backed (skipped unless TEST_DATABASE_URL is set). Covers the plan-derived default
// product rule that replaced the fixed auth/pos/inventory/treasury/subscription list after hadia
// (TREASURY_GROWTH, zero product rows) read as treasury-deactivated once its first
// whatsapp_messaging row appeared (2026-11-12).

// seedActivationCatalog creates the products and tagged feature definitions the tests use.
func seedActivationCatalog(t *testing.T, client *ent.Client) {
	t.Helper()
	ctx := context.Background()
	for _, code := range []string{"auth", "subscription", "notifications", "pos", "inventory", "treasury", "ordering", "whatsapp_messaging"} {
		if _, err := client.Product.Create().SetCode(code).SetName(code).SetCategory("product").Save(ctx); err != nil {
			t.Fatal(err)
		}
	}
	feats := map[string]string{
		"escrow_management":      "treasury",
		"payment_collection":     "treasury",
		"mpesa_integration":      "treasury",
		"custom_branding":        "platform",
		"paystack_integration":   "platform",
		"basic_inventory_access": "platform",
		"basic_treasury_access":  "platform",
		"pos_terminal":           "pos",
		"stock_tracking":         "inventory",
		"project_management":     "projects", // a tag with no product row: skipped
	}
	for code, tag := range feats {
		if _, err := client.FeatureDefinition.Create().SetFeatureCode(code).SetServiceTag(tag).SetLabel(code).Save(ctx); err != nil {
			t.Fatal(err)
		}
	}
}

func activationPlan(t *testing.T, client *ent.Client, code, serviceTag string, meta map[string]any, features ...string) *ent.SubscriptionPlan {
	t.Helper()
	ctx := context.Background()
	create := client.SubscriptionPlan.Create().
		SetPlanCode(code).SetName(code).SetDescription(code).
		SetBillingCycle("MONTHLY").SetBasePrice(1000).SetCurrency("KES").SetTierOrder(2).
		SetServiceTag(serviceTag)
	if meta != nil {
		create.SetMetadata(meta)
	}
	p, err := create.Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range features {
		if _, err := client.PlanFeature.Create().SetPlanID(p.ID).SetFeatureCode(f).SetIsIncluded(true).Save(ctx); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

func activationSub(t *testing.T, client *ent.Client, plan *ent.SubscriptionPlan) *ent.TenantSubscription {
	t.Helper()
	ctx := context.Background()
	tn, err := client.Tenant.Create().SetID(uuid.New()).SetName("Activation").SetSlug("act-" + uuid.NewString()[:6]).Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	sub, err := client.TenantSubscription.Create().
		SetTenantID(tn.ID).SetPlanID(plan.ID).SetStatus("ACTIVE").SetBillingCycle("MONTHLY").
		SetCurrentPeriodStart(now).SetCurrentPeriodEnd(now.AddDate(0, 1, 0)).Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return sub
}

func productRowCodes(t *testing.T, client *ent.Client, subID uuid.UUID) []string {
	t.Helper()
	rows, err := client.ProductSubscription.Query().
		Where(productsubscription.TenantSubscriptionIDEQ(subID)).
		All(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.ProductCode)
	}
	sort.Strings(out)
	return out
}

func sortedJoin(codes []string) string {
	c := append([]string(nil), codes...)
	sort.Strings(c)
	return strings.Join(c, ",")
}

func treasuryOnlyPlan(t *testing.T, client *ent.Client) *ent.SubscriptionPlan {
	return activationPlan(t, client, "TREASURY_GROWTH", "treasury", map[string]any{"product": "treasury"},
		"escrow_management", "payment_collection", "custom_branding", "paystack_integration")
}

func TestPlanDefaultsTreasuryOnlyPlan(t *testing.T) {
	_, client := newSupportTestService(t)
	seedActivationCatalog(t, client)
	plan := treasuryOnlyPlan(t, client)

	codes, err := PlanDefaultProductCodes(context.Background(), client, plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := sortedJoin(codes), "auth,notifications,subscription,treasury"; got != want {
		t.Fatalf("treasury-only plan defaults = %s, want %s (no pos/inventory)", got, want)
	}
}

func TestPlanDefaultsPowerSuiteDukaPlan(t *testing.T) {
	_, client := newSupportTestService(t)
	seedActivationCatalog(t, client)
	plan := activationPlan(t, client, "POWERSUITE_DUKA_PRO", "pos", nil,
		"pos_terminal", "stock_tracking", "mpesa_integration", "basic_inventory_access",
		"basic_treasury_access", "custom_branding", "project_management")

	codes, err := PlanDefaultProductCodes(context.Background(), client, plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := sortedJoin(codes), "auth,inventory,notifications,pos,subscription,treasury"; got != want {
		t.Fatalf("PowerSuite duka defaults = %s, want %s", got, want)
	}
}

// basic_*_access integration grants (tagged platform) still bring their service's product.
func TestPlanDefaultsBasicAccessGrants(t *testing.T) {
	_, client := newSupportTestService(t)
	seedActivationCatalog(t, client)
	plan := activationPlan(t, client, "POWERSUITE_DUKA_BASIC", "pos", nil,
		"pos_terminal", "basic_inventory_access", "basic_treasury_access")

	codes, err := PlanDefaultProductCodes(context.Background(), client, plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := sortedJoin(codes), "auth,inventory,notifications,pos,subscription,treasury"; got != want {
		t.Fatalf("basic access defaults = %s, want %s", got, want)
	}
}

// Signup inserts the plan defaults on the brand-new subscription.
func TestSignupInsertsPlanDefaults(t *testing.T) {
	_, client := newSupportTestService(t)
	seedActivationCatalog(t, client)
	plan := treasuryOnlyPlan(t, client)
	sub := activationSub(t, client, plan)
	ctx := context.Background()

	tx, err := client.Tx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	n, err := insertPlanDefaultProducts(ctx, tx.Client(), sub.ID, plan.ID, time.Now().UTC())
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if n != 4 {
		t.Fatalf("inserted %d rows, want 4", n)
	}
	if got := strings.Join(productRowCodes(t, client, sub.ID), ","); got != "auth,notifications,subscription,treasury" {
		t.Fatalf("signup rows = %s", got)
	}
}

// The hadia case: a zero-row subscription's first non-default product row also creates the plan
// defaults, so treasury stays active.
func TestFirstAddonRowOnZeroRowSubscriptionAddsDefaults(t *testing.T) {
	svc, client := newSupportTestService(t)
	seedActivationCatalog(t, client)
	plan := treasuryOnlyPlan(t, client)
	sub := activationSub(t, client, plan)
	ctx := context.Background()

	if err := svc.AssignProductPlan(ctx, sub.TenantID, "whatsapp_messaging", ""); err != nil {
		t.Fatal(err)
	}
	want := "auth,notifications,subscription,treasury,whatsapp_messaging"
	if got := strings.Join(productRowCodes(t, client, sub.ID), ","); got != want {
		t.Fatalf("rows after first add-on = %s, want %s", got, want)
	}

	// Idempotent: a second activation and a direct ensure add nothing.
	if err := svc.AssignProductPlan(ctx, sub.TenantID, "whatsapp_messaging", ""); err != nil {
		t.Fatal(err)
	}
	n, err := EnsurePlanDefaultProducts(ctx, client, sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("second ensure inserted %d rows, want 0", n)
	}
	if got := strings.Join(productRowCodes(t, client, sub.ID), ","); got != want {
		t.Fatalf("rows after repeat = %s, want %s", got, want)
	}
}

// Ensure leaves a subscription that already has rows alone, and skips codes already present.
func TestEnsureSkipsSubscriptionWithRows(t *testing.T) {
	_, client := newSupportTestService(t)
	seedActivationCatalog(t, client)
	plan := treasuryOnlyPlan(t, client)
	sub := activationSub(t, client, plan)
	ctx := context.Background()

	// Partial rows: inserting defaults again skips the one already there.
	n, err := insertPlanDefaultProducts(ctx, client, sub.ID, plan.ID, time.Now().UTC())
	if err != nil || n != 4 {
		t.Fatalf("first insert: n=%d err=%v", n, err)
	}
	n, err = insertPlanDefaultProducts(ctx, client, sub.ID, plan.ID, time.Now().UTC())
	if err != nil || n != 0 {
		t.Fatalf("repeat insert must skip existing codes: n=%d err=%v", n, err)
	}
	n, err = EnsurePlanDefaultProducts(ctx, client, sub.ID)
	if err != nil || n != 0 {
		t.Fatalf("ensure on a subscription with rows: n=%d err=%v", n, err)
	}
}

func TestBackfillZeroProductSubscriptions(t *testing.T) {
	_, client := newSupportTestService(t)
	seedActivationCatalog(t, client)
	plan := treasuryOnlyPlan(t, client)
	zero := activationSub(t, client, plan)
	cancelled := activationSub(t, client, plan)
	ctx := context.Background()
	if _, err := client.TenantSubscription.UpdateOneID(cancelled.ID).SetStatus("CANCELLED").Save(ctx); err != nil {
		t.Fatal(err)
	}

	res, err := BackfillZeroProductSubscriptions(ctx, client, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	if res.Subscriptions != 1 || res.Filled != 1 || res.RowsInserted != 4 || res.Failed != 0 {
		t.Fatalf("first backfill = %+v", res)
	}
	if got := strings.Join(productRowCodes(t, client, zero.ID), ","); got != "auth,notifications,subscription,treasury" {
		t.Fatalf("backfilled rows = %s", got)
	}
	if got := productRowCodes(t, client, cancelled.ID); len(got) != 0 {
		t.Fatalf("a cancelled subscription must not be backfilled: %v", got)
	}

	res, err = BackfillZeroProductSubscriptions(ctx, client, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	if res.Subscriptions != 0 || res.RowsInserted != 0 {
		t.Fatalf("second backfill must be a no-op: %+v", res)
	}
}

func TestBasicAccessService(t *testing.T) {
	cases := map[string]string{
		"basic_treasury_access":  "treasury",
		"basic_inventory_access": "inventory",
		"basic__access":          "",
		"escrow_management":      "",
	}
	for code, want := range cases {
		got, ok := basicAccessService(code)
		if got != want || ok != (want != "") {
			t.Errorf("basicAccessService(%q) = %q,%v want %q", code, got, ok, want)
		}
	}
}
