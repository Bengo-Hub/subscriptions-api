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
// Three tiers (Starter, Growth, Professional) × 2 billing cycles (monthly, yearly).
// Generic features — not tied to any single business vertical.
// Any tenant can run ordering, POS, logistics, hospitality, or retail.

func seedOrderingPlans(ctx context.Context, tx *ent.Tx) error {
	now := time.Now()
	serviceTag := "ordering"

	type planDef struct {
		id             uuid.UUID
		planCode       string
		name           string
		description    string
		billingCycle   string
		price          float64
		tierOrder      int
		freeTrialDays  int
		tierLimits     map[string]any
		features       []string
	}

	plans := []planDef{
		// ── Monthly Plans ──────────────────────────────────────────────────
		{
			id:            uuid.NewSHA1(uuid.NameSpaceOID, []byte("plan:ORDERING_STARTER")),
			planCode:      "ORDERING_STARTER",
			name:          "Ordering Starter",
			description:   "Core ordering and fulfilment for small businesses. Includes online ordering, payments, basic analytics, and a single outlet.",
			billingCycle:  "MONTHLY",
			price:         2500.0,
			tierOrder:     1,
			freeTrialDays: 30,
			tierLimits: map[string]any{
				"max_admins":                         2,
				"max_riders":                         5,
				"max_orders_per_day":                 300,
				"max_outlets":                        1,
				"api_calls_per_month":                10000,
				"live_tracking_requests_per_day":     500,
				"email_notifications_per_day":        50,
				"sms_notifications_per_day":          20,
				"webhook_calls_per_day":              100,
				"inventory_max_sku":                  500,
				"inventory_max_warehouses":           1,
				"max_staff":                          10,
				"max_tables":                         20,
				// Overage rates
				"overage_rider_price_per_month":      250.0,
				"overage_orders_price_per_100_month": 375.0,
			},
			features: []string{
				"online_ordering",
				"rider_app",
				"admin_dashboard",
				"mpesa_integration",
				"sms_notifications",
				"push_notifications",
				"basic_analytics",
				"custom_domain",
				"loyalty_program",
				"wallet",
				"basic_inventory_access",
				"basic_logistics_access",
				"table_management",
				"shift_reports",
				"offline_sync",
			},
		},
		{
			id:            uuid.NewSHA1(uuid.NameSpaceOID, []byte("plan:ORDERING_GROWTH")),
			planCode:      "ORDERING_GROWTH",
			name:          "Ordering Growth",
			description:   "Growing businesses with multiple outlets. Adds advanced analytics, POS integration, multi-outlet management, and promo codes.",
			billingCycle:  "MONTHLY",
			price:         6000.0,
			tierOrder:     2,
			freeTrialDays: 14,
			tierLimits: map[string]any{
				"max_admins":                         3,
				"max_riders":                         15,
				"max_orders_per_day":                 1000,
				"max_outlets":                        3,
				"api_calls_per_month":                50000,
				"live_tracking_requests_per_day":     5000,
				"email_notifications_per_day":        500,
				"sms_notifications_per_day":          200,
				"webhook_calls_per_day":              1000,
				"inventory_max_sku":                  2000,
				"inventory_max_warehouses":           3,
				"max_staff":                          30,
				"max_tables":                         50,
				"overage_rider_price_per_month":      250.0,
				"overage_orders_price_per_100_month": 375.0,
			},
			features: []string{
				"online_ordering",
				"rider_app",
				"admin_dashboard",
				"mpesa_integration",
				"sms_notifications",
				"push_notifications",
				"basic_analytics",
				"advanced_analytics",
				"custom_domain",
				"loyalty_program",
				"wallet",
				"multi_outlet",
				"promo_codes",
				"group_ordering",
				"paystack_gateway",
				"pos_terminal",
				"table_management",
				"shift_reports",
				"kds",
				"offline_sync",
			},
		},
		{
			id:            uuid.NewSHA1(uuid.NameSpaceOID, []byte("plan:ORDERING_PROFESSIONAL")),
			planCode:      "ORDERING_PROFESSIONAL",
			name:          "Ordering Professional",
			description:   "Multi-branch chains and enterprise operations. Unlimited outlets, hotel module, KDS, route optimisation, and priority support.",
			billingCycle:  "MONTHLY",
			price:         12500.0,
			tierOrder:     3,
			freeTrialDays: 14,
			tierLimits: map[string]any{
				"max_admins":                         -1,
				"max_riders":                         30,
				"max_orders_per_day":                 2500,
				"max_outlets":                        -1,
				"api_calls_per_month":                200000,
				"live_tracking_requests_per_day":     -1,
				"email_notifications_per_day":        5000,
				"sms_notifications_per_day":          2000,
				"webhook_calls_per_day":              10000,
				"inventory_max_sku":                  -1,
				"inventory_max_warehouses":           -1,
				"max_staff":                          -1,
				"max_tables":                         -1,
				"overage_rider_price_per_month":      250.0,
				"overage_orders_price_per_100_month": 375.0,
			},
			features: []string{
				"online_ordering",
				"rider_app",
				"admin_dashboard",
				"mpesa_integration",
				"sms_notifications",
				"push_notifications",
				"basic_analytics",
				"advanced_analytics",
				"custom_domain",
				"loyalty_program",
				"wallet",
				"multi_outlet",
				"promo_codes",
				"group_ordering",
				"paystack_gateway",
				"pos_terminal",
				"pos_integration",
				"table_management",
				"shift_reports",
				"kds",
				"hotel_module",
				"offline_sync",
				"route_optimization",
				"api_webhooks",
				"white_labeling",
				"priority_support",
				"premium_support",
			},
		},

		// ── Annual Plans (≈ 10 months pricing) ────────────────────────────
		{
			id:            uuid.NewSHA1(uuid.NameSpaceOID, []byte("plan:ORDERING_STARTER_YEARLY")),
			planCode:      "ORDERING_STARTER_YEARLY",
			name:          "Ordering Starter — Annual",
			description:   "Core ordering for small businesses. Save with annual billing.",
			billingCycle:  "ANNUAL",
			price:         27500.0,
			tierOrder:     1,
			freeTrialDays: 30,
			tierLimits: map[string]any{
				"max_admins":                         2,
				"max_riders":                         5,
				"max_orders_per_day":                 300,
				"max_outlets":                        1,
				"api_calls_per_month":                10000,
				"live_tracking_requests_per_day":     500,
				"email_notifications_per_day":        50,
				"sms_notifications_per_day":          20,
				"webhook_calls_per_day":              100,
				"inventory_max_sku":                  500,
				"inventory_max_warehouses":           1,
				"max_staff":                          10,
				"max_tables":                         20,
				"overage_rider_price_per_month":      250.0,
				"overage_orders_price_per_100_month": 375.0,
			},
			features: []string{
				"online_ordering",
				"rider_app",
				"admin_dashboard",
				"mpesa_integration",
				"sms_notifications",
				"push_notifications",
				"basic_analytics",
				"custom_domain",
				"loyalty_program",
				"wallet",
				"basic_inventory_access",
				"basic_logistics_access",
				"table_management",
				"shift_reports",
				"offline_sync",
			},
		},
		{
			id:            uuid.NewSHA1(uuid.NameSpaceOID, []byte("plan:ORDERING_GROWTH_YEARLY")),
			planCode:      "ORDERING_GROWTH_YEARLY",
			name:          "Ordering Growth — Annual",
			description:   "Growing multi-outlet businesses. Save with annual billing.",
			billingCycle:  "ANNUAL",
			price:         66000.0,
			tierOrder:     2,
			freeTrialDays: 14,
			tierLimits: map[string]any{
				"max_admins":                         3,
				"max_riders":                         15,
				"max_orders_per_day":                 1000,
				"max_outlets":                        3,
				"api_calls_per_month":                50000,
				"live_tracking_requests_per_day":     5000,
				"email_notifications_per_day":        500,
				"sms_notifications_per_day":          200,
				"webhook_calls_per_day":              1000,
				"inventory_max_sku":                  2000,
				"inventory_max_warehouses":           3,
				"max_staff":                          30,
				"max_tables":                         50,
				"overage_rider_price_per_month":      250.0,
				"overage_orders_price_per_100_month": 375.0,
			},
			features: []string{
				"online_ordering",
				"rider_app",
				"admin_dashboard",
				"mpesa_integration",
				"sms_notifications",
				"push_notifications",
				"basic_analytics",
				"advanced_analytics",
				"custom_domain",
				"loyalty_program",
				"wallet",
				"multi_outlet",
				"promo_codes",
				"group_ordering",
				"paystack_gateway",
				"pos_terminal",
				"table_management",
				"shift_reports",
				"kds",
				"offline_sync",
			},
		},
		{
			id:            uuid.NewSHA1(uuid.NameSpaceOID, []byte("plan:ORDERING_PROFESSIONAL_YEARLY")),
			planCode:      "ORDERING_PROFESSIONAL_YEARLY",
			name:          "Ordering Professional — Annual",
			description:   "Enterprise multi-branch operations. Save with annual billing.",
			billingCycle:  "ANNUAL",
			price:         137500.0,
			tierOrder:     3,
			freeTrialDays: 14,
			tierLimits: map[string]any{
				"max_admins":                         -1,
				"max_riders":                         30,
				"max_orders_per_day":                 2500,
				"max_outlets":                        -1,
				"api_calls_per_month":                200000,
				"live_tracking_requests_per_day":     -1,
				"email_notifications_per_day":        5000,
				"sms_notifications_per_day":          2000,
				"webhook_calls_per_day":              10000,
				"inventory_max_sku":                  -1,
				"inventory_max_warehouses":           -1,
				"max_staff":                          -1,
				"max_tables":                         -1,
				"overage_rider_price_per_month":      250.0,
				"overage_orders_price_per_100_month": 375.0,
			},
			features: []string{
				"online_ordering",
				"rider_app",
				"admin_dashboard",
				"mpesa_integration",
				"sms_notifications",
				"push_notifications",
				"basic_analytics",
				"advanced_analytics",
				"custom_domain",
				"loyalty_program",
				"wallet",
				"multi_outlet",
				"promo_codes",
				"group_ordering",
				"paystack_gateway",
				"pos_terminal",
				"pos_integration",
				"table_management",
				"shift_reports",
				"kds",
				"hotel_module",
				"offline_sync",
				"route_optimization",
				"api_webhooks",
				"white_labeling",
				"priority_support",
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
				SetPlanType(subscriptionplan.PlanTypeTIERED).
				SetBasePrice(p.price).
				SetCurrency("KES").
				SetIsActive(true).
				SetIsPublic(true).
				SetTierOrder(p.tierOrder).
				SetFreeTrialDays(p.freeTrialDays).
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
				SetPlanType(subscriptionplan.PlanTypeTIERED).
				SetBasePrice(p.price).
				SetCurrency("KES").
				SetIsActive(true).
				SetIsPublic(true).
				SetTierOrder(p.tierOrder).
				SetFreeTrialDays(p.freeTrialDays).
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

		log.Printf("  plan: %s (%s, KES %.0f, %d trial days)", p.name, p.billingCycle, p.price, p.freeTrialDays)
	}

	return nil
}
