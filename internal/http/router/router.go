package router

import (
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"go.uber.org/zap"

	"github.com/bengobox/subscription-service/internal/http/handlers"
	authclient "github.com/Bengo-Hub/shared-auth-client"
	httpware "github.com/Bengo-Hub/httpware"
	"context"
	"net/http"
)

// New creates and configures a new chi router.
func New(
	log *zap.Logger,
	healthHandler *handlers.HealthHandler,
	planHandler *handlers.PlanHandler,
	subscriptionHandler *handlers.SubscriptionHandler,
	addonHandler *handlers.AddonHandler,
	apiKey string,
	authMiddleware *authclient.AuthMiddleware,
	allowedOrigins []string,
) *chi.Mux {
	r := chi.NewRouter()

	// Standard middleware
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	// CORS
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   allowedOrigins,
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-CSRF-Token", "X-API-Key", "X-Tenant-ID", "X-Tenant-Slug"},
		ExposedHeaders:   []string{"Link"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/health", healthHandler.Health)

		// Public routes
		r.Get("/plans", planHandler.ListPlans)
		r.Get("/plans/{id}", planHandler.GetPlan)
		r.Get("/plans/code/{code}", planHandler.GetPlanByCode)

		// Protected routes
		r.Group(func(r chi.Router) {
			if authMiddleware != nil {
				r.Use(authMiddleware.RequireAuth)
			}

			// Tenant context (optional for bypass)
			r.Use(httpware.TenantV2(httpware.TenantConfig{
				ClaimsExtractor: func(ctx context.Context) (tenantID, tenantSlug string, isPlatformOwner bool, ok bool) {
					claims, found := authclient.ClaimsFromContext(ctx)
					if !found {
						return "", "", false, false
					}
					// Slug-based platform owner check
					isPO := claims.GetTenantSlug() == "codevertex"
					return claims.TenantID, claims.GetTenantSlug(), isPO, true
				},
				URLParamFunc: chi.URLParam,
				Required:     false,
			}))

			// Tenant-scoped subscription management
			r.Route("/subscription", func(r chi.Router) {
				r.Get("/", subscriptionHandler.Get)
				r.Post("/", subscriptionHandler.Create)
				r.Put("/plan", subscriptionHandler.ChangePlan)
				r.Post("/initiate", subscriptionHandler.Initiate)
			})

			// Admin routes for plans
			r.Route("/admin/plans", func(r chi.Router) {
				r.Post("/", planHandler.CreatePlan)
				r.Put("/{id}", planHandler.UpdatePlan)
				r.Delete("/{id}", planHandler.DeletePlan)
			})

			// Admin list all subscriptions
			r.Get("/admin/subscriptions", func(w http.ResponseWriter, r *http.Request) {
				if !httpware.IsPlatformOwner(r.Context()) {
					http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
					return
				}
				// Implement list all logic if needed
			})
		})
	})

	return r
}
