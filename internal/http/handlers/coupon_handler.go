package handlers

import (
	"encoding/json"
	"net/http"

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
