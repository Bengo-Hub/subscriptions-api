package credits

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	serviceclient "github.com/Bengo-Hub/shared-service-client"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/bengobox/subscription-service/internal/modules/billing"
	"github.com/bengobox/subscription-service/internal/modules/subscriptions"
)

// fakeTreasury records saved-card charges and answers them with a configurable result.
type fakeTreasury struct {
	mu      sync.Mutex
	charges []map[string]any
	status  int
	body    string
}

func (f *fakeTreasury) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !strings.HasSuffix(r.URL.Path, "/payments/intents/charge-saved-card") {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	f.mu.Lock()
	f.charges = append(f.charges, body)
	status, resp := f.status, f.body
	f.mu.Unlock()
	if status == 0 {
		status = http.StatusOK
	}
	if resp == "" {
		resp = `{"status":"pending"}`
	}
	w.WriteHeader(status)
	_, _ = w.Write([]byte(resp))
}

func (f *fakeTreasury) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.charges)
}

// withTreasury rebuilds the fixture's service so saved-card charges go to the fake.
func withTreasury(t *testing.T, f *fixture, ft *fakeTreasury) {
	t.Helper()
	srv := httptest.NewServer(ft)
	t.Cleanup(srv.Close)
	cfg := serviceclient.DefaultConfig(srv.URL, "treasury-api", zap.NewNop())
	cfg.MaxElapsedTime = 300 * time.Millisecond
	log := zap.NewNop()
	subs := subscriptions.New(f.orm, log, serviceclient.New(cfg), "key", uuid.Nil)
	f.svc = New(log, f.orm, billing.NewTokenWalletService(log, f.orm, f.db), subs)
}

