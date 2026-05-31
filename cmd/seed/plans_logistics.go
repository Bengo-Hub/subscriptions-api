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

// ── Logistics Standalone Plans ───────────────────────────────────────────────
// Standalone plans for the Delivery & Logistics product (rider management,
// delivery assignment, real-time tracking, route optimisation, rider mobile app).
// These are sold independently to tenants who manage their own delivery fleet
// without needing the full online-ordering bundle.

func seedLogisticsPlans(ctx context.Context, tx *ent.Tx) error {
	now := time.Now()
	serviceTag := "logistics"

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
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("logistics:STARTER")),
			planCode:     "LOGISTICS_STARTER",
			name:         "Logistics Starter",
			description:  "Essential rider management for small fleets. Up to 5 riders, live tracking, and basic dispatch.",
			billingCycle: "MONTHLY",
			price:        800.0,
			tierOrder:    1,
			tierLimits: map[string]any{
				"max_riders":                     5,
				"max_outlets":                    1,
				"max_orders_per_day":             200,
				"live_tracking_requests_per_day": 1000,
				"routing_requests_per_day":       100,
				"api_calls_per_month":            10000,
			},
			features: []string{"rider_management", "delivery_assignment", "live_tracking", "basic_dispatch", "sms_notifications"},
		},
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("logistics:GROWTH")),
			planCode:     "LOGISTICS_GROWTH",
			name:         "Logistics Growth",
			description:  "Multi-outlet dispatch for growing fleets. Up to 20 riders, route optimisation, and driver analytics.",
			billingCycle: "MONTHLY",
			price:        2500.0,
			tierOrder:    2,
			tierLimits: map[string]any{
				"max_riders":                     20,
				"max_outlets":                    3,
				"max_orders_per_day":             1000,
				"live_tracking_requests_per_day": 5000,
				"routing_requests_per_day":       500,
				"api_calls_per_month":            60000,
			},
			features: []string{"rider_management", "delivery_assignment", "live_tracking", "basic_dispatch", "sms_notifications", "route_optimisation", "driver_analytics", "multi_outlet", "performance_reports"},
		},
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("logistics:PROFESSIONAL")),
			planCode:     "LOGISTICS_PROFESSIONAL",
			name:         "Logistics Professional",
			description:  "Unlimited riders and outlets, full route optimisation, webhook events, and API access for enterprise fleet operations.",
			billingCycle: "MONTHLY",
			price:        5000.0,
			tierOrder:    3,
			tierLimits: map[string]any{
				"max_riders":                     -1,
				"max_outlets":                    -1,
				"max_orders_per_day":             -1,
				"live_tracking_requests_per_day": -1,
				"routing_requests_per_day":       -1,
				"api_calls_per_month":            -1,
			},
			features: []string{"rider_management", "delivery_assignment", "live_tracking", "basic_dispatch", "sms_notifications", "route_optimisation", "driver_analytics", "multi_outlet", "performance_reports", "api_access", "webhooks", "custom_integrations", "priority_support"},
		},

		// ── Annual Plans (10 months pricing ≈ 16.7% discount) ───────────
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("logistics:STARTER_YEARLY")),
			planCode:     "LOGISTICS_STARTER_YEARLY",
			name:         "Logistics Starter — Annual",
			description:  "Up to 5 riders with live tracking. Save with annual billing.",
			billingCycle: "ANNUAL",
			price:        8000.0,
			tierOrder:    1,
			tierLimits: map[string]any{
				"max_riders":                     5,
				"max_outlets":                    1,
				"max_orders_per_day":             200,
				"live_tracking_requests_per_day": 1000,
				"routing_requests_per_day":       100,
				"api_calls_per_month":            10000,
			},
			features: []string{"rider_management", "delivery_assignment", "live_tracking", "basic_dispatch", "sms_notifications"},
		},
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("logistics:GROWTH_YEARLY")),
			planCode:     "LOGISTICS_GROWTH_YEARLY",
			name:         "Logistics Growth — Annual",
			description:  "Up to 20 riders, route optimisation, driver analytics. Save with annual billing.",
			billingCycle: "ANNUAL",
			price:        25000.0,
			tierOrder:    2,
			tierLimits: map[string]any{
				"max_riders":                     20,
				"max_outlets":                    3,
				"max_orders_per_day":             1000,
				"live_tracking_requests_per_day": 5000,
				"routing_requests_per_day":       500,
				"api_calls_per_month":            60000,
			},
			features: []string{"rider_management", "delivery_assignment", "live_tracking", "basic_dispatch", "sms_notifications", "route_optimisation", "driver_analytics", "multi_outlet", "performance_reports"},
		},
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("logistics:PROFESSIONAL_YEARLY")),
			planCode:     "LOGISTICS_PROFESSIONAL_YEARLY",
			name:         "Logistics Professional — Annual",
			description:  "Unlimited riders and outlets, API access, webhooks, priority support. Save with annual billing.",
			billingCycle: "ANNUAL",
			price:        50000.0,
			tierOrder:    3,
			tierLimits: map[string]any{
				"max_riders":                     -1,
				"max_outlets":                    -1,
				"max_orders_per_day":             -1,
				"live_tracking_requests_per_day": -1,
				"routing_requests_per_day":       -1,
				"api_calls_per_month":            -1,
			},
			features: []string{"rider_management", "delivery_assignment", "live_tracking", "basic_dispatch", "sms_notifications", "route_optimisation", "driver_analytics", "multi_outlet", "performance_reports", "api_access", "webhooks", "custom_integrations", "priority_support"},
		},
	}

	for _, p := range plans {
		existing, err := tx.SubscriptionPlan.Get(ctx, p.id)
		if err != nil && !ent.IsNotFound(err) {
			return fmt.Errorf("lookup logistics plan %s: %w", p.planCode, err)
		}
		if existing != nil {
			_, err = tx.SubscriptionPlan.UpdateOneID(p.id).
				SetPlanCode(p.planCode).SetName(p.name).SetDescription(p.description).
				SetBillingCycle(p.billingCycle).SetPlanType(subscriptionplan.PlanTypeSTANDALONE_SERVICE).
				SetBasePrice(p.price).SetCurrency("KES").SetIsActive(true).SetIsPublic(true).
				SetTierOrder(p.tierOrder).SetTierLimitsJSON(p.tierLimits).SetServiceTag(serviceTag).SetUpdatedAt(now).Save(ctx)
		} else {
			_, err = tx.SubscriptionPlan.Create().
				SetID(p.id).SetPlanCode(p.planCode).SetName(p.name).SetDescription(p.description).
				SetBillingCycle(p.billingCycle).SetPlanType(subscriptionplan.PlanTypeSTANDALONE_SERVICE).
				SetBasePrice(p.price).SetCurrency("KES").SetIsActive(true).SetIsPublic(true).
				SetTierOrder(p.tierOrder).SetTierLimitsJSON(p.tierLimits).SetServiceTag(serviceTag).
				SetCreatedAt(now).SetUpdatedAt(now).Save(ctx)
		}
		if err != nil {
			return fmt.Errorf("upsert logistics plan %s: %w", p.planCode, err)
		}
		if err := seedPlanFeatures(ctx, tx, p.id, p.features); err != nil {
			return fmt.Errorf("seed features for logistics plan %s: %w", p.planCode, err)
		}
		log.Printf("  logistics plan: %s (%s, KES %.0f)", p.name, p.billingCycle, p.price)
	}
	return nil
}
