package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/bengobox/subscription-service/internal/config"
	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/planfeature"
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

	// 2.11 Seed MarketFlow CRM plans
	if err := seedMarketFlowPlans(ctx, tx); err != nil {
		return fmt.Errorf("seed marketflow plans: %w", err)
	}

	// 2.12 Seed Treasury & Finance standalone plans
	if err := seedTreasuryPlans(ctx, tx); err != nil {
		return fmt.Errorf("seed treasury plans: %w", err)
	}

	// 2.13 Seed ISP Billing standalone plans (hotspot + PPPoE product lines)
	if err := seedISPBillingPlans(ctx, tx); err != nil {
		return fmt.Errorf("seed isp_billing plans: %w", err)
	}

	// 2.14 Seed Projects & Invoicing standalone plans
	if err := seedProjectsPlans(ctx, tx); err != nil {
		return fmt.Errorf("seed projects plans: %w", err)
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
