package handlers

import (
	"encoding/json"
	"net/http"

	authclient "github.com/Bengo-Hub/shared-auth-client"
	httpware "github.com/Bengo-Hub/httpware"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/tenantsubscription"
	"github.com/bengobox/subscription-service/internal/modules/billing"
)

// CouponHandler manages subscription coupon redemption.
type CouponHandler struct {
	log           *zap.Logger
	client        *ent.Client
	couponService *billing.CouponService
	creditService *billing.CreditService
}

// NewCouponHandler creates a new CouponHandler.
func NewCouponHandler(log *zap.Logger, client *ent.Client) *CouponHandler {
	creditSvc := billing.NewCreditService(log, client)
	return &CouponHandler{
		log:           log.Named("coupon.handler"),
		client:        client,
		couponService: billing.NewCouponService(log, client, creditSvc),
		creditService: creditSvc,
	}
}

// RedeemCoupon handles POST /subscription/coupon/redeem
func (h *CouponHandler) RedeemCoupon(w http.ResponseWriter, r *http.Request) {
	tenantIDStr := resolveTenantID(r)
	if tenantIDStr == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "tenant_id required"})
		return
	}
	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid tenant_id"})
		return
	}

	var body struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Code == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "code is required"})
		return
	}

	ctx := r.Context()

	// Look up current plan to check coupon applicability
	sub, err := h.client.TenantSubscription.Query().
		Where(tenantsubscription.TenantIDEQ(tenantID)).
		WithPlan().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "no subscription found"})
			return
		}
		h.log.Error("coupon redeem: subscription query failed", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	planCode := ""
	planPrice := 0.0
	if sub.Edges.Plan != nil {
		planCode = sub.Edges.Plan.PlanCode
		planPrice = sub.Edges.Plan.BasePrice
	}

	creditsEarned, err := h.couponService.RedeemCoupon(ctx, tenantID, body.Code, planCode, planPrice)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	balance, _ := h.creditService.GetBalance(ctx, tenantID)

	writeJSON(w, http.StatusOK, map[string]any{
		"message":           "Coupon redeemed successfully",
		"credits_earned":    creditsEarned,
		"new_balance_kes":   balance,
	})
}

// GetCreditWallet handles GET /billing/credits
func (h *CouponHandler) GetCreditWallet(w http.ResponseWriter, r *http.Request) {
	tenantIDStr := resolveTenantID(r)
	if tenantIDStr == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "tenant_id required"})
		return
	}
	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid tenant_id"})
		return
	}

	ctx := r.Context()

	balance, err := h.creditService.GetBalance(ctx, tenantID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	txns, err := h.creditService.GetTransactions(ctx, tenantID)
	if err != nil {
		h.log.Warn("credit wallet: failed to get transactions", zap.Error(err))
		txns = nil
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"balance_kes":  balance,
		"transactions": txns,
	})
}

// GiftCredits handles POST /admin/tenants/{tenant_id}/credits/gift
// Adds credits to a tenant's wallet on behalf of a platform admin.
func (h *CouponHandler) GiftCredits(w http.ResponseWriter, r *http.Request) {
	if !httpware.IsPlatformOwner(r.Context()) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}

	tenantIDStr := chi.URLParam(r, "tenant_id")
	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid tenant_id"})
		return
	}

	var body struct {
		AmountKes int    `json:"amount_kes"`
		Reason    string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.AmountKes <= 0 || body.Reason == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "amount_kes (positive int) and reason are required"})
		return
	}

	adminUserID := uuid.Nil
	if claims, ok := authclient.ClaimsFromContext(r.Context()); ok {
		if id, err := claims.UserID(); err == nil {
			adminUserID = id
		}
	}

	ctx := r.Context()
	if err := h.creditService.AddCredits(ctx, tenantID, body.AmountKes, "gifted", adminUserID, body.Reason); err != nil {
		h.log.Error("gift credits failed", zap.String("tenant_id", tenantIDStr), zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to gift credits"})
		return
	}

	balance, _ := h.creditService.GetBalance(ctx, tenantID)
	h.log.Info("credits gifted by admin",
		zap.String("tenant_id", tenantIDStr),
		zap.Int("amount_kes", body.AmountKes),
		zap.String("reason", body.Reason),
		zap.String("admin", adminUserID.String()),
	)
	writeJSON(w, http.StatusOK, map[string]any{
		"status":          "gifted",
		"amount_kes":      body.AmountKes,
		"new_balance_kes": balance,
	})
}
