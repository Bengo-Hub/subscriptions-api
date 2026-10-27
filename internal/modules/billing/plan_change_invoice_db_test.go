package billing

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	serviceclient "github.com/Bengo-Hub/shared-service-client"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/bengobox/subscription-service/internal/modules/subscriptions"
)

// fakeTreasury records the S2S calls a plan-change invoice makes and answers them.
type fakeTreasury struct {
	mu         sync.Mutex
	created    []map[string]any
	voided     []string
	amountPaid string
}

func (f *fakeTreasury) handler(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	body, _ := io.ReadAll(r.Body)
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/invoices"):
		var m map[string]any
		_ = json.Unmarshal(body, &m)
		f.created = append(f.created, m)
		_, _ = w.Write([]byte(`{"id":"` + uuid.NewString() + `","invoice_number":"INV-PC-1","public_token":"tok"}`))
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/send"):
		_, _ = w.Write([]byte(`{}`))
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/payments/intents"):
		_, _ = w.Write([]byte(`{"intent_id":"intent-pc-1"}`))
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/void"):
		parts := strings.Split(r.URL.Path, "/")
		f.voided = append(f.voided, parts[len(parts)-2])
		_, _ = w.Write([]byte(`{"status":"cancelled"}`))
	case r.Method == http.MethodGet:
		_, _ = w.Write([]byte(`{"status":"sent","amount_paid":"` + f.amountPaid + `"}`))
	default:
		http.NotFound(w, r)
	}
}

// An upgrade scheduled for the coming renewal after its invoice went out bills only the price
// difference on its own invoice, once; cancelling the upgrade voids it; a part-paid one is kept.
func TestPlanChangeInvoiceForOpenRenewal(t *testing.T) {
	orm, _, tenantID := newUsageFixture(t, 3)
	ctx := context.Background()
	ft := &fakeTreasury{amountPaid: "0"}
	srv := httptest.NewServer(http.HandlerFunc(ft.handler))
	defer srv.Close()

	log := zap.NewNop()
	cfg := serviceclient.DefaultConfig(srv.URL, "treasury", log)
	cfg.MaxElapsedTime = time.Second
	treasury := serviceclient.New(cfg)
	subSvc := subscriptions.New(orm, log, treasury, "", uuid.Nil)
	inv := NewInvoiceService(log, orm, subSvc, treasury, nil, "", uuid.NewString(), "https://books.example", "https://booksapi.example", 16)

	sub, err := orm.TenantSubscription.Query().Only(ctx)
	if err != nil {
		t.Fatal(err)
	}
	basic, _ := orm.SubscriptionPlan.Get(ctx, sub.PlanID)
	pro, err := orm.SubscriptionPlan.Create().SetPlanCode("POWERSUITE_DUKA_PRO").SetName("Duka Pro").SetDescription("t").
		SetBillingCycle("MONTHLY").SetBasePrice(6000).SetCurrency("KES").SetTierOrder(2).Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	meta := map[string]any{
		"last_invoice_id":           uuid.NewString(),
		"last_invoice_period_start": sub.CurrentPeriodStart.UTC().Format(time.RFC3339),
		"last_invoice_period_end":   sub.CurrentPeriodEnd.UTC().Format(time.RFC3339),
		MetaLastInvoicePlanID:       basic.ID.String(),
		subscriptions.MetaScheduledPlanChange: map[string]any{
			"plan_id": pro.ID.String(), "plan_code": pro.PlanCode, "plan_name": pro.Name,
			"from_plan_id": basic.ID.String(), "effective_at": sub.CurrentPeriodEnd.UTC().Format(time.RFC3339Nano),
		},
	}
	if _, err := orm.TenantSubscription.UpdateOneID(sub.ID).SetMetadata(meta).Save(ctx); err != nil {
		t.Fatal(err)
	}

	if err := inv.ReconcileOpenRenewalInvoice(ctx, tenantID); err != nil {
		t.Fatal(err)
	}
	if len(ft.created) != 1 {
		t.Fatalf("plan-change invoices raised: %d", len(ft.created))
	}
	line := ft.created[0]["lines"].([]any)[0].(map[string]any)
	if line["unit_price"].(float64) != 3500 || line["quantity"].(float64) != 1 {
		t.Fatalf("difference line %v", line)
	}
	if !subSvc.IsPlanChangePayment(ctx, tenantID, "intent-pc-1") {
		t.Fatal("plan-change payment would renew the subscription")
	}

	// Running again changes nothing.
	if err := inv.ReconcileOpenRenewalInvoice(ctx, tenantID); err != nil || len(ft.created) != 1 {
		t.Fatalf("second run: %v, invoices %d", err, len(ft.created))
	}

	// A part payment on it blocks the void and keeps the markers.
	sub, _ = orm.TenantSubscription.Get(ctx, sub.ID)
	cancelled := cloneMeta(sub.Metadata)
	delete(cancelled, subscriptions.MetaScheduledPlanChange)
	if _, err := orm.TenantSubscription.UpdateOneID(sub.ID).SetMetadata(cancelled).Save(ctx); err != nil {
		t.Fatal(err)
	}
	ft.amountPaid = "100"
	if err := inv.ReconcileOpenRenewalInvoice(ctx, tenantID); err == nil {
		t.Fatal("a part-paid plan-change invoice was voided")
	}
	if len(ft.voided) != 0 {
		t.Fatal("void called on a part-paid invoice")
	}

	// Unpaid: cancelling the upgrade voids the plan-change invoice and clears its markers.
	ft.amountPaid = "0"
	if err := inv.ReconcileOpenRenewalInvoice(ctx, tenantID); err != nil {
		t.Fatal(err)
	}
	if len(ft.voided) != 1 {
		t.Fatalf("voids: %v", ft.voided)
	}
	sub, _ = orm.TenantSubscription.Get(ctx, sub.ID)
	if sub.Metadata[metaPlanChangeInvoiceID] != nil {
		t.Fatal("plan-change invoice markers kept after the void")
	}
}