func (f *fixture) saveCard(t *testing.T) {
	t.Helper()
	sub, err := f.orm.TenantSubscription.Get(context.Background(), f.subID)
	if err != nil {
		t.Fatal(err)
	}
	meta := map[string]any{}
	for k, v := range sub.Metadata {
		meta[k] = v
	}
	meta["paystack_auth_code"] = "AUTH_test"
	if _, err := f.orm.TenantSubscription.UpdateOneID(f.subID).SetMetadata(meta).Save(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func enableTopup(t *testing.T, f *fixture, service string, threshold int64, bundle string, limit *float64) AutoTopup {
	t.Helper()
	a, err := f.svc.SetAutoTopup(context.Background(), f.tenantID, service, AutoTopupInput{Enabled: true, Threshold: threshold, BundleCode: bundle, MonthlyLimitKES: limit})
	if err != nil {
		t.Fatalf("enable: %v", err)
	}
	return a
}

func TestSetAutoTopupValidation(t *testing.T) {
	f := newFixture(t, map[string]any{})
	withTreasury(t, f, &fakeTreasury{})
	ctx := context.Background()
	try := func(service string, in AutoTopupInput) error {
		_, err := f.svc.SetAutoTopup(ctx, f.tenantID, service, in)
		return err
	}
	good := AutoTopupInput{Enabled: true, Threshold: 20, BundleCode: "bundle_500"}

	if err := try("sms", good); err == nil {
		t.Fatal("must refuse before the service is activated")
	}
	f.grant(t, "sms", KindBundle, "bundle_100", "g", 1)
	if err := try("sms", good); err == nil {
		t.Fatal("must refuse without a saved card")
	}
	f.saveCard(t)
	for name, in := range map[string]AutoTopupInput{
		"no threshold":      {Enabled: true, BundleCode: "bundle_500"},
		"huge threshold":    {Enabled: true, Threshold: MaxAutoTopupThreshold + 1, BundleCode: "bundle_500"},
		"unknown bundle":    {Enabled: true, Threshold: 20, BundleCode: "bundle_7"},
		"limit below price": {Enabled: true, Threshold: 20, BundleCode: "bundle_1000", MonthlyLimitKES: ptr(500)},
		"limit too large":   {Enabled: true, Threshold: 20, BundleCode: "bundle_100", MonthlyLimitKES: ptr(MaxAutoTopupLimitKES + 1)},
	} {
		if err := try("sms", in); err == nil {
			t.Fatalf("%s must be refused", name)
		}
	}
	if err := try("ai_credits", good); err == nil {
		t.Fatal("AI credits have no activation to auto top up")
	}
	a, err := f.svc.SetAutoTopup(ctx, f.tenantID, "sms", good)
	if err != nil || !a.Enabled || a.MonthlyLimitKES != DefaultAutoTopupLimitKES || a.Threshold != 20 {
		t.Fatalf("valid setting with the default limit: %+v %v", a, err)
	}
	off, err := f.svc.SetAutoTopup(ctx, f.tenantID, "sms", AutoTopupInput{Enabled: false})
	if err != nil || off.Enabled {
		t.Fatalf("turn off: %+v %v", off, err)
	}
	got, _ := f.svc.GetAutoTopup(ctx, f.tenantID, "sms")
	if got.Enabled || got.Threshold != 20 {
		t.Fatalf("settings are kept when off: %+v", got)
	}
}

func ptr(v float64) *float64 { return &v }

func TestAutoTopupChargesOnceBelowThresholdAndNeverAbove(t *testing.T) {
	f := newFixture(t, map[string]any{})
	ft := &fakeTreasury{}
	withTreasury(t, f, ft)
	ctx := context.Background()
	f.grant(t, "sms", KindBundle, "bundle_100", "g", 1)
	f.saveCard(t)
	enableTopup(t, f, "sms", 50, "bundle_500", nil)
	now := time.Date(2026, 10, 19, 10, 0, 0, 0, time.UTC)

	// 100 units left, threshold 50: nothing happens.
	if r, err := f.svc.RunAutoTopups(ctx, now); err != nil || r.Charged != 0 || ft.count() != 0 {
		t.Fatalf("above the threshold must not charge: %+v %v charges=%d", r, err, ft.count())
	}
	f.spend(t, "sms", 70, "m1") // 30 left, below 50
	r, err := f.svc.RunAutoTopups(ctx, now)
	if err != nil || r.Charged != 1 || ft.count() != 1 {
		t.Fatalf("below the threshold buys one bundle: %+v %v charges=%d", r, err, ft.count())
	}
	c := ft.charges[0]
	meta, _ := c["metadata"].(map[string]any)
	if c["amount"].(float64) != 500 || c["reference_type"] != RefTypeCreditPurchase || c["authorization_code"] != "AUTH_test" ||
		meta["purchase_code"] != "bundle_500" || meta["auto_topup"] != true {
		t.Fatalf("charge request: %+v", c)
	}
	if ref, _ := c["reference_id"].(string); !strings.HasPrefix(ref, "AUTOTOP-") || !strings.HasSuffix(ref, "-sms-20261019") {
		t.Fatalf("reference must carry the day so Treasury dedupes: %q", ref)
	}
	// Same day, balance still low (payment not settled yet): never a second charge.
	for i := 0; i < 3; i++ {
		if r, _ := f.svc.RunAutoTopups(ctx, now.Add(time.Duration(i+1)*time.Hour)); r.Charged != 0 {
			t.Fatalf("second charge on the same day: %+v", r)
		}
	}
	if ft.count() != 1 {
		t.Fatalf("charges=%d", ft.count())
	}
	// Next day, still low: one more.
	if r, _ := f.svc.RunAutoTopups(ctx, now.Add(24*time.Hour)); r.Charged != 1 || ft.count() != 2 {
		t.Fatalf("next day buys again: %+v charges=%d", r, ft.count())
	}
}

func TestAutoTopupRespectsMonthlyLimitAndResetsNextMonth(t *testing.T) {
	f := newFixture(t, map[string]any{})
	ft := &fakeTreasury{}
	withTreasury(t, f, ft)
	ctx := context.Background()
	f.grant(t, "sms", KindBundle, "bundle_100", "g", 1)
	f.saveCard(t)
	enableTopup(t, f, "sms", 1000, "bundle_500", ptr(1000)) // room for two KES 500 bundles
	day := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)

	for i := 0; i < 5; i++ {
		f.svc.RunAutoTopups(ctx, day.Add(time.Duration(i)*24*time.Hour))
	}
	if ft.count() != 2 {
		t.Fatalf("a KES 1000 limit allows exactly two KES 500 bundles in a month, got %d", ft.count())
	}
	a, _ := f.svc.GetAutoTopup(ctx, f.tenantID, "sms")
	if a.SpentKES != 1000 || !a.Enabled {
		t.Fatalf("spend tracked, setting stays on at the limit: %+v", a)
	}
	f.svc.RunAutoTopups(ctx, time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC))
	if ft.count() != 3 {
		t.Fatalf("a new month resets the spend, got %d charges", ft.count())
	}
}

func TestAutoTopupDeclinedCardSwitchesOffAndNotifies(t *testing.T) {
	f := newFixture(t, map[string]any{})
	ft := &fakeTreasury{status: http.StatusPaymentRequired, body: `{"error":"insufficient funds"}`}
	withTreasury(t, f, ft)
	ctx := context.Background()
	f.grant(t, "sms", KindBundle, "bundle_100", "g", 1)
	f.saveCard(t)
	enableTopup(t, f, "sms", 1000, "bundle_500", nil)
	now := time.Date(2026, 10, 19, 10, 0, 0, 0, time.UTC)

	r, err := f.svc.RunAutoTopups(ctx, now)
	if err != nil || r.Failed != 1 || r.Charged != 0 {
		t.Fatalf("declined: %+v %v", r, err)
	}
	a, _ := f.svc.GetAutoTopup(ctx, f.tenantID, "sms")
	if a.Enabled || a.DisabledReason != "insufficient funds" || a.SpentKES != 0 {
		t.Fatalf("switched off with the reason, nothing counted as spent: %+v", a)
	}
	var events int
	_ = f.db.QueryRow(`SELECT count(*) FROM outbox_events WHERE payload::text LIKE '%' || $1 || '%'`, EventAutoTopupFailed).Scan(&events)
	if events != 1 {
		t.Fatalf("the tenant admin must be told: outbox events %d", events)
	}
	f.svc.RunAutoTopups(ctx, now.Add(48*time.Hour))
	if ft.count() != 1 {
		t.Fatalf("switched off means no more attempts, got %d charges", ft.count())
	}
}

