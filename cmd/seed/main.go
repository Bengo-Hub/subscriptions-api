package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strings"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/bengobox/subscription-service/internal/config"
	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/planfeature"
	"github.com/bengobox/subscription-service/internal/ent/subscriptionspermission"
	"github.com/bengobox/subscription-service/internal/modules/tenant"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	db, err := sql.Open("pgx", cfg.Postgres.URL)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer db.Close()

	db.SetMaxIdleConns(2)
	db.SetMaxOpenConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)
	db.SetConnMaxIdleTime(1 * time.Minute)

	driver := entsql.OpenDB(dialect.Postgres, db)
	client := ent.NewClient(ent.Driver(driver))
	defer client.Close()

	if err := runSeed(ctx, client, cfg); err != nil {
		log.Fatalf("seed data: %v", err)
	}

	log.Println("database seed completed successfully")
}

func runSeed(ctx context.Context, client *ent.Client, cfg *config.Config) error {
	tx, err := client.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
			return
		}
		err = tx.Commit()
	}()

	// 1. Seed products (microservices)
	if err := seedProducts(ctx, tx); err != nil {
		return fmt.Errorf("seed products: %w", err)
	}

	// 2. Seed subscription plans (Starter, Growth, Professional — monthly + yearly)
	if err := seedSubscriptionPlans(ctx, tx); err != nil {
		return fmt.Errorf("seed plans: %w", err)
	}

	// 2.5 Seed TruLoad org-level plans (Starter, Growth, Professional + License)
	if err := seedTruLoadOrgPlans(ctx, tx); err != nil {
		return fmt.Errorf("seed truload org plans: %w", err)
	}

	// 2.6 Seed TruLoad transporter portal plans (Basic, Standard, Premium)
	if err := seedTruLoadTransporterPlans(ctx, tx); err != nil {
		return fmt.Errorf("seed transporter plans: %w", err)
	}

	// 2.7 Seed logistics standalone plans
	if err := seedLogisticsPlans(ctx, tx); err != nil {
		return fmt.Errorf("seed logistics plans: %w", err)
	}

	// 2.8 Seed inventory standalone plans
	if err := seedInventoryPlans(ctx, tx); err != nil {
		return fmt.Errorf("seed inventory plans: %w", err)
	}

	// 2.9 Seed ERP standalone plans
	if err := seedERPPlans(ctx, tx); err != nil {
		return fmt.Errorf("seed erp plans: %w", err)
	}

	// 2.10 Seed POS per-device license plans
	if err := seedPOSLicensePlans(ctx, tx); err != nil {
		return fmt.Errorf("seed pos license plans: %w", err)
	}

	// 3. Seed bundles (delivery, pos-suite, complete)
	if err := seedBundles(ctx, tx); err != nil {
		return fmt.Errorf("seed bundles: %w", err)
	}

	// 3.5 Seed service charge plans
	if err := seedServiceChargePlans(ctx, tx); err != nil {
		return fmt.Errorf("seed service charge plans: %w", err)
	}

	// 4. Seed subscriptions for ALL tenants (each tenant must have a subscription, trial periods allowed)
	syncer := tenant.NewSyncer(tx.Client(), cfg.Services.AuthAPI)
	if err := seedAllTenantSubscriptions(ctx, tx, syncer); err != nil {
		return fmt.Errorf("seed tenant subscriptions: %w", err)
	}

	// 5. Seed RBAC permissions
	if err := seedRBACPermissions(ctx, tx); err != nil {
		return fmt.Errorf("seed rbac permissions: %w", err)
	}

	// 6. Seed rate limit configs
	if err := seedRateLimitConfigs(ctx, tx); err != nil {
		return fmt.Errorf("seed rate limit configs: %w", err)
	}

	// 7. Seed service configs
	if err := seedServiceConfigs(ctx, tx); err != nil {
		return fmt.Errorf("seed service configs: %w", err)
	}

	return nil
}

// featureDef supports both boolean features and rate-limited features with limits.
type featureDef struct {
	code             string
	limitValue       int     // 0 = unlimited (boolean feature), >0 = rate limit
	overageUnitPrice float64 // price per unit above limit (0 = no overage)
}

func seedPlanFeatures(ctx context.Context, tx *ent.Tx, planID uuid.UUID, featureCodes []string) error {
	// Convert string slice to featureDefs (backward compatible)
	defs := make([]featureDef, len(featureCodes))
	for i, code := range featureCodes {
		defs[i] = featureDef{code: code}
	}
	return seedPlanFeaturesWithLimits(ctx, tx, planID, defs)
}

