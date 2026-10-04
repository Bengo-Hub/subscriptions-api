package subscriptions

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/bengobox/subscription-service/internal/ent/supportagreement"
)

// Support-agreement schedule math. Pure functions only, so the billing rules are unit-tested
// without a database (support_schedule_test.go). Every support period is computed from a fixed
// schedule anchor as anchor + k intervals, never by chaining AddDate on the previous period, so a
// monthly agreement anchored on the 31st bills on the last day of short months and returns to
// the 31st afterwards instead of drifting to the 28th.

// SupportFeeGraceDays is how long an overdue support charge keeps mutations allowed. It must match
// the graceDays every service passes to authclient.RequireSupportFeeCurrentForMutations (7).
const SupportFeeGraceDays = 7

// SupportFeeInvoiceLeadDays is how far ahead of its due date a support charge is invoiced, the
// same lead the subscription invoicing job uses.
const SupportFeeInvoiceLeadDays = 7

// Bounds for a CUSTOM cycle, wide enough for any realistic agreement and tight enough to reject
// typos (a 0-day cycle would generate cycles in a loop; a 50-year one is never intended).
const (
	minCustomDays   = 7
	maxCustomDays   = 366
	maxCustomMonths = 36
)

// Metadata keys holding the active schedule anchor. A cadence change re-anchors the schedule at
// the next unbilled period without rewriting starts_at (which stays the original start date).
const (
	MetaScheduleAnchor      = "schedule_anchor"       // RFC3339 start of period index 0 of the current schedule
	MetaScheduleAnchorCycle = "schedule_anchor_cycle" // cycle_count when the schedule was anchored
)

// SupportInterval is the length of one billing period of a support agreement.
type SupportInterval struct {
	Count int
	Unit  supportagreement.IntervalUnit
}

// presetSupportMonths maps the named cycles to their length in months.
var presetSupportMonths = map[supportagreement.BillingCycle]int{
	supportagreement.BillingCycleMONTHLY:     1,
	supportagreement.BillingCycleQUARTERLY:   3,
	supportagreement.BillingCycleSEMI_ANNUAL: 6,
	supportagreement.BillingCycleANNUAL:      12,
}

// NormalizeSupportCycle canonicalizes a client-supplied cycle ("semi-annual", "Quarterly", ...).
func NormalizeSupportCycle(s string) (supportagreement.BillingCycle, error) {
	c := supportagreement.BillingCycle(strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(s), "-", "_")))
	if c == "" {
		return "", nil
	}
	if err := supportagreement.BillingCycleValidator(c); err != nil {
		return "", fmt.Errorf("invalid billing_cycle %q (want MONTHLY, QUARTERLY, SEMI_ANNUAL, ANNUAL or CUSTOM)", s)
	}
	return c, nil
}

// ResolveSupportInterval returns the period length for a cycle. Presets ignore count/unit; CUSTOM
// requires them (unit defaults to MONTH) and validates the bounds.
func ResolveSupportInterval(cycle supportagreement.BillingCycle, count int, unit string) (SupportInterval, error) {
	if months, ok := presetSupportMonths[cycle]; ok {
		return SupportInterval{Count: months, Unit: supportagreement.IntervalUnitMONTH}, nil
	}
	if cycle != supportagreement.BillingCycleCUSTOM {
		return SupportInterval{}, fmt.Errorf("unknown billing cycle %q", cycle)
	}
	u := supportagreement.IntervalUnit(strings.ToUpper(strings.TrimSpace(unit)))
	if u == "" {
		u = supportagreement.IntervalUnitMONTH
	}
	switch u {
	case supportagreement.IntervalUnitMONTH:
		if count < 1 || count > maxCustomMonths {
			return SupportInterval{}, fmt.Errorf("custom cycle must be 1 to %d months", maxCustomMonths)
		}
	case supportagreement.IntervalUnitDAY:
		if count < minCustomDays || count > maxCustomDays {
			return SupportInterval{}, fmt.Errorf("custom cycle must be %d to %d days", minCustomDays, maxCustomDays)
		}
	default:
		return SupportInterval{}, fmt.Errorf("invalid interval_unit %q (want MONTH or DAY)", unit)
	}
	return SupportInterval{Count: count, Unit: u}, nil
}

