package credits

import (
	"errors"
	"testing"
)

func TestParseWhatsAppUsageRange(t *testing.T) {
	r, err := ParseWhatsAppUsageRange("2026-11-01", "2026-11-30")
	if err != nil {
		t.Fatal(err)
	}
	if r.From.Format("2006-01-02") != "2026-11-01" || r.To.Format("2006-01-02") != "2026-11-30" {
		t.Fatalf("range %v", r)
	}
	for _, c := range [][2]string{{"", "2026-11-30"}, {"2026-11-01", "nope"}, {"2026-11-30", "2026-11-01"}, {"2024-01-01", "2026-11-01"}} {
		if _, err := ParseWhatsAppUsageRange(c[0], c[1]); !errors.Is(err, ErrInvalidAdminQuery) {
			t.Errorf("%v: want ErrInvalidAdminQuery, got %v", c, err)
		}
	}
}

func TestWhatsAppConsumedValue(t *testing.T) {
	// 10 credits spent, 2 of them overage charged KES 3 at KES 1.5 each: 8 bundle credits at 1.5.
	got := whatsAppConsumedValue(WhatsAppUsageCategory{UnitsConsumed: 10, OverageKES: 3, UnitPriceKES: 1.5})
	if got != 15 {
		t.Fatalf("consumed value %v, want 15", got)
	}
	// The legacy allowance has no unit price: only the overage counts.
	if got := whatsAppConsumedValue(WhatsAppUsageCategory{UnitsConsumed: 40}); got != 0 {
		t.Fatalf("legacy allowance value %v, want 0", got)
	}
}
