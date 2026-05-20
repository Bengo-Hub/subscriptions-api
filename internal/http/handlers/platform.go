package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/serviceconfig"
	"github.com/bengobox/subscription-service/internal/ent/subscriptionplan"
	"github.com/bengobox/subscription-service/internal/ent/tenantsubscription"
)

// PlatformHandler handles platform admin endpoints.
type PlatformHandler struct {
	log    *zap.Logger
	client *ent.Client
}

// NewPlatformHandler creates a new platform handler.
func NewPlatformHandler(log *zap.Logger, client *ent.Client) *PlatformHandler {
	return &PlatformHandler{
		log:    log.Named("platform.handler"),
		client: client,
	}
}

// GetPlatformStats godoc
// @Summary Get platform statistics (admin)
// @Description Returns aggregated platform stats: total plans, subscriptions, MRR. Requires platform owner.
// @Tags Platform
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{}
// @Failure 403 {object} map[string]string
// @Router /platform/stats [get]
func (h *PlatformHandler) GetPlatformStats(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	totalPlans, err := h.client.SubscriptionPlan.Query().
		Where(subscriptionplan.IsActive(true)).
		Count(ctx)
	if err != nil {
		h.log.Error("failed to count plans", zap.Error(err))
		totalPlans = 0
	}

	totalSubscriptions, err := h.client.TenantSubscription.Query().Count(ctx)
	if err != nil {
		h.log.Error("failed to count subscriptions", zap.Error(err))
		totalSubscriptions = 0
	}

	activeSubscriptions, err := h.client.TenantSubscription.Query().
		Where(tenantsubscription.StatusIn("ACTIVE", "TRIAL")).
		Count(ctx)
	if err != nil {
		h.log.Error("failed to count active subscriptions", zap.Error(err))
		activeSubscriptions = 0
	}

	// Calculate MRR from active plans
	activeSubs, err := h.client.TenantSubscription.Query().
		Where(tenantsubscription.StatusIn("ACTIVE", "TRIAL")).
		WithPlan().
		All(ctx)
	mrr := 0.0
	if err == nil {
		for _, s := range activeSubs {
			if s.Edges.Plan != nil {
				mrr += s.Edges.Plan.BasePrice
			}
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"totalPlans":          totalPlans,
		"totalSubscriptions":  totalSubscriptions,
		"activeSubscriptions": activeSubscriptions,
		"mrr":                 mrr,
		"currency":            "KES",
	})
}

