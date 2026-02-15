package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/planfeature"
	"github.com/bengobox/subscription-service/internal/ent/tenantsubscription"
	"github.com/bengobox/subscription-service/internal/modules/subscriptions"
)

// SubscriptionHandler handles tenant subscription endpoints.
type SubscriptionHandler struct {
	log    *zap.Logger
	client *ent.Client
	svc    *subscriptions.Service
}

// NewSubscriptionHandler creates a new subscription handler.
func NewSubscriptionHandler(log *zap.Logger, client *ent.Client, svc *subscriptions.Service) *SubscriptionHandler {
	return &SubscriptionHandler{
		log:    log.Named("subscription.handler"),
		client: client,
		svc:    svc,
	}
}

// TenantSubscriptionResponse is the response format for GET /api/v1/tenants/{tenant_id}/subscription
// This format is consumed by auth-service for JWT claims enrichment.
type TenantSubscriptionResponse struct {
	TenantID           uuid.UUID      `json:"tenant_id"`
	PlanCode           string         `json:"plan_code"`
	PlanName           string         `json:"plan_name"`
	Status             string         `json:"status"`
	TrialEndsAt        *time.Time     `json:"trial_ends_at"`
	CurrentPeriodStart time.Time      `json:"current_period_start"`
	CurrentPeriodEnd   time.Time      `json:"current_period_end"`
	Features           []string       `json:"features"`
	Limits             map[string]int `json:"limits"`
}

// GetTenantSubscription returns the current subscription for a tenant.
// @Summary Get tenant subscription
// @Tags subscriptions
// @Produce json
// @Param tenant_id path string true "Tenant ID (UUID)"
// @Success 200 {object} TenantSubscriptionResponse
// @Router /api/v1/tenants/{tenant_id}/subscription [get]
func (h *SubscriptionHandler) GetTenantSubscription(w http.ResponseWriter, r *http.Request) {
	tenantIDStr := chi.URLParam(r, "tenant_id")
	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid tenant ID format"})
		return
	}

	sub, err := h.client.TenantSubscription.Query().
		Where(tenantsubscription.TenantIDEQ(tenantID)).
		WithPlan(func(q *ent.SubscriptionPlanQuery) {
			q.WithFeatures(func(fq *ent.PlanFeatureQuery) {
				fq.Where(planfeature.IsIncludedEQ(true))
			})
		}).
		Only(r.Context())

	if err != nil {
		if ent.IsNotFound(err) {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "subscription not found"})
			return
		}
		h.log.Error("failed to get tenant subscription", zap.String("tenant_id", tenantIDStr), zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to retrieve subscription"})
		return
	}

	features := make([]string, 0)
	if sub.Edges.Plan != nil && sub.Edges.Plan.Edges.Features != nil {
		for _, f := range sub.Edges.Plan.Edges.Features {
			features = append(features, f.FeatureCode)
		}
	}

	limits := make(map[string]int)
	if sub.Edges.Plan != nil && sub.Edges.Plan.TierLimitsJSON != nil {
		for k, v := range sub.Edges.Plan.TierLimitsJSON {
			if intVal, ok := v.(float64); ok {
				limits[k] = int(intVal)
			} else if intVal, ok := v.(int); ok {
				limits[k] = intVal
			}
		}
	}

	writeJSON(w, http.StatusOK, TenantSubscriptionResponse{
		TenantID:           sub.TenantID,
		PlanCode:           sub.Edges.Plan.PlanCode,
		PlanName:           sub.Edges.Plan.Name,
		Status:             string(sub.Status),
		TrialEndsAt:        sub.TrialEndsAt,
		CurrentPeriodStart: sub.CurrentPeriodStart,
		CurrentPeriodEnd:   sub.CurrentPeriodEnd,
		Features:           features,
		Limits:             limits,
	})
}

