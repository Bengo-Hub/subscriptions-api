package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/tenantsubscription"
	"github.com/bengobox/subscription-service/internal/modules/subscriptions"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"
	httpware "github.com/Bengo-Hub/httpware"
)

type SubscriptionHandler struct {
	log     *zap.Logger
	client  *ent.Client
	service *subscriptions.Service
}

func NewSubscriptionHandler(log *zap.Logger, client *ent.Client, svc *subscriptions.Service) *SubscriptionHandler {
	return &SubscriptionHandler{
		log:     log.Named("subscription.handler"),
		client:  client,
		service: svc,
	}
}

// Get returns the current tenant's subscription.
func (h *SubscriptionHandler) Get(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tenantIDStr := resolveTenantID(r)
	if tenantIDStr == "" {
		h.respondWithError(w, http.StatusBadRequest, "tenant_id required")
		return
	}

	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		h.respondWithError(w, http.StatusBadRequest, "invalid tenant id")
		return
	}

	sub, err := h.service.GetSubscriptionResult(ctx, tenantID)
	if err != nil {
		h.log.Error("failed to get subscription", zap.Error(err))
		h.respondWithError(w, http.StatusNotFound, "subscription not found")
		return
	}

	h.respondWithJSON(w, http.StatusOK, sub)
}

// Create provisions a new subscription.
func (h *SubscriptionHandler) Create(w http.ResponseWriter, r *http.Request) {
	var in subscriptions.CreateInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		h.respondWithError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	ctx := r.Context()
	tenantIDStr := httpware.GetTenantID(ctx)
	if tenantIDStr != "" && !httpware.IsPlatformOwner(ctx) {
		// Non-platform owners are locked to their own tenant
		tid, _ := uuid.Parse(tenantIDStr)
		in.TenantID = tid
	}

	if in.TenantID == uuid.Nil {
		h.respondWithError(w, http.StatusBadRequest, "tenant_id required")
		return
	}

	sub, err := h.service.CreateSubscription(ctx, in)
	if err != nil {
		h.log.Error("failed to create subscription", zap.Error(err))
		h.respondWithError(w, http.StatusInternalServerError, err.Error())
		return
	}

	h.respondWithJSON(w, http.StatusCreated, sub)
}

// ChangePlan upgrades or downgrades the subscription.
func (h *SubscriptionHandler) ChangePlan(w http.ResponseWriter, r *http.Request) {
	var in subscriptions.ChangePlanInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		h.respondWithError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	ctx := r.Context()
	tenantIDStr := httpware.GetTenantID(ctx)
	if tenantIDStr != "" && !httpware.IsPlatformOwner(ctx) {
		tid, _ := uuid.Parse(tenantIDStr)
		in.TenantID = tid
	}

	if in.TenantID == uuid.Nil {
		h.respondWithError(w, http.StatusBadRequest, "tenant_id required")
		return
	}

	sub, err := h.service.ChangePlan(ctx, in)
	if err != nil {
		h.log.Error("failed to change plan", zap.Error(err))
		h.respondWithError(w, http.StatusInternalServerError, err.Error())
		return
	}

	h.respondWithJSON(w, http.StatusOK, sub)
}

// Initiate starts the checkout flow for a subscription.
func (h *SubscriptionHandler) Initiate(w http.ResponseWriter, r *http.Request) {
	var in subscriptions.InitiateSubscriptionInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		h.respondWithError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	ctx := r.Context()
	tenantIDStr := httpware.GetTenantID(ctx)
	if tenantIDStr != "" && !httpware.IsPlatformOwner(ctx) {
		tid, _ := uuid.Parse(tenantIDStr)
		in.TenantID = tid
	}

	if in.TenantID == uuid.Nil {
		h.respondWithError(w, http.StatusBadRequest, "tenant_id required")
		return
	}

	result, err := h.service.InitiateSubscription(ctx, in)
	if err != nil {
		h.log.Error("failed to initiate subscription", zap.Error(err))
		h.respondWithError(w, http.StatusInternalServerError, err.Error())
		return
	}

	h.respondWithJSON(w, http.StatusOK, result)
}

