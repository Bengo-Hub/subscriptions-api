package billing

import (
	"testing"
	"time"

	"github.com/bengobox/subscription-service/internal/ent"
)

// The dunning clock must survive the job's own metadata writes, retry on days 1, 3 and 7 only,
// cancel after day 7, and start over for a later suspension.
func TestDunningRetriesOnScheduleAndCancelsAfterLastDay(t *testing.T) {
	start := time.Date(2026, 11, 1, 6, 0, 0, 0, time.UTC)
	periodEnd := time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC)
	sub := &ent.TenantSubscription{CurrentPeriodEnd: periodEnd, Metadata: map[string]any{}}

	st, known := readDunningState(sub, start)
	if known || st.Attempts != 0 {
		t.Fatalf("first sight should start a fresh state, got %+v known=%v", st, known)
	}

	retries := []int{}
	for day := 0; day <= 9; day++ {
		now := start.AddDate(0, 0, day)
		st, _ = readDunningState(sub, now)
		retry, cancel := nextDunningStep(st, now)
		if cancel {
			if day != 8 {
				t.Fatalf("cancelled on day %d, want day 8", day)
			}
			break
		}
		if retry {
			st.Attempts++
			retries = append(retries, day)
		}
		// What processDunning saves; updated_at moving does not matter any more.
		sub.Metadata = map[string]any{metaDunning: map[string]any{
			"started_at": st.StartedAt.Format(time.RFC3339), "period_end": st.PeriodEnd, "attempts": float64(st.Attempts),
		}}
	}
	if len(retries) != 3 || retries[0] != 1 || retries[1] != 3 || retries[2] != 7 {
		t.Fatalf("retries ran on days %v, want [1 3 7]", retries)
	}

	sub.CurrentPeriodEnd = periodEnd.AddDate(0, 1, 0)
	if st, known := readDunningState(sub, start.AddDate(0, 2, 0)); known || st.Attempts != 0 {
		t.Fatalf("a later suspension should start over, got %+v known=%v", st, known)
	}
}