func TestAutoTopupUnknownOutcomeIsNotRetriedTodayOrSwitchedOff(t *testing.T) {
	f := newFixture(t, map[string]any{})
	ft := &fakeTreasury{status: http.StatusInternalServerError, body: `{}`}
	withTreasury(t, f, ft)
	ctx := context.Background()
	f.grant(t, "sms", KindBundle, "bundle_100", "g", 1)
	f.saveCard(t)
	enableTopup(t, f, "sms", 1000, "bundle_500", nil)
	now := time.Date(2026, 10, 19, 10, 0, 0, 0, time.UTC)

	f.svc.RunAutoTopups(ctx, now)
	first := ft.count()
	f.svc.RunAutoTopups(ctx, now.Add(2*time.Hour))
	if ft.count() != first {
		t.Fatalf("an attempt whose outcome is unclear must not be repeated the same day: %d then %d", first, ft.count())
	}
	if a, _ := f.svc.GetAutoTopup(ctx, f.tenantID, "sms"); !a.Enabled || a.SpentKES != 0 {
		t.Fatalf("a server error is not a decline: it stays on and nothing is counted as spent: %+v", a)
	}
	var events int
	_ = f.db.QueryRow(`SELECT count(*) FROM outbox_events WHERE payload::text LIKE '%' || $1 || '%'`, EventAutoTopupFailed).Scan(&events)
	if events != 0 {
		t.Fatalf("no failure notice for an unknown outcome, got %d", events)
	}
}

func TestAutoTopupSkipsExemptSuspendedAndMissingCard(t *testing.T) {
	f := newFixture(t, map[string]any{})
	ft := &fakeTreasury{}
	withTreasury(t, f, ft)
	ctx := context.Background()
	f.grant(t, "sms", KindBundle, "bundle_100", "g", 1)
	f.saveCard(t)
	enableTopup(t, f, "sms", 1000, "bundle_500", nil)
	now := time.Date(2026, 10, 19, 10, 0, 0, 0, time.UTC)

	if _, err := f.orm.TenantSubscription.UpdateOneID(f.subID).SetStatus("SUSPENDED").Save(ctx); err != nil {
		t.Fatal(err)
	}
	f.svc.RunAutoTopups(ctx, now)
	if ft.count() != 0 {
		t.Fatal("a suspended subscription is never charged")
	}
	if _, err := f.orm.TenantSubscription.UpdateOneID(f.subID).SetStatus("ACTIVE").Save(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := f.orm.Tenant.UpdateOneID(f.tenantID).SetSubscriptionExempt(true).Save(ctx); err != nil {
		t.Fatal(err)
	}
	f.svc.RunAutoTopups(ctx, now)
	if ft.count() != 0 {
		t.Fatal("an exempt tenant is never charged")
	}
	if _, err := f.orm.Tenant.UpdateOneID(f.tenantID).SetSubscriptionExempt(false).Save(ctx); err != nil {
		t.Fatal(err)
	}
	// Card removed after the setting was made: no charge, switched off with a reason.
	sub, _ := f.orm.TenantSubscription.Get(ctx, f.subID)
	meta := map[string]any{}
	for k, v := range sub.Metadata {
		if k != "paystack_auth_code" {
			meta[k] = v
		}
	}
	if _, err := f.orm.TenantSubscription.UpdateOneID(f.subID).SetMetadata(meta).Save(ctx); err != nil {
		t.Fatal(err)
	}
	r, _ := f.svc.RunAutoTopups(ctx, now)
	a, _ := f.svc.GetAutoTopup(ctx, f.tenantID, "sms")
	if ft.count() != 0 || r.Failed != 1 || a.Enabled || a.DisabledReason == "" {
		t.Fatalf("no card: charges=%d result=%+v setting=%+v", ft.count(), r, a)
	}
}

func TestAutoTopupIgnoresTenantsWithItOff(t *testing.T) {
	f := newFixture(t, map[string]any{})
	ft := &fakeTreasury{}
	withTreasury(t, f, ft)
	f.grant(t, "sms", KindBundle, "bundle_100", "g", 1) // balance 100, no setting at all
	r, err := f.svc.RunAutoTopups(context.Background(), time.Now())
	if err != nil || r.Checked != 0 || ft.count() != 0 {
		t.Fatalf("only enabled settings are visited: %+v %v charges=%d", r, err, ft.count())
	}
}
