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
	Backup    BackupConfig
}

// BackupConfig controls the tenant-scoped backup scheduler + retention churn. Artifacts are
// written to a local directory (Dir) — typically a PVC.
type BackupConfig struct {
	Dir             string `envconfig:"BACKUP_DIR" default:"/app/backups/subscriptions"`
	ScheduleEnabled bool   `envconfig:"BACKUP_SCHEDULE_ENABLED" default:"true"`
	ScheduleHour    int    `envconfig:"BACKUP_SCHEDULE_HOUR" default:"2"`
	RetentionDays   int    `envconfig:"BACKUP_RETENTION_DAYS" default:"4"`
}

type AppConfig struct {
	Name    string `envconfig:"APP_NAME" default:"subscription-service"`
	Env     string `envconfig:"APP_ENV" default:"development"`
	Version string `envconfig:"APP_VERSION" default:"0.1.0"`
}

type ServicesConfig struct {
	AuthAPI     string `envconfig:"AUTH_API_URL" default:"https://sso.codevertexafrica.com"`
	TreasuryAPI string `envconfig:"TREASURY_API_URL" default:"https://booksapi.codevertexafrica.com"`
	// TreasuryPublicAPI is treasury-api's browser-reachable base URL, used ONLY for links handed
	// to a customer's browser (invoice PDF). Deliberately separate from TreasuryAPI: that value is
	// commonly pointed at the cluster-internal Service DNS (e.g.
	// http://treasury-api.treasury.svc.cluster.local:4000) for faster/more-reliable S2S calls,
	// which a browser can never resolve — reusing it for a browser-facing URL produced a real
	// broken-invoice-PDF bug in production (2026-08-30). Mirrors treasury-api's own
	// internal/config's HTTP_PUBLIC_BASE_URL vs. its internal-only client configs.
	TreasuryPublicAPI string `envconfig:"TREASURY_PUBLIC_API_URL" default:"https://booksapi.codevertexafrica.com"`
	TreasuryUI        string `envconfig:"TREASURY_UI_URL" default:"https://books.codevertexafrica.com"`
	TreasuryAPIKey    string `envconfig:"INTERNAL_SERVICE_KEY"`
	MarketflowAPI     string `envconfig:"MARKETFLOW_API_URL" default:"https://marketflowapi.codevertexafrica.com"`
	// ISPBillingAPI is the isp-billing backend used to fetch the exact point-in-time active
	// PPPoE subscriber count for ISP usage billing (S2S, authenticated with INTERNAL_SERVICE_KEY).
	ISPBillingAPI string `envconfig:"ISP_BILLING_API_URL" default:"https://ispbillingapi.codevertexafrica.com"`
	// EmailProvisionerAPI is email-provisioner's cluster-internal-only endpoint (no Ingress,
	// no auth — same unauthenticated cluster-network-scoped convention as its existing
	// /internal/mail-stats//healthz endpoints, mirrored from auth-api's own client for it).
	// Used for domain onboarding (Part 1.3): subscriptions-api never talks to Stalwart's own
	// JMAP API directly, email-provisioner is the sole JMAP speaker.
	EmailProvisionerAPI string `envconfig:"EMAIL_PROVISIONER_API_URL" default:"http://email-provisioner.email.svc.cluster.local:8080"`
	// PlatformMailMXHost/PlatformMailIP are the platform's own mail-protocol hostname/IP —
	// already public information (published in the platform's own MX/SPF/PTR records),
	// used by VerifyEmailDomain to confirm a tenant's onboarded domain actually points at
	// this platform rather than needing to parse Stalwart's raw dnsZoneFile text.
	PlatformMailMXHost string `envconfig:"PLATFORM_MAIL_MX_HOST" default:"mx1.codevertexafrica.com"`
	PlatformMailIP     string `envconfig:"PLATFORM_MAIL_IP" default:"77.237.232.66"`
	// PlatformTenantID is the treasury/auth tenant UUID that ISSUES subscription invoices
	// (the platform org, e.g. codevertex). Subscription invoices are owned by this tenant
	// with the billed tenant as the customer. Must be set in prod.
	PlatformTenantID string `envconfig:"PLATFORM_TENANT_ID" default:"4414b8d9-6b00-4ad1-a0b4-3094cbc5e398"`
	// VATRate is the default VAT percentage applied (exclusive) to subscription invoice
	// lines, matching the platform-seeded VAT-16 tax code. Set 0 to disable.
	VATRate float64 `envconfig:"PLATFORM_VAT_RATE" default:"16"`
}

type HTTPConfig struct {
	Host           string        `envconfig:"HTTP_HOST" default:"0.0.0.0"`
	Port           int           `envconfig:"HTTP_PORT" default:"4008"`
	ReadTimeout    time.Duration `envconfig:"HTTP_READ_TIMEOUT" default:"15s"`
	WriteTimeout   time.Duration `envconfig:"HTTP_WRITE_TIMEOUT" default:"15s"`
	IdleTimeout    time.Duration `envconfig:"HTTP_IDLE_TIMEOUT" default:"60s"`
	AllowedOrigins []string      `envconfig:"HTTP_ALLOWED_ORIGINS" default:"https://accounts.codevertexafrica.com,https://sso.codevertexafrica.com,https://pricing.codevertexafrica.com,https://theurbanloftcafe.com,https://ordering.codevertexafrica.com,https://books.codevertexafrica.com,https://pos.codevertexafrica.com,https://inventory.codevertexafrica.com,https://logistics.codevertexafrica.com,http://localhost:3000,http://localhost:3001,http://127.0.0.1:3000"`
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
	JWKSURL     string `envconfig:"JWT_JWKS_URL" default:"https://sso.codevertexafrica.com/api/v1/.well-known/jwks.json"`
	Issuer      string `envconfig:"JWT_ISSUER" default:"https://sso.codevertexafrica.com"`
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
