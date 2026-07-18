package app

import (
	"context"
	"crypto/tls"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"entgo.io/ent/dialect/sql/schema"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/nats-io/nats.go"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	sharedcache "github.com/Bengo-Hub/cache"
	authclient "github.com/Bengo-Hub/shared-auth-client"
	eventslib "github.com/Bengo-Hub/shared-events"
	serviceclient "github.com/Bengo-Hub/shared-service-client"

	"github.com/bengobox/subscription-service/internal/config"
	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/migrate"
	handlers "github.com/bengobox/subscription-service/internal/http/handlers"
	router "github.com/bengobox/subscription-service/internal/http/router"
	"github.com/bengobox/subscription-service/internal/jobs"
	backupmod "github.com/bengobox/subscription-service/internal/modules/backup"
	"github.com/bengobox/subscription-service/internal/modules/billing"
	"github.com/bengobox/subscription-service/internal/modules/consumers"
	"github.com/bengobox/subscription-service/internal/modules/plans"
	"github.com/bengobox/subscription-service/internal/modules/rbac"
	"github.com/bengobox/subscription-service/internal/modules/subscriptions"
	"github.com/bengobox/subscription-service/internal/modules/tenant"
	"github.com/bengobox/subscription-service/internal/platform/cache"
	"github.com/bengobox/subscription-service/internal/platform/database"
	"github.com/bengobox/subscription-service/internal/platform/events"
	"github.com/bengobox/subscription-service/internal/shared/logger"
)

type App struct {
	cfg             *config.Config
	log             *zap.Logger
	httpServer      *http.Server
	db              *pgxpool.Pool
	cache           *redis.Client
	events          *nats.Conn
	orm             *ent.Client
	outboxPublisher *eventslib.Publisher
	tenantConsumer  *consumers.TenantCreatedConsumer
	usageConsumer   *consumers.UsageConsumer
	paymentConsumer *consumers.TreasuryPaymentConsumer
	subscriptionSvc *subscriptions.Service
	treasuryClient  *serviceclient.Client
	invoiceSvc      *billing.InvoiceService
}

