package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5/middleware"
)

// The audit IP comes from X-Real-IP (overwritten by ingress-nginx), never from client-supplied
// True-Client-IP or X-Forwarded-For.
func TestRequestIPIgnoresSpoofableHeaders(t *testing.T) {
	var got string
	h := middleware.ClientIPFromHeader("X-Real-IP")(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = requestIP(r)
	}))

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.RemoteAddr = "10.42.0.7:51234"
	req.Header.Set("X-Real-IP", "41.90.1.2")
	req.Header.Set("True-Client-IP", "1.1.1.1")
	req.Header.Set("X-Forwarded-For", "8.8.8.8, 41.90.1.2")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if got != "41.90.1.2" {
		t.Fatalf("requestIP = %q, want 41.90.1.2", got)
	}

	req = httptest.NewRequest(http.MethodPost, "/", nil)
	req.RemoteAddr = "10.42.0.7:51234"
	req.Header.Set("True-Client-IP", "1.1.1.1")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if got != "10.42.0.7" {
		t.Fatalf("requestIP without X-Real-IP = %q, want the remote address", got)
	}
}
