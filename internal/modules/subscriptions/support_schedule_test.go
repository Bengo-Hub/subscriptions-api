package subscriptions

import (
	"testing"
	"time"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/supportagreement"
)

func day(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 9, 0, 0, 0, time.UTC) }

var (
	monthly   = SupportInterval{Count: 1, Unit: supportagreement.IntervalUnitMONTH}
	quarterly = SupportInterval{Count: 3, Unit: supportagreement.IntervalUnitMONTH}
	annual    = SupportInterval{Count: 12, Unit: supportagreement.IntervalUnitMONTH}
	fortnight = SupportInterval{Count: 14, Unit: supportagreement.IntervalUnitDAY}
)

func TestResolveSupportInterval(t *testing.T) {
	cases := []struct {
		cycle   supportagreement.BillingCycle
		count   int
		unit    string
		want    SupportInterval
		wantErr bool
	}{
		{supportagreement.BillingCycleMONTHLY, 0, "", monthly, false},
		{supportagreement.BillingCycleQUARTERLY, 99, "DAY", quarterly, false}, // presets ignore count/unit
		{supportagreement.BillingCycleSEMI_ANNUAL, 0, "", SupportInterval{6, supportagreement.IntervalUnitMONTH}, false},
		{supportagreement.BillingCycleANNUAL, 0, "", annual, false},
		{supportagreement.BillingCycleCUSTOM, 2, "", SupportInterval{2, supportagreement.IntervalUnitMONTH}, false},
		{supportagreement.BillingCycleCUSTOM, 14, "day", fortnight, false},
		{supportagreement.BillingCycleCUSTOM, 0, "MONTH", SupportInterval{}, true},
		{supportagreement.BillingCycleCUSTOM, 37, "MONTH", SupportInterval{}, true},
		{supportagreement.BillingCycleCUSTOM, 6, "DAY", SupportInterval{}, true}, // below 7-day floor
		{supportagreement.BillingCycleCUSTOM, 367, "DAY", SupportInterval{}, true},
		{supportagreement.BillingCycleCUSTOM, 3, "WEEK", SupportInterval{}, true},
	}
	for _, c := range cases {
		got, err := ResolveSupportInterval(c.cycle, c.count, c.unit)
		if (err != nil) != c.wantErr {
			t.Fatalf("%s %d %s: err=%v wantErr=%v", c.cycle, c.count, c.unit, err, c.wantErr)
		}
		if !c.wantErr && got != c.want {
			t.Fatalf("%s %d %s: got %+v want %+v", c.cycle, c.count, c.unit, got, c.want)
		}
	}
}

func TestNormalizeSupportCycle(t *testing.T) {
	for in, want := range map[string]supportagreement.BillingCycle{
		"monthly": "MONTHLY", "semi-annual": "SEMI_ANNUAL", " Quarterly ": "QUARTERLY", "custom": "CUSTOM", "": "",
	} {
		got, err := NormalizeSupportCycle(in)
		if err != nil || got != want {
			t.Fatalf("%q: got %q err %v, want %q", in, got, err, want)
		}
	}
	if _, err := NormalizeSupportCycle("weekly"); err == nil {
		t.Fatal("weekly should be rejected")
	}
}

func TestSchedulePeriodStartClampsMonthEnd(t *testing.T) {
	anchor := day(2026, time.January, 31)
	want := []time.Time{
		day(2026, time.January, 31),
		day(2026, time.February, 28), // clamped, not Mar 3
		day(2026, time.March, 31),    // back to the 31st, no drift to the 28th
		day(2026, time.April, 30),
	}
	for k, w := range want {
		if got := SchedulePeriodStart(anchor, k, monthly); !got.Equal(w) {
			t.Fatalf("k=%d got %s want %s", k, got, w)
		}
	}
	// Leap year Feb 29 anchor, annual.
	leap := day(2028, time.February, 29)
	if got := SchedulePeriodStart(leap, 1, annual); !got.Equal(day(2029, time.February, 28)) {
		t.Fatalf("leap annual got %s", got)
	}
	if got := SchedulePeriodStart(leap, 4, annual); !got.Equal(day(2032, time.February, 29)) {
		t.Fatalf("leap annual k=4 got %s", got)
	}
	if got := SchedulePeriodStart(day(2026, time.March, 1), 2, fortnight); !got.Equal(day(2026, time.March, 29)) {
		t.Fatalf("days got %s", got)
	}
}

