package credits

import (
	"encoding/json"
	"testing"
)

// The check answer is a contract with marketflow-ai (handlers.HTTPCreditSpender.Balance): the
// balances travel as bundle_balance and quota_balance next to allowed and reason.
func TestDecisionJSONCarriesBalances(t *testing.T) {
	d := Decision{Allowed: true}
	d.BundleBalance, d.QuotaBalance = 60, 5
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got["allowed"] != true || got["bundle_balance"] != float64(60) || got["quota_balance"] != float64(5) {
		t.Fatalf("check answer = %s", raw)
	}
}
