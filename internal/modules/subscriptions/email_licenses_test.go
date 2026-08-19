package subscriptions

import "testing"

func TestParsePlanCodeAndQuantity(t *testing.T) {
	cases := []struct {
		name         string
		metadata     map[string]any
		wantPlanCode string
		wantQuantity int
		wantOK       bool
	}{
		{
			name:         "typical decoded-JSON shape (quantity as float64)",
			metadata:     map[string]any{"plan_code": "EMAIL_STANDARD", "quantity": float64(5)},
			wantPlanCode: "EMAIL_STANDARD",
			wantQuantity: 5,
			wantOK:       true,
		},
		{
			name:         "already-typed int quantity",
			metadata:     map[string]any{"plan_code": "EMAIL_LITE", "quantity": 3},
			wantPlanCode: "EMAIL_LITE",
			wantQuantity: 3,
			wantOK:       true,
		},
		{
			name:     "missing plan_code",
			metadata: map[string]any{"quantity": float64(2)},
			wantOK:   false,
		},
		{
			name:     "missing quantity",
			metadata: map[string]any{"plan_code": "EMAIL_LITE"},
			wantOK:   false,
		},
		{
			name:     "zero quantity is invalid",
			metadata: map[string]any{"plan_code": "EMAIL_LITE", "quantity": float64(0)},
			wantOK:   false,
		},
		{
			name:     "negative quantity is invalid",
			metadata: map[string]any{"plan_code": "EMAIL_LITE", "quantity": float64(-1)},
			wantOK:   false,
		},
		{
			name:     "nil metadata",
			metadata: nil,
			wantOK:   false,
		},
		{
			name:     "wrong type for plan_code is treated as absent",
			metadata: map[string]any{"plan_code": 123, "quantity": float64(2)},
			wantOK:   false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			planCode, quantity, ok := parsePlanCodeAndQuantity(c.metadata)
			if ok != c.wantOK {
				t.Fatalf("ok = %v, want %v", ok, c.wantOK)
			}
			if !c.wantOK {
				return
			}
			if planCode != c.wantPlanCode || quantity != c.wantQuantity {
				t.Errorf("got (%q, %d), want (%q, %d)", planCode, quantity, c.wantPlanCode, c.wantQuantity)
			}
		})
	}
}
