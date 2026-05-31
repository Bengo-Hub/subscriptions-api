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
	webhookHandler *handlers.WebhookHandler,
	customAddonHandler *handlers.CustomAddonHandler,
	couponHandler *handlers.CouponHandler,
	usageAdminHandler *handlers.UsageAdminHandler,
	couponAdminHandler *handlers.CouponAdminHandler,
	apiKey string,
	authMiddleware *authclient.AuthMiddleware,
	allowedOrigins []string,
	tenantSyncer *tenant.Syncer,
) *chi.Mux {
	r := chi.NewRouter()

	// Standard middleware — use httpware for structured zap logging and recovery,
	// consistent with all other platform services.
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(httpware.RequestID)
	r.Use(httpware.Logging(log))
	r.Use(httpware.Recover(log))

	// CORS
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   allowedOrigins,
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "Origin", "X-Request-ID", "X-CSRF-Token", "X-API-Key", "X-Tenant-ID", "X-Tenant-Slug"},
		ExposedHeaders:   []string{"Link", "X-RateLimit-Limit", "X-RateLimit-Remaining", "X-RateLimit-Reset", "Retry-After"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	// Root redirect to Swagger UI docs
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/v1/docs/", http.StatusFound)
	})
	r.Get("/healthz", healthHandler.Health)
	r.Get("/readyz", healthHandler.Health)

	// Swagger docs (outside /api/v1 — no auth required)
	r.Get("/v1/docs/*", handlers.SwaggerUI)
	r.Get("/api/v1/openapi.json", handlers.OpenAPIJSON)

	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/health", healthHandler.Health)
		r.Get("/healthz", healthHandler.Health) // readiness/liveness probe path used by Helm

		// Public routes (no auth required)
		r.Get("/plans", planHandler.ListPlans)
		r.Get("/plans/{id}", planHandler.GetPlan)
		r.Get("/plans/code/{code}", planHandler.GetPlanByCode)

		// S2S webhook routes (API key auth handled inside handler)
		if webhookHandler != nil {
			r.Post("/webhooks/treasury/payment-status", webhookHandler.HandleTreasuryPayment)
		}

		// Public read-only routes that accept optional auth for tenant context
		if serviceChargeHandler != nil {
			r.Get("/service-charges/plans", serviceChargeHandler.ListServiceChargePlans)
			r.Get("/service-charges/plans/{code}", serviceChargeHandler.GetServiceChargePlan)
		}

		// Authenticated routes
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
				r.Get("/billing/invoice-preview", billingHandler.InvoicePreview)
				r.Post("/subscription/payment-method/setup", billingHandler.SetupPaymentMethod)
				r.Post("/subscription/payment-method/confirm", billingHandler.ConfirmPaymentMethod)
			}

			// Credit wallet + coupon redemption
			if couponHandler != nil {
				r.Post("/subscription/coupon/redeem", couponHandler.RedeemCoupon)
				r.Get("/billing/credits", couponHandler.GetCreditWallet)
			}

			// S2S subscription lookup by tenant ID — used by auth-api for JWT enrichment.
			// Accessible via platform API key (service-to-service) or platform owner JWT.
			r.Get("/tenants/{tenant_id}/subscription", subscriptionHandler.GetByTenantID)
			// Per-service-tag subscription view — used by auth-ui billing tab.
			r.Get("/tenants/{tenant_id}/subscriptions", subscriptionHandler.GetServiceSubscriptions)

			// S2S expiring subscriptions — used by notifications-api for scheduled expiry warnings.
			r.Get("/subscriptions/expiring", subscriptionHandler.ListExpiring)
			// Switch plan by subscription ID — for billing UI plan change flows.
			r.Put("/subscriptions/{id}/switch-plan", subscriptionHandler.SwitchPlan)

			// Add-on management (PlanFeature-based tenant purchasable addons)
			if addonHandler != nil {
				r.Route("/addons", func(r chi.Router) {
					r.Get("/", addonHandler.ListAddons)
					r.Post("/{feature_code}/purchase", addonHandler.PurchaseAddon)
					r.Delete("/{feature_code}", addonHandler.RemoveAddon)
				})
			}

			// Custom addons — platform-admin configured; tenant read-only view
			if customAddonHandler != nil {
				r.Get("/custom-addons", customAddonHandler.ListMyCustomAddons)
			}

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
					r.Get("/summary", usageHandler.GetUsageDashboard)
					r.Get("/alerts", usageHandler.GetAlerts)
				})
			}

			// Service charge plans (GET plans moved to public; tenant-specific remains protected)
			if serviceChargeHandler != nil {
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

			// Admin: service charge plan CRUD
			if serviceChargeHandler != nil {
				r.Route("/admin/service-charges", func(r chi.Router) {
					r.Use(func(next http.Handler) http.Handler {
						return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
							if !httpware.IsPlatformOwner(r.Context()) {
								http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
								return
							}
							next.ServeHTTP(w, r)
						})
					})
					r.Post("/", serviceChargeHandler.CreateServiceChargePlan)
					r.Put("/{id}", serviceChargeHandler.UpdateServiceChargePlan)
					r.Delete("/{id}", serviceChargeHandler.DeleteServiceChargePlan)
				})
			}

			// Admin: tenant and subscription management
			if platformHandler != nil {
				r.Route("/admin/tenants", func(r chi.Router) {
					r.Use(func(next http.Handler) http.Handler {
						return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
							if !httpware.IsPlatformOwner(r.Context()) {
								http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
								return
							}
							next.ServeHTTP(w, r)
						})
					})
					r.Get("/", platformHandler.ListTenants)
					r.Post("/{tenant_id}/subscription", platformHandler.AssignPlanToTenant)
				})

				r.Route("/admin/subscriptions", func(r chi.Router) {
					r.Use(func(next http.Handler) http.Handler {
						return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
							if !httpware.IsPlatformOwner(r.Context()) {
								http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
								return
							}
							next.ServeHTTP(w, r)
						})
					})
					r.Get("/", platformHandler.ListAllSubscriptions)
					r.Put("/{id}/status", platformHandler.UpdateSubscriptionStatus)
				})

				// Platform admin: custom addon CRUD per tenant
				if customAddonHandler != nil {
					r.Route("/admin/tenants/{tenant_id}/custom-addons", func(r chi.Router) {
						r.Use(func(next http.Handler) http.Handler {
							return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
								if !httpware.IsPlatformOwner(r.Context()) {
									http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
									return
								}
								next.ServeHTTP(w, r)
							})
						})
						r.Post("/", customAddonHandler.CreateCustomAddon)
						r.Get("/", customAddonHandler.ListCustomAddonsByTenant)
						r.Patch("/{id}", customAddonHandler.UpdateCustomAddon)
						r.Delete("/{id}", customAddonHandler.CancelCustomAddon)
					})
				}

				// Platform admin: extend trial for a tenant subscription
				r.Post("/admin/tenants/{tenant_id}/subscription/extend-trial", subscriptionHandler.ExtendTrial)

				// Platform admin: gift credits to a tenant
				if couponHandler != nil {
					r.Post("/admin/tenants/{tenant_id}/credits/gift", couponHandler.GiftCredits)
				}

				r.Get("/platform/stats", func(w http.ResponseWriter, r *http.Request) {
					if !httpware.IsPlatformOwner(r.Context()) {
						http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
						return
					}
					platformHandler.GetPlatformStats(w, r)
				})

				r.Route("/admin/configs", func(r chi.Router) {
					r.Use(func(next http.Handler) http.Handler {
						return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
							if !httpware.IsPlatformOwner(r.Context()) {
								http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
								return
							}
							next.ServeHTTP(w, r)
						})
					})
					r.Get("/", platformHandler.ListServiceConfigs)
					r.Post("/", platformHandler.CreateServiceConfig)
					r.Put("/{id}", platformHandler.UpdateServiceConfig)
					r.Delete("/{id}", platformHandler.DeleteServiceConfig)
				})
			}

			// Platform admin: per-tenant usage view and manual metric override
			if usageAdminHandler != nil {
				r.Get("/admin/usage/tenants/{tenant_id}", usageAdminHandler.GetTenantUsage)
				r.Put("/admin/usage/tenants/{tenant_id}/override", usageAdminHandler.OverrideMetric)
			}

			// Platform admin: coupon CRUD
			if couponAdminHandler != nil {
				r.Route("/admin/coupons", func(r chi.Router) {
					r.Get("/", couponAdminHandler.ListCoupons)
					r.Post("/", couponAdminHandler.CreateCoupon)
					r.Get("/{id}", couponAdminHandler.GetCoupon)
					r.Patch("/{id}", couponAdminHandler.UpdateCoupon)
					r.Delete("/{id}", couponAdminHandler.DeleteCoupon)
				})
			}
		})
	})

	return r
}
