package handlers

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/bengobox/subscription-service/internal/modules/billing"
	"github.com/bengobox/subscription-service/internal/modules/credits"
)

// CreditsHandler exposes the shared credits engine: S2S spend and refund for services that send
// or meter (notifications-api, marketflow-ai), and tenant-facing balance, history and purchase.
type CreditsHandler struct {
	log         *zap.Logger
	svc         *credits.Service
	internalKey string
}

// NewCreditsHandler creates the handler. internalKey is INTERNAL_SERVICE_KEY.
func NewCreditsHandler(log *zap.Logger, svc *credits.Service, internalKey string) *CreditsHandler {
	return &CreditsHandler{log: log.Named("credits.handler"), svc: svc, internalKey: internalKey}
}

func (h *CreditsHandler) isService(r *http.Request) bool {
	got := r.Header.Get("X-API-Key")
	return h.internalKey != "" && got != "" && subtle.ConstantTimeCompare([]byte(got), []byte(h.internalKey)) == 1
}

type creditSpendRequest struct {
	TenantID      string `json:"tenant_id"`
	Units         int64  `json:"units"`
	Key           string `json:"key"`
	Description   string `json:"description"`
	PlatformScope bool   `json:"platform_scope"`
}

// Consume handles POST /s2s/credits/{service}/consume.
// It answers 200 with allowed=false and a reason when the spend is refused, so callers can tell a
// refusal from a failure (5xx) and never send free on a failure.
func (h *CreditsHandler) Consume(w http.ResponseWriter, r *http.Request) {
	if !h.isService(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "service key required"})
		return
	}
	var req creditSpendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	tenantID, err := uuid.Parse(req.TenantID)
	if err != nil || req.Units < 0 || req.Key == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "tenant_id, key and a non-negative units value are required"})
		return
	}
	dec, err := h.svc.Consume(r.Context(), credits.ConsumeInput{
		TenantID: tenantID, Service: chi.URLParam(r, "service"), Units: req.Units, Key: req.Key,
		Description: req.Description, PlatformScope: req.PlatformScope,
	})
	if err != nil {
		h.log.Error("credits consume failed", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "credit check failed"})
		return
	}
	writeJSON(w, http.StatusOK, dec)
}

type creditRefundRequest struct {
	TenantID   string `json:"tenant_id"`
	ConsumeKey string `json:"consume_key"`
	RefundKey  string `json:"refund_key"`
}

// Refund handles POST /s2s/credits/{service}/refund.
func (h *CreditsHandler) Refund(w http.ResponseWriter, r *http.Request) {
	if !h.isService(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "service key required"})
		return
	}
	var req creditRefundRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	tenantID, err := uuid.Parse(req.TenantID)
	if err != nil || req.ConsumeKey == "" || req.RefundKey == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "tenant_id, consume_key and refund_key are required"})
		return
	}
	res, err := h.svc.Refund(r.Context(), tenantID, chi.URLParam(r, "service"), req.ConsumeKey, req.RefundKey)
	if err != nil {
		h.log.Error("credits refund failed", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "refund failed"})
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (h *CreditsHandler) tenant(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(resolveTenantID(r))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "tenant_id required"})
		return uuid.Nil, false
	}
	return id, true
}

// List handles GET /credits: the caller's state for every credit service.
func (h *CreditsHandler) List(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := h.tenant(w, r)
	if !ok {
		return
	}
	snaps, err := h.svc.Snapshots(r.Context(), tenantID)
	if err != nil {
		h.log.Error("credits snapshot failed", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load credits"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"services": snaps})
}

// Transactions handles GET /credits/{service}/transactions (bundle wallet history).
func (h *CreditsHandler) Transactions(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := h.tenant(w, r)
	if !ok {
		return
	}
	spec, found := billing.LookupMeteredService(chi.URLParam(r, "service"))
	if !found || (spec.ProductCode == "" && spec.PlanQuotaKey == "") {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown service"})
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	entries, total, err := h.svc.Wallets().ListTransactions(r.Context(), tenantID, spec.BundleTag, limit, offset)
	if err != nil {
		h.log.Error("credits transactions failed", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load history"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": entries, "total": total, "limit": limit, "offset": offset})
}

type creditPurchaseRequest struct {
	Kind      string `json:"kind"`
	Code      string `json:"code"`
	ReturnURL string `json:"return_url,omitempty"`
}

// Purchase handles POST /credits/{service}/purchase for a bundle or tier. Prices come from the
// registry, never from the request.
func (h *CreditsHandler) Purchase(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := h.tenant(w, r)
	if !ok {
		return
	}
	var req creditPurchaseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	intent, err := h.svc.InitiatePurchase(r.Context(), credits.PurchaseInput{
		TenantID: tenantID, Service: chi.URLParam(r, "service"),
		Kind: credits.PurchaseKind(req.Kind), Code: req.Code, ReturnURL: req.ReturnURL,
	})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, intent)
}

type creditAdminGrantRequest struct {
	Units  int64  `json:"units"`
	Reason string `json:"reason"`
	Key    string `json:"key"`
}

// AdminGrant handles POST /admin/tenants/{tenant_id}/credits/{service}/grant (platform owner).
func (h *CreditsHandler) AdminGrant(w http.ResponseWriter, r *http.Request) {
	tenantID, err := uuid.Parse(chi.URLParam(r, "tenant_id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid tenant_id"})
		return
	}
	var req creditAdminGrantRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Units <= 0 || req.Reason == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "a positive units value and a reason are required"})
		return
	}
	bal, applied, err := h.svc.AdminGrant(r.Context(), tenantID, chi.URLParam(r, "service"), req.Units, req.Reason, req.Key)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"balance": bal, "applied": applied})
}
