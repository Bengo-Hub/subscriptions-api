package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

type dbPinger interface {
	Ping(context.Context) error
}

// HealthHandler exposes service health endpoints.
type HealthHandler struct {
	log    *zap.Logger
	db     dbPinger
	cache  *redis.Client
	events *nats.Conn
}

func NewHealthHandler(log *zap.Logger, db dbPinger, cache *redis.Client, events *nats.Conn) *HealthHandler {
	return &HealthHandler{log: log, db: db, cache: cache, events: events}
}

type livenessResponse struct {
	Status  string `json:"status" example:"ok"`
	Service string `json:"service" example:"subscription-service"`
}

type readinessResponse struct {
	Status       string            `json:"status" example:"OK"`
	Dependencies map[string]string `json:"dependencies"`
}

// Liveness returns the service status without touching external deps.
func (h *HealthHandler) Liveness(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(livenessResponse{Status: "ok", Service: "subscription-service"})
}

// Readiness checks upstream dependencies.
func (h *HealthHandler) Readiness(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	issues := map[string]string{}

	if h.db != nil {
		if err := h.db.Ping(ctx); err != nil {
			issues["postgres"] = err.Error()
		}
	}

	if h.cache != nil {
		if err := h.cache.Ping(ctx).Err(); err != nil {
			issues["redis"] = err.Error()
		}
	}

	if h.events != nil && !h.events.IsConnected() {
		issues["nats"] = "not connected"
	}

	status := http.StatusOK
	if len(issues) > 0 {
		status = http.StatusServiceUnavailable
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(readinessResponse{
		Status:       http.StatusText(status),
		Dependencies: issues,
	})
}

// Metrics exposes Prometheus metrics.
func (h *HealthHandler) Metrics(w http.ResponseWriter, r *http.Request) {
	promhttp.Handler().ServeHTTP(w, r)
}
