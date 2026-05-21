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

// ── Cafe Plans ────────────────────────────────────────────────────────────────
// Cafe-website is the customer-facing ordering portal for F&B outlets.
// Plans cover menu management, online ordering, table reservations, and analytics.

func seedCafePlans(ctx context.Context, tx *ent.Tx) error {
	now := time.Now()
	serviceTag := "cafe"

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
		// ── Monthly plans ────────────────────────────────────────────────────
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("cafe:STARTER")),
			planCode:     "CAFE_STARTER",
			name:         "Cafe Starter",
			description:  "Online menu and basic ordering for single-outlet cafes: up to 50 menu items, online ordering, and order notifications.",
			billingCycle: "MONTHLY",
			price:        2000.0,
			tierOrder:    1,
			tierLimits: map[string]any{
				"max_menu_items":  50,
				"max_categories":  10,
				"max_outlets":     1,
				"max_orders_day":  100,
			},
			features: []string{
				"online_menu", "online_ordering", "order_notifications",
				"basic_analytics", "qr_code_menu",
			},
		},
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("cafe:GROWTH")),
			planCode:     "CAFE_GROWTH",
			name:         "Cafe Growth",
			description:  "Multi-outlet cafe management: unlimited menu items, table reservations, loyalty program, and advanced analytics.",
			billingCycle: "MONTHLY",
			price:        5500.0,
			tierOrder:    2,
			tierLimits: map[string]any{
				"max_menu_items":  -1,
				"max_categories":  -1,
				"max_outlets":     5,
				"max_orders_day":  -1,
			},
			features: []string{
				"online_menu", "online_ordering", "order_notifications",
				"advanced_analytics", "qr_code_menu", "table_reservations",
				"loyalty_program", "promo_codes", "multi_outlet",
			},
		},
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("cafe:PROFESSIONAL")),
			planCode:     "CAFE_PROFESSIONAL",
			name:         "Cafe Professional",
			description:  "Enterprise F&B portal: unlimited outlets, custom branding, API access, integrations, and dedicated support.",
			billingCycle: "MONTHLY",
			price:        12000.0,
			tierOrder:    3,
			tierLimits: map[string]any{
				"max_menu_items":  -1,
				"max_categories":  -1,
				"max_outlets":     -1,
				"max_orders_day":  -1,
			},
			features: []string{
				"online_menu", "online_ordering", "order_notifications",
				"advanced_analytics", "qr_code_menu", "table_reservations",
				"loyalty_program", "promo_codes", "multi_outlet",
				"custom_branding", "api_access", "webhooks",
				"audit_trail", "priority_support",
			},
		},

		// ── Annual plans (10-month pricing) ──────────────────────────────────
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("cafe:STARTER_YEARLY")),
			planCode:     "CAFE_STARTER_YEARLY",
			name:         "Cafe Starter — Annual",
			description:  "Online menu and basic ordering for single-outlet cafes. Save with annual billing.",
			billingCycle: "ANNUAL",
			price:        20000.0,
			tierOrder:    1,
			tierLimits: map[string]any{
				"max_menu_items":  50,
				"max_categories":  10,
				"max_outlets":     1,
				"max_orders_day":  100,
			},
			features: []string{
				"online_menu", "online_ordering", "order_notifications",
				"basic_analytics", "qr_code_menu",
			},
		},
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("cafe:GROWTH_YEARLY")),
			planCode:     "CAFE_GROWTH_YEARLY",
			name:         "Cafe Growth — Annual",
			description:  "Multi-outlet management with reservations and loyalty. Save with annual billing.",
			billingCycle: "ANNUAL",
			price:        55000.0,
			tierOrder:    2,
			tierLimits: map[string]any{
				"max_menu_items":  -1,
				"max_categories":  -1,
				"max_outlets":     5,
				"max_orders_day":  -1,
			},
			features: []string{
				"online_menu", "online_ordering", "order_notifications",
				"advanced_analytics", "qr_code_menu", "table_reservations",
				"loyalty_program", "promo_codes", "multi_outlet",
			},
		},
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("cafe:PROFESSIONAL_YEARLY")),
			planCode:     "CAFE_PROFESSIONAL_YEARLY",
			name:         "Cafe Professional — Annual",
			description:  "Enterprise F&B portal with unlimited outlets and custom branding. Save with annual billing.",
			billingCycle: "ANNUAL",
			price:        120000.0,
			tierOrder:    3,
			tierLimits: map[string]any{
				"max_menu_items":  -1,
				"max_categories":  -1,
				"max_outlets":     -1,
				"max_orders_day":  -1,
			},
			features: []string{
				"online_menu", "online_ordering", "order_notifications",
				"advanced_analytics", "qr_code_menu", "table_reservations",
				"loyalty_program", "promo_codes", "multi_outlet",
				"custom_branding", "api_access", "webhooks",
				"audit_trail", "priority_support",
			},
		},
	}

	for _, p := range plans {
		existing, err := tx.SubscriptionPlan.Get(ctx, p.id)
		if err != nil && !ent.IsNotFound(err) {
			return fmt.Errorf("lookup cafe plan %s: %w", p.planCode, err)
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
			return fmt.Errorf("upsert cafe plan %s: %w", p.planCode, err)
		}
		if err := seedPlanFeatures(ctx, tx, p.id, p.features); err != nil {
			return fmt.Errorf("seed features for cafe plan %s: %w", p.planCode, err)
		}
		log.Printf("  cafe plan: %s (%s, KES %.0f)", p.name, p.billingCycle, p.price)
	}
	return nil
}
