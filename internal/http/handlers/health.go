package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/nats-io/nats.go"
	"go.uber.org/zap"
)

type HealthHandler struct {
	log   *zap.Logger
	db    *pgxpool.Pool
	redis *redis.Client
	nats  *nats.Conn
}

func NewHealthHandler(log *zap.Logger, db *pgxpool.Pool, redis *redis.Client, nats *nats.Conn) *HealthHandler {
	return &HealthHandler{
		log:   log.Named("health.handler"),
		db:    db,
		redis: redis,
		nats:  nats,
	}
}

// Health godoc
// @Summary Health check
// @Description Returns service health status
// @Tags Health
// @Produce json
// @Success 200 {object} map[string]string
// @Router /health [get]
func (h *HealthHandler) Health(w http.ResponseWriter, r *http.Request) {
	status := "OK"
	// Basic health check logic
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": status})
}
