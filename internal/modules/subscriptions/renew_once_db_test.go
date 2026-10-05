package subscriptions

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// The same paid invoice reported twice (a hand-recorded payment and the gateway's late settlement,
// both publishing intent_id = the invoice id) renews the subscription once; a different payment
// renews it again. Before, each report added a period (alpha-china-market, 2026-10-05).
func TestRenewSubscriptionOncePerPayment(t *testing.T) {
	svc, client := newSupportTestService(t)
	ctx := context.Background()
	tn, err := client.Tenant.Create().SetID(uuid.New()).SetName("Renew Once").SetSlug("renew-" + uuid.NewString()[:6]).Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	plan := mustPlan(t, client, "POWERSUITE_DUKA_BASIC", "MONTHLY", 3000)
	start := time.Now().UTC().Add(-31 * 24 * time.Hour)
	if _, err := client.TenantSubscription.Create().
		SetTenantID(tn.ID).SetPlanID(plan.ID).
		SetCurrentPeriodStart(start).SetCurrentPeriodEnd(start.AddDate(0, 1, 0)).
		SetBillingCycle("MONTHLY").SetStatus("ACTIVE").Save(ctx); err != nil {
		t.Fatal(err)
	}

	invoiceID := uuid.NewString()
	first, err := svc.RenewSubscription(ctx, RenewInput{TenantID: tn.ID, IntentID: invoiceID})
	if err != nil {
		t.Fatalf("first renewal: %v", err)
	}
	again, err := svc.RenewSubscription(ctx, RenewInput{TenantID: tn.ID, IntentID: invoiceID})
	if err != nil {
		t.Fatalf("second report of the same payment: %v", err)
	}
	if !again.CurrentPeriodEnd.Equal(first.CurrentPeriodEnd) {
		t.Fatalf("same payment renewed twice: period end %s then %s", first.CurrentPeriodEnd, again.CurrentPeriodEnd)
	}

	next, err := svc.RenewSubscription(ctx, RenewInput{TenantID: tn.ID, IntentID: uuid.NewString()})
	if err != nil {
		t.Fatalf("next payment: %v", err)
	}
	if !next.CurrentPeriodEnd.After(first.CurrentPeriodEnd) {
		t.Fatalf("a new payment did not renew: %s after %s", next.CurrentPeriodEnd, first.CurrentPeriodEnd)
	}
}