func New(ctx context.Context) (*App, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}

	log, err := logger.New(cfg.App.Env)
	if err != nil {
		return nil, fmt.Errorf("logger init: %w", err)
	}

	dbPool, err := database.NewPool(ctx, cfg.Postgres)
	if err != nil {
		return nil, fmt.Errorf("postgres init: %w", err)
	}

	redisClient := cache.NewClient(cfg.Redis)

	natsConn, err := events.Connect(cfg.Events)
	if err != nil {
		log.Warn("event bus connection failed", zap.Error(err))
	}

	healthHandler := handlers.NewHealthHandler(log, dbPool, redisClient, natsConn)

	// Initialize Ent ORM client
	sqlDB, err := sql.Open("pgx", cfg.Postgres.URL)
	if err != nil {
		return nil, fmt.Errorf("ent driver init: %w", err)
	}
	sqlDB.SetMaxOpenConns(cfg.Postgres.MaxOpenConns)
	sqlDB.SetMaxIdleConns(cfg.Postgres.MaxIdleConns)
	sqlDB.SetConnMaxLifetime(cfg.Postgres.ConnMaxLifetime)
	sqlDB.SetConnMaxIdleTime(1 * time.Minute)

	drv := entsql.OpenDB(dialect.Postgres, sqlDB)
	ormClient := ent.NewClient(ent.Driver(drv))
	if cfg.Postgres.RunMigrations {
		if err := ormClient.Schema.Create(ctx,
			schema.WithDir(migrate.Dir),
		); err != nil {
			return nil, fmt.Errorf("ent schema create: %w", err)
		}
		log.Info("versioned migrations completed - run 'go run cmd/seed/main.go' to seed initial data (idempotent)")
	}

	// Sync platform owner tenant
	tenantSyncer := tenant.NewSyncer(ormClient, cfg.Services.AuthAPI)
	if _, err := tenantSyncer.SyncTenant(ctx, "codevertex"); err != nil {
		log.Warn("failed to sync platform owner", zap.Error(err))
	}

	// Initialize Treasury service client
	treasuryCfg := serviceclient.DefaultConfig(cfg.Services.TreasuryAPI, "treasury-api", log)
	treasuryClient := serviceclient.New(treasuryCfg)

	// Initialize isp-billing service client (exact active PPPoE count for ISP usage billing).
	ispBillingCfg := serviceclient.DefaultConfig(cfg.Services.ISPBillingAPI, "isp-billing-backend", log)
	ispBillingClient := serviceclient.New(ispBillingCfg)

	// Initialize auth-service JWT validator
	var authMiddleware *authclient.AuthMiddleware
	if cfg.Security.RequireJWT {
		authConfig := authclient.DefaultConfig(
			cfg.Security.JWKSURL,
			cfg.Security.Issuer,
			cfg.Security.Audience,
		)
		// For local Docker development, skip TLS verification when connecting to auth-service
		if strings.Contains(cfg.Security.JWKSURL, "auth.codevertex.local") ||
			strings.Contains(cfg.Security.JWKSURL, "host.docker.internal") {
			tr := &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			}
			authConfig.HTTPClient = &http.Client{
				Timeout:   10 * time.Second,
				Transport: tr,
			}
		}
		validator, err := authclient.NewValidator(authConfig)
		if err != nil {
			return nil, fmt.Errorf("auth validator init: %w", err)
		}

		// Add API key validator if database URL is provided
		var apiKeyValidator *authclient.APIKeyValidator
		if cfg.Security.APIKeyDBURL != "" {
			apiKeyHTTPClient := &http.Client{Timeout: 10 * time.Second}
			if strings.Contains(cfg.Security.APIKeyDBURL, "auth.codevertex.local") ||
				strings.Contains(cfg.Security.APIKeyDBURL, "host.docker.internal") {
				tr := &http.Transport{
					TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
				}
				apiKeyHTTPClient = &http.Client{
					Timeout:   10 * time.Second,
					Transport: tr,
				}
			}
			apiKeyValidator = authclient.NewAPIKeyValidator(cfg.Security.APIKeyDBURL, apiKeyHTTPClient)
			authMiddleware = authclient.NewAuthMiddlewareWithAPIKey(validator, apiKeyValidator)
		} else {
			authMiddleware = authclient.NewAuthMiddleware(validator)
		}
	}

	// Initialize cache helper for read-heavy queries
	cacheAside := sharedcache.New(redisClient, log)

	// Initialize plans module (repository and handler)
	var planHandler *handlers.PlanHandler
	if ormClient != nil {
		planRepo := plans.NewEntRepository(ormClient)
		planService := plans.NewService(log, planRepo)
		planService.SetCache(cacheAside)
		planHandler = handlers.NewPlanHandler(log, planService)
	}

	// Ensure JetStream stream for subscription events
	if natsConn != nil {
		if err := events.EnsureStream(ctx, natsConn, cfg.Events); err != nil {
			log.Warn("failed to ensure subscription JetStream stream (non-fatal)", zap.Error(err))
		} else {
			log.Info("subscription JetStream stream ensured", zap.String("stream", cfg.Events.StreamName))
		}
	}

	// Initialize outbox publisher
	var outboxPublisher *eventslib.Publisher
	if natsConn != nil && dbPool != nil {
		js, err := natsConn.JetStream()
		if err != nil {
			log.Warn("failed to get jetstream context, outbox publisher disabled", zap.Error(err))
		} else {
			// Get underlying sql.DB for outbox repository
			sqlDB, err := sql.Open("pgx", cfg.Postgres.URL)
			if err == nil {
				outboxRepo := eventslib.NewSQLOutboxRepository(sqlDB)
				pubCfg := eventslib.DefaultPublisherConfig(js, outboxRepo, log)
				outboxPublisher = eventslib.NewPublisher(pubCfg)
				log.Info("outbox publisher initialized")
			} else {
				log.Warn("failed to create sql.DB for outbox, publisher disabled", zap.Error(err))
			}
		}
	}

	// Create subscription service. The platform tenant id is parsed once here so the
	// service can exempt the platform owner (alongside the demo/codevertex slugs) from
	// ever owning a subscription record.
	platformTenantID, _ := uuid.Parse(cfg.Services.PlatformTenantID)
	subscriptionSvc := subscriptions.New(ormClient, log, treasuryClient, cfg.Services.TreasuryAPIKey, platformTenantID)

	// Feature gate handler with Redis caching (constructed first — injected into mutation handlers)
	featureHandler := handlers.NewFeatureHandler(log, subscriptionSvc, redisClient)

	// Overage accrual service — shared by the usage + subscription handlers for pending-overage reads.
	overageSvc := billing.NewOverageService(log, ormClient)

	// Subscription, addon, and platform handlers (featureHandler injected for cache invalidation)
	subscriptionHandler := handlers.NewSubscriptionHandler(log, ormClient, dbPool, subscriptionSvc, featureHandler, overageSvc)
	addonHandler := handlers.NewAddonHandler(log, ormClient, subscriptionSvc, treasuryClient, cfg.Services.TreasuryAPIKey, featureHandler)

	// Usage tracking handler (raw SQL — requires UsageEvent Ent schema codegen to create table)
	usageHandler := handlers.NewUsageHandler(log, dbPool, ormClient, redisClient, overageSvc)

	// Service charge plan handler
	serviceChargeHandler := handlers.NewServiceChargeHandler(log, ormClient)

	// Billing and platform admin handlers
	billingHandler := handlers.NewBillingHandler(log, ormClient, treasuryClient, cfg.Services.TreasuryAPIKey, cfg.Services.MarketflowAPI, cfg.Services.PlatformTenantID)
	platformHandler := handlers.NewPlatformHandler(log, ormClient, featureHandler)
	platformHandler.WithSubscriptionService(subscriptionSvc)

	// Subscription invoice service (platform→tenant invoices via treasury S2S) — shared by
	// the 7-day invoice job, grace events, and the manual platform-owner endpoints.
	invoiceSvc := billing.NewInvoiceService(log, ormClient, subscriptionSvc, treasuryClient, ispBillingClient, cfg.Services.TreasuryAPIKey, cfg.Services.PlatformTenantID, cfg.Services.TreasuryUI, cfg.Services.TreasuryAPI, cfg.Services.VATRate)
	platformHandler.WithInvoiceService(invoiceSvc)

	// Inbound webhook handler (Treasury payment callbacks)
	webhookHandler := handlers.NewWebhookHandler(log, ormClient, subscriptionSvc, cfg.Security.APIKey, featureHandler)

	// RBAC module
	rbacRepo := rbac.NewEntRepository(ormClient)
	rbacService := rbac.NewService(rbacRepo, log, tenantSyncer)
	rbacHandler := handlers.NewRBACHandler(log, rbacService, rbacRepo)

	// Custom addon handler (platform-admin managed recurring/one-time charges)
	customAddonHandler := handlers.NewCustomAddonHandler(log, ormClient)

	// Coupon + credit wallet handler
	couponHandler := handlers.NewCouponHandler(log, ormClient)

	// Platform admin: per-tenant usage view + manual metric override
	usageAdminHandler := handlers.NewUsageAdminHandler(log, dbPool, ormClient, redisClient)

	// Platform admin: coupon CRUD
	couponAdminHandler := handlers.NewCouponAdminHandler(log, ormClient)

	// Feature catalog handler (platform-wide feature/limit registry for the plan builder)
	var featureCatalogHandler *handlers.FeatureCatalogHandler
	if ormClient != nil {
		featureCatalogHandler = handlers.NewFeatureCatalogHandler(log, ormClient)
	}

	// Pluggable backup destination (OneDrive/GDrive/S3/WebDAV/SFTP/SMB) — encrypted
	// at rest with a SECRET_KEY-derived AES-256 key. The handler owns the
	// destination Store; its Uploader is attached to the backup service so every
	// PVC backup is additionally mirrored best-effort.
	backupDestHandler := handlers.NewBackupDestinationHandler(ormClient, log)

	// Tenant-scoped backups + daily 02:00 auto-backup scheduler + retention churn.
	backupSvc := backupmod.NewService(sqlDB, ormClient, cfg.Backup.Dir, log).
		WithMirrorer(backupDestHandler.Uploader())
	backupHandler := handlers.NewBackupHandler(log, backupSvc, cfg.Backup.RetentionDays)
	backupmod.NewScheduler(backupSvc, backupmod.SchedulerConfig{
		Enabled:       cfg.Backup.ScheduleEnabled,
		Hour:          cfg.Backup.ScheduleHour,
		RetentionDays: cfg.Backup.RetentionDays,
	}, log).Start(ctx)

	httpRouter := router.New(log, healthHandler, planHandler, subscriptionHandler, addonHandler, featureHandler, usageHandler, serviceChargeHandler, billingHandler, platformHandler, rbacHandler, webhookHandler, customAddonHandler, couponHandler, usageAdminHandler, couponAdminHandler, featureCatalogHandler, backupHandler, backupDestHandler, cfg.Security.APIKey, authMiddleware, cfg.HTTP.AllowedOrigins, tenantSyncer)

	httpServer := &http.Server{
		Addr:              fmt.Sprintf("%s:%d", cfg.HTTP.Host, cfg.HTTP.Port),
		Handler:           httpRouter,
		ReadTimeout:       cfg.HTTP.ReadTimeout,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      cfg.HTTP.WriteTimeout,
		IdleTimeout:       cfg.HTTP.IdleTimeout,
	}

	paymentConsumer := consumers.NewTreasuryPaymentConsumer(log, ormClient)
	paymentConsumer.SetSubscriptionService(subscriptionSvc)

	// Usage consumer with per-event idempotency so JetStream redeliveries don't double-count
	// rolling usage metrics. Schema-ensure is best-effort: on failure metering still runs
	// (fail-open) rather than blocking billable event ingestion.
	usageConsumer := consumers.NewUsageConsumer(log, dbPool, ormClient, redisClient)
	idemStore := eventslib.NewIdempotencyStore(sqlDB)
	if err := idemStore.EnsureSchema(context.Background()); err != nil {
		log.Warn("usage idempotency schema ensure failed; metering will run without dedup", zap.Error(err))
	} else {
		usageConsumer.SetIdempotency(idemStore)
		// Payment consumer too: RenewSubscription period-stacking + loyalty credits
		// must not double-apply on JetStream redelivery.
		paymentConsumer.SetIdempotency(idemStore)
	}

	return &App{
		cfg:             cfg,
		log:             log,
		httpServer:      httpServer,
		db:              dbPool,
		cache:           redisClient,
		events:          natsConn,
		orm:             ormClient,
		outboxPublisher: outboxPublisher,
		tenantConsumer:  consumers.NewTenantCreatedConsumer(log, subscriptionSvc),
		usageConsumer:   usageConsumer,
		paymentConsumer: paymentConsumer,
		subscriptionSvc: subscriptionSvc,
		treasuryClient:  treasuryClient,
		invoiceSvc:      invoiceSvc,
	}, nil
}

