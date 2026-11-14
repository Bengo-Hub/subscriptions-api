package handlers

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"
)

func s2sPurchaseRequest(body, key string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/api/v1/s2s/credits/ai_credits/purchase", bytes.NewBufferString(body))
	if key != "" {
		r.Header.Set("X-API-Key", key)
	}
	rc := chi.NewRouteContext()
	rc.URLParams.Add("service", "ai_credits")
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rc))
}

// The prompt purchase is for services only, and refuses a request it cannot price or prompt
// before it touches the credits service.
func TestS2SPurchaseRefusals(t *testing.T) {
	h := NewCreditsHandler(zap.NewNop(), nil, "svc-key")

	w := httptest.NewRecorder()
	h.S2SPurchase(w, s2sPurchaseRequest(`{}`, ""))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("no key: %d", w.Code)
	}
	for _, body := range []string{
		`{"code":"bundle_60","phone":"0712345678","source":"marketflow_whatsapp"}`,                                      // no tenant
		`{"tenant_id":"6f1c2a9e-1b2c-4d5e-8f90-a1b2c3d4e5f6","phone":"0712345678","source":"marketflow_whatsapp"}`,       // no code
		`{"tenant_id":"6f1c2a9e-1b2c-4d5e-8f90-a1b2c3d4e5f6","code":"bundle_60","source":"marketflow_whatsapp"}`,         // no phone
		`{"tenant_id":"6f1c2a9e-1b2c-4d5e-8f90-a1b2c3d4e5f6","code":"bundle_60","phone":"+14155550100","source":"x"}`,     // not Kenyan
		`{"tenant_id":"6f1c2a9e-1b2c-4d5e-8f90-a1b2c3d4e5f6","code":"bundle_60","phone":"0712345678"}`,                   // no source
		`not json`,
	} {
		w := httptest.NewRecorder()
		h.S2SPurchase(w, s2sPurchaseRequest(body, "svc-key"))
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", body, w.Code)
		}
	}
}
