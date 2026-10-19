package handlers

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
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

// Consume godoc
// @Summary Spend credit units for one send (S2S)
// @Description Spends units in order: plan allowance, bundle balance, then KES 1 overage up to the platform cap. Idempotent per key. Answers 200 with allowed=false and a reason when refused, so callers can tell a refusal from a failure (5xx) and never send free on a failure.
// @Tags Credits
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param service path string true "sms, whatsapp or ai_credits"
// @Param request body creditSpendRequest true "Spend request"
// @Success 200 {object} map[string]any
// @Router /s2s/credits/{service}/consume [post]
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

// Refund godoc
// @Summary Reverse a spend (S2S)
// @Description Restores the units and overage taken under consume_key, for example when the provider rejected the message. Idempotent per refund_key.
// @Tags Credits
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param service path string true "sms, whatsapp or ai_credits"
// @Param request body creditRefundRequest true "Refund request"
// @Success 200 {object} map[string]any
// @Router /s2s/credits/{service}/refund [post]
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

// Catalog godoc
// @Summary Price list for SMS, WhatsApp and AI credits
// @Description Bundle sizes, monthly plans and the per-message price. Public, no tenant state.
// @Tags Credits
// @Produce json
// @Success 200 {object} map[string]any
// @Router /credits/catalog [get]
func (h *CreditsHandler) Catalog(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"services": credits.Catalog()})
}

// List godoc
// @Summary Credit balances for the acting tenant
// @Description Bundle units, plan allowance, owed overage against the cap, and what can be bought, for every credit service.
// @Tags Credits
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]any
// @Router /credits [get]
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

// Transactions godoc
// @Summary Bundle wallet history
// @Tags Credits
// @Produce json
// @Security BearerAuth
// @Param service path string true "sms, whatsapp or ai_credits"
// @Param limit query int false "Default 50, max 200"
// @Param offset query int false "Rows to skip"
// @Success 200 {object} map[string]any
// @Router /credits/{service}/transactions [get]
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

// Purchase godoc
// @Summary Buy a bundle or monthly plan
// @Description Creates a Treasury payment intent. Units or the plan are granted once payment succeeds. Prices come from the registry, never from the request.
// @Tags Credits
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param service path string true "sms, whatsapp or ai_credits"
// @Param request body creditPurchaseRequest true "Purchase request"
// @Success 200 {object} map[string]any
// @Router /credits/{service}/purchase [post]
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

// AdminGrant godoc
// @Summary Add credit units to a tenant without a payment
// @Tags Credits
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param tenant_id path string true "Tenant UUID"
// @Param service path string true "sms, whatsapp or ai_credits"
// @Param request body creditAdminGrantRequest true "Grant request"
// @Success 200 {object} map[string]any
// @Router /admin/tenants/{tenant_id}/credits/{service}/grant [post]
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

// Check godoc
// @Summary Would this send be allowed (S2S, records nothing)
// @Tags Credits
// @Produce json
// @Security ApiKeyAuth
// @Param service path string true "sms, whatsapp or ai_credits"
// @Param tenant_id query string true "Tenant UUID"
// @Param units query int true "Units the send would use"
// @Param platform_scope query bool false "Platform-owned send, never billed"
// @Success 200 {object} map[string]any
// @Router /s2s/credits/{service}/check [get]
func (h *CreditsHandler) Check(w http.ResponseWriter, r *http.Request) {
	if !h.isService(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "service key required"})
		return
	}
	tenantID, err := uuid.Parse(r.URL.Query().Get("tenant_id"))
	units, uerr := strconv.ParseInt(r.URL.Query().Get("units"), 10, 64)
	if err != nil || uerr != nil || units < 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "tenant_id and a non-negative units value are required"})
		return
	}
	dec, err := h.svc.Check(r.Context(), tenantID, chi.URLParam(r, "service"), units, r.URL.Query().Get("platform_scope") == "true")
	if err != nil {
		h.log.Error("credits check failed", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "credit check failed"})
		return
	}
	writeJSON(w, http.StatusOK, dec)
}

type creditImportRequest struct {
	DryRun bool                   `json:"dry_run"`
	Items  []credits.LegacyWallet `json:"items"`
}

// Import godoc
// @Summary Move legacy notifications balances into the credits ledger (S2S)
// @Description Additive and idempotent. With dry_run it reports without writing.
// @Tags Credits
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param request body creditImportRequest true "Import request"
// @Success 200 {object} map[string]any
// @Router /s2s/credits/import [post]
func (h *CreditsHandler) Import(w http.ResponseWriter, r *http.Request) {
	if !h.isService(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "service key required"})
		return
	}
	var req creditImportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Items) == 0 || len(req.Items) > 500 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "between 1 and 500 items are required"})
		return
	}
	results := make([]credits.ImportResult, 0, len(req.Items))
	for _, it := range req.Items {
		res, err := h.svc.ImportLegacy(r.Context(), it, req.DryRun)
		if err != nil {
			h.log.Error("credits import failed", zap.String("tenant_id", it.TenantID.String()), zap.Error(err))
			res.Skipped = "error: " + err.Error()
		}
		results = append(results, res)
	}
	writeJSON(w, http.StatusOK, map[string]any{"dry_run": req.DryRun, "results": results})
}

