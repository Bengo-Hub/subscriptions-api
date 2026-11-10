package consumers

import (
	"encoding/json"
	"testing"
)

// The renewal confirmation is also the payment confirmation, so the payment that renewed the
// subscription is read from either payload shape treasury publishes.
func TestPaymentOfReadsBothShapes(t *testing.T) {
	var nested treasuryPaymentEvent
	if err := json.Unmarshal([]byte(`{"payload":{"intent_id":"i1","amount":"2500.00","currency":"KES","payment_method":"mpesa","provider_reference":"TKA1B2C3D4"}}`), &nested); err != nil {
		t.Fatal(err)
	}
	p := paymentOf(&nested)
	if p == nil || p.Amount != "2500.00" || p.Currency != "KES" || p.Method != "mpesa" || p.Reference != "TKA1B2C3D4" {
		t.Fatalf("nested: %+v", p)
	}

	var flat treasuryPaymentEvent
	if err := json.Unmarshal([]byte(`{"amount":1500,"currency":"KES","provider_reference":"TKZ9"}`), &flat); err != nil {
		t.Fatal(err)
	}
	if p := paymentOf(&flat); p == nil || p.Amount != "1500" || p.Reference != "TKZ9" {
		t.Fatalf("flat: %+v", p)
	}

	var none treasuryPaymentEvent
	if p := paymentOf(&none); p != nil {
		t.Fatalf("no payment details means no payment: %+v", p)
	}
}