func (a *App) Run(ctx context.Context) error {
	// Start outbox publisher worker
	if a.outboxPublisher != nil {
		go func() {
			if err := a.outboxPublisher.Start(ctx); err != nil {
				a.log.Error("outbox publisher failed", zap.Error(err))
			}
		}()
		a.log.Info("outbox publisher started")
	}

	// Start renewal and expiry background jobs
	if a.orm != nil && a.subscriptionSvc != nil {
		go jobs.StartRenewalJob(ctx, a.log, a.orm, a.subscriptionSvc, a.treasuryClient, a.cfg.Services.TreasuryAPIKey)
		a.log.Info("subscription renewal and expiry jobs started")
	}

	// Start dunning job — retries payment for SUSPENDED subscriptions on days 1, 3, 7
	if a.orm != nil && a.subscriptionSvc != nil {
		go billing.StartDunningJob(ctx, a.log, a.orm, a.subscriptionSvc, a.treasuryClient, a.cfg.Services.TreasuryAPIKey)
		a.log.Info("dunning job started")
	}

	// Start subscription invoice job (generates + emails invoices ~7 days before expiry)
	if a.orm != nil && a.invoiceSvc != nil {
		go jobs.StartInvoiceJob(ctx, a.log, a.orm, a.invoiceSvc)
		a.log.Info("subscription invoice job started")
	}

	// Start grace reminder job (daily countdown emails while in the grace window)
	if a.orm != nil && a.subscriptionSvc != nil {
		go jobs.StartGraceReminderJob(ctx, a.log, a.orm, a.subscriptionSvc)
		a.log.Info("grace reminder job started")
	}

	// Start dormancy job (daily: flag >60d-idle accounts, 7-day grace, then suspend + queue purge)
	if a.orm != nil && a.subscriptionSvc != nil {
		go jobs.StartDormancyJob(ctx, a.log, a.orm, a.subscriptionSvc)
		a.log.Info("dormancy job started")
	}

	// Start auth.tenant.created consumer for auto-provisioning new tenants
	if a.tenantConsumer != nil && a.events != nil {
		js, err := a.events.JetStream()
		if err != nil {
			a.log.Warn("jetstream unavailable, tenant consumer not started", zap.Error(err))
		} else {
			go func() {
				if err := a.tenantConsumer.Start(ctx, js); err != nil {
					a.log.Error("tenant consumer stopped", zap.Error(err))
				}
			}()
			a.log.Info("tenant.created consumer started")
		}
	}

	// Start usage event consumers (ordering, pos, inventory, etc. → usage_events table)
	if a.usageConsumer != nil && a.events != nil {
		js, err := a.events.JetStream()
		if err != nil {
			a.log.Warn("jetstream unavailable, usage consumer not started", zap.Error(err))
		} else {
			go func() {
				if err := a.usageConsumer.Start(ctx, js); err != nil {
					a.log.Error("usage consumer stopped", zap.Error(err))
				}
			}()
			a.log.Info("usage event consumer started")
		}
	}

	// Start treasury payment consumer (stores Paystack auth_code for auto-renewal)
	if a.paymentConsumer != nil && a.events != nil {
		js, err := a.events.JetStream()
		if err != nil {
			a.log.Warn("jetstream unavailable, treasury payment consumer not started", zap.Error(err))
		} else {
			go func() {
				if err := a.paymentConsumer.Start(ctx, js); err != nil {
					a.log.Error("treasury payment consumer stopped", zap.Error(err))
				}
			}()
			a.log.Info("treasury payment consumer started")
		}
	}

	errCh := make(chan error, 1)
	a.log.Info("subscription service starting", zap.String("addr", a.httpServer.Addr))
	go func() {
		errCh <- a.httpServer.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := a.httpServer.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("http shutdown: %w", err)
		}

		return nil
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("http server error: %w", err)
	}
}

func (a *App) Close() {
	if a.events != nil {
		if err := a.events.Drain(); err != nil {
			a.log.Warn("nats drain failed", zap.Error(err))
		}
		a.events.Close()
	}

	if a.cache != nil {
		if err := a.cache.Close(); err != nil {
			a.log.Warn("redis close failed", zap.Error(err))
		}
	}

	if a.db != nil {
		a.db.Close()
	}

	if a.orm != nil {
		if err := a.orm.Close(); err != nil {
			a.log.Warn("ent client close failed", zap.Error(err))
		}
	}

	_ = a.log.Sync()
}