func seedPlanFeaturesWithLimits(ctx context.Context, tx *ent.Tx, planID uuid.UUID, features []featureDef) error {
	// Delete existing features for this plan to allow re-seeding
	existingFeatures, err := tx.PlanFeature.Query().
		Where(planfeature.PlanIDEQ(planID)).
		All(ctx)
	if err != nil && !ent.IsNotFound(err) {
		return fmt.Errorf("query existing features: %w", err)
	}

	for _, ef := range existingFeatures {
		if err := tx.PlanFeature.DeleteOne(ef).Exec(ctx); err != nil {
			return fmt.Errorf("delete existing feature: %w", err)
		}
	}

	for _, f := range features {
		builder := tx.PlanFeature.Create().
			SetPlanID(planID).
			SetFeatureCode(f.code).
			SetIsIncluded(true).
			SetOverageUnitPrice(f.overageUnitPrice)
		if f.limitValue > 0 {
			builder.SetLimitValue(f.limitValue)
		}
		if _, err := builder.Save(ctx); err != nil {
			return fmt.Errorf("create feature %s: %w", f.code, err)
		}
	}

	return nil
}

// ── RBAC Permissions ────────────────────────────────────────────────────────

func seedRBACPermissions(ctx context.Context, tx *ent.Tx) error {
	type permDef struct {
		code   string
		name   string
		module string
		action string
	}

	modules := []string{"plans", "features", "bundles", "pricing", "subscriptions", "usage", "billing", "config", "users"}
	actions := []string{"add", "view", "view_own", "change", "change_own", "delete", "delete_own", "manage", "manage_own"}

	var perms []permDef
	for _, mod := range modules {
		for _, act := range actions {
			code := fmt.Sprintf("subscriptions.%s.%s", mod, act)
			name := fmt.Sprintf("%s %s", capitalise(act), capitalise(mod))
			perms = append(perms, permDef{
				code:   code,
				name:   name,
				module: mod,
				action: act,
			})
		}
	}

	for _, p := range perms {
		permID := uuid.NewSHA1(uuid.NameSpaceOID, []byte(p.code))
		err := tx.SubscriptionsPermission.Create().
			SetID(permID).
			SetPermissionCode(p.code).
			SetName(p.name).
			SetModule(p.module).
			SetAction(p.action).
			SetResource(p.module).
			OnConflictColumns(subscriptionspermission.FieldPermissionCode).
			DoNothing().
			Exec(ctx)
		if err != nil && err.Error() != "sql: no rows in result set" {
			return fmt.Errorf("create permission %s: %w", p.code, err)
		}
	}

	// Seed system roles per existing tenant
	// NOTE: Roles are tenant-scoped. We seed them for ALL tenants in the database.
	tenants, err := tx.Tenant.Query().All(ctx)
	if err != nil {
		return fmt.Errorf("list tenants for role seeding: %w", err)
	}

	type roleDef struct {
		code        string
		name        string
		description string
		permModules []string // modules this role gets ALL actions for
	}

	roles := []roleDef{
		{
			code:        "subscriptions_admin",
			name:        "Subscriptions Admin",
			description: "Full access to all subscriptions management features",
			permModules: modules, // all modules
		},
		{
			code:        "billing_manager",
			name:        "Billing Manager",
			description: "Manage billing, subscriptions, and pricing",
			permModules: []string{"subscriptions", "billing", "pricing", "usage"},
		},
		{
			code:        "viewer",
			name:        "Viewer",
			description: "Read-only access to subscriptions data",
			permModules: nil, // will assign view + view_own only
		},
	}

	for _, t := range tenants {
		for _, rd := range roles {
			roleID := uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf("%s:%s", t.ID, rd.code)))
			err := tx.SubscriptionsRole.Create().
				SetID(roleID).
				SetTenantID(t.ID).
				SetRoleCode(rd.code).
				SetName(rd.name).
				SetDescription(rd.description).
				SetIsSystemRole(true).
				OnConflict(
					entsql.ConflictColumns("tenant_id", "role_code"),
				).
				DoNothing().
				Exec(ctx)
			if err != nil && err.Error() != "sql: no rows in result set" {
				return fmt.Errorf("create role %s for tenant %s: %w", rd.code, t.Slug, err)
			}

			// Assign permissions to role
			var permCodes []string
			if rd.code == "viewer" {
				for _, mod := range modules {
					for _, act := range []string{"view", "view_own"} {
						permCodes = append(permCodes, fmt.Sprintf("subscriptions.%s.%s", mod, act))
					}
				}
			} else {
				for _, mod := range rd.permModules {
					for _, act := range actions {
						permCodes = append(permCodes, fmt.Sprintf("subscriptions.%s.%s", mod, act))
					}
				}
			}
			for _, code := range permCodes {
				permID := uuid.NewSHA1(uuid.NameSpaceOID, []byte(code))
				err := tx.RolePermission.Create().
					SetRoleID(roleID).
					SetPermissionID(permID).
					OnConflict(
						entsql.ConflictColumns("role_id", "permission_id"),
					).
					DoNothing().
					Exec(ctx)
				if err != nil && err.Error() != "sql: no rows in result set" {
					return fmt.Errorf("assign permission %s to role %s: %w", code, rd.code, err)
				}
			}
		}
	}

	log.Println("  ✓ RBAC permissions and roles seeded")
	return nil
}

func capitalise(s string) string {
	if s == "" {
		return s
	}
	// Replace underscores with spaces and capitalise first letter
	s = strings.ReplaceAll(s, "_", " ")
	return strings.ToUpper(s[:1]) + s[1:]
}

