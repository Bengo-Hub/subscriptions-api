package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/bengobox/subscription-service/internal/modules/plans"
)

// PlanHandler handles subscription plan endpoints.
type PlanHandler struct {
	log     *zap.Logger
	service *plans.Service
}

// NewPlanHandler creates a new plan handler.
func NewPlanHandler(log *zap.Logger, svc *plans.Service) *PlanHandler {
	return &PlanHandler{
		log:     log.Named("plan.handler"),
		service: svc,
	}
}

// ListPlans godoc
// @Summary List subscription plans
// @Description Returns all available subscription plans with pricing
// @Tags Plans
// @Produce json
// @Param active query bool false "Filter by active status"
// @Param service query string false "Filter by service tag (ordering, truload, logistics, inventory, erp, pos, marketflow)"
// @Success 200 {object} listPlansResponse
// @Failure 500 {object} errorResponse
// @Router /plans [get]
func (h *PlanHandler) ListPlans(w http.ResponseWriter, r *http.Request) {
	activeOnly := false
	if activeStr := r.URL.Query().Get("active"); activeStr != "" {
		var err error
		activeOnly, err = strconv.ParseBool(activeStr)
		if err != nil {
			h.respondWithError(w, http.StatusBadRequest, "invalid active parameter, must be true or false")
			return
		}
	}

	var serviceTag *string
	if svcStr := r.URL.Query().Get("service"); svcStr != "" {
		serviceTag = &svcStr
	}

	plansList, err := h.service.ListPlansWithPrices(r.Context(), activeOnly, serviceTag)
	if err != nil {
		h.log.Error("failed to list plans", zap.Error(err))
		h.respondWithError(w, http.StatusInternalServerError, "failed to retrieve plans")
		return
	}

	h.respondWithJSON(w, http.StatusOK, listPlansResponse{
		Plans: plansList,
		Count: len(plansList),
	})
}

// GetPlan godoc
// @Summary Get plan by ID
// @Description Returns a specific subscription plan by UUID
// @Tags Plans
// @Produce json
// @Param id path string true "Plan UUID"
// @Success 200 {object} planResponse
// @Failure 400 {object} errorResponse
// @Failure 404 {object} errorResponse
// @Router /plans/{id} [get]
func (h *PlanHandler) GetPlan(w http.ResponseWriter, r *http.Request) {
	planIDStr := chi.URLParam(r, "id")
	planID, err := uuid.Parse(planIDStr)
	if err != nil {
		h.respondWithError(w, http.StatusBadRequest, "invalid plan ID format")
		return
	}

	plan, err := h.service.GetPlanWithPrice(r.Context(), planID)
	if err != nil {
		h.log.Error("failed to get plan", zap.String("plan_id", planIDStr), zap.Error(err))
		h.respondWithError(w, http.StatusNotFound, "plan not found")
		return
	}

	h.respondWithJSON(w, http.StatusOK, planResponse{Plan: plan})
}

// GetPlanByCode godoc
// @Summary Get plan by code
// @Description Returns a specific subscription plan by plan code (e.g. STARTER, GROWTH, PROFESSIONAL)
// @Tags Plans
// @Produce json
// @Param code path string true "Plan code"
// @Success 200 {object} planResponse
// @Failure 400 {object} errorResponse
// @Failure 404 {object} errorResponse
// @Router /plans/code/{code} [get]
func (h *PlanHandler) GetPlanByCode(w http.ResponseWriter, r *http.Request) {
	planCode := chi.URLParam(r, "code")
	if planCode == "" {
		h.respondWithError(w, http.StatusBadRequest, "plan code is required")
		return
	}

	plan, err := h.service.GetPlanByCodeWithPrice(r.Context(), planCode)
	if err != nil {
		h.log.Error("failed to get plan by code", zap.String("plan_code", planCode), zap.Error(err))
		h.respondWithError(w, http.StatusNotFound, "plan not found")
		return
	}

	h.respondWithJSON(w, http.StatusOK, planResponse{Plan: plan})
}

// CreatePlan godoc
// @Summary Create subscription plan (admin)
// @Description Creates a new subscription plan. Requires platform owner access.
// @Tags Plans
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 201 {object} planResponse
// @Failure 400 {object} errorResponse
// @Failure 403 {object} errorResponse
// @Failure 500 {object} errorResponse
// @Router /admin/plans [post]
func (h *PlanHandler) CreatePlan(w http.ResponseWriter, r *http.Request) {
	var plan plans.SubscriptionPlan
	if err := json.NewDecoder(r.Body).Decode(&plan); err != nil {
		h.respondWithError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if plan.ID == uuid.Nil {
		plan.ID = uuid.New()
	}

	if err := h.service.CreatePlan(r.Context(), &plan); err != nil {
		h.respondWithError(w, http.StatusInternalServerError, "failed to create plan")
		return
	}

	h.respondWithJSON(w, http.StatusCreated, planResponse{Plan: &plan})
}

// UpdatePlan godoc
// @Summary Update subscription plan (admin)
// @Description Updates an existing subscription plan. Requires platform owner access.
// @Tags Plans
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "Plan UUID"
// @Success 200 {object} planResponse
// @Failure 400 {object} errorResponse
// @Failure 403 {object} errorResponse
// @Failure 500 {object} errorResponse
// @Router /admin/plans/{id} [put]
func (h *PlanHandler) UpdatePlan(w http.ResponseWriter, r *http.Request) {
	planIDStr := chi.URLParam(r, "id")
	planID, err := uuid.Parse(planIDStr)
	if err != nil {
		h.respondWithError(w, http.StatusBadRequest, "invalid plan ID format")
		return
	}

	var plan plans.SubscriptionPlan
	if err := json.NewDecoder(r.Body).Decode(&plan); err != nil {
		h.respondWithError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	plan.ID = planID

	if err := h.service.UpdatePlan(r.Context(), &plan); err != nil {
		h.respondWithError(w, http.StatusInternalServerError, "failed to update plan")
		return
	}

	h.respondWithJSON(w, http.StatusOK, planResponse{Plan: &plan})
}

// DeletePlan godoc
// @Summary Delete subscription plan (admin)
// @Description Deletes a subscription plan. Requires platform owner access.
// @Tags Plans
// @Security BearerAuth
// @Param id path string true "Plan UUID"
// @Success 204
// @Failure 400 {object} errorResponse
// @Failure 403 {object} errorResponse
// @Failure 500 {object} errorResponse
// @Router /admin/plans/{id} [delete]
func (h *PlanHandler) DeletePlan(w http.ResponseWriter, r *http.Request) {
	planIDStr := chi.URLParam(r, "id")
	planID, err := uuid.Parse(planIDStr)
	if err != nil {
		h.respondWithError(w, http.StatusBadRequest, "invalid plan ID format")
		return
	}

	if err := h.service.DeletePlan(r.Context(), planID); err != nil {
		h.respondWithError(w, http.StatusInternalServerError, "failed to delete plan")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *PlanHandler) respondWithError(w http.ResponseWriter, code int, message string) {
	h.respondWithJSON(w, code, errorResponse{Error: message})
}

func (h *PlanHandler) respondWithJSON(w http.ResponseWriter, code int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(payload)
}

// Response types
type listPlansResponse struct {
	Plans []*plans.SubscriptionPlan `json:"plans"`
	Count int                       `json:"count"`
}

type planResponse struct {
	Plan *plans.SubscriptionPlan `json:"plan"`
}

type errorResponse struct {
	Error string `json:"error"`
}