// addMonthsClamped adds n months to t, clamping the day to the target month's last day
// (Jan 31 + 1 month = Feb 28/29, not Mar 3).
func addMonthsClamped(t time.Time, n int) time.Time {
	y, m, d := t.Date()
	first := time.Date(y, m+time.Month(n), 1, t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), t.Location())
	last := first.AddDate(0, 1, -1).Day()
	if d > last {
		d = last
	}
	return time.Date(first.Year(), first.Month(), d, t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), t.Location())
}

// SchedulePeriodStart returns the start of period k (0-based) of a schedule anchored at anchor.
func SchedulePeriodStart(anchor time.Time, k int, iv SupportInterval) time.Time {
	if iv.Unit == supportagreement.IntervalUnitDAY {
		return anchor.AddDate(0, 0, k*iv.Count)
	}
	return addMonthsClamped(anchor, k*iv.Count)
}

// PeriodIndexAt returns the index of the period containing t (0 when t is before the anchor).
func PeriodIndexAt(anchor, t time.Time, iv SupportInterval) int {
	if !t.After(anchor) {
		return 0
	}
	var k int
	if iv.Unit == supportagreement.IntervalUnitDAY {
		k = int(t.Sub(anchor).Hours() / 24 / float64(iv.Count))
	} else {
		months := (t.Year()-anchor.Year())*12 + int(t.Month()-anchor.Month())
		k = months / iv.Count
	}
	// Correct the estimate so that start(k) <= t < start(k+1).
	for k > 0 && SchedulePeriodStart(anchor, k, iv).After(t) {
		k--
	}
	for !SchedulePeriodStart(anchor, k+1, iv).After(t) {
		k++
	}
	return k
}

// SupportDueDate is when a period's charge falls due.
func SupportDueDate(periodStart, periodEnd time.Time, timing supportagreement.BillingTiming) time.Time {
	if timing == supportagreement.BillingTimingADVANCE {
		return periodStart
	}
	return periodEnd
}

// SupportCycleCreateAt is when the enrollment job should create a period's cycle: at the period
// start (so the tenant sees the upcoming charge) or the invoice lead before its due date,
// whichever comes first.
func SupportCycleCreateAt(periodStart, due time.Time) time.Time {
	lead := due.AddDate(0, 0, -SupportFeeInvoiceLeadDays)
	if lead.Before(periodStart) {
		return lead
	}
	return periodStart
}

// SupportBackdatedDue protects a tenant from being blocked the moment a cycle is generated for a
// period whose due date already passed (an agreement created with a past start date, or a job
// that was down): such a cycle is due one invoice lead from now, so the tenant gets the normal
// notice plus the grace window.
func SupportBackdatedDue(due, now time.Time) time.Time {
	floor := now.AddDate(0, 0, SupportFeeInvoiceLeadDays)
	if due.Before(floor) && due.Before(now) {
		return floor
	}
	return due
}

// IntervalMonths is the period length in (fractional) months, used to prorate an annual price and
// to normalize any agreement to a monthly figure for MRR.
func (iv SupportInterval) IntervalMonths() float64 {
	if iv.Unit == supportagreement.IntervalUnitDAY {
		return float64(iv.Count) * 12 / 365
	}
	return float64(iv.Count)
}

// ProrateAnnualSupport returns the share of an annual support price for one period, rounded to
// whole cents.
func ProrateAnnualSupport(annual float64, iv SupportInterval) float64 {
	return roundMoney(annual * iv.IntervalMonths() / 12)
}

// MonthlyEquivalent normalizes a per-period charge to a monthly amount.
func MonthlyEquivalent(perPeriod float64, iv SupportInterval) float64 {
	m := iv.IntervalMonths()
	if m <= 0 {
		return 0
	}
	return perPeriod / m
}

func roundMoney(v float64) float64 {
	return math.Round(v*100) / 100
}
