package billing

import (
	"context"
	"encoding/json"
	"testing"
)

// alerts returns the balance alerts in the outbox, oldest first, as (resource, level, remaining).
func (f *ledgerFixture) alerts(t *testing.T) []map[string]any {
	t.Helper()
	rows, err := f.db.Query(`SELECT payload FROM outbox_events WHERE tenant_id = $1 AND aggregate_type = 'billing' AND event_type = 'balance.low' ORDER BY created_at, id`, f.tenantID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var b []byte
		if err := rows.Scan(&b); err != nil {
			t.Fatal(err)
		}
		var env struct {
			EventType     string         `json:"event_type"`
			AggregateType string         `json:"aggregate_type"`
			Payload       map[string]any `json:"payload"`
		}
		if err := json.Unmarshal(b, &env); err != nil || env.EventType != "balance.low" || env.AggregateType != "billing" {
			t.Fatalf("outbox payload must be the full envelope: %s", b)
		}
		out = append(out, env.Payload)
	}
	return out
}

func (f *ledgerFixture) spend(t *testing.T, spec MeteredService, units int64) {
	t.Helper()
	res, err := f.svc.Consume(context.Background(), ConsumeRequest{TenantID: f.tenantID, Service: spec, Units: units})
	if err != nil || !res.Allowed {
		t.Fatalf("spend %d: %+v %v", units, res, err)
	}
}

// AI credits: an allowance of 80 plus a bundle of 20 makes a period total of 100, so the
// low mark is 20. Each crossing alerts once; a top-up re-arms the low alert.
func TestBalanceAlertQuotaServiceCrossesTwentyAndZero(t *testing.T) {
	f := newLedgerFixture(t)
	ctx := context.Background()
	ai, _ := LookupMeteredService(ServiceAI)
	f.credit(t, ServiceAI, 20)
	if _, err := f.svc.SetQuota(ctx, f.tenantID, ai.QuotaTag, 80, "w:test", "allowance"); err != nil {
		t.Fatal(err)
	}
	f.spend(t, ai, 79) // 21 left
	if a := f.alerts(t); len(a) != 0 {
		t.Fatalf("no crossing yet: %v", a)
	}
	f.spend(t, ai, 2) // 19 left: low
	f.spend(t, ai, 1) // 18 left: still low, no new alert
	a := f.alerts(t)
	if len(a) != 1 || a[0]["level"] != "low" || a[0]["resource"] != "ai_credits" || a[0]["remaining"] != 19.0 ||
		a[0]["threshold"] != 20.0 || a[0]["unit"] != "credits" || a[0]["scope"] != "tenant" || a[0]["tenant_id"] != f.tenantID.String() {
		t.Fatalf("low alert = %v", a)
	}
	if u, _ := a[0]["buy_url"].(string); u == "" {
		t.Fatal("the alert carries a Buy credits link")
	}
	f.spend(t, ai, 18) // empty
	if a = f.alerts(t); len(a) != 2 || a[1]["level"] != "empty" || a[1]["remaining"] != 0.0 {
		t.Fatalf("empty alert = %v", a)
	}
	// A refused spend writes nothing and raises nothing.
	if res, _ := f.svc.Consume(ctx, ConsumeRequest{TenantID: f.tenantID, Service: ai, Units: 1}); res.Allowed {
		t.Fatal("an empty prepaid service refuses")
	}
	// A top-up re-arms: draining below the mark again alerts again.
	f.credit(t, ServiceAI, 30)
	f.spend(t, ai, 12) // 18 left
	if a = f.alerts(t); len(a) != 3 || a[2]["level"] != "low" {
		t.Fatalf("after a top-up = %v", a)
	}
}

// eTIMS tokens have no allowance: the wallet's own low_balance_threshold (50) is the mark.
func TestBalanceAlertPrepaidServiceUsesWalletThreshold(t *testing.T) {
	f := newLedgerFixture(t)
	ctx := context.Background()
	f.credit(t, ServiceETIMS, 60)
	if res, err := f.svc.DeductTokens(ctx, DeductInput{TenantID: f.tenantID, ServiceTag: ServiceETIMS, Tokens: 5}); err != nil || !res.Allowed {
		t.Fatal(err)
	}
	if a := f.alerts(t); len(a) != 0 {
		t.Fatalf("55 left is above 50: %v", a)
	}
	if res, err := f.svc.DeductTokens(ctx, DeductInput{TenantID: f.tenantID, ServiceTag: ServiceETIMS, Tokens: 10}); err != nil || !res.Allowed {
		t.Fatal(err)
	}
	a := f.alerts(t)
	if len(a) != 1 || a[0]["resource"] != "etims_api" || a[0]["level"] != "low" || a[0]["unit"] != "tokens" || a[0]["threshold"] != 50.0 {
		t.Fatalf("etims alert = %v", a)
	}
}

// SMS runs into overage past its bundle: reaching zero is the empty alert.
func TestBalanceAlertOverageServiceEmpties(t *testing.T) {
	f := newLedgerFixture(t)
	ctx := context.Background()
	f.credit(t, ServiceSMS, 10) // already below the 50 mark: only empty can cross
	res, err := f.svc.Consume(ctx, ConsumeRequest{TenantID: f.tenantID, Service: sms(), Units: 12, AllowOverage: true, SubscriptionID: f.subID})
	if err != nil || !res.Allowed || res.Overage != 2 {
		t.Fatalf("overage spend: %+v %v", res, err)
	}
	a := f.alerts(t)
	if len(a) != 1 || a[0]["resource"] != "sms" || a[0]["level"] != "empty" {
		t.Fatalf("sms alert = %v", a)
	}
}