func TestPeriodIndexAt(t *testing.T) {
	anchor := day(2025, time.July, 13)
	cases := []struct {
		at   time.Time
		iv   SupportInterval
		want int
	}{
		{day(2025, time.July, 1), annual, 0},  // before the anchor
		{anchor, annual, 0},                   // exactly at the anchor
		{day(2026, time.July, 12), annual, 0}, // day before the first anniversary
		{day(2026, time.July, 13), annual, 1}, // anniversary starts period 1
		{day(2027, time.July, 14), annual, 2},
		{day(2025, time.October, 12), quarterly, 0},
		{day(2025, time.October, 13), quarterly, 1},
		{day(2025, time.July, 27), fortnight, 1},
		{day(2025, time.July, 26), fortnight, 0},
	}
	for _, c := range cases {
		if got := PeriodIndexAt(anchor, c.at, c.iv); got != c.want {
			t.Fatalf("at %s iv %+v: got %d want %d", c.at.Format("2006-01-02"), c.iv, got, c.want)
		}
	}
	// Month-end anchor: Feb 28 belongs to the period that starts on the clamped Feb 28.
	if got := PeriodIndexAt(day(2026, time.January, 31), day(2026, time.February, 28), monthly); got != 1 {
		t.Fatalf("month-end period index got %d want 1", got)
	}
}

func TestSupportDueDateAndCreateAt(t *testing.T) {
	start, end := day(2026, time.March, 1), day(2026, time.April, 1)
	if got := SupportDueDate(start, end, supportagreement.BillingTimingADVANCE); !got.Equal(start) {
		t.Fatalf("advance due %s", got)
	}
	if got := SupportDueDate(start, end, supportagreement.BillingTimingARREARS); !got.Equal(end) {
		t.Fatalf("arrears due %s", got)
	}
	// ADVANCE: created one invoice lead before the period starts.
	if got := SupportCycleCreateAt(start, start); !got.Equal(start.AddDate(0, 0, -SupportFeeInvoiceLeadDays)) {
		t.Fatalf("advance create-at %s", got)
	}
	// ARREARS over a long period: created when the period starts (tenant sees it coming).
	if got := SupportCycleCreateAt(start, end); !got.Equal(start) {
		t.Fatalf("arrears create-at %s", got)
	}
	// ARREARS over a period shorter than the lead: lead wins so the invoice is still on time.
	short := start.AddDate(0, 0, 5)
	if got := SupportCycleCreateAt(start, short); !got.Equal(short.AddDate(0, 0, -SupportFeeInvoiceLeadDays)) {
		t.Fatalf("short arrears create-at %s", got)
	}
}

func TestSupportBackdatedDue(t *testing.T) {
	now := day(2026, time.October, 4)
	// A due date already in the past is pushed to now + lead: notice plus grace, never an
	// instant block.
	if got := SupportBackdatedDue(day(2026, time.June, 1), now); !got.Equal(now.AddDate(0, 0, SupportFeeInvoiceLeadDays)) {
		t.Fatalf("past due got %s", got)
	}
	// A future due date is untouched, even inside the lead window.
	soon := now.AddDate(0, 0, 2)
	if got := SupportBackdatedDue(soon, now); !got.Equal(soon) {
		t.Fatalf("future due moved to %s", got)
	}
}

