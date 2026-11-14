package subscriptions

import (
	"testing"
	"time"

	"github.com/bengobox/subscription-service/internal/ent"
)

func TestProratedUpgradeCharge(t *testing.T) {
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 0, 30)
	sub := &ent.TenantSubscription{BillingCycle: "MONTHLY", CurrentPeriodStart: start, CurrentPeriodEnd: end}

	c := ProratedUpgradeCharge(sub, 1500, 3500, start.AddDate(0, 0, 10))
	if c.NetAmount != 1333.33 || c.MonthlyDiff != 2000 || c.Months != 1 {
		t.Fatalf("20 of 30 days of a KES 2,000 difference: %+v", c)
	}
	if c := ProratedUpgradeCharge(sub, 3500, 1500, start.AddDate(0, 0, 10)); c.NetAmount != 0 {
		t.Fatalf("a downgrade is never charged: %+v", c)
	}
	if c := ProratedUpgradeCharge(sub, 1500, 3500, end.Add(time.Hour)); c.NetAmount != 0 {
		t.Fatalf("no paid time left, no charge: %+v", c)
	}
	if c := ProratedUpgradeCharge(sub, 1500, 3500, start.Add(-time.Hour)); c.NetAmount != 2000 {
		t.Fatalf("at most the whole period: %+v", c)
	}
	semi := &ent.TenantSubscription{BillingCycle: "SEMI_ANNUAL", CurrentPeriodStart: start, CurrentPeriodEnd: start.AddDate(0, 6, 0)}
	half := start.Add(semi.CurrentPeriodEnd.Sub(start) / 2)
	if c := ProratedUpgradeCharge(semi, 1000, 2000, half); c.Months != 6 || c.NetAmount != 3000 {
		t.Fatalf("half of a six-month period at KES 1,000 a month more: %+v", c)
	}
}

func TestHasPaidTimeLeft(t *testing.T) {
	now := time.Now().UTC()
	monthly := &ent.SubscriptionPlan{BillingCycle: "MONTHLY", BasePrice: 1500}
	free := &ent.SubscriptionPlan{BillingCycle: "MONTHLY", BasePrice: 0}
	oneTime := &ent.SubscriptionPlan{BillingCycle: "ONE_TIME", BasePrice: 45000}
	active := &ent.TenantSubscription{Status: "ACTIVE", CurrentPeriodEnd: now.Add(24 * time.Hour)}
	if !hasPaidTimeLeft(active, monthly, now) {
		t.Fatal("an active paid period has time left")
	}
	if hasPaidTimeLeft(active, free, now) || hasPaidTimeLeft(active, oneTime, now) {
		t.Fatal("free plans and one-time licences switch at once")
	}
	trial := &ent.TenantSubscription{Status: "TRIAL", CurrentPeriodEnd: now.Add(24 * time.Hour)}
	if hasPaidTimeLeft(trial, monthly, now) {
		t.Fatal("a trial has no paid time")
	}
	lapsed := &ent.TenantSubscription{Status: "ACTIVE", CurrentPeriodEnd: now.Add(-time.Hour)}
	if hasPaidTimeLeft(lapsed, monthly, now) {
		t.Fatal("a period that ended has no paid time")
	}
}
