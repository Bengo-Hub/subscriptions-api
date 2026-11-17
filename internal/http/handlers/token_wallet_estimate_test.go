package handlers

import "testing"

func TestCheapestPlan(t *testing.T) {
	// Current eTIMS API catalog: Basic 2,500 + 5,000 tokens at 0.60, Growth 4,000 + 20,000 at
	// 0.45, Scale 8,000 + 100,000 at 0.30.
	build := func(tokens int64) []planComparison {
		defs := []struct {
			code     string
			base     float64
			included int64
			price    float64
		}{
			{"ETIMS_API_BASIC", 2500, 5000, 0.60},
			{"ETIMS_API_GROWTH", 4000, 20000, 0.45},
			{"ETIMS_API_SCALE", 8000, 100000, 0.30},
		}
		out := make([]planComparison, 0, len(defs))
		for _, d := range defs {
			covers := d.included >= tokens
			topup := 0.0
			if !covers {
				topup = float64(tokens-d.included) * d.price
			}
			out = append(out, planComparison{
				PlanCode: d.code, MonthlyPriceKes: d.base, IncludedTokens: d.included,
				TokenPriceKes: d.price, CoversEstimate: covers,
				ProjectedTopUpKes: topup, ProjectedTotalKes: d.base + topup,
			})
		}
		return out
	}

	cases := []struct {
		name   string
		tokens int64
		want   string
	}{
		{"small volume fits Basic", 4000, "ETIMS_API_BASIC"},
		// 600 sales: Basic 2,500 + 1,000 x 0.60 = 3,100, cheaper than Growth's 4,000.
		{"just over Basic stays on Basic", 6000, "ETIMS_API_BASIC"},
		// 1,000 sales: Basic 2,500 + 5,000 x 0.60 = 5,500, Growth 4,000.
		{"mid volume moves to Growth", 10000, "ETIMS_API_GROWTH"},
		// 20,000 sales: Growth 4,000 + 180,000 x 0.45 = 85,000, Scale 8,000 + 100,000 x 0.30 = 38,000.
		{"beyond Scale allowance still Scale", 200000, "ETIMS_API_SCALE"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := cheapestPlan(build(tc.tokens)); got != tc.want {
				t.Fatalf("cheapestPlan(%d tokens) = %s, want %s", tc.tokens, got, tc.want)
			}
		})
	}
}

func TestCheapestPlanSkipsUnpricedShortfall(t *testing.T) {
	plans := []planComparison{
		{PlanCode: "NO_TOPUP", MonthlyPriceKes: 100, CoversEstimate: false, ProjectedTotalKes: 100},
		{PlanCode: "COVERS", MonthlyPriceKes: 500, CoversEstimate: true, ProjectedTotalKes: 500},
	}
	if got := cheapestPlan(plans); got != "COVERS" {
		t.Fatalf("got %s, want COVERS", got)
	}
	if got := cheapestPlan(nil); got != "" {
		t.Fatalf("empty catalog should recommend nothing, got %s", got)
	}
}