// GetByTenantID returns subscription data for a specific tenant by ID.
// This is a S2S endpoint — intended for service-to-service calls (e.g. auth-api JWT enrichment).
// Requires API key auth (platform service key).
func (h *SubscriptionHandler) GetByTenantID(w http.ResponseWriter, r *http.Request) {
	tenantIDStr := chi.URLParam(r, "tenant_id")
	if tenantIDStr == "" {
		h.respondWithError(w, http.StatusBadRequest, "tenant_id required")
		return
	}

	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		h.respondWithError(w, http.StatusBadRequest, "invalid tenant_id")
		return
	}

	sub, err := h.service.GetSubscriptionResult(r.Context(), tenantID)
	if err != nil {
		h.log.Debug("subscription not found for tenant", zap.String("tenant_id", tenantIDStr), zap.Error(err))
		h.respondWithError(w, http.StatusNotFound, "subscription not found")
		return
	}

	h.respondWithJSON(w, http.StatusOK, sub)
}

// GetSettings returns subscription settings (auto-renew, notification preferences).
func (h *SubscriptionHandler) GetSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tenantIDStr := httpware.GetTenantID(ctx)
	if tenantIDStr == "" {
		h.respondWithError(w, http.StatusBadRequest, "tenant_id required")
		return
	}
	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		h.respondWithError(w, http.StatusBadRequest, "invalid tenant_id")
		return
	}

	sub, err := h.client.TenantSubscription.Query().
		Where(tenantsubscription.TenantIDEQ(tenantID)).
		Only(ctx)
	if err != nil {
		h.respondWithError(w, http.StatusNotFound, "subscription not found")
		return
	}

	// Extract settings from metadata
	metadata := sub.Metadata
	if metadata == nil {
		metadata = map[string]any{}
	}

	settings := map[string]any{
		"autoRenew":          metadata["auto_renew"] != false,
		"emailNotifications": metadata["email_notifications"] != false,
		"usageAlerts":        metadata["usage_alerts"] != false,
		"usageThreshold":     metadata["usage_threshold"],
		"billingEmail":       metadata["billing_email"],
	}

	h.respondWithJSON(w, http.StatusOK, settings)
}

// UpdateSettings updates subscription settings.
func (h *SubscriptionHandler) UpdateSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tenantIDStr := httpware.GetTenantID(ctx)
	if tenantIDStr == "" {
		h.respondWithError(w, http.StatusBadRequest, "tenant_id required")
		return
	}
	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		h.respondWithError(w, http.StatusBadRequest, "invalid tenant_id")
		return
	}

	var req map[string]any
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.respondWithError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Get existing subscription
	sub, err := h.client.TenantSubscription.Query().
		Where(tenantsubscription.TenantIDEQ(tenantID)).
		Only(ctx)
	if err != nil {
		h.respondWithError(w, http.StatusNotFound, "subscription not found")
		return
	}

	// Merge settings into metadata
	metadata := sub.Metadata
	if metadata == nil {
		metadata = map[string]any{}
	}
	for k, v := range req {
		metadata[k] = v
	}

	_, err = h.client.TenantSubscription.UpdateOneID(sub.ID).
		SetMetadata(metadata).
		Save(ctx)
	if err != nil {
		h.log.Error("failed to update settings", zap.Error(err))
		h.respondWithError(w, http.StatusInternalServerError, "failed to update settings")
		return
	}

	h.respondWithJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

func (h *SubscriptionHandler) respondWithError(w http.ResponseWriter, code int, message string) {
	h.respondWithJSON(w, code, map[string]string{"error": message})
}

func (h *SubscriptionHandler) respondWithJSON(w http.ResponseWriter, code int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(payload)
}
