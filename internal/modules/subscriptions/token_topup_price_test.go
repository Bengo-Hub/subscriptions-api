package subscriptions

import "testing"

func TestTokenPriceFromLimits(t *testing.T) {
	cases := []struct {
		name   string
		limits map[string]any
		want   float64
		ok     bool
	}{
		{"float from JSON", map[string]any{"token_price_kes": 0.45}, 0.45, true},
		{"int value", map[string]any{"token_price_kes": 1}, 1, true},
		{"zero is not a price", map[string]any{"token_price_kes": 0.0}, 0, false},
		{"missing key", map[string]any{"included_tokens": 5000}, 0, false},
		{"wrong type", map[string]any{"token_price_kes": "0.6"}, 0, false},
		{"nil map", nil, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := tokenPriceFromLimits(tc.limits)
			if ok != tc.ok || got != tc.want {
				t.Fatalf("tokenPriceFromLimits(%v) = (%v, %v), want (%v, %v)", tc.limits, got, ok, tc.want, tc.ok)
			}
		})
	}
}