func TestProrationAndMonthlyEquivalent(t *testing.T) {
	cases := []struct {
		iv      SupportInterval
		annual  float64
		period  float64
		monthly float64
	}{
		{annual, 25000, 25000, 2083.33},
		{monthly, 25000, 2083.33, 2083.33},
		{quarterly, 18000, 4500, 1500},
		{SupportInterval{6, supportagreement.IntervalUnitMONTH}, 12000, 6000, 1000},
		{SupportInterval{365, supportagreement.IntervalUnitDAY}, 36500, 36500, 3041.67},
	}
	for _, c := range cases {
		p := ProrateAnnualSupport(c.annual, c.iv)
		if p != c.period {
			t.Fatalf("%+v prorate got %v want %v", c.iv, p, c.period)
		}
		if m := roundMoney(MonthlyEquivalent(p, c.iv)); m != c.monthly {
			t.Fatalf("%+v monthly got %v want %v", c.iv, m, c.monthly)
		}
	}
}

func TestAgreementPeriodAmount(t *testing.T) {
	plan := &ent.SubscriptionPlan{BasePrice: 24000}
	std := &ent.SupportAgreement{BillingCycle: supportagreement.BillingCycleMONTHLY, IntervalCount: 1, IntervalUnit: supportagreement.IntervalUnitMONTH}
	if got := AgreementPeriodAmount(std, plan); got != 2000 {
		t.Fatalf("prorated standard got %v", got)
	}
	agreed := 1500.0
	std.Amount = &agreed
	if got := AgreementPeriodAmount(std, plan); got != 1500 {
		t.Fatalf("agreed amount should win, got %v", got)
	}
	special := &ent.SupportAgreement{BillingCycle: supportagreement.BillingCycleCUSTOM, IntervalCount: 2, IntervalUnit: supportagreement.IntervalUnitMONTH}
	if got := AgreementPeriodAmount(special, nil); got != 0 {
		t.Fatalf("special without amount must be 0 (never billed), got %v", got)
	}
}

func TestScheduleAnchorReanchor(t *testing.T) {
	start := day(2025, time.January, 15)
	a := &ent.SupportAgreement{StartsAt: start}
	anchor, cycle := scheduleAnchor(a)
	if !anchor.Equal(start) || cycle != 0 {
		t.Fatalf("default anchor %s/%d", anchor, cycle)
	}
	// Reschedule after 4 annual cycles to monthly from 2029-01-15: cycle 5 is period 0 of the new
	// schedule, and the JSON round trip (float64) is handled.
	re := day(2029, time.January, 15)
	meta := withAnchor(nil, re, 4)
	meta[MetaScheduleAnchorCycle] = float64(4)
	a.Metadata = meta
	anchor, cycle = scheduleAnchor(a)
	if !anchor.Equal(re) || cycle != 4 {
		t.Fatalf("reanchored %s/%d", anchor, cycle)
	}
	n := 5
	if got := SchedulePeriodStart(anchor, n-1-cycle, monthly); !got.Equal(re) {
		t.Fatalf("cycle 5 starts %s want %s", got, re)
	}
}

func TestSupportPlanCodeFor(t *testing.T) {
	for in, want := range map[string]string{
		"POWERSUITE_DUKA_GOLD_ONE_TIME": "SUPPORT_DUKA_GOLD",
		"LIBRARY_PROFESSIONAL_ONE_TIME": "SUPPORT_LIBRARY_PROFESSIONAL",
		"ERP_STARTER_ONE_TIME":          "SUPPORT_ERP_STARTER",
		"AFYA_CLINIC_ONE_TIME":          "SUPPORT_AFYA_CLINIC",
	} {
		if got, ok := SupportPlanCodeFor(in); !ok || got != want {
			t.Fatalf("%s: got %s", in, got)
		}
	}
	if _, ok := SupportPlanCodeFor("POWERSUITE_DUKA_GOLD"); ok {
		t.Fatal("recurring plan must not map to a support plan")
	}
}
