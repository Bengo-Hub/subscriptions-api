package handlers

import (
	"encoding/json"
	"net/http"
)

// writeJSON writes the provided payload as a JSON response with the given status code.
// All handlers in this package should use this helper for consistency.
func writeJSON(w http.ResponseWriter, code int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(payload)
}
