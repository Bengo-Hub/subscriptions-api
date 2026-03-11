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

// ListPlans returns all available subscription plans.
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

	plansList, err := h.service.ListPlansWithPrices(r.Context(), activeOnly)
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

// GetPlan returns a specific plan by ID.
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

// GetPlanByCode returns a specific plan by plan code.
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

// CreatePlan creates a new subscription plan.
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

// UpdatePlan updates an existing subscription plan.
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

// DeletePlan deletes a subscription plan.
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
