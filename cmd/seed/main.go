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
	"github.com/bengobox/subscription-service/internal/ent/subscriptionplan"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	// Prefer the direct PostgreSQL URL to bypass PgBouncer for seed DDL/DML.
	dbURL := cfg.Postgres.URL
	if cfg.Postgres.MigrateURL != "" {
		dbURL = cfg.Postgres.MigrateURL
	}

	db, err := sql.Open("pgx", dbURL)
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

	// 0. Seed the platform feature/limit catalog (single source of truth, per service).
	// Must run before plan seeds so plan features can be validated against the catalog.
	if err := seedFeatureCatalog(ctx, tx); err != nil {
		return fmt.Errorf("seed feature catalog: %w", err)
	}

	// 1. Seed products (microservices)
	if err := seedProducts(ctx, tx); err != nil {
		return fmt.Errorf("seed products: %w", err)
	}

	// 2. Seed ordering plans (Starter, Growth, Professional — monthly + yearly)
	if err := seedOrderingPlans(ctx, tx); err != nil {
		return fmt.Errorf("seed ordering plans: %w", err)
	}

	// 2.1 Seed bundle-specific plans (POS_SUITE_*, COMPLETE_*, ORDERING_SETUP_FEE)
	if err := seedBundlePlans(ctx, tx); err != nil {
		return fmt.Errorf("seed bundle plans: %w", err)
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

	// 3.4 Seed purchasable addon features (is_included=false) for each plan
	if err := seedPlanAddonFeatures(ctx, tx); err != nil {
		return fmt.Errorf("seed plan addon features: %w", err)
	}

	// 3.5 Seed service charge plans
	if err := seedServiceChargePlans(ctx, tx); err != nil {
		return fmt.Errorf("seed service charge plans: %w", err)
	}

	// 3.55 Reconcile every plan's tier_limits_json keys against the feature catalog:
	// canonicalize aliases and warn on any key that is not a catalog LIMIT (or, for
	// standalone-service plans, belongs to a different service). Keeps plan limits
	// linked to the catalog instead of drifting as free-form strings.
	if err := reconcilePlanTierLimits(ctx, tx); err != nil {
		return fmt.Errorf("reconcile plan tier limits: %w", err)
	}

	// 3.6 Seed subscription coupons (platform discount codes for tenant billing)
	if err := seedCoupons(ctx, tx); err != nil {
		return fmt.Errorf("seed coupons: %w", err)
	}

	// 4. Seed RBAC permissions
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

// universalFeatures are added to every plan at every tier.
// custom_branding represents the tenant branding tab in auth-ui (logo, primary
// color, font) which syncs to all downstream services via Redis — available
// to all tenants regardless of plan level.
var universalFeatures = []string{"custom_branding"}

func seedPlanFeatures(ctx context.Context, tx *ent.Tx, planID uuid.UUID, featureCodes []string) error {
	// Merge universal features (deduplicated) then convert to featureDefs
	seen := make(map[string]bool, len(featureCodes)+len(universalFeatures))
	merged := make([]string, 0, len(featureCodes)+len(universalFeatures))
	for _, c := range featureCodes {
		if !seen[c] {
			seen[c] = true
			merged = append(merged, c)
		}
	}
	for _, c := range universalFeatures {
		if !seen[c] {
			seen[c] = true
			merged = append(merged, c)
		}
	}

	defs := make([]featureDef, len(merged))
	for i, code := range merged {
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

	// Normalize aliases to canonical catalog codes, dedupe, and warn on codes
	// that are not present in the feature catalog (keeps the catalog authoritative).
	seen := make(map[string]bool, len(features))
	for _, f := range features {
		code := canonicalFeatureCode(f.code)
		if seen[code] {
			continue // duplicate after alias normalization (e.g. paystack_gateway + paystack_integration)
		}
		seen[code] = true

		if !isInCatalog(code) {
			log.Printf("WARN seed: plan feature %q is not in the feature catalog — add it to featureCatalog in feature_catalog.go", code)
		}

		builder := tx.PlanFeature.Create().
			SetPlanID(planID).
			SetFeatureCode(code).
			SetIsIncluded(true).
			SetOverageUnitPrice(f.overageUnitPrice)
		if f.limitValue > 0 {
			builder.SetLimitValue(f.limitValue)
		}
		if _, err := builder.Save(ctx); err != nil {
			return fmt.Errorf("create feature %s: %w", code, err)
		}
	}

	return nil
}

// reconcilePlanTierLimits links every plan's tier_limits_json keys back to the
// feature catalog. For each plan it canonicalizes alias keys, warns on any key
// that is not a catalog LIMIT, and — for standalone-service plans only — warns on
// keys owned by a different service. It NEVER drops keys (that would corrupt the
// flat Limits map emitted by buildResult into JWT sub_limits and the per-service
// gates); unknown keys surface as warnings for manual follow-up. The row is only
// rewritten when canonicalization actually changed a key, so it is idempotent and
// a no-op on data that already uses canonical limit codes.
func reconcilePlanTierLimits(ctx context.Context, tx *ent.Tx) error {
	plans, err := tx.SubscriptionPlan.Query().All(ctx)
	if err != nil {
		return fmt.Errorf("query plans for tier-limit reconcile: %w", err)
	}
	for _, p := range plans {
		if len(p.TierLimitsJSON) == 0 {
			continue
		}
		planTag := ""
		if p.ServiceTag != nil {
			planTag = *p.ServiceTag
		}
		standalone := p.PlanType == subscriptionplan.PlanTypeSTANDALONE_SERVICE

		rewritten := make(map[string]any, len(p.TierLimitsJSON))
		changed := false
		for k, v := range p.TierLimitsJSON {
			canon := canonicalFeatureCode(k)
			if canon != k {
				changed = true
			}
			svc, ok := limitServiceTag(canon)
			switch {
			case !ok:
				log.Printf("WARN seed: plan %q tier_limit %q is not a LIMIT in the feature catalog — add it to featureCatalog in feature_catalog.go", p.PlanCode, k)
			case standalone && svc != "platform" && planTag != "" && svc != planTag:
				log.Printf("WARN seed: plan %q (service=%s) tier_limit %q belongs to service %q", p.PlanCode, planTag, k, svc)
			}
			rewritten[canon] = v // last-writer-wins if an alias collides with its canonical form
		}
		if !changed {
			continue
		}
		if _, err := tx.SubscriptionPlan.UpdateOneID(p.ID).SetTierLimitsJSON(rewritten).Save(ctx); err != nil {
			return fmt.Errorf("rewrite tier limits for plan %s: %w", p.PlanCode, err)
		}
		log.Printf("  reconciled tier_limits for plan %s", p.PlanCode)
	}
	return nil
}
