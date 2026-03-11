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
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/nats-io/nats.go"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	authclient "github.com/Bengo-Hub/shared-auth-client"
	eventslib "github.com/Bengo-Hub/shared-events"
	serviceclient "github.com/Bengo-Hub/shared-service-client"

	"github.com/bengobox/subscription-service/internal/config"
	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/migrate"
	handlers "github.com/bengobox/subscription-service/internal/http/handlers"
	router "github.com/bengobox/subscription-service/internal/http/router"
	"github.com/bengobox/subscription-service/internal/modules/outbox"
	"github.com/bengobox/subscription-service/internal/modules/plans"
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
	sqlDB.SetMaxIdleConns(10)
	sqlDB.SetMaxOpenConns(25)
	sqlDB.SetConnMaxIdleTime(5 * time.Minute)

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

	// Initialize plans module (repository and handler)
	var planHandler *handlers.PlanHandler
	if ormClient != nil {
		planRepo := plans.NewEntRepository(ormClient)
		planService := plans.NewService(log, planRepo)
		planHandler = handlers.NewPlanHandler(log, planService)
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
				outboxRepo := outbox.NewRepository(sqlDB)
				pubCfg := eventslib.DefaultPublisherConfig(js, outboxRepo, log)
				outboxPublisher = eventslib.NewPublisher(pubCfg)
				log.Info("outbox publisher initialized")
			} else {
				log.Warn("failed to create sql.DB for outbox, publisher disabled", zap.Error(err))
			}
		}
	}

	// Create subscription service and handler
	subscriptionSvc := subscriptions.New(ormClient, log, treasuryClient)
	subscriptionHandler := handlers.NewSubscriptionHandler(log, ormClient, subscriptionSvc)
	addonHandler := handlers.NewAddonHandler(log, ormClient)

	httpRouter := router.New(log, healthHandler, planHandler, subscriptionHandler, addonHandler, cfg.Security.APIKey, authMiddleware, cfg.HTTP.AllowedOrigins, tenantSyncer)

	httpServer := &http.Server{
		Addr:              fmt.Sprintf("%s:%d", cfg.HTTP.Host, cfg.HTTP.Port),
		Handler:           httpRouter,
		ReadTimeout:       cfg.HTTP.ReadTimeout,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      cfg.HTTP.WriteTimeout,
		IdleTimeout:       cfg.HTTP.IdleTimeout,
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