// ── Rate Limit Configs ──────────────────────────────────────────────────────

func seedRateLimitConfigs(ctx context.Context, tx *ent.Tx) error {
	type rlDef struct {
		serviceName       string
		keyType           string
		endpointPattern   string
		requestsPerWindow int
		windowSeconds     int
		burstMultiplier   float64
		description       string
	}

	configs := []rlDef{
		{
			serviceName:       "subscriptions-api",
			keyType:           "ip",
			endpointPattern:   "*",
			requestsPerWindow: 120,
			windowSeconds:     60,
			burstMultiplier:   2.0,
			description:       "Default IP-based rate limit for subscriptions-api",
		},
		{
			serviceName:       "subscriptions-api",
			keyType:           "tenant",
			endpointPattern:   "*",
			requestsPerWindow: 300,
			windowSeconds:     60,
			burstMultiplier:   1.5,
			description:       "Default tenant-based rate limit for subscriptions-api",
		},
		{
			serviceName:       "subscriptions-api",
			keyType:           "user",
			endpointPattern:   "*",
			requestsPerWindow: 60,
			windowSeconds:     60,
			burstMultiplier:   1.5,
			description:       "Default user-based rate limit for subscriptions-api",
		},
		{
			serviceName:       "subscriptions-api",
			keyType:           "endpoint",
			endpointPattern:   "/api/v1/subscription",
			requestsPerWindow: 30,
			windowSeconds:     60,
			burstMultiplier:   1.0,
			description:       "Rate limit for subscription creation/modification",
		},
		{
			serviceName:       "subscriptions-api",
			keyType:           "endpoint",
			endpointPattern:   "/api/v1/usage/report",
			requestsPerWindow: 600,
			windowSeconds:     60,
			burstMultiplier:   3.0,
			description:       "Higher limit for usage reporting (called by other microservices)",
		},
	}

	for _, c := range configs {
		id := uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf("rl:%s:%s:%s", c.serviceName, c.keyType, c.endpointPattern)))
		exists, _ := tx.RateLimitConfig.Get(ctx, id)
		if exists != nil {
			continue
		}
		_, err := tx.RateLimitConfig.Create().
			SetID(id).
			SetServiceName(c.serviceName).
			SetKeyType(c.keyType).
			SetEndpointPattern(c.endpointPattern).
			SetRequestsPerWindow(c.requestsPerWindow).
			SetWindowSeconds(c.windowSeconds).
			SetBurstMultiplier(c.burstMultiplier).
			SetIsActive(true).
			SetDescription(c.description).
			Save(ctx)
		if err != nil {
			return fmt.Errorf("create rate limit config %s/%s/%s: %w", c.serviceName, c.keyType, c.endpointPattern, err)
		}
	}

	log.Println("  ✓ Rate limit configs seeded")
	return nil
}

// ── Service Configs ─────────────────────────────────────────────────────────

func seedServiceConfigs(ctx context.Context, tx *ent.Tx) error {
	type scDef struct {
		configKey   string
		configValue string
		configType  string
		description string
		isSecret    bool
	}

	configs := []scDef{
		{
			configKey:   "subscriptions.trial_days",
			configValue: "14",
			configType:  "int",
			description: "Default number of trial days for new subscriptions",
		},
		{
			configKey:   "subscriptions.max_plans_per_tenant",
			configValue: "1",
			configType:  "int",
			description: "Maximum active subscription plans per tenant",
		},
		{
			configKey:   "subscriptions.grace_period_days",
			configValue: "7",
			configType:  "int",
			description: "Days after expiration before access is revoked",
		},
		{
			configKey:   "subscriptions.auto_renew_default",
			configValue: "true",
			configType:  "bool",
			description: "Whether new subscriptions auto-renew by default",
		},
		{
			configKey:   "subscriptions.usage_reporting_interval_seconds",
			configValue: "300",
			configType:  "int",
			description: "Minimum interval between usage reports from the same service",
		},
		{
			configKey:   "subscriptions.feature_cache_ttl_seconds",
			configValue: "60",
			configType:  "int",
			description: "TTL for feature entitlement cache in Redis",
		},
		{
			configKey:   "subscriptions.rbac_enabled",
			configValue: "true",
			configType:  "bool",
			description: "Whether RBAC enforcement is active",
		},
	}

	for _, c := range configs {
		id := uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf("sc::%s", c.configKey)))
		exists, _ := tx.ServiceConfig.Get(ctx, id)
		if exists != nil {
			continue
		}
		_, err := tx.ServiceConfig.Create().
			SetID(id).
			SetConfigKey(c.configKey).
			SetConfigValue(c.configValue).
			SetConfigType(c.configType).
			SetDescription(c.description).
			SetIsSecret(c.isSecret).
			Save(ctx)
		if err != nil {
			return fmt.Errorf("create service config %s: %w", c.configKey, err)
		}
	}

	log.Println("  ✓ Service configs seeded")
	return nil
}
