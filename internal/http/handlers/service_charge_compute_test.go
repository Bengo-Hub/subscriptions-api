package handlers

import (
	"testing"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/servicechargeplan"
)

func f64(v float64) *float64 { return &v }

func TestComputeCharge_Percentage_WithMinMaxClamp(t *testing.T) {
	// 1.5% of gross, min KES 2, max KES 250 (the Flex/PAYG model).
	plan := &ent.ServiceChargePlan{
		ChargeType:  servicechargeplan.ChargeTypePERCENTAGE,
		ChargeValue: 1.5,
		MinCharge:   f64(2),
		MaxCharge:   f64(250),
	}
	cases := []struct {
		gross, want float64
	}{
		{gross: 10000, want: 150},  // 1.5% within band
		{gross: 50, want: 2},       // 0.75 → clamped up to min 2
		{gross: 1000000, want: 250}, // 15000 → clamped down to max 250
	}
	for _, c := range cases {
		got, pct := computeCharge(plan, c.gross)
		if got != c.want {
			t.Errorf("gross %.0f: want charge %.2f, got %.2f", c.gross, c.want, got)
		}
		if pct != 1.5 {
			t.Errorf("gross %.0f: want pct 1.5, got %.2f", c.gross, pct)
		}
	}
}

func TestComputeCharge_FixedPerTransaction(t *testing.T) {
	plan := &ent.ServiceChargePlan{
		ChargeType:  servicechargeplan.ChargeTypeFIXED_PER_TRANSACTION,
		ChargeValue: 5,
	}
	got, pct := computeCharge(plan, 9999)
	if got != 5 || pct != 0 {
		t.Fatalf("fixed: want (5,0), got (%.2f,%.2f)", got, pct)
	}
}

func TestServiceApplies(t *testing.T) {
	if !serviceApplies(nil, "pos") {
		t.Error("empty applicable list must mean all services")
	}
	if !serviceApplies([]string{"POS", "ordering"}, "pos") {
		t.Error("case-insensitive match failed")
	}
	if serviceApplies([]string{"pos", "ordering"}, "isp") {
		t.Error("isp must NOT match a pos/ordering-only plan (ISP billed monthly, not per-txn)")
	}
}
