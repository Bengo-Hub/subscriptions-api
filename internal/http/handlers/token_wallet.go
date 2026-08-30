package handlers

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/subscriptionplan"
	"github.com/bengobox/subscription-service/internal/modules/billing"
	"github.com/bengobox/subscription-service/internal/modules/subscriptions"
)

// TokenWalletHandler exposes the prepaid ApiTokenWallet S2S (for treasury-api's external eTIMS
// API middleware) and tenant-facing (for a self-serve wallet dashboard) endpoints. Generalized
// by service_tag, not eTIMS-specific — see billing/token_wallet.go's doc comment.
type TokenWalletHandler struct {
	log     *zap.Logger
	orm     *ent.Client
	wallet  *billing.TokenWalletService
	subsSvc *subscriptions.Service
}

// NewTokenWalletHandler creates a new TokenWalletHandler.
func NewTokenWalletHandler(log *zap.Logger, orm *ent.Client, subsSvc *subscriptions.Service) *TokenWalletHandler {
	return &TokenWalletHandler{
		log:     log.Named("token_wallet.handler"),
		orm:     orm,
		wallet:  billing.NewTokenWalletService(log, orm),
		subsSvc: subsSvc,
	}
}

func tenantAndServiceTag(r *http.Request) (uuid.UUID, string, bool) {
	tenantID, err := uuid.Parse(chi.URLParam(r, "tenant_id"))
	if err != nil {
		return uuid.Nil, "", false
	}
	serviceTag := r.URL.Query().Get("service_tag")
	if serviceTag == "" {
		serviceTag = "etims_api"
	}
	return tenantID, serviceTag, true
}

