package billing

import "testing"

func TestTokenCostForEndpoint(t *testing.T) {
	cases := []struct {
		name       string
		serviceTag string
		endpoint   string
		wantCost   int64
		wantMatch  bool
	}{
		{"sales transmission is the expensive tier", "etims_api", "/api/v1/external/etims/sales", ApiTokenCostTransmit, true},
		{"credit note is the expensive tier", "etims_api", "/api/v1/external/etims/credit-note", ApiTokenCostTransmit, true},
		{"stock-io is the expensive tier", "etims_api", "/api/v1/external/etims/stock-io", ApiTokenCostTransmit, true},
		{"device register is a write", "etims_api", "/api/v1/external/etims/devices", ApiTokenCostWrite, true},
		{"device init is a write", "etims_api", "/api/v1/external/etims/devices/abc-123/init", ApiTokenCostWrite, true},
		{"item register is a write", "etims_api", "/api/v1/external/etims/items", ApiTokenCostWrite, true},
		{"code-list lookup is cheap", "etims_api", "/api/v1/external/etims/code-lists", ApiTokenCostLookup, true},
		{"code-list refresh is a write", "etims_api", "/api/v1/external/etims/code-lists/refresh", ApiTokenCostWrite, true},
		{"sales-transactions lookup must NOT match the /sales transmit rule", "etims_api", "/api/v1/external/etims/sales-transactions", ApiTokenCostLookup, true},
		{"certification status is free", "etims_api", "/api/v1/external/etims/certification-status", 0, true},
		{"sandbox endpoints are free", "etims_api", "/api/v1/external/etims/sandbox/sales", 0, true},
		{"token wallet endpoints are free", "etims_api", "/api/v1/external/etims/tokens/balance", 0, true},
		{"unknown endpoint defaults to write and reports unmatched", "etims_api", "/api/v1/external/etims/frobnicate", ApiTokenCostWrite, false},
		{"unknown service_tag never matches", "notifications_api", "/api/v1/external/etims/sales", ApiTokenCostWrite, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotCost, gotMatch := TokenCostForEndpoint(c.serviceTag, c.endpoint)
			if gotCost != c.wantCost {
				t.Errorf("cost = %d, want %d", gotCost, c.wantCost)
			}
			if gotMatch != c.wantMatch {
				t.Errorf("matched = %v, want %v", gotMatch, c.wantMatch)
			}
		})
	}
}

// TestApiTokenCostTransmit200OrdersPerDayWorkedExample verifies the exact capacity-planning
// figure a tenant doing ~200 sales/day would need per month, matching the estimate endpoint's
// AvgSalesPerDay shortcut math (see handlers/token_wallet.go's Estimate).
func TestApiTokenCostTransmit200OrdersPerDayWorkedExample(t *testing.T) {
	const ordersPerDay = 200
	const daysPerMonth = 30
	got := ApiTokenCostTransmit * int64(ordersPerDay) * int64(daysPerMonth)
	want := int64(10 * 200 * 30) // 60,000 tokens/month
	if got != want {
		t.Fatalf("tokens/month = %d, want %d", got, want)
	}
	// 60,000 tokens comfortably exceeds even ETIMS_API_GROWTH's 20,000 included_tokens grant —
	// this tenant needs ETIMS_API_SCALE (100,000 included) or a top-up on Growth.
	const growthIncluded = 20000
	const scaleIncluded = 100000
	if got <= growthIncluded {
		t.Fatalf("expected 200 orders/day to exceed Growth's included_tokens=%d, got %d", growthIncluded, got)
	}
	if got > scaleIncluded {
		t.Fatalf("expected 200 orders/day to fit within Scale's included_tokens=%d, got %d", scaleIncluded, got)
	}
}
