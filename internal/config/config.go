package config

import (
	"fmt"
	"time"

	"github.com/joho/godotenv"
	"github.com/kelseyhightower/envconfig"
)

// Empty namespace so common env keys match all Go backends: POSTGRES_URL, REDIS_ADDR, REDIS_PASSWORD (no service prefix).
const namespace = ""

// Config captures environment configuration for the Subscription service.
type Config struct {
	App       AppConfig
	HTTP      HTTPConfig
	Postgres  PostgresConfig
	Redis     RedisConfig
	Events    EventsConfig
	Telemetry TelemetryConfig
	Security  SecurityConfig
	Services  ServicesConfig
}

type AppConfig struct {
	Name    string `envconfig:"APP_NAME" default:"subscription-service"`
	Env     string `envconfig:"APP_ENV" default:"development"`
	Version string `envconfig:"APP_VERSION" default:"0.1.0"`
}

type ServicesConfig struct {
	AuthAPI        string `envconfig:"AUTH_API_URL" default:"https://sso.codevertexitsolutions.com"`
	TreasuryAPI    string `envconfig:"TREASURY_API_URL" default:"https://booksapi.codevertexitsolutions.com"`
	TreasuryAPIKey string `envconfig:"INTERNAL_SERVICE_KEY"`
}

type HTTPConfig struct {
	Host           string        `envconfig:"HTTP_HOST" default:"0.0.0.0"`
	Port           int           `envconfig:"HTTP_PORT" default:"4008"`
	ReadTimeout    time.Duration `envconfig:"HTTP_READ_TIMEOUT" default:"15s"`
	WriteTimeout   time.Duration `envconfig:"HTTP_WRITE_TIMEOUT" default:"15s"`
	IdleTimeout    time.Duration `envconfig:"HTTP_IDLE_TIMEOUT" default:"60s"`
	AllowedOrigins []string      `envconfig:"HTTP_ALLOWED_ORIGINS" default:"https://accounts.codevertexitsolutions.com,https://sso.codevertexitsolutions.com,https://pricing.codevertexitsolutions.com,https://theurbanloftcafe.com,https://ordersapp.codevertexitsolutions.com,https://books.codevertexitsolutions.com,https://pos.codevertexitsolutions.com,https://inventory.codevertexitsolutions.com,https://logistics.codevertexitsolutions.com,http://localhost:3000,http://localhost:3001,http://127.0.0.1:3000"`
}

type PostgresConfig struct {
	URL             string        `envconfig:"POSTGRES_URL" default:"postgres://postgres:postgres@localhost:5432/subscriptions?sslmode=disable"`
	MigrateURL      string        `envconfig:"POSTGRES_MIGRATE_URL"` // Direct PostgreSQL URL bypassing PgBouncer; falls back to URL if empty
	MaxOpenConns    int           `envconfig:"POSTGRES_MAX_OPEN_CONNS" default:"5"`
	MaxIdleConns    int           `envconfig:"POSTGRES_MAX_IDLE_CONNS" default:"3"`
	ConnMaxLifetime time.Duration `envconfig:"POSTGRES_CONN_MAX_LIFETIME" default:"5m"`
	RunMigrations   bool          `envconfig:"POSTGRES_RUN_MIGRATIONS" default:"false"`
}

type RedisConfig struct {
	Addr      string `envconfig:"REDIS_ADDR" default:"localhost:6379"`
	Username  string `envconfig:"REDIS_USERNAME"`
	Password  string `envconfig:"REDIS_PASSWORD"`
	DB        int    `envconfig:"REDIS_DB" default:"0"`
	Namespace string `envconfig:"REDIS_NAMESPACE" default:"subscription"`
}

type EventsConfig struct {
	NATSURL    string `envconfig:"EVENTS_NATS_URL" default:"nats://127.0.0.1:4222"`
	StreamName string `envconfig:"NATS_STREAM" default:"subscription"`
}

type TelemetryConfig struct {
	OTLPEndpoint string `envconfig:"OTLP_ENDPOINT"`
}

type SecurityConfig struct {
	// Auth-service SSO integration
	RequireJWT  bool   `envconfig:"REQUIRE_JWT" default:"true"`
	JWKSURL     string `envconfig:"JWT_JWKS_URL" default:"https://sso.codevertexitsolutions.com/api/v1/.well-known/jwks.json"`
	Issuer      string `envconfig:"JWT_ISSUER" default:"https://sso.codevertexitsolutions.com"`
	Audience    string `envconfig:"JWT_AUDIENCE" default:"codevertex"`
	APIKey      string `envconfig:"INTERNAL_SERVICE_KEY"`
	APIKeyDBURL string `envconfig:"API_KEY_DB_URL"`
}

// Load reads configuration from environment variables and optional .env files.
func Load() (*Config, error) {
	_ = godotenv.Load()

	var cfg Config
	if err := envconfig.Process(namespace, &cfg); err != nil {
		return nil, fmt.Errorf("config: failed to load environment variables: %w", err)
	}

	return &cfg, nil
}
