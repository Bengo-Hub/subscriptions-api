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

// seedBundlePlans seeds dedicated plan codes for the pos-suite and complete bundles.
// These replace the erroneous ORDERING_* references in BundleTier.PlanCode so that
// POS-only tenants receive max_devices/max_cashiers limits rather than ordering limits.
func seedBundlePlans(ctx context.Context, tx *ent.Tx) error {
	now := time.Now()

	type planDef struct {
		id           uuid.UUID
		planCode     string
		name         string
		description  string
		billingCycle string
		price        float64
		tierOrder    int
		serviceTag   string
		tierLimits   map[string]any
		features     []string
	}

	plans := []planDef{
		// ── POS Suite — Monthly ──────────────────────────────────────────────
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("plan:POS_SUITE_STARTER")),
			planCode:     "POS_SUITE_STARTER",
			name:         "POS Suite Starter",
			description:  "In-store POS with payment processing and basic table management for a single outlet.",
			billingCycle: "MONTHLY",
			price:        2000.0,
			tierOrder:    1,
			serviceTag:   "pos",
			tierLimits: map[string]any{
				"max_devices":               1,
				"max_cashiers":              2,
				"max_tables":                20,
				"max_transactions_per_month": 500,
				"max_admins":                1,
				"max_staff":                 5,
				"max_outlets":               1,
				"email_notifications_per_day": 20,
				"sms_notifications_per_day":   10,
				"overage_transaction_price":   0.0,
			},
			features: []string{
				"pos_terminal",
				"mpesa_pos",
				"table_management",
				"shift_reports",
				"offline_sync",
				"basic_analytics",
				"basic_treasury_access",
				"loyalty_program",
				"wallet",
			},
		},
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("plan:POS_SUITE_GROWTH")),
			planCode:     "POS_SUITE_GROWTH",
			name:         "POS Suite Growth",
			description:  "Multi-cashier POS with KDS, advanced analytics, and multi-outlet support.",
			billingCycle: "MONTHLY",
			price:        4500.0,
			tierOrder:    2,
			serviceTag:   "pos",
			tierLimits: map[string]any{
				"max_devices":               5,
				"max_cashiers":              10,
				"max_tables":                50,
				"max_transactions_per_month": 5000,
				"max_admins":                3,
				"max_staff":                 20,
				"max_outlets":               3,
				"email_notifications_per_day": 100,
				"sms_notifications_per_day":   50,
				"overage_transaction_price":   0.0,
			},
			features: []string{
				"pos_terminal",
				"mpesa_pos",
				"table_management",
				"shift_reports",
				"offline_sync",
				"basic_analytics",
				"advanced_analytics",
				"basic_treasury_access",
				"multi_cashier",
				"kds",
				"multi_outlet",
				"loyalty_program",
				"wallet",
				"pos_integration",
			},
		},
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("plan:POS_SUITE_PROFESSIONAL")),
			planCode:     "POS_SUITE_PROFESSIONAL",
			name:         "POS Suite Professional",
			description:  "Enterprise POS with hotel module, API access, and unlimited devices for chains.",
			billingCycle: "MONTHLY",
			price:        8000.0,
			tierOrder:    3,
			serviceTag:   "pos",
			tierLimits: map[string]any{
				"max_devices":               -1,
				"max_cashiers":              -1,
				"max_tables":                -1,
				"max_transactions_per_month": -1,
				"max_admins":                -1,
				"max_staff":                 -1,
				"max_outlets":               -1,
				"email_notifications_per_day": 500,
				"sms_notifications_per_day":   200,
				"overage_transaction_price":   0.0,
			},
			features: []string{
				"pos_terminal",
				"mpesa_pos",
				"table_management",
				"shift_reports",
				"offline_sync",
				"basic_analytics",
				"advanced_analytics",
				"basic_treasury_access",
				"multi_cashier",
				"kds",
				"multi_outlet",
				"hotel_module",
				"loyalty_program",
				"wallet",
				"pos_integration",
				"api_webhooks",
				"white_labeling",
				"priority_support",
				"premium_support",
			},
		},

		// ── POS Suite — Annual ───────────────────────────────────────────────
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("plan:POS_SUITE_STARTER_YEARLY")),
			planCode:     "POS_SUITE_STARTER_YEARLY",
			name:         "POS Suite Starter — Annual",
			description:  "In-store POS for a single outlet. Save with annual billing.",
			billingCycle: "ANNUAL",
			price:        22000.0,
			tierOrder:    1,
			serviceTag:   "pos",
			tierLimits: map[string]any{
				"max_devices":               1,
				"max_cashiers":              2,
				"max_tables":                20,
				"max_transactions_per_month": 500,
				"max_admins":                1,
				"max_staff":                 5,
				"max_outlets":               1,
				"email_notifications_per_day": 20,
				"sms_notifications_per_day":   10,
				"overage_transaction_price":   0.0,
			},
			features: []string{
				"pos_terminal",
				"mpesa_pos",
				"table_management",
				"shift_reports",
				"offline_sync",
				"basic_analytics",
				"basic_treasury_access",
				"loyalty_program",
				"wallet",
			},
		},
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("plan:POS_SUITE_GROWTH_YEARLY")),
			planCode:     "POS_SUITE_GROWTH_YEARLY",
			name:         "POS Suite Growth — Annual",
			description:  "Multi-cashier POS for growing outlets. Save with annual billing.",
			billingCycle: "ANNUAL",
			price:        49500.0,
			tierOrder:    2,
			serviceTag:   "pos",
			tierLimits: map[string]any{
				"max_devices":               5,
				"max_cashiers":              10,
				"max_tables":                50,
				"max_transactions_per_month": 5000,
				"max_admins":                3,
				"max_staff":                 20,
				"max_outlets":               3,
				"email_notifications_per_day": 100,
				"sms_notifications_per_day":   50,
				"overage_transaction_price":   0.0,
			},
			features: []string{
				"pos_terminal",
				"mpesa_pos",
				"table_management",
				"shift_reports",
				"offline_sync",
				"basic_analytics",
				"advanced_analytics",
				"basic_treasury_access",
				"multi_cashier",
				"kds",
				"multi_outlet",
				"loyalty_program",
				"wallet",
				"pos_integration",
			},
		},
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("plan:POS_SUITE_PROFESSIONAL_YEARLY")),
			planCode:     "POS_SUITE_PROFESSIONAL_YEARLY",
			name:         "POS Suite Professional — Annual",
			description:  "Enterprise POS with hotel module and API access. Save with annual billing.",
			billingCycle: "ANNUAL",
			price:        88000.0,
			tierOrder:    3,
			serviceTag:   "pos",
			tierLimits: map[string]any{
				"max_devices":               -1,
				"max_cashiers":              -1,
				"max_tables":                -1,
				"max_transactions_per_month": -1,
				"max_admins":                -1,
				"max_staff":                 -1,
				"max_outlets":               -1,
				"email_notifications_per_day": 500,
				"sms_notifications_per_day":   200,
				"overage_transaction_price":   0.0,
			},
			features: []string{
				"pos_terminal",
				"mpesa_pos",
				"table_management",
				"shift_reports",
				"offline_sync",
				"basic_analytics",
				"advanced_analytics",
				"basic_treasury_access",
				"multi_cashier",
				"kds",
				"multi_outlet",
				"hotel_module",
				"loyalty_program",
				"wallet",
				"pos_integration",
				"api_webhooks",
				"white_labeling",
				"priority_support",
				"premium_support",
			},
		},

		// ── Complete Bundle — Monthly ────────────────────────────────────────
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("plan:COMPLETE_STARTER")),
			planCode:     "COMPLETE_STARTER",
			name:         "Complete Starter",
			description:  "Full-service: online ordering, in-store POS, logistics, payments, and storefront for a single outlet.",
			billingCycle: "MONTHLY",
			price:        4000.0,
			tierOrder:    1,
			serviceTag:   "ordering",
			tierLimits: map[string]any{
				// Ordering
				"max_orders_per_day":          300,
				"max_admins":                  2,
				"max_staff":                   10,
				"max_outlets":                 1,
				"api_calls_per_month":         10000,
				"webhook_calls_per_day":       100,
				// Logistics
				"max_riders":                             5,
				"live_tracking_requests_per_day":         500,
				"overage_rider_price_per_month":          250.0,
				"overage_orders_price_per_100_month":     375.0,
				// POS
				"max_devices":               1,
				"max_cashiers":              2,
				"max_tables":                20,
				"max_transactions_per_month": 500,
				// Inventory
				"inventory_max_sku":        500,
				"inventory_max_warehouses": 1,
				// Notifications
				"email_notifications_per_day": 50,
				"sms_notifications_per_day":   20,
			},
			features: []string{
				"online_ordering",
				"rider_app",
				"admin_dashboard",
				"mpesa_integration",
				"mpesa_pos",
				"sms_notifications",
				"push_notifications",
				"basic_analytics",
				"custom_domain",
				"loyalty_program",
				"wallet",
				"basic_inventory_access",
				"basic_logistics_access",
				"basic_treasury_access",
				"pos_terminal",
				"table_management",
				"shift_reports",
				"offline_sync",
			},
		},
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("plan:COMPLETE_GROWTH")),
			planCode:     "COMPLETE_GROWTH",
			name:         "Complete Growth",
			description:  "Full-service with multi-outlet management, advanced analytics, and KDS for growing chains.",
			billingCycle: "MONTHLY",
			price:        9500.0,
			tierOrder:    2,
			serviceTag:   "ordering",
			tierLimits: map[string]any{
				"max_orders_per_day":          1000,
				"max_admins":                  3,
				"max_staff":                   30,
				"max_outlets":                 3,
				"api_calls_per_month":         50000,
				"webhook_calls_per_day":       1000,
				"max_riders":                             15,
				"live_tracking_requests_per_day":         5000,
				"overage_rider_price_per_month":          250.0,
				"overage_orders_price_per_100_month":     375.0,
				"max_devices":               5,
				"max_cashiers":              10,
				"max_tables":                50,
				"max_transactions_per_month": 5000,
				"inventory_max_sku":        2000,
				"inventory_max_warehouses": 3,
				"email_notifications_per_day": 500,
				"sms_notifications_per_day":   200,
			},
			features: []string{
				"online_ordering",
				"rider_app",
				"admin_dashboard",
				"mpesa_integration",
				"mpesa_pos",
				"sms_notifications",
				"push_notifications",
				"basic_analytics",
				"advanced_analytics",
				"custom_domain",
				"loyalty_program",
				"wallet",
				"basic_inventory_access",
				"basic_logistics_access",
				"basic_treasury_access",
				"multi_outlet",
				"promo_codes",
				"group_ordering",
				"paystack_gateway",
				"pos_terminal",
				"pos_integration",
				"multi_cashier",
				"table_management",
				"shift_reports",
				"kds",
				"offline_sync",
			},
		},
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("plan:COMPLETE_PROFESSIONAL")),
			planCode:     "COMPLETE_PROFESSIONAL",
			name:         "Complete Professional",
			description:  "Enterprise full-service with hotel module, route optimisation, API access, and priority support.",
			billingCycle: "MONTHLY",
			price:        18000.0,
			tierOrder:    3,
			serviceTag:   "ordering",
			tierLimits: map[string]any{
				"max_orders_per_day":          2500,
				"max_admins":                  -1,
				"max_staff":                   -1,
				"max_outlets":                 -1,
				"api_calls_per_month":         200000,
				"webhook_calls_per_day":       10000,
				"max_riders":                             30,
				"live_tracking_requests_per_day":         -1,
				"overage_rider_price_per_month":          250.0,
				"overage_orders_price_per_100_month":     375.0,
				"max_devices":               -1,
				"max_cashiers":              -1,
				"max_tables":                -1,
				"max_transactions_per_month": -1,
				"inventory_max_sku":        -1,
				"inventory_max_warehouses": -1,
				"email_notifications_per_day": 5000,
				"sms_notifications_per_day":   2000,
			},
			features: []string{
				"online_ordering",
				"rider_app",
				"admin_dashboard",
				"mpesa_integration",
				"mpesa_pos",
				"sms_notifications",
				"push_notifications",
				"basic_analytics",
				"advanced_analytics",
				"custom_domain",
				"loyalty_program",
				"wallet",
				"basic_inventory_access",
				"basic_logistics_access",
				"basic_treasury_access",
				"multi_outlet",
				"promo_codes",
				"group_ordering",
				"paystack_gateway",
				"pos_terminal",
				"pos_integration",
				"multi_cashier",
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

		// ── Complete Bundle — Annual ─────────────────────────────────────────
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("plan:COMPLETE_STARTER_YEARLY")),
			planCode:     "COMPLETE_STARTER_YEARLY",
			name:         "Complete Starter — Annual",
			description:  "Full-service for a single outlet. Save with annual billing.",
			billingCycle: "ANNUAL",
			price:        44000.0,
			tierOrder:    1,
			serviceTag:   "ordering",
			tierLimits: map[string]any{
				"max_orders_per_day":          300,
				"max_admins":                  2,
				"max_staff":                   10,
				"max_outlets":                 1,
				"api_calls_per_month":         10000,
				"webhook_calls_per_day":       100,
				"max_riders":                             5,
				"live_tracking_requests_per_day":         500,
				"overage_rider_price_per_month":          250.0,
				"overage_orders_price_per_100_month":     375.0,
				"max_devices":               1,
				"max_cashiers":              2,
				"max_tables":                20,
				"max_transactions_per_month": 500,
				"inventory_max_sku":        500,
				"inventory_max_warehouses": 1,
				"email_notifications_per_day": 50,
				"sms_notifications_per_day":   20,
			},
			features: []string{
				"online_ordering",
				"rider_app",
				"admin_dashboard",
				"mpesa_integration",
				"mpesa_pos",
				"sms_notifications",
				"push_notifications",
				"basic_analytics",
				"custom_domain",
				"loyalty_program",
				"wallet",
				"basic_inventory_access",
				"basic_logistics_access",
				"basic_treasury_access",
				"pos_terminal",
				"table_management",
				"shift_reports",
				"offline_sync",
			},
		},
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("plan:COMPLETE_GROWTH_YEARLY")),
			planCode:     "COMPLETE_GROWTH_YEARLY",
			name:         "Complete Growth — Annual",
			description:  "Full-service multi-outlet. Save with annual billing.",
			billingCycle: "ANNUAL",
			price:        104500.0,
			tierOrder:    2,
			serviceTag:   "ordering",
			tierLimits: map[string]any{
				"max_orders_per_day":          1000,
				"max_admins":                  3,
				"max_staff":                   30,
				"max_outlets":                 3,
				"api_calls_per_month":         50000,
				"webhook_calls_per_day":       1000,
				"max_riders":                             15,
				"live_tracking_requests_per_day":         5000,
				"overage_rider_price_per_month":          250.0,
				"overage_orders_price_per_100_month":     375.0,
				"max_devices":               5,
				"max_cashiers":              10,
				"max_tables":                50,
				"max_transactions_per_month": 5000,
				"inventory_max_sku":        2000,
				"inventory_max_warehouses": 3,
				"email_notifications_per_day": 500,
				"sms_notifications_per_day":   200,
			},
			features: []string{
				"online_ordering",
				"rider_app",
				"admin_dashboard",
				"mpesa_integration",
				"mpesa_pos",
				"sms_notifications",
				"push_notifications",
				"basic_analytics",
				"advanced_analytics",
				"custom_domain",
				"loyalty_program",
				"wallet",
				"basic_inventory_access",
				"basic_logistics_access",
				"basic_treasury_access",
				"multi_outlet",
				"promo_codes",
				"group_ordering",
				"paystack_gateway",
				"pos_terminal",
				"pos_integration",
				"multi_cashier",
				"table_management",
				"shift_reports",
				"kds",
				"offline_sync",
			},
		},
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("plan:COMPLETE_PROFESSIONAL_YEARLY")),
			planCode:     "COMPLETE_PROFESSIONAL_YEARLY",
			name:         "Complete Professional — Annual",
			description:  "Enterprise full-service operations. Save with annual billing.",
			billingCycle: "ANNUAL",
			price:        198000.0,
			tierOrder:    3,
			serviceTag:   "ordering",
			tierLimits: map[string]any{
				"max_orders_per_day":          2500,
				"max_admins":                  -1,
				"max_staff":                   -1,
				"max_outlets":                 -1,
				"api_calls_per_month":         200000,
				"webhook_calls_per_day":       10000,
				"max_riders":                             30,
				"live_tracking_requests_per_day":         -1,
				"overage_rider_price_per_month":          250.0,
				"overage_orders_price_per_100_month":     375.0,
				"max_devices":               -1,
				"max_cashiers":              -1,
				"max_tables":                -1,
				"max_transactions_per_month": -1,
				"inventory_max_sku":        -1,
				"inventory_max_warehouses": -1,
				"email_notifications_per_day": 5000,
				"sms_notifications_per_day":   2000,
			},
			features: []string{
				"online_ordering",
				"rider_app",
				"admin_dashboard",
				"mpesa_integration",
				"mpesa_pos",
				"sms_notifications",
				"push_notifications",
				"basic_analytics",
				"advanced_analytics",
				"custom_domain",
				"loyalty_program",
				"wallet",
				"basic_inventory_access",
				"basic_logistics_access",
				"basic_treasury_access",
				"multi_outlet",
				"promo_codes",
				"group_ordering",
				"paystack_gateway",
				"pos_terminal",
				"pos_integration",
				"multi_cashier",
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
				SetFreeTrialDays(0).
				SetTierLimitsJSON(p.tierLimits).
				SetServiceTag(p.serviceTag).
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
				SetFreeTrialDays(0).
				SetTierLimitsJSON(p.tierLimits).
				SetServiceTag(p.serviceTag).
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

		log.Printf("  bundle plan: %s (%s, KES %.0f)", p.name, p.billingCycle, p.price)
	}

	return nil
}
