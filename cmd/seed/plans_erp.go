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

// ── ERP Plans ─────────────────────────────────────────────────────────────────

func seedERPPlans(ctx context.Context, tx *ent.Tx) error {
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
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("erp:STARTER")),
			planCode:     "ERP_STARTER",
			name:         "ERP Starter",
			description:  "Core HR, payroll, and basic procurement for small teams. Up to 20 employees.",
			billingCycle: "MONTHLY",
			price:        2000.0,
			tierOrder:    1,
			tierLimits:   map[string]any{"max_employees": 20, "max_users": 5, "max_departments": 3},
			features:     []string{"hr_management", "payroll", "basic_procurement", "leave_management", "basic_reports"},
		},
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("erp:GROWTH")),
			planCode:     "ERP_GROWTH",
			name:         "ERP Growth",
			description:  "Multi-department HR, payroll, asset management, budgeting, and advanced reporting. Up to 100 employees.",
			billingCycle: "MONTHLY",
			price:        5000.0,
			tierOrder:    2,
			tierLimits:   map[string]any{"max_employees": 100, "max_users": 20, "max_departments": -1},
			features:     []string{"hr_management", "payroll", "basic_procurement", "leave_management", "basic_reports", "asset_management", "budgeting", "advanced_reports", "multi_department", "approval_workflows"},
		},
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("erp:PROFESSIONAL")),
			planCode:     "ERP_PROFESSIONAL",
			name:         "ERP Professional",
			description:  "Unlimited employees, full financial suite, API access, custom workflows, and priority support.",
			billingCycle: "MONTHLY",
			price:        10000.0,
			tierOrder:    3,
			tierLimits:   map[string]any{"max_employees": -1, "max_users": -1, "max_departments": -1},
			features:     []string{"hr_management", "payroll", "basic_procurement", "leave_management", "basic_reports", "asset_management", "budgeting", "advanced_reports", "multi_department", "approval_workflows", "api_access", "custom_workflows", "audit_trail", "priority_support"},
		},
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("erp:STARTER_YEARLY")),
			planCode:     "ERP_STARTER_YEARLY",
			name:         "ERP Starter — Annual",
			description:  "Core HR and payroll. Save with annual billing.",
			billingCycle: "ANNUAL",
			price:        22000.0,
			tierOrder:    1,
			tierLimits:   map[string]any{"max_employees": 20, "max_users": 5, "max_departments": 3},
			features:     []string{"hr_management", "payroll", "basic_procurement", "leave_management", "basic_reports"},
		},
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("erp:GROWTH_YEARLY")),
			planCode:     "ERP_GROWTH_YEARLY",
			name:         "ERP Growth — Annual",
			description:  "Multi-department HR, asset management, budgeting. Save with annual billing.",
			billingCycle: "ANNUAL",
			price:        55000.0,
			tierOrder:    2,
			tierLimits:   map[string]any{"max_employees": 100, "max_users": 20, "max_departments": -1},
			features:     []string{"hr_management", "payroll", "basic_procurement", "leave_management", "basic_reports", "asset_management", "budgeting", "advanced_reports", "multi_department", "approval_workflows"},
		},
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("erp:PROFESSIONAL_YEARLY")),
			planCode:     "ERP_PROFESSIONAL_YEARLY",
			name:         "ERP Professional — Annual",
			description:  "Unlimited employees, full financial suite, API access. Save with annual billing.",
			billingCycle: "ANNUAL",
			price:        110000.0,
			tierOrder:    3,
			tierLimits:   map[string]any{"max_employees": -1, "max_users": -1, "max_departments": -1},
			features:     []string{"hr_management", "payroll", "basic_procurement", "leave_management", "basic_reports", "asset_management", "budgeting", "advanced_reports", "multi_department", "approval_workflows", "api_access", "custom_workflows", "audit_trail", "priority_support"},
		},
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("erp:ONE_TIME")),
			planCode:     "ERP_ONE_TIME",
			name:         "ERP — Perpetual License",
			description:  "One-time purchase of ERP Suite. All features included. Excludes ongoing cloud hosting and support.",
			billingCycle: "ONE_TIME",
			price:        150000.0,
			tierOrder:    4,
			tierLimits:   map[string]any{"max_employees": -1, "max_users": -1, "max_departments": -1},
			features:     []string{"hr_management", "payroll", "basic_procurement", "leave_management", "basic_reports", "asset_management", "budgeting", "advanced_reports", "multi_department", "approval_workflows", "api_access", "custom_workflows", "audit_trail", "priority_support"},
		},
	}

	for _, p := range plans {
		existing, err := tx.SubscriptionPlan.Get(ctx, p.id)
		if err != nil && !ent.IsNotFound(err) {
			return fmt.Errorf("lookup erp plan %s: %w", p.planCode, err)
		}
		if existing != nil {
			_, err = tx.SubscriptionPlan.UpdateOneID(p.id).
				SetPlanCode(p.planCode).SetName(p.name).SetDescription(p.description).
				SetBillingCycle(p.billingCycle).SetPlanType(subscriptionplan.PlanTypeSTANDALONE_SERVICE).
				SetBasePrice(p.price).SetCurrency("KES").SetIsActive(true).SetIsPublic(true).
				SetTierOrder(p.tierOrder).SetTierLimitsJSON(p.tierLimits).SetUpdatedAt(now).Save(ctx)
		} else {
			_, err = tx.SubscriptionPlan.Create().
				SetID(p.id).SetPlanCode(p.planCode).SetName(p.name).SetDescription(p.description).
				SetBillingCycle(p.billingCycle).SetPlanType(subscriptionplan.PlanTypeSTANDALONE_SERVICE).
				SetBasePrice(p.price).SetCurrency("KES").SetIsActive(true).SetIsPublic(true).
				SetTierOrder(p.tierOrder).SetTierLimitsJSON(p.tierLimits).
				SetCreatedAt(now).SetUpdatedAt(now).Save(ctx)
		}
		if err != nil {
			return fmt.Errorf("upsert erp plan %s: %w", p.planCode, err)
		}
		if err := seedPlanFeatures(ctx, tx, p.id, p.features); err != nil {
			return fmt.Errorf("seed features for erp plan %s: %w", p.planCode, err)
		}
		log.Printf("  erp plan: %s (%s, KES %.0f)", p.name, p.billingCycle, p.price)
	}
	return nil
}
