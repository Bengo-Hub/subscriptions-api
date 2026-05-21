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

// ── Inventory Plans ──────────────────────────────────────────────────────────

func seedInventoryPlans(ctx context.Context, tx *ent.Tx) error {
	now := time.Now()
	serviceTag := "inventory"

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
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("inventory:STARTER")),
			planCode:     "INVENTORY_STARTER",
			name:         "Inventory Starter",
			description:  "Essential stock tracking for small operations. Single warehouse, manual purchase orders, low-stock alerts.",
			billingCycle: "MONTHLY",
			price:        500.0,
			tierOrder:    1,
			tierLimits: map[string]any{
				"max_warehouses":    1,
				"max_products":      500,
				"max_suppliers":     10,
				"max_users":         3,
			},
			features: []string{"stock_tracking", "low_stock_alerts", "purchase_orders", "basic_reports"},
		},
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("inventory:GROWTH")),
			planCode:     "INVENTORY_GROWTH",
			name:         "Inventory Growth",
			description:  "Multi-warehouse support, batch/expiry tracking, supplier portal, and advanced analytics.",
			billingCycle: "MONTHLY",
			price:        1500.0,
			tierOrder:    2,
			tierLimits: map[string]any{
				"max_warehouses":    5,
				"max_products":      5000,
				"max_suppliers":     50,
				"max_users":         10,
			},
			features: []string{"stock_tracking", "low_stock_alerts", "purchase_orders", "basic_reports", "multi_warehouse", "batch_expiry_tracking", "supplier_portal", "advanced_analytics"},
		},
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("inventory:PROFESSIONAL")),
			planCode:     "INVENTORY_PROFESSIONAL",
			name:         "Inventory Professional",
			description:  "Unlimited warehouses, API access, barcode scanning, bulk imports, and priority support.",
			billingCycle: "MONTHLY",
			price:        3000.0,
			tierOrder:    3,
			tierLimits: map[string]any{
				"max_warehouses": -1,
				"max_products":   -1,
				"max_suppliers":  -1,
				"max_users":      -1,
			},
			features: []string{"stock_tracking", "low_stock_alerts", "purchase_orders", "basic_reports", "multi_warehouse", "batch_expiry_tracking", "supplier_portal", "advanced_analytics", "api_access", "barcode_scanning", "bulk_import", "priority_support"},
		},
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("inventory:STARTER_YEARLY")),
			planCode:     "INVENTORY_STARTER_YEARLY",
			name:         "Inventory Starter — Annual",
			description:  "Essential stock tracking. Save with annual billing.",
			billingCycle: "ANNUAL",
			price:        5500.0,
			tierOrder:    1,
			tierLimits:   map[string]any{"max_warehouses": 1, "max_products": 500, "max_suppliers": 10, "max_users": 3},
			features:     []string{"stock_tracking", "low_stock_alerts", "purchase_orders", "basic_reports"},
		},
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("inventory:GROWTH_YEARLY")),
			planCode:     "INVENTORY_GROWTH_YEARLY",
			name:         "Inventory Growth — Annual",
			description:  "Multi-warehouse, batch/expiry tracking, supplier portal. Save with annual billing.",
			billingCycle: "ANNUAL",
			price:        16500.0,
			tierOrder:    2,
			tierLimits:   map[string]any{"max_warehouses": 5, "max_products": 5000, "max_suppliers": 50, "max_users": 10},
			features:     []string{"stock_tracking", "low_stock_alerts", "purchase_orders", "basic_reports", "multi_warehouse", "batch_expiry_tracking", "supplier_portal", "advanced_analytics"},
		},
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("inventory:PROFESSIONAL_YEARLY")),
			planCode:     "INVENTORY_PROFESSIONAL_YEARLY",
			name:         "Inventory Professional — Annual",
			description:  "Unlimited capacity, API access, barcode scanning. Save with annual billing.",
			billingCycle: "ANNUAL",
			price:        33000.0,
			tierOrder:    3,
			tierLimits:   map[string]any{"max_warehouses": -1, "max_products": -1, "max_suppliers": -1, "max_users": -1},
			features:     []string{"stock_tracking", "low_stock_alerts", "purchase_orders", "basic_reports", "multi_warehouse", "batch_expiry_tracking", "supplier_portal", "advanced_analytics", "api_access", "barcode_scanning", "bulk_import", "priority_support"},
		},
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("inventory:ONE_TIME")),
			planCode:     "INVENTORY_ONE_TIME",
			name:         "Inventory — Perpetual License",
			description:  "One-time purchase of Inventory Management. All features included. Excludes ongoing cloud hosting.",
			billingCycle: "ONE_TIME",
			price:        80000.0,
			tierOrder:    4,
			tierLimits:   map[string]any{"max_warehouses": -1, "max_products": -1, "max_suppliers": -1, "max_users": -1},
			features:     []string{"stock_tracking", "low_stock_alerts", "purchase_orders", "basic_reports", "multi_warehouse", "batch_expiry_tracking", "supplier_portal", "advanced_analytics", "api_access", "barcode_scanning", "bulk_import", "priority_support"},
		},
	}

	for _, p := range plans {
		existing, err := tx.SubscriptionPlan.Get(ctx, p.id)
		if err != nil && !ent.IsNotFound(err) {
			return fmt.Errorf("lookup inventory plan %s: %w", p.planCode, err)
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
			return fmt.Errorf("upsert inventory plan %s: %w", p.planCode, err)
		}
		if err := seedPlanFeatures(ctx, tx, p.id, p.features); err != nil {
			return fmt.Errorf("seed features for inventory plan %s: %w", p.planCode, err)
		}
		log.Printf("  inventory plan: %s (%s, KES %.0f)", p.name, p.billingCycle, p.price)
	}
	return nil
}
