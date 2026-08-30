package billing

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// UsageCounterKey is the SINGLE canonical Redis key format for a tenant's live usage
// counter, shared by every writer (the HTTP /usage/report handler and the NATS usage
// consumer both increment the same key for the same metric — they must never compute it
// independently, or the two enforcement paths silently drift apart again, which is exactly
// what happened before: both used to key off time.Now()'s calendar month regardless of the
// tenant's real subscription period, disagreeing with the usage-dashboard aggregate that
// has always windowed off current_period_end).
//
// Metered throughput metrics (orders, transactions, api_calls, …) are keyed off the
// tenant's OWN subscription period (current_period_start) so the counter resets exactly
// when the tenant's billing period rolls over, whatever day of the month that is.
//
// Structural counts (devices, tables, cashiers, outlets, warehouses, SKUs, …) get a flat,
// period-independent key — they track how many of a resource currently exist, not a
// throughput rate, and must never reset on a timer.
func UsageCounterKey(tenantID uuid.UUID, metricType string, currentPeriodStart time.Time) string {
	if IsOverageEligibleMetric(metricType) {
		return fmt.Sprintf("usage:limit:%s:%s:%s", tenantID.String(), metricType, currentPeriodStart.UTC().Format("2006-01-02"))
	}
	return fmt.Sprintf("usage:limit:%s:%s:current", tenantID.String(), metricType)
}
