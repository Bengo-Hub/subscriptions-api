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
	"github.com/bengobox/subscription-service/internal/ent/bundle"
	"github.com/bengobox/subscription-service/internal/ent/planfeature"
	"github.com/bengobox/subscription-service/internal/ent/product"
	"github.com/bengobox/subscription-service/internal/ent/productsubscription"
	"github.com/bengobox/subscription-service/internal/ent/schema"
	"github.com/bengobox/subscription-service/internal/ent/tenantsubscription"
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

	if err := runSeed(ctx, client); err != nil {
		log.Fatalf("seed data: %v", err)
	}

	log.Println("database seed completed successfully")
}

func runSeed(ctx context.Context, client *ent.Client) error {
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

	// 4. Seed demo tenant subscription (Urban Loft on GROWTH plan, 14-day trial)
	if err := seedDemoSubscription(ctx, tx); err != nil {
		return fmt.Errorf("seed demo subscription: %w", err)
	}

	return nil
}

// ── Products ─────────────────────────────────────────────────────────────────

func seedProducts(ctx context.Context, tx *ent.Tx) error {
	type productDef struct {
		id           uuid.UUID
		code         string
		name         string
		description  string
		category     product.Category
		isPlatform   bool
		monthlyPrice float64
		dependencies []string
		sortOrder    int
	}

	products := []productDef{
		// Platform products — always included, no extra cost
		{
			id:           uuid.MustParse("10000000-0000-0000-0000-000000000001"),
			code:         "auth",
			name:         "Authentication & SSO",
			description:  "User authentication, multi-factor auth, OAuth 2.0, OpenID Connect, session management, and single sign-on across all BengoBox services.",
			category:     product.CategoryPlatform,
			isPlatform:   true,
			monthlyPrice: 0,
			sortOrder:    1,
		},
		{
			id:           uuid.MustParse("10000000-0000-0000-0000-000000000002"),
			code:         "notifications",
			name:         "Notifications",
			description:  "Multi-channel notification delivery: email (SMTP/SendGrid), SMS (Africa's Talking), and push notifications with template management.",
			category:     product.CategoryPlatform,
			isPlatform:   true,
			monthlyPrice: 0,
			sortOrder:    2,
		},
		{
			id:           uuid.MustParse("10000000-0000-0000-0000-000000000003"),
			code:         "subscription",
			name:         "Subscription Management",
			description:  "Plan management, billing lifecycle, feature entitlements, usage tracking, and tier enforcement.",
			category:     product.CategoryPlatform,
			isPlatform:   true,
			monthlyPrice: 0,
			sortOrder:    3,
		},

		// Core products — subscribable individually or via bundles
		{
			id:           uuid.MustParse("10000000-0000-0000-0000-000000000010"),
			code:         "ordering",
			name:         "Online Ordering",
			description:  "Customer-facing ordering portal (web + PWA), menu management, cart & checkout, order lifecycle tracking, and M-Pesa payment integration.",
			category:     product.CategoryProduct,
			monthlyPrice: 1500,
			sortOrder:    10,
		},
		{
			id:           uuid.MustParse("10000000-0000-0000-0000-000000000011"),
			code:         "logistics",
			name:         "Delivery & Logistics",
			description:  "Rider management, delivery assignment, real-time tracking, route optimization, and rider mobile app.",
			category:     product.CategoryProduct,
			monthlyPrice: 1500,
			dependencies: []string{"ordering"},
			sortOrder:    11,
		},
		{
			id:           uuid.MustParse("10000000-0000-0000-0000-000000000012"),
			code:         "treasury",
			name:         "Payments & Invoicing",
			description:  "Payment processing, M-Pesa integration, invoicing, receipts, financial reporting, and revenue tracking.",
			category:     product.CategoryProduct,
			monthlyPrice: 1000,
			sortOrder:    12,
		},

		// Add-on products
		{
			id:           uuid.MustParse("10000000-0000-0000-0000-000000000020"),
			code:         "pos",
			name:         "Point of Sale",
			description:  "In-store point-of-sale terminal integration, walk-in order management, and cash register support.",
			category:     product.CategoryAddOn,
			monthlyPrice: 2000,
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
			sortOrder:    30,
		},
		{
			id:           uuid.MustParse("10000000-0000-0000-0000-000000000031"),
			code:         "paystack_gateway",
			name:         "Paystack Payment Gateway",
			description:  "Paystack card and mobile money payments. Enables Visa/Mastercard, bank transfers, and USSD payments alongside M-Pesa.",
			category:     product.CategoryAddOn,
			monthlyPrice: 300,
			sortOrder:    31,
		},
		{
			id:           uuid.MustParse("10000000-0000-0000-0000-000000000032"),
			code:         "sms_credits",
			name:         "SMS Credit Pack",
			description:  "Bulk SMS credits for order notifications, OTP verification, and marketing. Base plan includes 100 SMS/month; this adds 500 extra/month.",
			category:     product.CategoryAddOn,
			monthlyPrice: 200,
			sortOrder:    32,
		},
		{
			id:           uuid.MustParse("10000000-0000-0000-0000-000000000033"),
			code:         "premium_support",
			name:         "Premium Support",
			description:  "Dedicated support channel with 4-hour SLA, priority issue resolution, and custom integrations assistance.",
			category:     product.CategoryAddOn,
			monthlyPrice: 1000,
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
				SetMonthlyPrice(p.monthlyPrice).
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
				SetMonthlyPrice(p.monthlyPrice).
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
		price        float64
		tierOrder    int
		tierLimits   map[string]any
		features     []string
	}

	plans := []planDef{
		// ── Monthly Plans ────────────────────────────────────────────────
		{
			id:          uuid.MustParse("00000000-0000-0000-0000-000000000001"),
			planCode:    "STARTER",
			name:        "Starter (Lite)",
			description: "Perfect for small cafes and pilot operations. Core ordering features with essential admin tools.",
			billingCycle: "MONTHLY",
			price:       2500.0,
			tierOrder:   1,
			tierLimits: map[string]any{
				"max_admins":          2,
				"max_riders":          5,
				"max_orders_per_day":  300,
				"max_outlets":         1,
				"api_calls_per_month": 10000,
				// Overage rates
				"overage_rider_price_per_month":      250.0,
				"overage_orders_price_per_100_month":  375.0,
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
			id:          uuid.MustParse("00000000-0000-0000-0000-000000000002"),
			planCode:    "GROWTH",
			name:        "Growth (Standard)",
			description: "Ideal for growing cafes with multiple outlets. Advanced features including loyalty program and multi-outlet support.",
			billingCycle: "MONTHLY",
			price:       6000.0,
			tierOrder:   2,
			tierLimits: map[string]any{
				"max_admins":          3,
				"max_riders":          15,
				"max_orders_per_day":  1000,
				"max_outlets":         3,
				"api_calls_per_month": 50000,
				"overage_rider_price_per_month":      250.0,
				"overage_orders_price_per_100_month":  375.0,
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
			id:          uuid.MustParse("00000000-0000-0000-0000-000000000003"),
			planCode:    "PROFESSIONAL",
			name:        "Professional (Scale)",
			description: "For multi-branch cafes and chains. Complete feature set with POS integration, route optimization, and priority support.",
			billingCycle: "MONTHLY",
			price:       12500.0,
			tierOrder:   3,
			tierLimits: map[string]any{
				"max_admins":          -1, // Unlimited
				"max_riders":          30,
				"max_orders_per_day":  2500,
				"max_outlets":         -1, // Unlimited
				"api_calls_per_month": 200000,
				"overage_rider_price_per_month":      250.0,
				"overage_orders_price_per_100_month":  375.0,
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
			id:          uuid.MustParse("00000000-0000-0000-0000-000000000011"),
			planCode:    "STARTER_YEARLY",
			name:        "Starter (Lite) — Annual",
			description: "Perfect for small cafes and pilot operations. Save with annual billing.",
			billingCycle: "ANNUAL",
			price:       27500.0, // 11 months for price of 10 (KES 2,500 × 11)
			tierOrder:   1,
			tierLimits: map[string]any{
				"max_admins":          2,
				"max_riders":          5,
				"max_orders_per_day":  300,
				"max_outlets":         1,
				"api_calls_per_month": 10000,
				"overage_rider_price_per_month":      250.0,
				"overage_orders_price_per_100_month":  375.0,
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
			id:          uuid.MustParse("00000000-0000-0000-0000-000000000012"),
			planCode:    "GROWTH_YEARLY",
			name:        "Growth (Standard) — Annual",
			description: "Ideal for growing cafes with multiple outlets. Save with annual billing.",
			billingCycle: "ANNUAL",
			price:       66000.0, // KES 6,000 × 11
			tierOrder:   2,
			tierLimits: map[string]any{
				"max_admins":          3,
				"max_riders":          15,
				"max_orders_per_day":  1000,
				"max_outlets":         3,
				"api_calls_per_month": 50000,
				"overage_rider_price_per_month":      250.0,
				"overage_orders_price_per_100_month":  375.0,
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
			id:          uuid.MustParse("00000000-0000-0000-0000-000000000013"),
			planCode:    "PROFESSIONAL_YEARLY",
			name:        "Professional (Scale) — Annual",
			description: "For multi-branch cafes and chains. Save with annual billing.",
			billingCycle: "ANNUAL",
			price:       137500.0, // KES 12,500 × 11
			tierOrder:   3,
			tierLimits: map[string]any{
				"max_admins":          -1,
				"max_riders":          30,
				"max_orders_per_day":  2500,
				"max_outlets":         -1,
				"api_calls_per_month": 200000,
				"overage_rider_price_per_month":      250.0,
				"overage_orders_price_per_100_month":  375.0,
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

func seedPlanFeatures(ctx context.Context, tx *ent.Tx, planID uuid.UUID, featureCodes []string) error {
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

	for _, code := range featureCodes {
		_, err := tx.PlanFeature.Create().
			SetPlanID(planID).
			SetFeatureCode(code).
			SetIsIncluded(true).
			Save(ctx)
		if err != nil {
			return fmt.Errorf("create feature %s: %w", code, err)
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

// ── Demo Subscription ────────────────────────────────────────────────────────
// Creates a demo tenant subscription for Urban Loft Cafe on the GROWTH plan
// with a 14-day trial period. Activates the delivery bundle products.

func seedDemoSubscription(ctx context.Context, tx *ent.Tx) error {
	// Fixed UUIDs matching auth-service seed
	demoTenantID := uuid.MustParse("11111111-2222-3333-4444-555555555555")
	growthPlanID := uuid.MustParse("00000000-0000-0000-0000-000000000002") // GROWTH monthly
	demoSubID := uuid.MustParse("30000000-0000-0000-0000-000000000001")

	now := time.Now()
	trialEnd := now.Add(14 * 24 * time.Hour)

	// Check if subscription already exists for this tenant
	existing, err := tx.TenantSubscription.Query().
		Where(tenantsubscription.TenantIDEQ(demoTenantID)).
		First(ctx)
	if err != nil && !ent.IsNotFound(err) {
		return fmt.Errorf("lookup demo subscription: %w", err)
	}

	if existing != nil {
		_, err = tx.TenantSubscription.UpdateOne(existing).
			SetPlanID(growthPlanID).
			SetStatus(tenantsubscription.StatusTRIAL).
			SetTrialEndsAt(trialEnd).
			SetCurrentPeriodStart(now).
			SetCurrentPeriodEnd(trialEnd).
			SetBundleCode("delivery").
			SetMetadata(map[string]any{
				"seeded":      true,
				"tenant_name": "Urban Loft Cafe",
			}).
			Save(ctx)
		if err != nil {
			return fmt.Errorf("update demo subscription: %w", err)
		}
		demoSubID = existing.ID
		log.Printf("  subscription: Urban Loft Cafe → GROWTH (trial, updated)")
	} else {
		_, err = tx.TenantSubscription.Create().
			SetID(demoSubID).
			SetTenantID(demoTenantID).
			SetPlanID(growthPlanID).
			SetStatus(tenantsubscription.StatusTRIAL).
			SetTrialEndsAt(trialEnd).
			SetCurrentPeriodStart(now).
			SetCurrentPeriodEnd(trialEnd).
			SetBundleCode("delivery").
			SetMetadata(map[string]any{
				"seeded":      true,
				"tenant_name": "Urban Loft Cafe",
			}).
			Save(ctx)
		if err != nil {
			return fmt.Errorf("create demo subscription: %w", err)
		}
		log.Printf("  subscription: Urban Loft Cafe → GROWTH (trial, 14 days)")
	}

	// Activate delivery bundle products for the demo subscription
	type productActivation struct {
		productID   uuid.UUID
		productCode string
	}

	deliveryProducts := []productActivation{
		{uuid.MustParse("10000000-0000-0000-0000-000000000010"), "ordering"},
		{uuid.MustParse("10000000-0000-0000-0000-000000000011"), "logistics"},
		{uuid.MustParse("10000000-0000-0000-0000-000000000012"), "treasury"},
		{uuid.MustParse("10000000-0000-0000-0000-000000000021"), "storefront"},
	}

	for _, p := range deliveryProducts {
		existingPS, err := tx.ProductSubscription.Query().
			Where(
				productsubscription.TenantSubscriptionIDEQ(demoSubID),
				productsubscription.ProductCodeEQ(p.productCode),
			).
			First(ctx)
		if err != nil && !ent.IsNotFound(err) {
			return fmt.Errorf("lookup product subscription %s: %w", p.productCode, err)
		}

		if existingPS != nil {
			_, err = tx.ProductSubscription.UpdateOne(existingPS).
				SetProductID(p.productID).
				SetStatus(productsubscription.StatusActive).
				SetActivatedAt(now).
				Save(ctx)
			if err != nil {
				return fmt.Errorf("update product subscription %s: %w", p.productCode, err)
			}
		} else {
			_, err = tx.ProductSubscription.Create().
				SetTenantSubscriptionID(demoSubID).
				SetProductCode(p.productCode).
				SetProductID(p.productID).
				SetStatus(productsubscription.StatusActive).
				SetActivatedAt(now).
				Save(ctx)
			if err != nil {
				return fmt.Errorf("create product subscription %s: %w", p.productCode, err)
			}
		}

		log.Printf("    product activated: %s", p.productCode)
	}

	return nil
}
