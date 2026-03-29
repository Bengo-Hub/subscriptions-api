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
	"github.com/bengobox/subscription-service/internal/ent/bundle"
	"github.com/bengobox/subscription-service/internal/ent/planfeature"
	"github.com/bengobox/subscription-service/internal/ent/product"
	"github.com/bengobox/subscription-service/internal/ent/productsubscription"
	"github.com/bengobox/subscription-service/internal/ent/schema"
	"github.com/bengobox/subscription-service/internal/ent/servicechargeplan"
	"github.com/bengobox/subscription-service/internal/ent/subscriptionplan"
	"github.com/bengobox/subscription-service/internal/ent/subscriptionspermission"
	"github.com/bengobox/subscription-service/internal/ent/tenantsubscription"
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

	db.SetMaxIdleConns(10)
	db.SetMaxOpenConns(25)
	db.SetConnMaxIdleTime(5 * time.Minute)

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

// ── Products ─────────────────────────────────────────────────────────────────

func seedProducts(ctx context.Context, tx *ent.Tx) error {
	type productDef struct {
		id            uuid.UUID
		code          string
		name          string
		description   string
		category      product.Category
		isPlatform    bool
		isBaseService bool
		monthlyPrice  float64
		yearlyPrice   float64
		onetimePrice  float64
		dependencies  []string
		sortOrder     int
	}

	products := []productDef{
		// Platform products — always included, no extra cost
		{
			id:            uuid.MustParse("10000000-0000-0000-0000-000000000001"),
			code:          "auth",
			name:          "Authentication & SSO",
			description:   "User authentication, multi-factor auth, OAuth 2.0, OpenID Connect, session management, and single sign-on across all BengoBox services.",
			category:      product.CategoryPlatform,
			isPlatform:    true,
			isBaseService: true,
			monthlyPrice:  0,
			yearlyPrice:   0,
			onetimePrice:  0,
			sortOrder:     1,
		},
		{
			id:            uuid.MustParse("10000000-0000-0000-0000-000000000002"),
			code:          "notifications",
			name:          "Notifications",
			description:   "Multi-channel notification delivery: email (SMTP/SendGrid), SMS (Africa's Talking), and push notifications with template management.",
			category:      product.CategoryPlatform,
			isPlatform:    true,
			isBaseService: true,
			monthlyPrice:  0,
			yearlyPrice:   0,
			onetimePrice:  0,
			sortOrder:     2,
		},
		{
			id:            uuid.MustParse("10000000-0000-0000-0000-000000000003"),
			code:          "subscription",
			name:          "Subscription Management",
			description:   "Plan management, billing lifecycle, feature entitlements, usage tracking, and tier enforcement.",
			category:      product.CategoryPlatform,
			isPlatform:    true,
			isBaseService: true,
			monthlyPrice:  0,
			yearlyPrice:   0,
			onetimePrice:  0,
			sortOrder:     3,
		},

		// Core products — subscribable individually or via bundles
		{
			id:           uuid.MustParse("10000000-0000-0000-0000-000000000010"),
			code:         "ordering",
			name:         "Online Ordering",
			description:  "Customer-facing ordering portal (web + PWA), menu management, cart & checkout, order lifecycle tracking, and M-Pesa payment integration.",
			category:     product.CategoryProduct,
			monthlyPrice: 1500,
			yearlyPrice:  15000,
			onetimePrice: 100000,
			sortOrder:    10,
		},
		{
			id:           uuid.MustParse("10000000-0000-0000-0000-000000000011"),
			code:         "logistics",
			name:         "Delivery & Logistics",
			description:  "Rider management, delivery assignment, real-time tracking, route optimization, and rider mobile app.",
			category:     product.CategoryProduct,
			monthlyPrice: 1500,
			yearlyPrice:  15000,
			onetimePrice: 80000,
			dependencies: []string{"ordering"},
			sortOrder:    11,
		},
		{
			id:            uuid.MustParse("10000000-0000-0000-0000-000000000012"),
			code:          "treasury",
			name:          "Payments & Invoicing",
			description:   "Payment processing, M-Pesa integration, invoicing, receipts, financial reporting, and revenue tracking.",
			category:      product.CategoryProduct,
			isBaseService: true,
			monthlyPrice:  1000,
			yearlyPrice:   10000,
			onetimePrice:  80000,
			sortOrder:     12,
		},

		// Add-on products
		{
			id:           uuid.MustParse("10000000-0000-0000-0000-000000000020"),
			code:         "pos",
			name:         "Point of Sale",
			description:  "In-store point-of-sale terminal integration, walk-in order management, and cash register support.",
			category:     product.CategoryAddOn,
			monthlyPrice: 2000,
			yearlyPrice:  20000,
			onetimePrice: 120000,
			dependencies: []string{"treasury"},
			sortOrder:    20,
		},
		{
			id:           uuid.MustParse("10000000-0000-0000-0000-000000000021"),
			code:         "storefront",
			name:         "Website & Storefront",
			description:  "Branded website with menu display, business info, SEO, custom domain, and social media integration.",
			category:     product.CategoryAddOn,
			monthlyPrice: 500,
			yearlyPrice:  5000,
			onetimePrice: 80000,
			sortOrder:    21,
		},

		// Integration add-ons — purchased per-tenant, enable premium features
		{
			id:           uuid.MustParse("10000000-0000-0000-0000-000000000030"),
			code:         "google_maps",
			name:         "Google Maps Integration",
			description:  "Google Maps API for live delivery tracking with accurate ETAs, geocoding, and route visualization. Default: OpenStreetMap (free).",
			category:     product.CategoryAddOn,
			monthlyPrice: 500,
			yearlyPrice:  5000,
			onetimePrice: 80000,
			sortOrder:    30,
		},
		{
			id:           uuid.MustParse("10000000-0000-0000-0000-000000000031"),
			code:         "paystack_gateway",
			name:         "Paystack Payment Gateway",
			description:  "Paystack card and mobile money payments. Enables Visa/Mastercard, bank transfers, and USSD payments alongside M-Pesa.",
			category:     product.CategoryAddOn,
			monthlyPrice: 300,
			yearlyPrice:  3000,
			onetimePrice: 80000,
			sortOrder:    31,
		},
		{
			id:           uuid.MustParse("10000000-0000-0000-0000-000000000032"),
			code:         "sms_credits",
			name:         "SMS Credit Pack",
			description:  "Bulk SMS credits for order notifications, OTP verification, and marketing. Base plan includes 100 SMS/month; this adds 500 extra/month.",
			category:     product.CategoryAddOn,
			monthlyPrice: 200,
			yearlyPrice:  2000,
			onetimePrice: 80000,
			sortOrder:    32,
		},
		{
			id:           uuid.MustParse("10000000-0000-0000-0000-000000000033"),
			code:         "premium_support",
			name:         "Premium Support",
			description:  "Dedicated support channel with 4-hour SLA, priority issue resolution, and custom integrations assistance.",
			category:     product.CategoryAddOn,
			monthlyPrice: 1000,
			yearlyPrice:  10000,
			onetimePrice: 80000,
			sortOrder:    33,
		},
	}

	for _, p := range products {
		existing, err := tx.Product.Get(ctx, p.id)
		if err != nil && !ent.IsNotFound(err) {
			return fmt.Errorf("lookup product %s: %w", p.code, err)
		}

		if existing != nil {
			builder := tx.Product.UpdateOneID(p.id).
				SetCode(p.code).
				SetName(p.name).
				SetDescription(p.description).
				SetCategory(p.category).
				SetStatus(product.StatusActive).
				SetIsPlatform(p.isPlatform).
				SetIsBaseService(p.isBaseService).
				SetMonthlyPrice(p.monthlyPrice).
				SetYearlyPrice(p.yearlyPrice).
				SetOnetimePrice(p.onetimePrice).
				SetIncludedInBundle(true).
				SetSortOrder(p.sortOrder)
			if len(p.dependencies) > 0 {
				builder = builder.SetDependencies(p.dependencies)
			} else {
				builder = builder.ClearDependencies()
			}
			if _, err := builder.Save(ctx); err != nil {
				return fmt.Errorf("update product %s: %w", p.code, err)
			}
		} else {
			builder := tx.Product.Create().
				SetID(p.id).
				SetCode(p.code).
				SetName(p.name).
				SetDescription(p.description).
				SetCategory(p.category).
				SetStatus(product.StatusActive).
				SetIsPlatform(p.isPlatform).
				SetIsBaseService(p.isBaseService).
				SetMonthlyPrice(p.monthlyPrice).
				SetYearlyPrice(p.yearlyPrice).
				SetOnetimePrice(p.onetimePrice).
				SetIncludedInBundle(true).
				SetSortOrder(p.sortOrder)
			if len(p.dependencies) > 0 {
				builder = builder.SetDependencies(p.dependencies)
			}
			if _, err := builder.Save(ctx); err != nil {
				return fmt.Errorf("create product %s: %w", p.code, err)
			}
		}

		log.Printf("  product: %s (%s)", p.name, p.code)
	}

	return nil
}

