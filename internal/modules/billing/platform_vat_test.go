package billing

import "testing"

// VAT goes on a credit bundle only when the platform's treasury profile has VAT active and eTIMS
// activated; anything else, including a body that cannot be read, means no VAT.
func TestPlatformChargesVAT(t *testing.T) {
	cases := map[string]bool{
		`{"vat_active":true,"etims_activated":true}`:  true,
		`{"vat_active":true,"etims_activated":false}`: false,
		`{"vat_active":false,"etims_activated":true}`: false,
		`{}`:     false,
		`broken`: false,
	}
	for body, want := range cases {
		if got := platformChargesVAT([]byte(body)); got != want {
			t.Errorf("%s: got %v want %v", body, got, want)
		}
	}
}
