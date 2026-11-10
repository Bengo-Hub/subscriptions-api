package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"
)

const testPlatformTenant = "4414b8d9-6b00-4ad1-a0b4-3094cbc5e398"

// The card-setup CRM call must file the subscriber under the platform tenant, not under
// the subscribing tenant, and must carry a first name.
func TestCardSetupUpsertsUnderPlatformTenant(t *testing.T) {
	var got map[string]any
	var gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("X-API-Key")
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"c0ffee00-0000-4000-8000-000000000001"}`))
	}))
	defer srv.Close()

	h := &BillingHandler{
		log:              zap.NewNop(),
		serviceKey:       "test-key",
		marketflowURL:    srv.URL,
		platformTenantID: testPlatformTenant,
	}

	id := h.upsertCRMContactByEmail(context.Background(), "jane.doe@example.com", "Jane Mary Doe")
	if id == "" {
		t.Fatal("expected a contact id from the upsert")
	}
	if got["tenant_id"] != testPlatformTenant {
		t.Errorf("tenant_id = %v, want the platform tenant %s", got["tenant_id"], testPlatformTenant)
	}
	if got["first_name"] != "Jane" || got["last_name"] != "Mary Doe" {
		t.Errorf("name = %v %v, want Jane / Mary Doe", got["first_name"], got["last_name"])
	}
	if got["email"] != "jane.doe@example.com" {
		t.Errorf("email = %v", got["email"])
	}
	if gotKey != "test-key" {
		t.Errorf("X-API-Key = %q, want the service key", gotKey)
	}
}

func TestSplitContactNameFallsBackToEmailLocalPart(t *testing.T) {
	first, last := splitContactName("", "owner@example.com")
	if first != "owner" || last != "" {
		t.Errorf("got %q %q, want owner and empty", first, last)
	}
	first, last = splitContactName("  Amina  ", "a@example.com")
	if first != "Amina" || last != "" {
		t.Errorf("got %q %q, want Amina and empty", first, last)
	}
}