// CheckFeature checks if a tenant has a specific feature enabled.
// @Summary Check feature availability
// @Tags subscriptions
// @Produce json
// @Param tenant_id path string true "Tenant ID (UUID)"
// @Param feature_code path string true "Feature code"
// @Success 200 {object} FeatureCheckResponse
// @Router /api/v1/tenants/{tenant_id}/features/{feature_code}/check [get]
func (h *SubscriptionHandler) CheckFeature(w http.ResponseWriter, r *http.Request) {
	tenantIDStr := chi.URLParam(r, "tenant_id")
	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid tenant ID format"})
		return
	}

	featureCode := chi.URLParam(r, "feature_code")
	if featureCode == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "feature code is required"})
		return
	}

	sub, err := h.client.TenantSubscription.Query().
		Where(tenantsubscription.TenantIDEQ(tenantID)).
		WithPlan(func(q *ent.SubscriptionPlanQuery) {
			q.WithFeatures(func(fq *ent.PlanFeatureQuery) {
				fq.Where(planfeature.FeatureCodeEQ(featureCode))
			})
		}).
		Only(r.Context())

	if err != nil {
		if ent.IsNotFound(err) {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "subscription not found"})
			return
		}
		h.log.Error("failed to check feature", zap.String("tenant_id", tenantIDStr), zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "failed to check feature"})
		return
	}

	enabled := false
	var limit *int
	planRequired := ""

	if sub.Edges.Plan != nil {
		planRequired = sub.Edges.Plan.PlanCode
		if sub.Edges.Plan.Edges.Features != nil && len(sub.Edges.Plan.Edges.Features) > 0 {
			feature := sub.Edges.Plan.Edges.Features[0]
			enabled = feature.IsIncluded
			if feature.LimitValue != 0 {
				limitVal := feature.LimitValue
				limit = &limitVal
			}
		}
	}

	writeJSON(w, http.StatusOK, FeatureCheckResponse{
		FeatureCode:  featureCode,
		Enabled:      enabled,
		Limit:        limit,
		PlanRequired: planRequired,
	})
}

// --- Lifecycle endpoints ---

// CreateSubscription provisions a new subscription for a tenant.
// @Summary Create subscription
// @Tags subscriptions
// @Accept json
// @Produce json
// @Param tenant_id path string true "Tenant ID (UUID)"
// @Success 201 {object} subscriptions.SubscriptionResult
// @Router /api/v1/tenants/{tenant_id}/subscription [post]
func (h *SubscriptionHandler) CreateSubscription(w http.ResponseWriter, r *http.Request) {
	tenantID, err := parseTenantID(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}

	var req struct {
		PlanCode   string `json:"plan_code"`
		BundleCode string `json:"bundle_code,omitempty"`
		TrialDays  int    `json:"trial_days,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return
	}
	if req.PlanCode == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "plan_code is required"})
		return
	}

	result, err := h.svc.CreateSubscription(r.Context(), subscriptions.CreateInput{
		TenantID:   tenantID,
		PlanCode:   req.PlanCode,
		BundleCode: req.BundleCode,
		TrialDays:  req.TrialDays,
	})
	if err != nil {
		h.log.Error("create subscription failed", zap.Error(err))
		writeJSON(w, http.StatusConflict, errorResponse{Error: err.Error()})
		return
	}

	writeJSON(w, http.StatusCreated, result)
}

// ChangePlan upgrades or downgrades a tenant's subscription plan.
// @Summary Change subscription plan
// @Tags subscriptions
// @Accept json
// @Produce json
// @Param tenant_id path string true "Tenant ID (UUID)"
// @Success 200 {object} subscriptions.SubscriptionResult
// @Router /api/v1/tenants/{tenant_id}/subscription/plan [put]
func (h *SubscriptionHandler) ChangePlan(w http.ResponseWriter, r *http.Request) {
	tenantID, err := parseTenantID(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}

	var req struct {
		PlanCode string `json:"plan_code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return
	}
	if req.PlanCode == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "plan_code is required"})
		return
	}

	result, err := h.svc.ChangePlan(r.Context(), subscriptions.ChangePlanInput{
		TenantID:    tenantID,
		NewPlanCode: req.PlanCode,
	})
	if err != nil {
		h.log.Error("change plan failed", zap.Error(err))
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, result)
}

