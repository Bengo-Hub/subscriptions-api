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

// ── Projects & Invoicing Plans ───────────────────────────────────────────────
// Plans for the Projects service — project management, task tracking,
// time tracking, client invoicing, expense management, and client portal.
// Sold as a standalone service to freelancers, agencies, and SMEs.

func seedProjectsPlans(ctx context.Context, tx *ent.Tx) error {
	now := time.Now()
	serviceTag := "projects"

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
		// ── Monthly Plans ────────────────────────────────────────────────────
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("projects:STARTER")),
			planCode:     "PROJECTS_STARTER",
			name:         "Projects Starter",
			description:  "Manage up to 5 active projects with task tracking, time logging, and basic invoicing for up to 10 clients.",
			billingCycle: "MONTHLY",
			price:        800.0,
			tierOrder:    1,
			tierLimits: map[string]any{
				"max_projects":          5,
				"max_clients":           10,
				"max_users":             2,
				"max_invoices_per_month": 20,
				"max_storage_gb":        2,
			},
			features: []string{
				"project_management", "task_tracking", "time_tracking",
				"basic_invoicing", "expense_tracking", "basic_reports",
			},
		},
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("projects:GROWTH")),
			planCode:     "PROJECTS_GROWTH",
			name:         "Projects Growth",
			description:  "Unlimited projects and clients. Adds client portal, recurring invoices, milestone billing, and advanced reports.",
			billingCycle: "MONTHLY",
			price:        2500.0,
			tierOrder:    2,
			tierLimits: map[string]any{
				"max_projects":          -1,
				"max_clients":           -1,
				"max_users":             10,
				"max_invoices_per_month": -1,
				"max_storage_gb":        20,
			},
			features: []string{
				"project_management", "task_tracking", "time_tracking",
				"advanced_invoicing", "expense_tracking", "advanced_reports",
				"client_portal", "recurring_invoices", "milestone_billing",
				"team_collaboration", "gantt_chart", "budget_tracking",
			},
		},
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("projects:PROFESSIONAL")),
			planCode:     "PROJECTS_PROFESSIONAL",
			name:         "Projects Professional",
			description:  "Unlimited users and storage. Full API access, webhooks, white-label client portal, and priority support.",
			billingCycle: "MONTHLY",
			price:        6000.0,
			tierOrder:    3,
			tierLimits: map[string]any{
				"max_projects":          -1,
				"max_clients":           -1,
				"max_users":             -1,
				"max_invoices_per_month": -1,
				"max_storage_gb":        -1,
			},
			features: []string{
				"project_management", "task_tracking", "time_tracking",
				"advanced_invoicing", "expense_tracking", "advanced_reports",
				"client_portal", "recurring_invoices", "milestone_billing",
				"team_collaboration", "gantt_chart", "budget_tracking",
				"api_access", "webhooks", "white_label_portal",
				"custom_integrations", "audit_trail", "priority_support",
			},
		},

		// ── Annual Plans (10-month pricing ≈ 16.7% discount) ─────────────────
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("projects:STARTER_YEARLY")),
			planCode:     "PROJECTS_STARTER_YEARLY",
			name:         "Projects Starter — Annual",
			description:  "Up to 5 projects, 10 clients, basic invoicing. Save with annual billing.",
			billingCycle: "ANNUAL",
			price:        8000.0,
			tierOrder:    1,
			tierLimits: map[string]any{
				"max_projects":          5,
				"max_clients":           10,
				"max_users":             2,
				"max_invoices_per_month": 20,
				"max_storage_gb":        2,
			},
			features: []string{
				"project_management", "task_tracking", "time_tracking",
				"basic_invoicing", "expense_tracking", "basic_reports",
			},
		},
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("projects:GROWTH_YEARLY")),
			planCode:     "PROJECTS_GROWTH_YEARLY",
			name:         "Projects Growth — Annual",
			description:  "Unlimited projects and clients, client portal, recurring invoices, Gantt charts. Save with annual billing.",
			billingCycle: "ANNUAL",
			price:        25000.0,
			tierOrder:    2,
			tierLimits: map[string]any{
				"max_projects":          -1,
				"max_clients":           -1,
				"max_users":             10,
				"max_invoices_per_month": -1,
				"max_storage_gb":        20,
			},
			features: []string{
				"project_management", "task_tracking", "time_tracking",
				"advanced_invoicing", "expense_tracking", "advanced_reports",
				"client_portal", "recurring_invoices", "milestone_billing",
				"team_collaboration", "gantt_chart", "budget_tracking",
			},
		},
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("projects:PROFESSIONAL_YEARLY")),
			planCode:     "PROJECTS_PROFESSIONAL_YEARLY",
			name:         "Projects Professional — Annual",
			description:  "Unlimited everything, API access, white-label portal, priority support. Save with annual billing.",
			billingCycle: "ANNUAL",
			price:        60000.0,
			tierOrder:    3,
			tierLimits: map[string]any{
				"max_projects":          -1,
				"max_clients":           -1,
				"max_users":             -1,
				"max_invoices_per_month": -1,
				"max_storage_gb":        -1,
			},
			features: []string{
				"project_management", "task_tracking", "time_tracking",
				"advanced_invoicing", "expense_tracking", "advanced_reports",
				"client_portal", "recurring_invoices", "milestone_billing",
				"team_collaboration", "gantt_chart", "budget_tracking",
				"api_access", "webhooks", "white_label_portal",
				"custom_integrations", "audit_trail", "priority_support",
			},
		},
	}

	for _, p := range plans {
		existing, err := tx.SubscriptionPlan.Get(ctx, p.id)
		if err != nil && !ent.IsNotFound(err) {
			return fmt.Errorf("lookup projects plan %s: %w", p.planCode, err)
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
			return fmt.Errorf("upsert projects plan %s: %w", p.planCode, err)
		}
		if err := seedPlanFeatures(ctx, tx, p.id, p.features); err != nil {
			return fmt.Errorf("seed features for projects plan %s: %w", p.planCode, err)
		}
		log.Printf("  projects plan: %s (%s, KES %.0f)", p.name, p.billingCycle, p.price)
	}
	return nil
}
