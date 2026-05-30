package handlers

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"entgo.io/ent/dialect/sql"
	authclient "github.com/Bengo-Hub/shared-auth-client"
	httpware "github.com/Bengo-Hub/httpware"
	"github.com/bengobox/subscription-service/internal/ent"
	entcoupon "github.com/bengobox/subscription-service/internal/ent/coupon"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// CouponAdminHandler provides platform-admin CRUD for subscription coupon codes.
type CouponAdminHandler struct {
	log    *zap.Logger
	client *ent.Client
}

// NewCouponAdminHandler creates a new CouponAdminHandler.
func NewCouponAdminHandler(log *zap.Logger, client *ent.Client) *CouponAdminHandler {
	return &CouponAdminHandler{
		log:    log.Named("coupon.admin.handler"),
		client: client,
	}
}

type couponAdminRequest struct {
	Code                string    `json:"code"`
	Name                string    `json:"name"`
	Description         *string   `json:"description,omitempty"`
	Type                string    `json:"type"` // percentage | fixed_kes | free_months
	Value               float64   `json:"value"`
	ApplicablePlanCodes []string  `json:"applicable_plan_codes,omitempty"`
	MinPlanPrice        float64   `json:"min_plan_price"`
	MaxUses             int       `json:"max_uses"` // -1 = unlimited
	MaxStacks           int       `json:"max_stacks"`
	IsActive            bool      `json:"is_active"`
	ValidFrom           *string   `json:"valid_from,omitempty"`
	ValidUntil          *string   `json:"valid_until,omitempty"`
}

type couponAdminResponse struct {
	ID                  string    `json:"id"`
	Code                string    `json:"code"`
	Name                string    `json:"name"`
	Description         *string   `json:"description,omitempty"`
	Type                string    `json:"type"`
	Value               float64   `json:"value"`
	ApplicablePlanCodes []string  `json:"applicable_plan_codes"`
	MinPlanPrice        float64   `json:"min_plan_price"`
	MaxUses             int       `json:"max_uses"`
	UsedCount           int       `json:"used_count"`
	MaxStacks           int       `json:"max_stacks"`
	IsActive            bool      `json:"is_active"`
	ValidFrom           string    `json:"valid_from"`
	ValidUntil          *string   `json:"valid_until,omitempty"`
	CreatedAt           string    `json:"created_at"`
}

func couponToResponse(c *ent.Coupon) couponAdminResponse {
	resp := couponAdminResponse{
		ID:                  c.ID.String(),
		Code:                c.Code,
		Name:                c.Name,
		Description:         c.Description,
		Type:                string(c.Type),
		Value:               c.Value,
		ApplicablePlanCodes: c.ApplicablePlanCodes,
		MinPlanPrice:        c.MinPlanPrice,
		MaxUses:             c.MaxUses,
		UsedCount:           c.UsedCount,
		MaxStacks:           c.MaxStacks,
		IsActive:            c.IsActive,
		ValidFrom:           c.ValidFrom.Format(time.RFC3339),
		CreatedAt:           c.CreatedAt.Format(time.RFC3339),
	}
	if c.ValidUntil != nil {
		s := c.ValidUntil.Format(time.RFC3339)
		resp.ValidUntil = &s
	}
	if resp.ApplicablePlanCodes == nil {
		resp.ApplicablePlanCodes = []string{}
	}
	return resp
}

// ListCoupons handles GET /admin/coupons
func (h *CouponAdminHandler) ListCoupons(w http.ResponseWriter, r *http.Request) {
	if !httpware.IsPlatformOwner(r.Context()) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}

	ctx := r.Context()
	q := h.client.Coupon.Query().Order(entcoupon.ByCreatedAt(sql.OrderDesc()))

	if activeStr := r.URL.Query().Get("active"); activeStr == "true" {
		q = q.Where(entcoupon.IsActiveEQ(true))
	} else if activeStr == "false" {
		q = q.Where(entcoupon.IsActiveEQ(false))
	}

	coupons, err := q.All(ctx)
	if err != nil {
		h.log.Error("admin coupons: list failed", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list coupons"})
		return
	}

	resp := make([]couponAdminResponse, 0, len(coupons))
	for _, c := range coupons {
		resp = append(resp, couponToResponse(c))
	}

	writeJSON(w, http.StatusOK, map[string]any{"data": resp, "total": len(resp)})
}

// GetCoupon handles GET /admin/coupons/{id}
func (h *CouponAdminHandler) GetCoupon(w http.ResponseWriter, r *http.Request) {
	if !httpware.IsPlatformOwner(r.Context()) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}

	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid coupon id"})
		return
	}

	c, err := h.client.Coupon.Get(r.Context(), id)
	if err != nil {
		if ent.IsNotFound(err) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "coupon not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	writeJSON(w, http.StatusOK, couponToResponse(c))
}

