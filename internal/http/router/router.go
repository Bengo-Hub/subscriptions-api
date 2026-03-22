package router

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"go.uber.org/zap"

	httpware "github.com/Bengo-Hub/httpware"
	authclient "github.com/Bengo-Hub/shared-auth-client"
	"github.com/bengobox/subscription-service/internal/http/handlers"
	"github.com/bengobox/subscription-service/internal/modules/tenant"
)

// New creates and configures a new chi router.
func New(
	log *zap.Logger,
	healthHandler *handlers.HealthHandler,
	planHandler *handlers.PlanHandler,
	subscriptionHandler *handlers.SubscriptionHandler,
	addonHandler *handlers.AddonHandler,
	featureHandler *handlers.FeatureHandler,
	usageHandler *handlers.UsageHandler,
	serviceChargeHandler *handlers.ServiceChargeHandler,
	billingHandler *handlers.BillingHandler,
	platformHandler *handlers.PlatformHandler,
	rbacHandler *handlers.RBACHandler,
	apiKey string,
	authMiddleware *authclient.AuthMiddleware,
	allowedOrigins []string,
	tenantSyncer *tenant.Syncer,
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
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "Origin", "X-Request-ID", "X-CSRF-Token", "X-API-Key", "X-Tenant-ID", "X-Tenant-Slug"},
		ExposedHeaders:   []string{"Link", "X-RateLimit-Limit", "X-RateLimit-Remaining", "X-RateLimit-Reset", "Retry-After"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/health", healthHandler.Health)
		r.Get("/healthz", healthHandler.Health) // readiness/liveness probe path used by Helm

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

			// JIT tenant sync: ensure tenant exists in local DB when slug is in context (avoids "tenant not found" after SSO)
			if tenantSyncer != nil {
				r.Use(func(next http.Handler) http.Handler {
					return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						slug := httpware.GetTenantSlug(r.Context())
						if slug != "" {
							if _, err := tenantSyncer.SyncTenant(r.Context(), slug); err != nil {
								log.Warn("tenant sync failed during request", zap.String("tenant_slug", slug), zap.Error(err))
							}
						}
						next.ServeHTTP(w, r)
					})
				})
			}

			// Tenant-scoped subscription management
			r.Route("/subscription", func(r chi.Router) {
				r.Get("/", subscriptionHandler.Get)
				r.Post("/", subscriptionHandler.Create)
				r.Put("/plan", subscriptionHandler.ChangePlan)
				r.Post("/initiate", subscriptionHandler.Initiate)
				r.Get("/settings", subscriptionHandler.GetSettings)
				r.Put("/settings", subscriptionHandler.UpdateSettings)
			})

			// Billing
			if billingHandler != nil {
				r.Get("/billing", billingHandler.GetBilling)
			}

			// S2S subscription lookup by tenant ID — used by auth-api for JWT enrichment.
			// Accessible via platform API key (service-to-service) or platform owner JWT.
			r.Get("/tenants/{tenant_id}/subscription", subscriptionHandler.GetByTenantID)

			// Feature gate checks — used by all microservices for Trinity Authorization
			if featureHandler != nil {
				r.Route("/features", func(r chi.Router) {
					r.Get("/", featureHandler.GetEntitlements)
					r.Get("/{code}/check", featureHandler.CheckFeature)
				})
			}

			// Usage reporting — called by microservices to track consumption
			if usageHandler != nil {
				r.Route("/usage", func(r chi.Router) {
					r.Post("/report", usageHandler.ReportUsage)
					r.Get("/", usageHandler.GetUsageSummary)
					r.Get("/summary", usageHandler.GetUsageSummary) // alias for frontend
				})
			}

			// Service charge plans
			if serviceChargeHandler != nil {
				r.Route("/service-charges", func(r chi.Router) {
					r.Get("/plans", serviceChargeHandler.ListServiceChargePlans)
					r.Get("/plans/{code}", serviceChargeHandler.GetServiceChargePlan)
				})
				r.Get("/tenants/{tenantID}/service-charges", serviceChargeHandler.GetTenantServiceCharges)
			}

			// RBAC routes
			if rbacHandler != nil {
				rbacHandler.RegisterRoutes(r)
			}

			// Admin routes for plans (platform admin only)
			r.Route("/admin/plans", func(r chi.Router) {
				r.Use(func(next http.Handler) http.Handler {
					return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if !httpware.IsPlatformOwner(r.Context()) {
							http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
							return
						}
						next.ServeHTTP(w, r)
					})
				})
				r.Post("/", planHandler.CreatePlan)
				r.Put("/{id}", planHandler.UpdatePlan)
				r.Delete("/{id}", planHandler.DeletePlan)
			})

			// Admin list all subscriptions and platform stats
			if platformHandler != nil {
				r.Route("/admin", func(adminRouter chi.Router) {
					adminRouter.Use(func(next http.Handler) http.Handler {
						return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
							if !httpware.IsPlatformOwner(r.Context()) {
								http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
								return
							}
							next.ServeHTTP(w, r)
						})
					})
					adminRouter.Get("/subscriptions", platformHandler.ListAllSubscriptions)
				})
				r.Get("/platform/stats", func(w http.ResponseWriter, r *http.Request) {
					if !httpware.IsPlatformOwner(r.Context()) {
						http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
						return
					}
					platformHandler.GetPlatformStats(w, r)
				})
			}
		})
	})

	return r
}
