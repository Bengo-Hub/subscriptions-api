package credits

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	serviceclient "github.com/Bengo-Hub/shared-service-client"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/bengobox/subscription-service/internal/modules/billing"
	"github.com/bengobox/subscription-service/internal/modules/subscriptions"
)

// FulfilIntent grants credits only for a paid credit_purchase intent that covered the offer's
// price. Anything else (another kind of payment, an unpaid intent, a short payment) grants nothing.
func TestFulfilIntentGrantsOnlyForAPaidCreditPurchase(t *testing.T) {
	f := newFixture(t, map[string]any{})
	intents := map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for id, body := range intents {
			if r.URL.Path == "/api/v1/"+f.tenantID.String()+"/payments/intents/"+id {
				_, _ = w.Write([]byte(body))
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	cfg := serviceclient.DefaultConfig(srv.URL, "treasury-api", zap.NewNop())
	cfg.MaxElapsedTime = 300 * time.Millisecond
	log := zap.NewNop()
	f.svc = New(log, f.orm, billing.NewTokenWalletService(log, f.orm, f.db), subscriptions.New(f.orm, log, serviceclient.New(cfg), "key", uuid.Nil))
	ctx := context.Background()

	meta := `"metadata":{"credit_service":"sms","purchase_kind":"bundle","purchase_code":"bundle_100"}`
	refused := map[string]string{
		"a tenant's own sale":  `{"reference_type":"pos_order","status":"succeeded","amount":"100",` + meta + `}`,
		"an unpaid intent":     `{"reference_type":"credit_purchase","status":"pending","amount":"100",` + meta + `}`,
		"a short payment":      `{"reference_type":"credit_purchase","status":"succeeded","amount":"1",` + meta + `}`,
		"a cancelled purchase": `{"reference_type":"credit_purchase","status":"cancelled","amount":"100",` + meta + `}`,
	}
	for name, body := range refused {
		id := uuid.New()
		intents[id.String()] = body
		if err := f.svc.FulfilIntent(ctx, f.tenantID, id); err == nil {
			t.Errorf("%s must not grant credits", name)
		}
	}
	if got := f.bal(t, "sms"); got != 0 {
		t.Fatalf("refused intents granted %d SMS", got)
	}

	paid := uuid.New()
	intents[paid.String()] = `{"reference_type":"credit_purchase","status":"succeeded","amount":"100",` + meta + `}`
	for i := 0; i < 2; i++ { // a redelivery grants nothing twice
		if err := f.svc.FulfilIntent(ctx, f.tenantID, paid); err != nil {
			t.Fatalf("paid purchase: %v", err)
		}
	}
	if got := f.bal(t, "sms"); got != 100 {
		t.Fatalf("paid bundle_100 granted %d SMS, want 100", got)
	}
}