// GetBalance godoc
// @Summary Get API token wallet balance
// @Description Returns the current token balance for a tenant+service. S2S (treasury-api's external eTIMS middleware) or tenant-facing.
// @Tags Subscriptions
// @Produce json
// @Security BearerAuth
// @Security ApiKeyAuth
// @Param tenant_id path string true "Tenant UUID"
// @Param service_tag query string false "Defaults to etims_api"
// @Success 200 {object} billing.WalletSnapshot
// @Router /tenants/{tenant_id}/tokens/balance [get]
func (h *TokenWalletHandler) GetBalance(w http.ResponseWriter, r *http.Request) {
	tenantID, serviceTag, ok := tenantAndServiceTag(r)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid tenant_id"})
		return
	}
	snap, err := h.wallet.GetBalance(r.Context(), tenantID, serviceTag)
	if err != nil {
		h.log.Error("get token balance failed", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to retrieve balance"})
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

// GetTransactions godoc
// @Summary List API token wallet transactions
// @Tags Subscriptions
// @Produce json
// @Security BearerAuth
// @Security ApiKeyAuth
// @Param tenant_id path string true "Tenant UUID"
// @Param service_tag query string false "Defaults to etims_api"
// @Param limit query int false "Default 50, max 200"
// @Param offset query int false
// @Success 200 {object} map[string]any
// @Router /tenants/{tenant_id}/tokens/transactions [get]
func (h *TokenWalletHandler) GetTransactions(w http.ResponseWriter, r *http.Request) {
	tenantID, serviceTag, ok := tenantAndServiceTag(r)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid tenant_id"})
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	entries, total, err := h.wallet.ListTransactions(r.Context(), tenantID, serviceTag, limit, offset)
	if err != nil {
		h.log.Error("list token transactions failed", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to retrieve transactions"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": entries, "total": total, "limit": limit, "offset": offset})
}

type deductRequest struct {
	ServiceTag      string `json:"service_tag"`
	EndpointPattern string `json:"endpoint_pattern"`
	RefID           string `json:"ref_id"`
	RefType         string `json:"ref_type"`
	Description     string `json:"description"`
}

// Deduct godoc
// @Summary Spend API tokens for one call (S2S)
// @Description Atomically deducts the resolved token cost for endpoint_pattern from the tenant's wallet, refusing (402) if the balance is insufficient. Called BEFORE the metered operation runs, not after — see treasury-api's ExternalAPIKeyAuth middleware.
// @Tags Subscriptions
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param tenant_id path string true "Tenant UUID"
// @Param request body deductRequest true "Deduct request"
// @Success 200 {object} billing.DeductResult
// @Failure 402 {object} map[string]any "insufficient_tokens"
// @Router /tenants/{tenant_id}/tokens/deduct [post]
func (h *TokenWalletHandler) Deduct(w http.ResponseWriter, r *http.Request) {
	tenantID, err := uuid.Parse(chi.URLParam(r, "tenant_id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid tenant_id"})
		return
	}
	var req deductRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if req.ServiceTag == "" || req.EndpointPattern == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "service_tag and endpoint_pattern are required"})
		return
	}
	cost, matched := billing.TokenCostForEndpoint(req.ServiceTag, req.EndpointPattern)
	if !matched {
		h.log.Warn("token cost registry has no match for endpoint, defaulting",
			zap.String("service_tag", req.ServiceTag), zap.String("endpoint_pattern", req.EndpointPattern), zap.Int64("default_cost", cost))
	}

	var refID uuid.UUID
	if req.RefID != "" {
		refID, _ = uuid.Parse(req.RefID)
	}

	result, err := h.wallet.DeductTokens(r.Context(), billing.DeductInput{
		TenantID:        tenantID,
		ServiceTag:      req.ServiceTag,
		Tokens:          cost,
		EndpointPattern: req.EndpointPattern,
		RefID:           refID,
		RefType:         req.RefType,
		Description:     req.Description,
	})
	if err != nil {
		h.log.Error("deduct tokens failed", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to deduct tokens"})
		return
	}
	if !result.Allowed {
		writeJSON(w, http.StatusPaymentRequired, map[string]any{
			"code":     "insufficient_tokens",
			"error":    "insufficient token balance",
			"balance":  result.NewBalance,
			"required": cost,
		})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

type refundRequest struct {
	ServiceTag  string `json:"service_tag"`
	Tokens      int64  `json:"tokens"`
	RefID       string `json:"ref_id"`
	Description string `json:"description"`
}

// Refund godoc
// @Summary Refund previously-deducted API tokens (S2S)
// @Description Reverses a Deduct when the downstream operation turned out not to have done real work (e.g. the caller's request failed validation before reaching KRA) — called by treasury-api's ExternalAPIKeyAuth middleware after the handler runs, only for status codes that indicate no real KRA attempt happened.
// @Tags Subscriptions
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Param tenant_id path string true "Tenant UUID"
// @Param request body refundRequest true "Refund request"
// @Success 200 {object} map[string]any
// @Router /tenants/{tenant_id}/tokens/refund [post]
func (h *TokenWalletHandler) Refund(w http.ResponseWriter, r *http.Request) {
	tenantID, err := uuid.Parse(chi.URLParam(r, "tenant_id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid tenant_id"})
		return
	}
	var req refundRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if req.ServiceTag == "" || req.Tokens <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "service_tag and a positive tokens value are required"})
		return
	}
	var refID uuid.UUID
	if req.RefID != "" {
		refID, _ = uuid.Parse(req.RefID)
	}
	newBalance, err := h.wallet.RefundTokens(r.Context(), tenantID, req.ServiceTag, req.Tokens, refID, req.Description)
	if err != nil {
		h.log.Error("refund tokens failed", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to refund tokens"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"new_balance": newBalance, "tokens_refunded": req.Tokens})
}

type topUpRequest struct {
	ServiceTag string  `json:"service_tag"`
	AmountKes  float64 `json:"amount_kes"`
	ReturnURL  string  `json:"return_url,omitempty"`
}

// InitiateTopUp godoc
// @Summary Buy API tokens (self-serve)
// @Description Creates a Treasury payment intent for a token top-up; tokens are credited once payment is confirmed (see the api_token_topup NATS consumer branch).
// @Tags Subscriptions
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param tenant_id path string true "Tenant UUID"
// @Param request body topUpRequest true "Top-up request"
// @Success 200 {object} map[string]any
// @Router /tenants/{tenant_id}/tokens/topup [post]
func (h *TokenWalletHandler) InitiateTopUp(w http.ResponseWriter, r *http.Request) {
	tenantID, err := uuid.Parse(chi.URLParam(r, "tenant_id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid tenant_id"})
		return
	}
	var req topUpRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if req.ServiceTag == "" {
		req.ServiceTag = "etims_api"
	}
	if h.subsSvc == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "top-up unavailable"})
		return
	}

	// Gate: a tenant may only buy tokens for a service it has actually activated (its own
	// plan or an active product overlay carries this service_tag), or is exempt. Without this,
	// any authenticated tenant could create a payment intent — and, on payment, a funded
	// wallet — for an external API product it never subscribed to. Resolved generically by
	// service_tag (not hardcoded to eTIMS) so a future token-metered product reuses this same
	// check for free. See HasActiveServiceTagSubscription's doc comment for why this, and not
	// ActiveProducts/product_code, is the correct resolution here.
	entitled, err := h.subsSvc.HasActiveServiceTagSubscription(r.Context(), tenantID, req.ServiceTag)
	if err != nil {
		h.log.Error("check token wallet entitlement failed", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to verify subscription"})
		return
	}
	if !entitled {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"code":        "feature_not_available",
			"service_tag": req.ServiceTag,
			"error":       "tenant has not activated this API product",
			"upgrade_url": "/plans?service=" + req.ServiceTag,
		})
		return
	}

	result, err := h.subsSvc.CreateTokenTopUpIntent(r.Context(), subscriptions.TokenTopUpInput{
		TenantID:   tenantID,
		ServiceTag: req.ServiceTag,
		AmountKes:  req.AmountKes,
		ReturnURL:  req.ReturnURL,
	})
	if err != nil {
		h.log.Error("initiate token top-up failed", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

type estimateRequest struct {
	ServiceTag  string         `json:"service_tag"`
	CallsPerDay map[string]int `json:"calls_per_day,omitempty"` // endpoint_pattern -> calls/day
	// Shortcut: assumes every call is a sales-transmission-weight call (the common "N sales/day" case).
	AvgSalesPerDay int `json:"avg_sales_per_day,omitempty"`
	DaysPerMonth   int `json:"days_per_month,omitempty"`
}

type planComparison struct {
	PlanCode          string  `json:"plan_code"`
	Name              string  `json:"name"`
	MonthlyPriceKes   float64 `json:"monthly_price_kes"`
	IncludedTokens    int64   `json:"included_tokens"`
	TokenPriceKes     float64 `json:"token_price_kes"`
	CoversEstimate    bool    `json:"covers_estimate"`
	ProjectedTopUpKes float64 `json:"projected_topup_kes"`
}

type estimateResponse struct {
	TokensPerMonth  int64            `json:"tokens_per_month"`
	RecommendedPlan string           `json:"recommended_plan,omitempty"`
	PlansCompared   []planComparison `json:"plans_compared"`
}

// Estimate godoc
// @Summary Estimate monthly API token consumption and recommend a plan
// @Description Given expected call volume, computes tokens/month from the token-cost registry and compares against every published plan for service_tag, so a tenant can buy from a point of information instead of blindly.
// @Tags Subscriptions
// @Accept json
// @Produce json
// @Param request body estimateRequest true "Estimate request"
// @Success 200 {object} estimateResponse
// @Router /tenants/{tenant_id}/tokens/estimate [post]
func (h *TokenWalletHandler) Estimate(w http.ResponseWriter, r *http.Request) {
	var req estimateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if req.ServiceTag == "" {
		req.ServiceTag = "etims_api"
	}

	var tokensPerMonth int64
	if len(req.CallsPerDay) > 0 {
		days := req.DaysPerMonth
		if days <= 0 {
			days = 30
		}
		for endpoint, calls := range req.CallsPerDay {
			cost, _ := billing.TokenCostForEndpoint(req.ServiceTag, endpoint)
			tokensPerMonth += cost * int64(calls) * int64(days)
		}
	} else {
		days := req.DaysPerMonth
		if days <= 0 {
			days = 30
		}
		tokensPerMonth = billing.ApiTokenCostTransmit * int64(req.AvgSalesPerDay) * int64(days)
	}

	resp := estimateResponse{TokensPerMonth: tokensPerMonth}

	if h.orm != nil {
		plans, err := h.orm.SubscriptionPlan.Query().
			Where(
				subscriptionplan.ServiceTagEQ(req.ServiceTag),
				subscriptionplan.IsActiveEQ(true),
				subscriptionplan.IsPublicEQ(true),
			).
			All(r.Context())
		if err == nil {
			sort.Slice(plans, func(i, j int) bool { return plans[i].TierOrder < plans[j].TierOrder })
			for _, p := range plans {
				included, _ := toInt64(p.TierLimitsJSON["included_tokens"])
				priceKes, _ := p.TierLimitsJSON["token_price_kes"].(float64)
				covers := included >= tokensPerMonth
				projected := 0.0
				if !covers && priceKes > 0 {
					projected = float64(tokensPerMonth-included) * priceKes
				}
				resp.PlansCompared = append(resp.PlansCompared, planComparison{
					PlanCode:          p.PlanCode,
					Name:              p.Name,
					MonthlyPriceKes:   p.BasePrice,
					IncludedTokens:    included,
					TokenPriceKes:     priceKes,
					CoversEstimate:    covers,
					ProjectedTopUpKes: projected,
				})
				if covers && resp.RecommendedPlan == "" {
					resp.RecommendedPlan = p.PlanCode
				}
			}
			// No plan alone covers it — recommend the largest tier (least overage cost per token).
			if resp.RecommendedPlan == "" && len(resp.PlansCompared) > 0 {
				resp.RecommendedPlan = resp.PlansCompared[len(resp.PlansCompared)-1].PlanCode
			}
		}
	}

	writeJSON(w, http.StatusOK, resp)
}

func toInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case float64:
		return int64(n), true
	case int:
		return int64(n), true
	case int64:
		return n, true
	default:
		return 0, false
	}
}
