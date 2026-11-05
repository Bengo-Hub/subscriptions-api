package subscriptions

import (
	"context"
	"testing"
	"time"
)

// A price agreed for one plan never prices another: an upgrade scheduled over a custom price bills
// the new plan's list price, and moving plan_id clears the agreed price for good. This is the bug
// that would have billed Alpha China KES 3,000 on Professional.
func TestCustomPriceDoesNotFollowAPlanChange(t *testing.T) {
	f := newTrialFixture(t)
	ctx := context.Background()
	if _, err := f.client.TenantSubscription.Update().SetCustomBasePrice(3500).SetCustomPriceReason("agreed").Save(ctx); err != nil {
		t.Fatal(err)
	}
	sub := f.sub(t)
	if got := EffectivePrice(sub, f.basic); got != 3500 {
		t.Fatalf("own plan: %v, want the agreed 3500", got)
	}
	if got := EffectivePrice(sub, f.pro); got != f.pro.BasePrice {
		t.Fatalf("another plan: %v, want its list price %v", got, f.pro.BasePrice)
	}
	// With no plan to compare against the agreed price still answers (a caller holding only sub).
	if got := EffectivePrice(sub, nil); got != 3500 {
		t.Fatalf("no plan: %v, want 3500", got)
	}

	// Scheduled: the renewal bills the new plan at list price, not the agreed price.
	due := time.Now().UTC().Add(-time.Minute)
	if err := f.svc.schedulePlanChange(ctx, sub, f.basic, f.pro, due, ChangeSourceTenant, ""); err != nil {
		t.Fatal(err)
	}
	sub = f.sub(t)
	billing, err := BillingPlanFor(ctx, f.client, sub)
	if err != nil {
		t.Fatal(err)
	}
	if billing.ID != f.pro.ID || EffectivePrice(sub, billing) != f.pro.BasePrice {
		t.Fatalf("renewal bills %s at %v, want Pro at %v", billing.PlanCode, EffectivePrice(sub, billing), f.pro.BasePrice)
	}

	// Applied: plan_id moves and the agreed price is gone.
	if _, err := f.svc.ApplyScheduledPlanChangeNow(ctx, f.tenant); err != nil {
		t.Fatal(err)
	}
	sub = f.sub(t)
	if sub.PlanID != f.pro.ID || sub.CustomBasePrice != nil || sub.CustomPriceReason != nil {
		t.Fatalf("after applying: plan %s custom %v reason %v", sub.PlanID, sub.CustomBasePrice, sub.CustomPriceReason)
	}
	if got := EffectivePrice(sub, f.pro); got != f.pro.BasePrice {
		t.Fatalf("after applying: %v, want %v", got, f.pro.BasePrice)
	}
}

// Keeping the same plan keeps the agreed price: renewing is not a plan change.
func TestCustomPriceSurvivesARenewal(t *testing.T) {
	f := newTrialFixture(t)
	ctx := context.Background()
	if _, err := f.client.TenantSubscription.Update().SetCustomBasePrice(3500).Save(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.RenewSubscription(ctx, RenewInput{TenantID: f.tenant}); err != nil {
		t.Fatal(err)
	}
	sub := f.sub(t)
	if sub.CustomBasePrice == nil || *sub.CustomBasePrice != 3500 {
		t.Fatalf("custom price after renewal = %v, want 3500", sub.CustomBasePrice)
	}
}
