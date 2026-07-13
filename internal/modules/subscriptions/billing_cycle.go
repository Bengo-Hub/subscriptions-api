package subscriptions

import (
	"fmt"
	"strings"
	"time"

	"github.com/bengobox/subscription-service/internal/ent/tenantsubscription"
)

// Billing-period model (single source of truth — reuse these helpers everywhere a billing
// cycle is interpreted: create/renew/initiate, invoicing, coupons, UI-facing results).
//
// Every recurring plan is priced per month (subscription_plans.base_price) and offered at
// three billing periods chosen by the tenant at subscribe/renew time:
//   MONTHLY (1 mo) · SEMI_ANNUAL (6 mo) · ANNUAL (12 mo)   [QUARTERLY kept for legacy subs]
// The period price is exactly months × base_price — no automatic discount. The ONLY
// incentive is the one-time setup-fee waiver below, and no other special discount may be
// applied to a subscription whose waiver is active (coupons are rejected for those).

// SetupFeeWaiverMonths is the minimum billing-period length (in months) that waives the
// one-time setup/installation fee. Paying for 6 or 12 months up front waives it entirely.
const SetupFeeWaiverMonths = 6

// billingCycleMonths maps each recurring cycle to its period length in months.
// ONE_TIME is a perpetual licence (no recurring period) and maps to 0.
var billingCycleMonths = map[tenantsubscription.BillingCycle]int{
	tenantsubscription.BillingCycleMONTHLY:     1,
	tenantsubscription.BillingCycleQUARTERLY:   3,
	tenantsubscription.BillingCycleSEMI_ANNUAL: 6,
	tenantsubscription.BillingCycleANNUAL:      12,
	tenantsubscription.BillingCycleONE_TIME:    0,
}

// NormalizeBillingCycle canonicalizes a client-supplied billing cycle string
// (case-insensitive, accepts "SEMI-ANNUAL"/"semi_annual" etc.). Empty input returns
// empty with no error so callers can fall back to a default.
func NormalizeBillingCycle(s string) (tenantsubscription.BillingCycle, error) {
	if strings.TrimSpace(s) == "" {
		return "", nil
	}
	c := tenantsubscription.BillingCycle(strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(s), "-", "_")))
	if _, ok := billingCycleMonths[c]; !ok {
		return "", fmt.Errorf("invalid billing_cycle %q (want MONTHLY, QUARTERLY, SEMI_ANNUAL or ANNUAL)", s)
	}
	return c, nil
}

// BillingCycleMonths returns the period length in months for a cycle (0 for ONE_TIME or
// unknown values — callers treat 0 as "no recurring period").
func BillingCycleMonths(cycle string) int {
	return billingCycleMonths[tenantsubscription.BillingCycle(cycle)]
}

// CycleWaivesSetupFee reports whether the chosen billing period is long enough
// (>= SetupFeeWaiverMonths) to waive the one-time setup/installation fee.
func CycleWaivesSetupFee(cycle string) bool {
	return BillingCycleMonths(cycle) >= SetupFeeWaiverMonths
}

// AddBillingCycle advances t by one billing period of the given cycle.
// ONE_TIME/unknown cycles advance by one month (legacy behaviour for the stored period).
func AddBillingCycle(t time.Time, cycle string) time.Time {
	months := BillingCycleMonths(cycle)
	if months <= 0 {
		months = 1
	}
	return t.AddDate(0, months, 0)
}

// PerpetualYears is how far out a ONE_TIME (perpetual) licence's current_period_end is placed so
// it never appears "expiring" and never trips a period-window check. Matches the exempt-tenant
// sentinel (exemption.go). The renewal/invoicing jobs additionally exclude ONE_TIME via
// notPerpetual(), so this is belt-and-suspenders — but storing a real far-future date keeps the
// stored row self-consistent instead of showing a finite ~1-month expiry on a perpetual licence.
const PerpetualYears = 100

// ResolvePeriodEnd returns the current_period_end for a freshly (re)started period: a far-future
// perpetual date for a ONE_TIME licence, otherwise `now` advanced by one billing period. Use this
// everywhere a subscription period is (re)started so a ONE_TIME plan never gets a finite,
// soon-to-expire period.
func ResolvePeriodEnd(now time.Time, cycle string) time.Time {
	if tenantsubscription.BillingCycle(cycle) == tenantsubscription.BillingCycleONE_TIME {
		return now.AddDate(PerpetualYears, 0, 0)
	}
	return AddBillingCycle(now, cycle)
}

// ResolveAssignedCycle picks the billing cycle to store on a subscription when a plan is assigned:
// the admin's explicit request wins; otherwise inherit the PLAN's own cycle (so a ONE_TIME plan is
// never silently stored as MONTHLY); falling back to MONTHLY only when neither is set/valid.
func ResolveAssignedCycle(requested, planCycle string) tenantsubscription.BillingCycle {
	if c, err := NormalizeBillingCycle(requested); err == nil && c != "" {
		return c
	}
	if c, err := NormalizeBillingCycle(planCycle); err == nil && c != "" {
		return c
	}
	return tenantsubscription.BillingCycleMONTHLY
}

// Metadata keys for the setup-fee waiver audit trail and for the intent-scoped pending
// billing-period choice recorded at checkout (consumed by RenewSubscription when the
// matching payment.succeeded event arrives).
const (
	MetaSetupFeeWaived       = "setup_fee_waived"        // bool — waiver applied
	MetaSetupFeeWaivedAmount = "setup_fee_waived_amount" // float64 — the fee that was waived
	MetaSetupFeeWaiverReason = "setup_fee_waiver_reason" // string

	MetaPendingBillingCycle = "pending_billing_cycle" // string — cycle chosen at checkout
	MetaPendingPlanCode     = "pending_plan_code"     // string — plan chosen at checkout
	MetaPendingIntentID     = "pending_intent_id"     // string — treasury intent the choice is bound to
	MetaPendingSetupFee     = "pending_setup_fee_included" // bool — checkout amount included the setup fee
)

// SetupFeeWaiverReason is the human-readable audit reason stamped when the waiver applies.
func SetupFeeWaiverReason(cycle string) string {
	return fmt.Sprintf("waived: %d-month billing period (>= %d months)", BillingCycleMonths(cycle), SetupFeeWaiverMonths)
}
