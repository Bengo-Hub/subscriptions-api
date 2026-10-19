package billing

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"
)

// The daily overage sweep sums the day's usage events in SQL; an empty day and a day over the
// plan limit must both come out right.
func TestDailyOverageSumsInSQL(t *testing.T) {
	orm, _, tenantID := newUsageFixture(t, 3)
	ctx := context.Background()
	sub, err := orm.TenantSubscription.Query().Only(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := orm.TenantSubscription.UpdateOneID(sub.ID).SetAllowOverage(true).Save(ctx); err != nil {
		t.Fatal(err)
	}
	plan, _ := orm.SubscriptionPlan.Get(ctx, sub.PlanID)
	if _, err := orm.SubscriptionPlan.UpdateOneID(plan.ID).SetTierLimitsJSON(map[string]any{
		"max_orders_per_month": 3, "overage_orders_price_per_100_month": 100.0,
	}).Save(ctx); err != nil {
		t.Fatal(err)
	}

	svc := NewOverageService(zap.NewNop(), orm)
	today := time.Now().UTC()

	// No events yet: nothing is billed and nothing fails on the empty sum.
	if err := svc.CalculateDailyOverages(ctx, today); err != nil {
		t.Fatal(err)
	}
	if total, _ := svc.GetAccumulatedOverage(ctx, tenantID); total != 0 {
		t.Fatalf("empty day billed %v", total)
	}

	// 5 orders against a limit of 3: 2 over at KES 100 per 100 = KES 2.
	for i := 0; i < 5; i++ {
		if _, err := orm.UsageEvent.Create().SetTenantID(tenantID).SetMetricType("orders").SetServiceName("ordering").SetValue(1).Save(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if err := svc.CalculateDailyOverages(ctx, today); err != nil {
		t.Fatal(err)
	}
	if err := svc.CalculateDailyOverages(ctx, today); err != nil {
		t.Fatal(err)
	}
	total, err := svc.GetAccumulatedOverage(ctx, tenantID)
	if err != nil || total != 2 {
		t.Fatalf("overage for the day: %v %v", total, err)
	}
}
