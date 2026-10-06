package handlers

import (
	"strings"
	"testing"
	"time"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/tenantsubscription"
)

func TestPaymentMethodMatchesCardAndMobileMoney(t *testing.T) {
	card := map[string]any{"type": "card", "last4": "4242"}
	mpesa := map[string]any{"type": "mobile_money", "phone": "+254 712 345 678"}
	cases := []struct {
		m    any
		key  string
		want bool
	}{
		{card, "4242", true},
		{card, "1111", false},
		{mpesa, "254712345678", true}, // UI sends the stored phone; format differences ignored
		{mpesa, "0712345679", false},
		{mpesa, "", false},
		{"mpesa_standing_order", "4242", false}, // legacy bare string is never a method
	}
	for _, c := range cases {
		if got := paymentMethodMatches(c.m, c.key); got != c.want {
			t.Errorf("paymentMethodMatches(%v, %q) = %v, want %v", c.m, c.key, got, c.want)
		}
	}
}

func TestWithoutPaymentMethodAllowsRemovingTheLastOne(t *testing.T) {
	only := []any{map[string]any{"type": "card", "last4": "4242"}}
	out, removed := withoutPaymentMethod(only, "4242")
	if !removed || len(out) != 0 {
		t.Fatalf("removing the only card: removed=%v left=%d, want true and 0", removed, len(out))
	}
	if _, removed := withoutPaymentMethod(only, "9999"); removed {
		t.Error("an unknown key must not remove anything")
	}
	two := []any{map[string]any{"last4": "1111"}, map[string]any{"phone": "254700000001"}}
	out, removed = withoutPaymentMethod(two, "254700000001")
	if !removed || len(out) != 1 || out[0].(map[string]any)["last4"] != "1111" {
		t.Errorf("removing mobile money left %v", out)
	}
}

func TestStandingOrderTermsAndAuthorisationText(t *testing.T) {
	end := time.Date(2026, 11, 5, 16, 0, 0, 0, time.UTC)
	sub := &ent.TenantSubscription{BillingCycle: tenantsubscription.BillingCycleMONTHLY, CurrentPeriodEnd: end}
	sub.Edges.Plan = &ent.SubscriptionPlan{Name: "Duka Basic", BasePrice: 2500, Currency: "KES"}

	terms, reason := standingOrderTermsFor(sub)
	if reason != "" {
		t.Fatalf("unexpected reason %q", reason)
	}
	if terms.Frequency != "monthly" || terms.StartDate != "2026-11-05" || terms.Payee != standingOrderPayee || terms.Amount <= 0 {
		t.Errorf("terms = %+v", terms)
	}
	text := standingOrderAuthorisationText(terms)
	for _, part := range []string{standingOrderPayee, "KES", "every month", "Duka Basic", "2026-11-05", "until I cancel"} {
		if !strings.Contains(text, part) {
			t.Errorf("authorisation text %q is missing %q", text, part)
		}
	}

	if _, reason := standingOrderTermsFor(&ent.TenantSubscription{}); reason == "" {
		t.Error("no plan must give a reason, not terms")
	}
	oneTime := &ent.TenantSubscription{BillingCycle: tenantsubscription.BillingCycleONE_TIME, CurrentPeriodEnd: end}
	oneTime.Edges.Plan = &ent.SubscriptionPlan{Name: "Licence", BasePrice: 50000}
	if _, reason := standingOrderTermsFor(oneTime); reason == "" {
		t.Error("a one-time licence cannot be paid by standing order")
	}
}
