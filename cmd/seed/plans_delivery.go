package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/subscriptionplan"
)

// ── Ordering Plans ────────────────────────────────────────────────────────────
// Derived from Urban Cafe Food Delivery System Inception Report v1.0 (Nov 2025).
// Three tiers: Starter (Lite), Growth (Standard), Professional (Scale).
// Each tier has monthly and yearly billing variants (yearly = 10 months pricing).
// The ORDERING_STARTER tier bundles basic access to inventory, logistics, and cafe-website.

func seedSubscriptionPlans(ctx context.Context, tx *ent.Tx) error {
	now := time.Now()
	serviceTag := "ordering"

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
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("plan:ORDERING_STARTER")),
			planCode:     "ORDERING_STARTER",
			name:         "Ordering Starter (Lite)",
			description:  "Perfect for small cafes and pilot operations. Core ordering features with essential admin tools. Includes basic inventory, logistics, and cafe-website access.",
			billingCycle: "MONTHLY",
			planType:     subscriptionplan.PlanTypeTIERED,
			price:        2500.0,
			tierOrder:    1,
			tierLimits: map[string]any{
				"max_admins":                     2,
				"max_riders":                     5,
				"max_orders_per_day":             300,
				"max_outlets":                    1,
				"api_calls_per_month":            10000,
				"live_tracking_requests_per_day": 500,
				"live_tracking_duration_minutes": 30,
				"routing_requests_per_day":       100,
				"map_loads_per_day":              200,
				"email_notifications_per_day":    50,
				"webhook_calls_per_day":          100,
				// Overage rates
				"overage_rider_price_per_month":      250.0,
				"overage_orders_price_per_100_month": 375.0,
				// Cross-service basic access (bundled in starter)
				"inventory_max_sku":            500,
				"inventory_max_warehouses":     1,
				"logistics_max_active_routes":  5,
				"logistics_max_zones":          1,
				"cafe_website_enabled":         true,
				"cafe_website_max_menu_items":  50,
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
				"wallet",
				"basic_inventory_access",
				"basic_logistics_access",
				"cafe_website_basic",
			},
		},
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("plan:ORDERING_GROWTH")),
			planCode:     "ORDERING_GROWTH",
			name:         "Ordering Growth (Standard)",
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
				"pos_terminal",
			},
		},
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("plan:ORDERING_PROFESSIONAL")),
			planCode:     "ORDERING_PROFESSIONAL",
			name:         "Ordering Professional (Scale)",
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
				"pos_terminal",
				"kds",
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
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("plan:ORDERING_STARTER_YEARLY")),
			planCode:     "ORDERING_STARTER_YEARLY",
			name:         "Ordering Starter (Lite) — Annual",
			description:  "Perfect for small cafes and pilot operations. Includes basic inventory, logistics, and cafe-website access. Save with annual billing.",
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
				"inventory_max_sku":                  500,
				"inventory_max_warehouses":           1,
				"logistics_max_active_routes":        5,
				"logistics_max_zones":                1,
				"cafe_website_enabled":               true,
				"cafe_website_max_menu_items":        50,
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
				"wallet",
				"basic_inventory_access",
				"basic_logistics_access",
				"cafe_website_basic",
			},
		},
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("plan:ORDERING_GROWTH_YEARLY")),
			planCode:     "ORDERING_GROWTH_YEARLY",
			name:         "Ordering Growth (Standard) — Annual",
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
				"pos_terminal",
			},
		},
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("plan:ORDERING_PROFESSIONAL_YEARLY")),
			planCode:     "ORDERING_PROFESSIONAL_YEARLY",
			name:         "Ordering Professional (Scale) — Annual",
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
				"pos_terminal",
				"kds",
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
				SetServiceTag(serviceTag).
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
				SetServiceTag(serviceTag).
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
