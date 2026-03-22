package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

)

// UsageHandler accepts usage metric reports from microservices.
// Storage uses raw SQL on the usage_events table (created by Ent schema UsageEvent after codegen).
type UsageHandler struct {
	log *zap.Logger
	db  *pgxpool.Pool
}

// NewUsageHandler creates a new UsageHandler.
func NewUsageHandler(log *zap.Logger, db *pgxpool.Pool) *UsageHandler {
	return &UsageHandler{
		log: log.Named("usage.handler"),
		db:  db,
	}
}

type reportUsageRequest struct {
	MetricType  string         `json:"metric_type"`
	ServiceName string         `json:"service_name"`
	Value       float64        `json:"value"`
	PeriodStart *time.Time     `json:"period_start,omitempty"`
	PeriodEnd   *time.Time     `json:"period_end,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

// ReportUsage ingests a usage event for a tenant.
// POST /api/v1/usage/report
func (h *UsageHandler) ReportUsage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	tenantIDStr := resolveTenantID(r)
	if tenantIDStr == "" {
		http.Error(w, `{"error":"tenant_id required"}`, http.StatusBadRequest)
		return
	}
	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		http.Error(w, `{"error":"invalid tenant_id"}`, http.StatusBadRequest)
		return
	}

	var req reportUsageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	if req.MetricType == "" || req.ServiceName == "" {
		http.Error(w, `{"error":"metric_type and service_name are required"}`, http.StatusBadRequest)
		return
	}
	if req.Metadata == nil {
		req.Metadata = map[string]any{}
	}

	metadataJSON, _ := json.Marshal(req.Metadata)
	eventID := uuid.New()
	now := time.Now()

	_, err = h.db.Exec(ctx, `
		INSERT INTO usage_events (id, tenant_id, metric_type, service_name, value, period_start, period_end, metadata, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`, eventID, tenantID, req.MetricType, req.ServiceName, req.Value, req.PeriodStart, req.PeriodEnd, metadataJSON, now)
	if err != nil {
		h.log.Error("failed to save usage event", zap.Error(err))
		http.Error(w, `{"error":"failed to record usage"}`, http.StatusInternalServerError)
		return
	}

	h.log.Info("usage event recorded",
		zap.String("tenant_id", tenantIDStr),
		zap.String("metric_type", req.MetricType),
		zap.String("service", req.ServiceName),
		zap.Float64("value", req.Value),
	)

	writeJSON(w, http.StatusCreated, map[string]any{
		"id":          eventID.String(),
		"tenant_id":   tenantID.String(),
		"metric_type": req.MetricType,
		"service":     req.ServiceName,
		"value":       req.Value,
		"created_at":  now.Format(time.RFC3339),
	})
}

// GetUsageSummary returns aggregated usage metrics for the tenant in the given period.
// GET /api/v1/usage?from=&to=&service=
func (h *UsageHandler) GetUsageSummary(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tenantIDStr := resolveTenantID(r)
	if tenantIDStr == "" {
		http.Error(w, `{"error":"tenant_id required"}`, http.StatusBadRequest)
		return
	}
	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		http.Error(w, `{"error":"invalid tenant_id"}`, http.StatusBadRequest)
		return
	}

	from, to := parseUsageDateRange(r)
	serviceFilter := r.URL.Query().Get("service")

	type metricSummary struct {
		MetricType string  `json:"metric_type"`
		Total      float64 `json:"total"`
		EventCount int     `json:"event_count"`
	}

	metrics, err := h.queryUsageSummary(ctx, tenantID, from, to, serviceFilter)
	if err != nil {
		h.log.Error("failed to query usage summary", zap.Error(err))
		http.Error(w, `{"error":"failed to retrieve usage"}`, http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"tenant_id":    tenantIDStr,
		"period_from":  from.Format("2006-01-02"),
		"period_to":    to.Format("2006-01-02"),
		"metrics":      metrics,
	})
}

type metricRow struct {
	MetricType string  `json:"metric_type"`
	Total      float64 `json:"total"`
	EventCount int     `json:"event_count"`
}

func (h *UsageHandler) queryUsageSummary(ctx context.Context, tenantID uuid.UUID, from, to time.Time, serviceFilter string) ([]metricRow, error) {
	query := `
		SELECT metric_type, SUM(value) as total, COUNT(*) as event_count
		FROM usage_events
		WHERE tenant_id = $1 AND created_at >= $2 AND created_at <= $3
	`
	args := []any{tenantID, from, to}
	if serviceFilter != "" {
		query += " AND service_name = $4 GROUP BY metric_type ORDER BY metric_type"
		args = append(args, serviceFilter)
	} else {
		query += " GROUP BY metric_type ORDER BY metric_type"
	}

	rows, err := h.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []metricRow
	for rows.Next() {
		var row metricRow
		if err := rows.Scan(&row.MetricType, &row.Total, &row.EventCount); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

func parseUsageDateRange(r *http.Request) (time.Time, time.Time) {
	now := time.Now()
	from := now.AddDate(0, -1, 0)
	to := now
	if s := r.URL.Query().Get("from"); s != "" {
		if t, err := time.Parse("2006-01-02", s); err == nil {
			from = t
		}
	}
	if s := r.URL.Query().Get("to"); s != "" {
		if t, err := time.Parse("2006-01-02", s); err == nil {
			to = t.Add(24*time.Hour - time.Nanosecond)
		}
	}
	return from, to
}
