package jobs

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/bengobox/subscription-service/internal/ent"
)

// The grace email names the plan in its heading and summary. Before this, grace_started and
// grace_reminder carried no plan, so Gram Auto Spares got "Your subscription for has reached...".
func TestGracePayloadNamesThePlan(t *testing.T) {
	now := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	sub := &ent.TenantSubscription{
		TenantID: uuid.New(),
		Metadata: map[string]any{"last_invoice_number": "INV-261105-000037", "last_invoice_currency": "KES", "last_invoice_total": 3000.0},
		Edges:    ent.TenantSubscriptionEdges{Plan: &ent.SubscriptionPlan{PlanCode: "DUKA_BASIC", Name: "Duka Basic"}},
	}
	p := graceEventPayload(sub, now.AddDate(0, 0, 7), now)
	if p["plan_name"] != "Duka Basic" || p["plan_code"] != "DUKA_BASIC" {
		t.Fatalf("plan missing: %v", p)
	}
	if p["amount"] != "KES 3000.00" || p["days_remaining"] != 7 || p["invoice_number"] != "INV-261105-000037" {
		t.Fatalf("payload: %v", p)
	}
}
