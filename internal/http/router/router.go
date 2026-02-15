package router

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"go.uber.org/zap"

	httpware "github.com/Bengo-Hub/httpware"
	authclient "github.com/Bengo-Hub/shared-auth-client"
	handlers "github.com/bengobox/subscription-service/internal/http/handlers"
)

func New(log *zap.Logger, health *handlers.HealthHandler, planHandler *handlers.PlanHandler, subscriptionHandler *handlers.SubscriptionHandler, apiKey string, authMiddleware *authclient.AuthMiddleware, allowedOrigins []string) http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.RealIP)
	r.Use(httpware.RequestID)
	r.Use(httpware.Tenant)
	r.Use(httpware.Logging(log))
	r.Use(httpware.Recover(log))
	r.Use(middleware.Timeout(30 * time.Second))
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   allowedOrigins,
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Authorization", "Content-Type", "X-Tenant-ID", "X-Request-ID", "X-API-Key"},
		ExposedHeaders:   []string{"Link", "X-Request-ID"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	r.Route("/api/v1", func(api chi.Router) {
		// Health endpoints (public)
		api.Get("/healthz", health.Liveness)
		api.Get("/readyz", health.Readiness)
		api.Get("/metrics", health.Metrics)

		// Redirect root path to Swagger documentation
		api.Get("/", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/v1/docs/", http.StatusMovedPermanently)
		})

		// Apply auth middleware if configured, otherwise allow API key
		if authMiddleware != nil {
			api.Use(authMiddleware.RequireAuth)
		} else if apiKey != "" {
			api.Use(func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Header.Get("X-API-Key") != apiKey {
						http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
						return
					}
					next.ServeHTTP(w, r)
				})
			})
		}

		// Plan routes
		if planHandler != nil {
			api.Route("/plans", func(plans chi.Router) {
				plans.Get("/", planHandler.ListPlans)
				plans.Get("/code/{code}", planHandler.GetPlanByCode)
				plans.Get("/{id}", planHandler.GetPlan)
			})
		}

		// Tenant subscription routes
		if subscriptionHandler != nil {
			api.Route("/tenants", func(tenants chi.Router) {
				tenants.Route("/{tenant_id}", func(tenant chi.Router) {
					// Read-only
					tenant.Get("/subscription", subscriptionHandler.GetTenantSubscription)
					tenant.Get("/features/{feature_code}/check", subscriptionHandler.CheckFeature)

					// Lifecycle
					tenant.Post("/subscription", subscriptionHandler.CreateSubscription)
					tenant.Put("/subscription/plan", subscriptionHandler.ChangePlan)
					tenant.Post("/subscription/cancel", subscriptionHandler.CancelSubscription)
					tenant.Post("/subscription/renew", subscriptionHandler.RenewSubscription)

					// Product subscriptions
					tenant.Get("/products", subscriptionHandler.ListProducts)
					tenant.Post("/products/{code}/activate", subscriptionHandler.ActivateProduct)
					tenant.Post("/products/{code}/deactivate", subscriptionHandler.DeactivateProduct)
				})
			})
		}
	})

	return r
}
