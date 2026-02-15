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
	log        *zap.Logger
	repository plans.Repository
}

// NewPlanHandler creates a new plan handler.
func NewPlanHandler(log *zap.Logger, repo plans.Repository) *PlanHandler {
	return &PlanHandler{
		log:        log.Named("plan.handler"),
		repository: repo,
	}
}

// ListPlans returns all available subscription plans.
// @Summary List subscription plans
// @Description Get all available subscription plans, optionally filtered by active status
// @Tags plans
// @Accept json
// @Produce json
// @Param active query bool false "Filter by active status"
// @Success 200 {object} listPlansResponse
// @Failure 500 {object} errorResponse
// @Router /api/v1/plans [get]
func (h *PlanHandler) ListPlans(w http.ResponseWriter, r *http.Request) {
	activeOnly := false
	if activeStr := r.URL.Query().Get("active"); activeStr != "" {
		var err error
		activeOnly, err = strconv.ParseBool(activeStr)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(errorResponse{Error: "invalid active parameter, must be true or false"})
			return
		}
	}

	plansList, err := h.repository.ListPlans(r.Context(), activeOnly)
	if err != nil {
		h.log.Error("failed to list plans", zap.Error(err))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(errorResponse{Error: "failed to retrieve plans"})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(listPlansResponse{
		Plans: plansList,
		Count: len(plansList),
	})
}

// GetPlan returns a specific plan by ID.
// @Summary Get subscription plan by ID
// @Description Get a specific subscription plan by its UUID
// @Tags plans
// @Accept json
// @Produce json
// @Param id path string true "Plan ID (UUID)"
// @Success 200 {object} planResponse
// @Failure 400 {object} errorResponse
// @Failure 404 {object} errorResponse
// @Failure 500 {object} errorResponse
// @Router /api/v1/plans/{id} [get]
func (h *PlanHandler) GetPlan(w http.ResponseWriter, r *http.Request) {
	planIDStr := chi.URLParam(r, "id")
	planID, err := uuid.Parse(planIDStr)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(errorResponse{Error: "invalid plan ID format"})
		return
	}

	plan, err := h.repository.FindPlanByID(r.Context(), planID)
	if err != nil {
		h.log.Error("failed to get plan", zap.String("plan_id", planIDStr), zap.Error(err))
		if err.Error() == "plans: plan not found" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(errorResponse{Error: "plan not found"})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(errorResponse{Error: "failed to retrieve plan"})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(planResponse{Plan: plan})
}

// GetPlanByCode returns a specific plan by plan code.
// @Summary Get subscription plan by code
// @Description Get a specific subscription plan by its plan code (e.g., STARTER, GROWTH, PROFESSIONAL)
// @Tags plans
// @Accept json
// @Produce json
// @Param code path string true "Plan code"
// @Success 200 {object} planResponse
// @Failure 400 {object} errorResponse
// @Failure 404 {object} errorResponse
// @Failure 500 {object} errorResponse
// @Router /api/v1/plans/code/{code} [get]
func (h *PlanHandler) GetPlanByCode(w http.ResponseWriter, r *http.Request) {
	planCode := chi.URLParam(r, "code")
	if planCode == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(errorResponse{Error: "plan code is required"})
		return
	}

	plan, err := h.repository.FindPlanByCode(r.Context(), planCode)
	if err != nil {
		h.log.Error("failed to get plan by code", zap.String("plan_code", planCode), zap.Error(err))
		if err.Error() == "plans: plan not found" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(errorResponse{Error: "plan not found"})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(errorResponse{Error: "failed to retrieve plan"})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(planResponse{Plan: plan})
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
