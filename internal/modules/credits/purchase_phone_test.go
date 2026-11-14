package credits

import (
	"testing"

	"github.com/google/uuid"

	"github.com/bengobox/subscription-service/internal/modules/billing"
)

func TestNormalizeKenyanPhone(t *testing.T) {
	ok := map[string]string{
		"+254712345678": "254712345678",
		"254712345678":  "254712345678",
		"0712345678":    "254712345678",
		"712345678":     "254712345678",
		"0112 345-678":  "254112345678",
	}
	for in, want := range ok {
		if got, good := NormalizeKenyanPhone(in); !good || got != want {
			t.Errorf("%q = %q %v, want %q", in, got, good, want)
		}
	}
	for _, bad := range []string{"", "+1 415 555 0100", "254212345678", "07123", "0712345678;rm", "2547123456789"} {
		if got, good := NormalizeKenyanPhone(bad); good {
			t.Errorf("%q accepted as %q", bad, got)
		}
	}
}

// A purchase with a phone is an M-Pesa prompt to that phone; without one it stays the pending
// intent the pay page completes. Both are credit_purchase (platform-billed) and carry the source.
func TestPurchaseIntentRequest(t *testing.T) {
	tid := uuid.New()
	offer, err := ResolveOffer(billing.ServiceAI, KindBundle, "bundle_60")
	if err != nil {
		t.Fatal(err)
	}
	meta := offer.IntentMetadata(tid)
	pending := purchaseIntentRequest(PurchaseInput{TenantID: tid, Service: billing.ServiceAI, Kind: KindBundle, Code: "bundle_60"}, offer, 750, meta)
	if pending["payment_method"] != "pending" || pending["phone_number"] != nil || pending["reference_type"] != RefTypeCreditPurchase {
		t.Fatalf("pending intent: %+v", pending)
	}
	stk := purchaseIntentRequest(PurchaseInput{TenantID: tid, Service: billing.ServiceAI, Kind: KindBundle, Code: "bundle_60",
		Phone: "254712345678", Source: "marketflow_whatsapp", SourceRef: "ref-1"}, offer, 870, meta)
	if stk["payment_method"] != PromptMethod || stk["phone_number"] != "254712345678" || stk["amount"] != 870.0 {
		t.Fatalf("prompt intent: %+v", stk)
	}
	if stk["reference_type"] != RefTypeCreditPurchase {
		t.Fatalf("a prompt purchase must stay platform-billed: %v", stk["reference_type"])
	}
}
