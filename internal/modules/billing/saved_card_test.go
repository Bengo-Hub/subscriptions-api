package billing

import (
	"context"
	"testing"

	"go.uber.org/zap"

	"github.com/bengobox/subscription-service/internal/ent"
)

// Without a saved card nothing is generated or charged: the emailed pay link stays the way to pay.
func TestChargeSavedCard_NoCardDoesNothing(t *testing.T) {
	s := &InvoiceService{log: zap.NewNop()}
	res, err := s.ChargeSavedCard(context.Background(), &ent.TenantSubscription{Metadata: map[string]any{"last_invoice_id": "x"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Attempted || res.Reason != "no saved card" {
		t.Errorf("res = %+v", res)
	}
}
