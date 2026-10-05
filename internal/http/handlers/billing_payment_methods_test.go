package handlers

import "testing"

// TestSavedPaymentMethods: only method objects count as saved payment methods. A bare string in
// payment_method (the standing-order registration once wrote "mpesa_standing_order") crashed the
// billing page when it reached the UI as a method.
func TestSavedPaymentMethods(t *testing.T) {
	card := map[string]any{"type": "card", "last4": "4242"}
	cases := []struct {
		name string
		meta map[string]any
		want int
	}{
		{"none", map[string]any{}, 0},
		{"legacy string", map[string]any{"payment_method": "mpesa_standing_order"}, 0},
		{"legacy object", map[string]any{"payment_method": card}, 1},
		{"array with a string", map[string]any{"payment_methods": []any{card, "mpesa_standing_order"}}, 1},
		{"array wins over legacy", map[string]any{"payment_methods": []any{card}, "payment_method": "x"}, 1},
	}
	for _, c := range cases {
		if got := len(savedPaymentMethods(c.meta)); got != c.want {
			t.Errorf("%s: got %d methods, want %d", c.name, got, c.want)
		}
	}
}