type creditAutoRenewRequest struct {
	AutoRenew bool `json:"auto_renew"`
}

// SetAutoRenew godoc
// @Summary Turn monthly plan renewal on or off
// @Description Turning it off keeps the plan until the end of the paid period and stops it appearing on the next invoice.
// @Tags Credits
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param service path string true "whatsapp"
// @Param request body creditAutoRenewRequest true "Renewal choice"
// @Success 200 {object} map[string]any
// @Router /credits/{service}/auto-renew [post]
func (h *CreditsHandler) SetAutoRenew(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := h.tenant(w, r)
	if !ok {
		return
	}
	var req creditAutoRenewRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if err := h.svc.SetTierAutoRenew(r.Context(), tenantID, chi.URLParam(r, "service"), req.AutoRenew); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"auto_renew": req.AutoRenew})
}

// AdminTenants godoc
// @Summary Tenants holding a messaging or AI credit position (platform owner)
// @Description One row per tenant with a wallet, activation or overage for the service: plan, bundle balance, tier and renewal, pending overage against the cap, units used this UTC month and last activity. Sorted by last activity.
// @Tags Credits
// @Produce json
// @Security BearerAuth
// @Param service query string true "sms, whatsapp or ai_credits"
// @Param q query string false "Search tenant name or slug"
// @Param status query string false "active, capped, not_activated or exempt"
// @Param limit query int false "Default 25, max 100"
// @Param offset query int false "Rows to skip"
// @Success 200 {object} credits.AdminTenantPage
// @Failure 400 {object} map[string]string
// @Failure 403 {object} map[string]string
// @Router /admin/credits/tenants [get]
func (h *CreditsHandler) AdminTenants(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	page, err := h.svc.ListTenants(r.Context(), credits.AdminListParams{
		Service: q.Get("service"), Q: q.Get("q"), Status: q.Get("status"), Limit: limit, Offset: offset,
	})
	if err != nil {
		if errors.Is(err, credits.ErrInvalidAdminQuery) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		h.log.Error("credits admin tenants failed", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load tenants"})
		return
	}
	writeJSON(w, http.StatusOK, page)
}

// AdminOverview godoc
// @Summary Platform totals per credit service (platform owner)
// @Description For each service: tenants activated, units sold and consumed this UTC month, bundle revenue this month, overage accrued this month, overage still unpaid, and tenants at or over the cap.
// @Tags Credits
// @Produce json
// @Security BearerAuth
// @Success 200 {object} credits.AdminOverview
// @Failure 403 {object} map[string]string
// @Router /admin/credits/overview [get]
func (h *CreditsHandler) AdminOverview(w http.ResponseWriter, r *http.Request) {
	out, err := h.svc.Overview(r.Context())
	if err != nil {
		h.log.Error("credits admin overview failed", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load overview"})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// AdminGetSettings godoc
// @Summary Platform messaging settings (platform owner)
// @Tags Credits
// @Produce json
// @Security BearerAuth
// @Success 200 {object} credits.AdminSettings
// @Failure 403 {object} map[string]string
// @Router /admin/credits/settings [get]
func (h *CreditsHandler) AdminGetSettings(w http.ResponseWriter, r *http.Request) {
	out, err := h.svc.Settings(r.Context())
	if err != nil {
		h.log.Error("credits admin settings failed", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load settings"})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

type creditAdminSettingsRequest struct {
	OverageCapKES *float64 `json:"overage_cap_kes"`
}

// AdminPutSettings godoc
// @Summary Change platform messaging settings (platform owner)
// @Description overage_cap_kes is the most unpaid overage in KES a tenant can run up before SMS and WhatsApp sends pause. 0 means no cap, the maximum is 1,000,000, two decimal places at most. Takes effect immediately on the serving replica and within one minute on the others.
// @Tags Credits
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body creditAdminSettingsRequest true "Settings"
// @Success 200 {object} credits.AdminSettings
// @Failure 400 {object} map[string]string
// @Failure 403 {object} map[string]string
// @Router /admin/credits/settings [put]
func (h *CreditsHandler) AdminPutSettings(w http.ResponseWriter, r *http.Request) {
	var req creditAdminSettingsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.OverageCapKES == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "overage_cap_kes is required"})
		return
	}
	if err := credits.ValidateOverageCap(*req.OverageCapKES); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	out, err := h.svc.SetOverageCap(r.Context(), *req.OverageCapKES)
	if err != nil {
		h.log.Error("credits admin settings update failed", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save settings"})
		return
	}
	writeJSON(w, http.StatusOK, out)
}