// CancelSubscription cancels a tenant's subscription.
// @Summary Cancel subscription
// @Tags subscriptions
// @Accept json
// @Produce json
// @Param tenant_id path string true "Tenant ID (UUID)"
// @Success 200 {object} subscriptions.SubscriptionResult
// @Router /api/v1/tenants/{tenant_id}/subscription/cancel [post]
func (h *SubscriptionHandler) CancelSubscription(w http.ResponseWriter, r *http.Request) {
	tenantID, err := parseTenantID(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}

	var req struct {
		Reason string `json:"reason,omitempty"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	result, err := h.svc.CancelSubscription(r.Context(), subscriptions.CancelInput{
		TenantID: tenantID,
		Reason:   req.Reason,
	})
	if err != nil {
		h.log.Error("cancel subscription failed", zap.Error(err))
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, result)
}

// RenewSubscription renews an expired or cancelled subscription.
// @Summary Renew subscription
// @Tags subscriptions
// @Accept json
// @Produce json
// @Param tenant_id path string true "Tenant ID (UUID)"
// @Success 200 {object} subscriptions.SubscriptionResult
// @Router /api/v1/tenants/{tenant_id}/subscription/renew [post]
func (h *SubscriptionHandler) RenewSubscription(w http.ResponseWriter, r *http.Request) {
	tenantID, err := parseTenantID(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}

	var req struct {
		PlanCode string `json:"plan_code,omitempty"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	result, err := h.svc.RenewSubscription(r.Context(), subscriptions.RenewInput{
		TenantID: tenantID,
		PlanCode: req.PlanCode,
	})
	if err != nil {
		h.log.Error("renew subscription failed", zap.Error(err))
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, result)
}

// ActivateProduct enables a product subscription for a tenant.
// @Summary Activate product
// @Tags products
// @Param tenant_id path string true "Tenant ID (UUID)"
// @Param code path string true "Product code"
// @Success 200
// @Router /api/v1/tenants/{tenant_id}/products/{code}/activate [post]
func (h *SubscriptionHandler) ActivateProduct(w http.ResponseWriter, r *http.Request) {
	tenantID, err := parseTenantID(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}
	code := chi.URLParam(r, "code")
	if code == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "product code is required"})
		return
	}

	if err := h.svc.ActivateProduct(r.Context(), tenantID, code); err != nil {
		h.log.Error("activate product failed", zap.Error(err))
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "activated"})
}

// DeactivateProduct disables a product subscription for a tenant.
// @Summary Deactivate product
// @Tags products
// @Param tenant_id path string true "Tenant ID (UUID)"
// @Param code path string true "Product code"
// @Success 200
// @Router /api/v1/tenants/{tenant_id}/products/{code}/deactivate [post]
func (h *SubscriptionHandler) DeactivateProduct(w http.ResponseWriter, r *http.Request) {
	tenantID, err := parseTenantID(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}
	code := chi.URLParam(r, "code")
	if code == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "product code is required"})
		return
	}

	if err := h.svc.DeactivateProduct(r.Context(), tenantID, code); err != nil {
		h.log.Error("deactivate product failed", zap.Error(err))
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "deactivated"})
}

// ListProducts returns all product subscriptions for a tenant.
// @Summary List subscribed products
// @Tags products
// @Produce json
// @Param tenant_id path string true "Tenant ID (UUID)"
// @Success 200 {array} ent.ProductSubscription
// @Router /api/v1/tenants/{tenant_id}/products [get]
func (h *SubscriptionHandler) ListProducts(w http.ResponseWriter, r *http.Request) {
	tenantID, err := parseTenantID(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}

	products, err := h.svc.ListProducts(r.Context(), tenantID)
	if err != nil {
		h.log.Error("list products failed", zap.Error(err))
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"products": products,
		"count":    len(products),
	})
}

// --- Helpers ---

// FeatureCheckResponse is the response format for feature check endpoint.
type FeatureCheckResponse struct {
	FeatureCode  string `json:"feature_code"`
	Enabled      bool   `json:"enabled"`
	Limit        *int   `json:"limit"`
	PlanRequired string `json:"plan_required"`
}

func parseTenantID(r *http.Request) (uuid.UUID, error) {
	tenantIDStr := chi.URLParam(r, "tenant_id")
	return uuid.Parse(tenantIDStr)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