// CreateCoupon handles POST /admin/coupons
func (h *CouponAdminHandler) CreateCoupon(w http.ResponseWriter, r *http.Request) {
	if !httpware.IsPlatformOwner(r.Context()) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}

	var req couponAdminRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if req.Code == "" || req.Name == "" || req.Type == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "code, name, and type are required"})
		return
	}

	// Enforce uppercase code
	req.Code = strings.ToUpper(strings.TrimSpace(req.Code))

	adminUserID := uuid.Nil
	if claims, ok := authclient.ClaimsFromContext(r.Context()); ok {
		if id, err := claims.UserID(); err == nil {
			adminUserID = id
		}
	}

	if req.MaxUses == 0 {
		req.MaxUses = -1 // default unlimited
	}
	if req.MaxStacks == 0 {
		req.MaxStacks = 1
	}

	ctx := r.Context()
	create := h.client.Coupon.Create().
		SetCode(req.Code).
		SetName(req.Name).
		SetType(entcoupon.Type(req.Type)).
		SetValue(req.Value).
		SetMinPlanPrice(req.MinPlanPrice).
		SetMaxUses(req.MaxUses).
		SetMaxStacks(req.MaxStacks).
		SetIsActive(req.IsActive).
		SetCreatedBy(adminUserID)

	if req.Description != nil {
		create = create.SetDescription(*req.Description)
	}
	if len(req.ApplicablePlanCodes) > 0 {
		create = create.SetApplicablePlanCodes(req.ApplicablePlanCodes)
	}
	if req.ValidFrom != nil {
		if t, err := time.Parse(time.RFC3339, *req.ValidFrom); err == nil {
			create = create.SetValidFrom(t)
		}
	}
	if req.ValidUntil != nil {
		if t, err := time.Parse(time.RFC3339, *req.ValidUntil); err == nil {
			create = create.SetValidUntil(t)
		}
	}

	c, err := create.Save(ctx)
	if err != nil {
		if strings.Contains(err.Error(), "unique") || strings.Contains(err.Error(), "duplicate") {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "coupon code already exists"})
			return
		}
		h.log.Error("admin coupon: create failed", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create coupon"})
		return
	}

	h.log.Info("coupon created by admin", zap.String("code", c.Code), zap.String("admin", adminUserID.String()))
	writeJSON(w, http.StatusCreated, couponToResponse(c))
}

// UpdateCoupon handles PATCH /admin/coupons/{id}
func (h *CouponAdminHandler) UpdateCoupon(w http.ResponseWriter, r *http.Request) {
	if !httpware.IsPlatformOwner(r.Context()) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}

	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid coupon id"})
		return
	}

	var req map[string]any
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	ctx := r.Context()
	update := h.client.Coupon.UpdateOneID(id)

	if v, ok := req["name"].(string); ok && v != "" {
		update = update.SetName(v)
	}
	if v, ok := req["description"].(string); ok {
		update = update.SetDescription(v)
	}
	if v, ok := req["value"].(float64); ok {
		update = update.SetValue(v)
	}
	if v, ok := req["is_active"].(bool); ok {
		update = update.SetIsActive(v)
	}
	if v, ok := req["max_uses"].(float64); ok {
		update = update.SetMaxUses(int(v))
	}
	if v, ok := req["max_stacks"].(float64); ok {
		update = update.SetMaxStacks(int(v))
	}
	if v, ok := req["min_plan_price"].(float64); ok {
		update = update.SetMinPlanPrice(v)
	}
	if v, ok := req["valid_until"].(string); ok {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			update = update.SetValidUntil(t)
		}
	}
	if v, ok := req["applicable_plan_codes"].([]any); ok {
		codes := make([]string, 0, len(v))
		for _, c := range v {
			if s, ok := c.(string); ok {
				codes = append(codes, s)
			}
		}
		update = update.SetApplicablePlanCodes(codes)
	}

	c, err := update.Save(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "coupon not found"})
			return
		}
		h.log.Error("admin coupon: update failed", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to update coupon"})
		return
	}

	writeJSON(w, http.StatusOK, couponToResponse(c))
}

// DeleteCoupon handles DELETE /admin/coupons/{id} — deactivates rather than hard-deletes.
func (h *CouponAdminHandler) DeleteCoupon(w http.ResponseWriter, r *http.Request) {
	if !httpware.IsPlatformOwner(r.Context()) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}

	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid coupon id"})
		return
	}

	ctx := r.Context()
	_, err = h.client.Coupon.UpdateOneID(id).SetIsActive(false).Save(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "coupon not found"})
			return
		}
		h.log.Error("admin coupon: delete (deactivate) failed", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to deactivate coupon"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "deactivated"})
}