// ListAllSubscriptions godoc
// @Summary List all subscriptions (admin)
// @Description Returns paginated list of all tenant subscriptions. Requires platform owner.
// @Tags Platform
// @Produce json
// @Security BearerAuth
// @Param page query int false "Page number (default 1)"
// @Param pageSize query int false "Page size (default 20)"
// @Param status query string false "Filter by status (ACTIVE, TRIAL, EXPIRED, etc.)"
// @Success 200 {object} map[string]interface{}
// @Failure 403 {object} map[string]string
// @Router /admin/subscriptions [get]
func (h *PlatformHandler) ListAllSubscriptions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("pageSize"))
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize

	query := h.client.TenantSubscription.Query().
		WithPlan().
		WithTenant().
		WithProductSubscriptions()

	if status := r.URL.Query().Get("status"); status != "" {
		query = query.Where(tenantsubscription.StatusEQ(tenantsubscription.Status(status)))
	}

	total, err := query.Clone().Count(ctx)
	if err != nil {
		h.log.Error("failed to count subscriptions", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	subs, err := query.
		Offset(offset).
		Limit(pageSize).
		Order(ent.Desc(tenantsubscription.FieldCreatedAt)).
		All(ctx)
	if err != nil {
		h.log.Error("failed to list subscriptions", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	type subRow struct {
		ID         string `json:"id"`
		TenantID   string `json:"tenantId"`
		TenantName string `json:"tenantName"`
		PlanCode   string `json:"planCode"`
		PlanName   string `json:"planName"`
		Status     string `json:"status"`
		CreatedAt  string `json:"createdAt"`
	}

	data := make([]subRow, 0, len(subs))
	for _, s := range subs {
		row := subRow{
			ID:        s.ID.String(),
			TenantID:  s.TenantID.String(),
			Status:    string(s.Status),
			CreatedAt: s.CreatedAt.Format("2006-01-02T15:04:05Z"),
		}
		if s.Edges.Plan != nil {
			row.PlanCode = s.Edges.Plan.PlanCode
			row.PlanName = s.Edges.Plan.Name
		}
		if s.Edges.Tenant != nil {
			row.TenantName = s.Edges.Tenant.Name
		}
		data = append(data, row)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"data":     data,
		"total":    total,
		"page":     page,
		"pageSize": pageSize,
	})
}

// AssignPlanToTenant godoc
// @Summary Assign subscription plan to a tenant (admin)
// @Description Creates or updates a tenant's subscription to the given plan. Requires platform owner.
// @Tags Platform
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param tenant_id path string true "Tenant UUID"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /admin/tenants/{tenant_id}/subscription [post]
func (h *PlatformHandler) AssignPlanToTenant(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	tenantIDStr := chi.URLParam(r, "tenant_id")
	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid tenant_id"})
		return
	}

	var body struct {
		PlanCode   string `json:"planCode"`
		StartTrial bool   `json:"startTrial"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if body.PlanCode == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "planCode is required"})
		return
	}

	plan, err := h.client.SubscriptionPlan.Query().
		Where(subscriptionplan.PlanCode(body.PlanCode)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "plan not found"})
			return
		}
		h.log.Error("failed to find plan", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	status := tenantsubscription.StatusACTIVE
	if body.StartTrial {
		status = tenantsubscription.StatusTRIAL
	}

	now := time.Now()
	periodEnd := now.AddDate(0, 1, 0)

	// Check if subscription already exists for tenant
	existing, err := h.client.TenantSubscription.Query().
		Where(tenantsubscription.TenantID(tenantID)).
		Only(ctx)
	if err != nil && !ent.IsNotFound(err) {
		h.log.Error("failed to query existing subscription", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	var sub interface{}
	if existing != nil {
		// Update existing subscription
		updated, err := h.client.TenantSubscription.UpdateOneID(existing.ID).
			SetPlanID(plan.ID).
			SetStatus(status).
			SetCurrentPeriodStart(now).
			SetCurrentPeriodEnd(periodEnd).
			Save(ctx)
		if err != nil {
			h.log.Error("failed to update tenant subscription", zap.Error(err))
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to update subscription"})
			return
		}
		sub = updated
	} else {
		// Create new subscription
		created, err := h.client.TenantSubscription.Create().
			SetTenantID(tenantID).
			SetPlanID(plan.ID).
			SetStatus(status).
			SetCurrentPeriodStart(now).
			SetCurrentPeriodEnd(periodEnd).
			Save(ctx)
		if err != nil {
			h.log.Error("failed to create tenant subscription", zap.Error(err))
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create subscription"})
			return
		}
		sub = created
	}

	writeJSON(w, http.StatusOK, sub)
}

// UpdateSubscriptionStatus godoc
// @Summary Update subscription status (admin)
// @Description Updates the status of a tenant subscription. Requires platform owner.
// @Tags Platform
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "Subscription UUID"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /admin/subscriptions/{id}/status [put]
func (h *PlatformHandler) UpdateSubscriptionStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}

	var body struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if body.Status == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "status is required"})
		return
	}

	sub, err := h.client.TenantSubscription.UpdateOneID(id).
		SetStatus(tenantsubscription.Status(body.Status)).
		Save(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "subscription not found"})
			return
		}
		h.log.Error("failed to update subscription status", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to update subscription"})
		return
	}

	writeJSON(w, http.StatusOK, sub)
}

// ListTenants godoc
// @Summary List all tenants (admin)
// @Description Returns all tenants with their current subscription status. Requires platform owner.
// @Tags Platform
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{}
// @Failure 500 {object} map[string]string
// @Router /admin/tenants [get]
func (h *PlatformHandler) ListTenants(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	tenants, err := h.client.Tenant.Query().
		WithSubscriptions(func(q *ent.TenantSubscriptionQuery) {
			q.WithPlan()
		}).
		All(ctx)
	if err != nil {
		h.log.Error("failed to list tenants", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	type tenantRow struct {
		ID                 string `json:"id"`
		Name               string `json:"name"`
		Slug               string `json:"slug"`
		SubscriptionStatus string `json:"subscriptionStatus,omitempty"`
		PlanName           string `json:"planName,omitempty"`
		PlanCode           string `json:"planCode,omitempty"`
	}

	data := make([]tenantRow, 0, len(tenants))
	for _, t := range tenants {
		row := tenantRow{
			ID:   t.ID.String(),
			Name: t.Name,
			Slug: t.Slug,
		}
		if len(t.Edges.Subscriptions) > 0 {
			sub := t.Edges.Subscriptions[0]
			row.SubscriptionStatus = string(sub.Status)
			if sub.Edges.Plan != nil {
				row.PlanName = sub.Edges.Plan.Name
				row.PlanCode = sub.Edges.Plan.PlanCode
			}
		}
		data = append(data, row)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"data":  data,
		"total": len(data),
	})
}

// ListServiceConfigs returns all platform-level (tenant_id = nil) service configs.
func (h *PlatformHandler) ListServiceConfigs(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	configs, err := h.client.ServiceConfig.Query().
		Where(serviceconfig.TenantIDIsNil()).
		Order(ent.Asc(serviceconfig.FieldConfigKey)).
		All(ctx)
	if err != nil {
		h.log.Error("failed to list service configs", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	type configRow struct {
		ID          string `json:"id"`
		ConfigKey   string `json:"configKey"`
		ConfigValue string `json:"configValue"`
		ConfigType  string `json:"configType"`
		Description string `json:"description"`
		IsSecret    bool   `json:"isSecret"`
		UpdatedAt   string `json:"updatedAt"`
	}

	data := make([]configRow, 0, len(configs))
	for _, c := range configs {
		val := c.ConfigValue
		if c.IsSecret {
			val = "••••••••"
		}
		data = append(data, configRow{
			ID:          c.ID.String(),
			ConfigKey:   c.ConfigKey,
			ConfigValue: val,
			ConfigType:  c.ConfigType,
			Description: c.Description,
			IsSecret:    c.IsSecret,
			UpdatedAt:   c.UpdatedAt.Format("2006-01-02T15:04:05Z"),
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{"data": data, "total": len(data)})
}

// CreateServiceConfig creates a new platform-level service config.
func (h *PlatformHandler) CreateServiceConfig(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body struct {
		ConfigKey   string `json:"configKey"`
		ConfigValue string `json:"configValue"`
		ConfigType  string `json:"configType"`
		Description string `json:"description"`
		IsSecret    bool   `json:"isSecret"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if body.ConfigKey == "" || body.ConfigValue == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "configKey and configValue are required"})
		return
	}
	if body.ConfigType == "" {
		body.ConfigType = "string"
	}

	id := uuid.NewSHA1(uuid.NameSpaceOID, []byte("sc::"+body.ConfigKey))
	cfg, err := h.client.ServiceConfig.Create().
		SetID(id).
		SetConfigKey(body.ConfigKey).
		SetConfigValue(body.ConfigValue).
		SetConfigType(body.ConfigType).
		SetDescription(body.Description).
		SetIsSecret(body.IsSecret).
		Save(ctx)
	if err != nil {
		h.log.Error("failed to create service config", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create config"})
		return
	}

	writeJSON(w, http.StatusCreated, cfg)
}

// UpdateServiceConfig updates an existing service config's value and metadata.
func (h *PlatformHandler) UpdateServiceConfig(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}

	var body struct {
		ConfigValue string `json:"configValue"`
		ConfigType  string `json:"configType"`
		Description string `json:"description"`
		IsSecret    bool   `json:"isSecret"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if body.ConfigValue == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "configValue is required"})
		return
	}

	upd := h.client.ServiceConfig.UpdateOneID(id).
		SetConfigValue(body.ConfigValue).
		SetDescription(body.Description).
		SetIsSecret(body.IsSecret)
	if body.ConfigType != "" {
		upd = upd.SetConfigType(body.ConfigType)
	}

	cfg, err := upd.Save(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "config not found"})
			return
		}
		h.log.Error("failed to update service config", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to update config"})
		return
	}

	writeJSON(w, http.StatusOK, cfg)
}

// DeleteServiceConfig removes a platform-level service config.
func (h *PlatformHandler) DeleteServiceConfig(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}

	if err := h.client.ServiceConfig.DeleteOneID(id).Exec(ctx); err != nil {
		if ent.IsNotFound(err) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "config not found"})
			return
		}
		h.log.Error("failed to delete service config", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to delete config"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}