// ── Subscription Plans ───────────────────────────────────────────────────────
// Derived from Urban Cafe Food Delivery System Inception Report v1.0 (Nov 2025).
// Three tiers: Starter (Lite), Growth (Standard), Professional (Scale).
// Each tier has monthly and yearly billing variants (yearly = 10 months pricing).

func seedSubscriptionPlans(ctx context.Context, tx *ent.Tx) error {
	now := time.Now()

	type planDef struct {
		id           uuid.UUID
		planCode     string
		name         string
		description  string
		billingCycle string
		planType     subscriptionplan.PlanType
		price        float64
		tierOrder    int
		tierLimits   map[string]any
		features     []string
	}

	plans := []planDef{
		// ── Monthly Plans ────────────────────────────────────────────────
		{
			id:           uuid.MustParse("00000000-0000-0000-0000-000000000001"),
			planCode:     "STARTER",
			name:         "Starter (Lite)",
			description:  "Perfect for small cafes and pilot operations. Core ordering features with essential admin tools.",
			billingCycle: "MONTHLY",
			planType:     subscriptionplan.PlanTypeTIERED,
			price:        2500.0,
			tierOrder:    1,
			tierLimits: map[string]any{
				"max_admins":                    2,
				"max_riders":                    5,
				"max_orders_per_day":            300,
				"max_outlets":                   1,
				"api_calls_per_month":           10000,
				"live_tracking_requests_per_day": 500,
				"live_tracking_duration_minutes": 30,
				"routing_requests_per_day":       100,
				"map_loads_per_day":              200,
				"email_notifications_per_day":    50,
				"webhook_calls_per_day":          100,
				// Overage rates
				"overage_rider_price_per_month":      250.0,
				"overage_orders_price_per_100_month": 375.0,
			},
			features: []string{
				"customer_portal",
				"rider_app",
				"admin_dashboard",
				"mpesa_integration",
				"sms_notifications",
				"push_notifications",
				"basic_analytics",
				"custom_domain",
				"openstreetmap_tracking",
			},
		},
		{
			id:           uuid.MustParse("00000000-0000-0000-0000-000000000002"),
			planCode:     "GROWTH",
			name:         "Growth (Standard)",
			description:  "Ideal for growing cafes with multiple outlets. Advanced features including loyalty program and multi-outlet support.",
			billingCycle: "MONTHLY",
			planType:     subscriptionplan.PlanTypeTIERED,
			price:        6000.0,
			tierOrder:    2,
			tierLimits: map[string]any{
				"max_admins":                         3,
				"max_riders":                         15,
				"max_orders_per_day":                 1000,
				"max_outlets":                        3,
				"api_calls_per_month":                50000,
				"live_tracking_requests_per_day":     5000,
				"live_tracking_duration_minutes":     120,
				"routing_requests_per_day":           1000,
				"map_loads_per_day":                  2000,
				"email_notifications_per_day":        500,
				"webhook_calls_per_day":              1000,
				"overage_rider_price_per_month":      250.0,
				"overage_orders_price_per_100_month": 375.0,
			},
			features: []string{
				"customer_portal",
				"rider_app",
				"admin_dashboard",
				"mpesa_integration",
				"sms_notifications",
				"push_notifications",
				"basic_analytics",
				"custom_domain",
				"openstreetmap_tracking",
				"loyalty_program",
				"multi_outlet",
				"advanced_analytics",
				"promo_codes",
				"group_ordering",
				"paystack_gateway",
			},
		},
		{
			id:           uuid.MustParse("00000000-0000-0000-0000-000000000003"),
			planCode:     "PROFESSIONAL",
			name:         "Professional (Scale)",
			description:  "For multi-branch cafes and chains. Complete feature set with POS integration, route optimization, and priority support.",
			billingCycle: "MONTHLY",
			planType:     subscriptionplan.PlanTypeTIERED,
			price:        12500.0,
			tierOrder:    3,
			tierLimits: map[string]any{
				"max_admins":                         -1, // Unlimited
				"max_riders":                         30,
				"max_orders_per_day":                 2500,
				"max_outlets":                        -1, // Unlimited
				"api_calls_per_month":                200000,
				"live_tracking_requests_per_day":     -1, // Unlimited
				"live_tracking_duration_minutes":     -1, // Unlimited
				"routing_requests_per_day":           10000,
				"map_loads_per_day":                  -1, // Unlimited
				"email_notifications_per_day":        5000,
				"webhook_calls_per_day":              10000,
				"overage_rider_price_per_month":      250.0,
				"overage_orders_price_per_100_month": 375.0,
			},
			features: []string{
				"customer_portal",
				"rider_app",
				"admin_dashboard",
				"mpesa_integration",
				"sms_notifications",
				"push_notifications",
				"basic_analytics",
				"custom_domain",
				"openstreetmap_tracking",
				"loyalty_program",
				"multi_outlet",
				"advanced_analytics",
				"promo_codes",
				"group_ordering",
				"paystack_gateway",
				"pos_integration",
				"route_optimization",
				"priority_support",
				"api_webhooks",
				"white_labeling",
				"google_maps",
				"premium_support",
			},
		},

		// ── Yearly Plans (≈10 months pricing = ~8.3% discount) ──────────
		{
			id:           uuid.MustParse("00000000-0000-0000-0000-000000000011"),
			planCode:     "STARTER_YEARLY",
			name:         "Starter (Lite) — Annual",
			description:  "Perfect for small cafes and pilot operations. Save with annual billing.",
			billingCycle: "ANNUAL",
			planType:     subscriptionplan.PlanTypeTIERED,
			price:        27500.0,
			tierOrder:    1,
			tierLimits: map[string]any{
				"max_admins":                         2,
				"max_riders":                         5,
				"max_orders_per_day":                 300,
				"max_outlets":                        1,
				"api_calls_per_month":                10000,
				"live_tracking_requests_per_day":     500,
				"live_tracking_duration_minutes":     30,
				"routing_requests_per_day":           100,
				"map_loads_per_day":                  200,
				"email_notifications_per_day":        50,
				"webhook_calls_per_day":              100,
				"overage_rider_price_per_month":      250.0,
				"overage_orders_price_per_100_month": 375.0,
			},
			features: []string{
				"customer_portal",
				"rider_app",
				"admin_dashboard",
				"mpesa_integration",
				"sms_notifications",
				"push_notifications",
				"basic_analytics",
				"custom_domain",
				"openstreetmap_tracking",
			},
		},
		{
			id:           uuid.MustParse("00000000-0000-0000-0000-000000000012"),
			planCode:     "GROWTH_YEARLY",
			name:         "Growth (Standard) — Annual",
			description:  "Ideal for growing cafes with multiple outlets. Save with annual billing.",
			billingCycle: "ANNUAL",
			planType:     subscriptionplan.PlanTypeTIERED,
			price:        66000.0,
			tierOrder:    2,
			tierLimits: map[string]any{
				"max_admins":                         3,
				"max_riders":                         15,
				"max_orders_per_day":                 1000,
				"max_outlets":                        3,
				"api_calls_per_month":                50000,
				"live_tracking_requests_per_day":     5000,
				"live_tracking_duration_minutes":     120,
				"routing_requests_per_day":           1000,
				"map_loads_per_day":                  2000,
				"email_notifications_per_day":        500,
				"webhook_calls_per_day":              1000,
				"overage_rider_price_per_month":      250.0,
				"overage_orders_price_per_100_month": 375.0,
			},
			features: []string{
				"customer_portal",
				"rider_app",
				"admin_dashboard",
				"mpesa_integration",
				"sms_notifications",
				"push_notifications",
				"basic_analytics",
				"custom_domain",
				"openstreetmap_tracking",
				"loyalty_program",
				"multi_outlet",
				"advanced_analytics",
				"promo_codes",
				"group_ordering",
				"paystack_gateway",
			},
		},
		{
			id:           uuid.MustParse("00000000-0000-0000-0000-000000000013"),
			planCode:     "PROFESSIONAL_YEARLY",
			name:         "Professional (Scale) — Annual",
			description:  "For multi-branch cafes and chains. Save with annual billing.",
			billingCycle: "ANNUAL",
			planType:     subscriptionplan.PlanTypeTIERED,
			price:        137500.0,
			tierOrder:    3,
			tierLimits: map[string]any{
				"max_admins":                         -1,
				"max_riders":                         30,
				"max_orders_per_day":                 2500,
				"max_outlets":                        -1,
				"api_calls_per_month":                200000,
				"live_tracking_requests_per_day":     -1,
				"live_tracking_duration_minutes":     -1,
				"routing_requests_per_day":           10000,
				"map_loads_per_day":                  -1,
				"email_notifications_per_day":        5000,
				"webhook_calls_per_day":              10000,
				"overage_rider_price_per_month":      250.0,
				"overage_orders_price_per_100_month": 375.0,
			},
			features: []string{
				"customer_portal",
				"rider_app",
				"admin_dashboard",
				"mpesa_integration",
				"sms_notifications",
				"push_notifications",
				"basic_analytics",
				"custom_domain",
				"openstreetmap_tracking",
				"loyalty_program",
				"multi_outlet",
				"advanced_analytics",
				"promo_codes",
				"group_ordering",
				"paystack_gateway",
				"pos_integration",
				"route_optimization",
				"priority_support",
				"api_webhooks",
				"white_labeling",
				"google_maps",
				"premium_support",
			},
		},
	}

	for _, p := range plans {
		existing, err := tx.SubscriptionPlan.Get(ctx, p.id)
		if err != nil && !ent.IsNotFound(err) {
			return fmt.Errorf("lookup plan %s: %w", p.planCode, err)
		}

		if existing != nil {
			_, err = tx.SubscriptionPlan.UpdateOneID(p.id).
				SetPlanCode(p.planCode).
				SetName(p.name).
				SetDescription(p.description).
				SetBillingCycle(p.billingCycle).
				SetPlanType(p.planType).
				SetBasePrice(p.price).
				SetCurrency("KES").
				SetIsActive(true).
				SetIsPublic(true).
				SetTierOrder(p.tierOrder).
				SetTierLimitsJSON(p.tierLimits).
				SetUpdatedAt(now).
				Save(ctx)
			if err != nil {
				return fmt.Errorf("update plan %s: %w", p.planCode, err)
			}
		} else {
			_, err = tx.SubscriptionPlan.Create().
				SetID(p.id).
				SetPlanCode(p.planCode).
				SetName(p.name).
				SetDescription(p.description).
				SetBillingCycle(p.billingCycle).
				SetPlanType(p.planType).
				SetBasePrice(p.price).
				SetCurrency("KES").
				SetIsActive(true).
				SetIsPublic(true).
				SetTierOrder(p.tierOrder).
				SetTierLimitsJSON(p.tierLimits).
				SetCreatedAt(now).
				SetUpdatedAt(now).
				Save(ctx)
			if err != nil {
				return fmt.Errorf("create plan %s: %w", p.planCode, err)
			}
		}

		if err := seedPlanFeatures(ctx, tx, p.id, p.features); err != nil {
			return fmt.Errorf("seed features for plan %s: %w", p.planCode, err)
		}

		log.Printf("  plan: %s (%s, %s)", p.name, p.planCode, p.billingCycle)
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

// ── Bundles ──────────────────────────────────────────────────────────────────
// Curated product combinations with tiered pricing.
// The "delivery" bundle maps directly to the Urban Loft inception report tiers.

func seedBundles(ctx context.Context, tx *ent.Tx) error {
	type bundleDef struct {
		id          uuid.UUID
		code        string
		name        string
		description string
		products    []schema.BundleProduct
		tiers       []schema.BundleTier
		isDefault   bool
		sortOrder   int
	}

	bundles := []bundleDef{
		{
			id:   uuid.MustParse("20000000-0000-0000-0000-000000000001"),
			code: "delivery",
			name: "BengoBox Delivery",
			description: "Complete food delivery platform: online ordering, logistics, payments, and storefront. " +
				"Pricing matches the Urban Cafe Food Delivery System Inception Report tiers.",
			products: []schema.BundleProduct{
				{ProductCode: "ordering"},
				{ProductCode: "logistics"},
				{ProductCode: "treasury"},
				{ProductCode: "storefront"},
			},
			tiers: []schema.BundleTier{
				{PlanCode: "STARTER", MonthlyPrice: 2500, YearlyPrice: 27500},
				{PlanCode: "GROWTH", MonthlyPrice: 6000, YearlyPrice: 66000},
				{PlanCode: "PROFESSIONAL", MonthlyPrice: 12500, YearlyPrice: 137500},
			},
			isDefault: true,
			sortOrder: 1,
		},
		{
			id:          uuid.MustParse("20000000-0000-0000-0000-000000000002"),
			code:        "pos-suite",
			name:        "BengoBox POS",
			description: "In-store point of sale with payment processing. Ideal for walk-in restaurants and cafes.",
			products: []schema.BundleProduct{
				{ProductCode: "pos"},
				{ProductCode: "treasury"},
			},
			tiers: []schema.BundleTier{
				{PlanCode: "STARTER", MonthlyPrice: 2000, YearlyPrice: 22000},
				{PlanCode: "GROWTH", MonthlyPrice: 4500, YearlyPrice: 49500},
				{PlanCode: "PROFESSIONAL", MonthlyPrice: 8000, YearlyPrice: 88000},
			},
			sortOrder: 2,
		},
		{
			id:          uuid.MustParse("20000000-0000-0000-0000-000000000003"),
			code:        "complete",
			name:        "BengoBox Complete",
			description: "Everything included: online ordering, delivery logistics, POS, payments, and storefront. Best value for full-service operations.",
			products: []schema.BundleProduct{
				{ProductCode: "ordering"},
				{ProductCode: "logistics"},
				{ProductCode: "treasury"},
				{ProductCode: "pos"},
				{ProductCode: "storefront"},
			},
			tiers: []schema.BundleTier{
				{PlanCode: "STARTER", MonthlyPrice: 4000, YearlyPrice: 44000},
				{PlanCode: "GROWTH", MonthlyPrice: 9500, YearlyPrice: 104500},
				{PlanCode: "PROFESSIONAL", MonthlyPrice: 18000, YearlyPrice: 198000},
			},
			sortOrder: 3,
		},
	}

	for _, b := range bundles {
		existing, err := tx.Bundle.Get(ctx, b.id)
		if err != nil && !ent.IsNotFound(err) {
			return fmt.Errorf("lookup bundle %s: %w", b.code, err)
		}

		if existing != nil {
			_, err = tx.Bundle.UpdateOneID(b.id).
				SetCode(b.code).
				SetName(b.name).
				SetDescription(b.description).
				SetProducts(b.products).
				SetTiers(b.tiers).
				SetDiscountType(bundle.DiscountTypeNone).
				SetIsActive(true).
				SetIsDefault(b.isDefault).
				SetSortOrder(b.sortOrder).
				Save(ctx)
			if err != nil {
				return fmt.Errorf("update bundle %s: %w", b.code, err)
			}
		} else {
			_, err = tx.Bundle.Create().
				SetID(b.id).
				SetCode(b.code).
				SetName(b.name).
				SetDescription(b.description).
				SetProducts(b.products).
				SetTiers(b.tiers).
				SetDiscountType(bundle.DiscountTypeNone).
				SetIsActive(true).
				SetIsDefault(b.isDefault).
				SetSortOrder(b.sortOrder).
				Save(ctx)
			if err != nil {
				return fmt.Errorf("create bundle %s: %w", b.code, err)
			}
		}

		log.Printf("  bundle: %s (%s, %d products)", b.name, b.code, len(b.products))
	}

	return nil
}

func seedAllTenantSubscriptions(ctx context.Context, tx *ent.Tx, syncer *tenant.Syncer) error {
	// Tenant definitions with slug → plan mapping.
	// UUIDs are resolved at runtime from auth-api (see resolveTenantID above).
	// Platform owner slug (codevertex) is excluded — no tenant subscription; see TRINITY-AUTHORIZATION-PATTERN.md.
	tenantDefs := []struct {
		slug string
		name string
		plan string
	}{
		{"urban-loft", "Urban Loft Cafe", "GROWTH"},
		{"mss", "Masterspace Solutions", "GROWTH"},
		{"kura", "Kenya Urban Roads Authority", "STARTER"},
		{"ultichange", "UltiChange", "STARTER"},
	}

	type resolvedTenant struct {
		id   uuid.UUID
		slug string
		name string
		plan string
	}

	var tenants []resolvedTenant
	for _, td := range tenantDefs {
		id, err := syncer.SyncTenant(ctx, td.slug)
		if err != nil {
			// Log and skip rather than aborting — allows partial seeding during
			// initial setup when some tenants may not exist yet.
			log.Printf("  [SKIP] tenant %q: %v", td.slug, err)
			continue
		}
		tenants = append(tenants, resolvedTenant{id: id, slug: td.slug, name: td.name, plan: td.plan})
	}

	// Plan UUIDs from seedSubscriptionPlans
	planIDs := map[string]uuid.UUID{
		"STARTER":      uuid.MustParse("00000000-0000-0000-0000-000000000001"),
		"GROWTH":       uuid.MustParse("00000000-0000-0000-0000-000000000002"),
		"PROFESSIONAL": uuid.MustParse("00000000-0000-0000-0000-000000000003"),
	}

	now := time.Now()
	trialEnd := now.Add(14 * 24 * time.Hour)

	for i, t := range tenants {
		tenantSubID := uuid.MustParse(fmt.Sprintf("30000000-0000-0000-0000-00000000000%d", i+1))
		planID := planIDs[t.plan]

		// Check if subscription already exists
		existing, err := tx.TenantSubscription.Query().
			Where(tenantsubscription.TenantIDEQ(t.id)).
			First(ctx)
		if err != nil && !ent.IsNotFound(err) {
			return fmt.Errorf("lookup subscription for %s: %w", t.slug, err)
		}

		if existing != nil {
			_, err = tx.TenantSubscription.UpdateOne(existing).
				SetPlanID(planID).
				SetStatus(tenantsubscription.StatusTRIAL).
				SetTrialEndsAt(trialEnd).
				SetCurrentPeriodStart(now).
				SetCurrentPeriodEnd(trialEnd).
				SetBundleCode("delivery").
				SetMetadata(map[string]any{
					"seeded":      true,
					"tenant_name": t.name,
					"tier":        t.plan,
				}).
				Save(ctx)
			if err != nil {
				return fmt.Errorf("update subscription for %s: %w", t.slug, err)
			}
			log.Printf("  subscription: %s → %s (trial, updated)", t.name, t.plan)
		} else {
			_, err = tx.TenantSubscription.Create().
				SetID(tenantSubID).
				SetTenantID(t.id).
				SetPlanID(planID).
				SetStatus(tenantsubscription.StatusTRIAL).
				SetTrialEndsAt(trialEnd).
				SetCurrentPeriodStart(now).
				SetCurrentPeriodEnd(trialEnd).
				SetBundleCode("delivery").
				SetMetadata(map[string]any{
					"seeded":      true,
					"tenant_name": t.name,
					"tier":        t.plan,
				}).
				Save(ctx)
			if err != nil {
				return fmt.Errorf("create subscription for %s: %w", t.slug, err)
			}
			log.Printf("  subscription: %s → %s (trial, 14 days)", t.name, t.plan)
		}

		// Activate delivery bundle products for the subscription
		if err := activateDeliveryProducts(ctx, tx, tenantSubID, now); err != nil {
			return fmt.Errorf("activate products for %s: %w", t.slug, err)
		}
	}

	return nil
}

func activateDeliveryProducts(ctx context.Context, tx *ent.Tx, subID uuid.UUID, now time.Time) error {
	deliveryProducts := []struct {
		id   uuid.UUID
		code string
	}{
		{uuid.MustParse("10000000-0000-0000-0000-000000000010"), "ordering"},
		{uuid.MustParse("10000000-0000-0000-0000-000000000011"), "logistics"},
		{uuid.MustParse("10000000-0000-0000-0000-000000000012"), "treasury"},
		{uuid.MustParse("10000000-0000-0000-0000-000000000021"), "storefront"},
	}

	for _, p := range deliveryProducts {
		existingPS, err := tx.ProductSubscription.Query().
			Where(
				productsubscription.TenantSubscriptionIDEQ(subID),
				productsubscription.ProductCodeEQ(p.code),
			).
			First(ctx)
		if err != nil && !ent.IsNotFound(err) {
			return fmt.Errorf("lookup product subscription %s: %w", p.code, err)
		}

		if existingPS != nil {
			_, err = tx.ProductSubscription.UpdateOne(existingPS).
				SetProductID(p.id).
				SetStatus(productsubscription.StatusActive).
				SetActivatedAt(now).
				Save(ctx)
			if err != nil {
				return fmt.Errorf("update product subscription %s: %w", p.code, err)
			}
		} else {
			_, err = tx.ProductSubscription.Create().
				SetTenantSubscriptionID(subID).
				SetProductCode(p.code).
				SetProductID(p.id).
				SetStatus(productsubscription.StatusActive).
				SetActivatedAt(now).
				Save(ctx)
			if err != nil {
				return fmt.Errorf("create product subscription %s: %w", p.code, err)
			}
		}
	}

	return nil
}

// ── Service Charge Plans ─────────────────────────────────────────────────────

func seedServiceChargePlans(ctx context.Context, tx *ent.Tx) error {
	type scPlan struct {
		code        string
		name        string
		description string
		chargeType  servicechargeplan.ChargeType
		chargeValue float64
		minCharge   *float64
		maxCharge   *float64
		services    []string
		isDefault   bool
	}

	minCharge50 := 50.0
	maxCharge5000 := 5000.0
	maxCharge2000 := 2000.0

	plans := []scPlan{
		{
			code:        "SC_ORDERING_5PCT",
			name:        "Ordering 5% Service Charge",
			description: "Platform takes 5% of each online order transaction amount. Suitable for small tenants who prefer no monthly subscription fee.",
			chargeType:  servicechargeplan.ChargeTypePERCENTAGE,
			chargeValue: 5.0,
			minCharge:   &minCharge50,
			maxCharge:   &maxCharge5000,
			services:    []string{"ordering"},
			isDefault:   true,
		},
		{
			code:        "SC_ORDERING_3PCT",
			name:        "Ordering 3% Service Charge",
			description: "Reduced 3% service charge for high-volume ordering tenants.",
			chargeType:  servicechargeplan.ChargeTypePERCENTAGE,
			chargeValue: 3.0,
			minCharge:   &minCharge50,
			maxCharge:   &maxCharge5000,
			services:    []string{"ordering"},
		},
		{
			code:        "SC_LOGISTICS_7PCT",
			name:        "Logistics 7% Service Charge",
			description: "Platform takes 7% of each delivery fee processed through logistics service.",
			chargeType:  servicechargeplan.ChargeTypePERCENTAGE,
			chargeValue: 7.0,
			services:    []string{"logistics"},
			isDefault:   true,
		},
		{
			code:        "SC_POS_2PCT",
			name:        "POS 2% Service Charge",
			description: "Platform takes 2% of each POS transaction.",
			chargeType:  servicechargeplan.ChargeTypePERCENTAGE,
			chargeValue: 2.0,
			maxCharge:   &maxCharge2000,
			services:    []string{"pos"},
			isDefault:   true,
		},
		{
			code:        "SC_UNIVERSAL_FLAT_50",
			name:        "Universal KES 50 Flat Fee",
			description: "Flat KES 50 per transaction regardless of amount. Applies to any service.",
			chargeType:  servicechargeplan.ChargeTypeFIXED_PER_TRANSACTION,
			chargeValue: 50.0,
			services:    nil, // any service
		},
		{
			code:        "SC_TRULOAD_10PCT",
			name:        "TruLoad 10% Service Charge",
			description: "Platform takes 10% of each weighing/overload fine transaction processed through TruLoad.",
			chargeType:  servicechargeplan.ChargeTypePERCENTAGE,
			chargeValue: 10.0,
			services:    []string{"truload"},
			isDefault:   true,
		},
	}

	for _, p := range plans {
		planID := uuid.NewSHA1(uuid.NameSpaceURL, []byte("bengobox:sc_plan:"+p.code))

		existing, err := tx.ServiceChargePlan.Query().Where(servicechargeplan.Code(p.code)).Only(ctx)
		if err == nil {
			upd := tx.ServiceChargePlan.UpdateOneID(existing.ID).
				SetName(p.name).
				SetDescription(p.description).
				SetChargeType(p.chargeType).
				SetChargeValue(p.chargeValue).
				SetIsDefault(p.isDefault).
				SetNillableMinCharge(p.minCharge).
				SetNillableMaxCharge(p.maxCharge)
			if p.services != nil {
				upd.SetApplicableServices(p.services)
			}
			if _, err := upd.Save(ctx); err != nil {
				return fmt.Errorf("update service charge plan %s: %w", p.code, err)
			}
			continue
		}
		if !ent.IsNotFound(err) {
			return fmt.Errorf("lookup service charge plan %s: %w", p.code, err)
		}

		builder := tx.ServiceChargePlan.Create().
			SetID(planID).
			SetCode(p.code).
			SetName(p.name).
			SetDescription(p.description).
			SetChargeType(p.chargeType).
			SetChargeValue(p.chargeValue).
			SetIsActive(true).
			SetIsDefault(p.isDefault).
			SetNillableMinCharge(p.minCharge).
			SetNillableMaxCharge(p.maxCharge)
		if p.services != nil {
			builder.SetApplicableServices(p.services)
		}
		if _, err := builder.Save(ctx); err != nil {
			return fmt.Errorf("create service charge plan %s: %w", p.code, err)
		}
	}

	log.Println("  ✓ Service charge plans seeded")
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
