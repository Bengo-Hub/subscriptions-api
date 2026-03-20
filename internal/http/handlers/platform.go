package handlers

import (
	"net/http"
	"strconv"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/subscriptionplan"
	"github.com/bengobox/subscription-service/internal/ent/tenantsubscription"
	"go.uber.org/zap"
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

// GetPlatformStats returns aggregated platform statistics.
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

// ListAllSubscriptions returns paginated list of all subscriptions.
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

// writeJSON is defined in features.go
