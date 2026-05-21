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

// ── MarketFlow CRM Plans ──────────────────────────────────────────────────────
// MarketFlow is the AI-powered marketing automation & CRM platform.
// Covers: contacts, leads, campaigns, funnels, sequences, analytics,
//         AI chat agents, knowledge base, webhooks, and integrations.
// Ticketing / helpdesk features (ticketing-service) are included from Growth up.

func seedMarketFlowPlans(ctx context.Context, tx *ent.Tx) error {
	now := time.Now()
	serviceTag := "marketflow"

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
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("marketflow:STARTER")),
			planCode:     "MARKETFLOW_STARTER",
			name:         "MarketFlow Starter",
			description:  "Core CRM for growing teams: contact & lead management, basic campaigns, landing pages, and 5 AI chat credits/month.",
			billingCycle: "MONTHLY",
			price:        3500.0,
			tierOrder:    1,
			tierLimits: map[string]any{
				"max_contacts":       1000,
				"max_leads":          500,
				"max_campaigns":      5,
				"max_sequences":      3,
				"max_funnels":        2,
				"max_users":          3,
				"ai_credits_monthly": 5,
				"max_integrations":   2,
			},
			features: []string{
				"contact_management", "lead_management", "basic_campaigns",
				"landing_pages", "email_sequences", "ai_chat_agent",
				"basic_analytics", "shortlinks",
			},
		},
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("marketflow:GROWTH")),
			planCode:     "MARKETFLOW_GROWTH",
			name:         "MarketFlow Growth",
			description:  "Full CRM suite: unlimited campaigns, automation sequences, funnel builder, lead scoring, helpdesk ticketing, and 100 AI credits/month.",
			billingCycle: "MONTHLY",
			price:        8500.0,
			tierOrder:    2,
			tierLimits: map[string]any{
				"max_contacts":       10000,
				"max_leads":          5000,
				"max_campaigns":      -1,
				"max_sequences":      -1,
				"max_funnels":        20,
				"max_users":          15,
				"ai_credits_monthly": 100,
				"max_integrations":   10,
				"max_tickets":        -1,
				"max_agents":         5,
			},
			features: []string{
				"contact_management", "lead_management", "unlimited_campaigns",
				"landing_pages", "email_sequences", "ai_chat_agent",
				"advanced_analytics", "shortlinks", "lead_scoring",
				"funnel_builder", "automation_workflows", "webhooks",
				"ticketing", "helpdesk", "sla_policies",
				"knowledge_base", "testimonials",
			},
		},
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("marketflow:PROFESSIONAL")),
			planCode:     "MARKETFLOW_PROFESSIONAL",
			name:         "MarketFlow Professional",
			description:  "Enterprise marketing + CRM: unlimited contacts, unlimited AI credits, custom integrations, white-label, API access, and dedicated support.",
			billingCycle: "MONTHLY",
			price:        18000.0,
			tierOrder:    3,
			tierLimits: map[string]any{
				"max_contacts":       -1,
				"max_leads":          -1,
				"max_campaigns":      -1,
				"max_sequences":      -1,
				"max_funnels":        -1,
				"max_users":          -1,
				"ai_credits_monthly": -1,
				"max_integrations":   -1,
				"max_tickets":        -1,
				"max_agents":         -1,
			},
			features: []string{
				"contact_management", "lead_management", "unlimited_campaigns",
				"landing_pages", "email_sequences", "ai_chat_agent",
				"advanced_analytics", "shortlinks", "lead_scoring",
				"funnel_builder", "automation_workflows", "webhooks",
				"ticketing", "helpdesk", "sla_policies",
				"knowledge_base", "testimonials",
				"api_access", "white_label", "custom_integrations",
				"audit_trail", "priority_support", "dedicated_account_manager",
			},
		},

		// ── Annual plans (10-month pricing) ──────────────────────────────────
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("marketflow:STARTER_YEARLY")),
			planCode:     "MARKETFLOW_STARTER_YEARLY",
			name:         "MarketFlow Starter — Annual",
			description:  "Core CRM with contact & lead management, basic campaigns, and AI chat. Save with annual billing.",
			billingCycle: "ANNUAL",
			price:        35000.0,
			tierOrder:    1,
			tierLimits: map[string]any{
				"max_contacts":       1000,
				"max_leads":          500,
				"max_campaigns":      5,
				"max_sequences":      3,
				"max_funnels":        2,
				"max_users":          3,
				"ai_credits_monthly": 5,
				"max_integrations":   2,
			},
			features: []string{
				"contact_management", "lead_management", "basic_campaigns",
				"landing_pages", "email_sequences", "ai_chat_agent",
				"basic_analytics", "shortlinks",
			},
		},
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("marketflow:GROWTH_YEARLY")),
			planCode:     "MARKETFLOW_GROWTH_YEARLY",
			name:         "MarketFlow Growth — Annual",
			description:  "Full CRM + AI + helpdesk ticketing suite. Save with annual billing.",
			billingCycle: "ANNUAL",
			price:        85000.0,
			tierOrder:    2,
			tierLimits: map[string]any{
				"max_contacts":       10000,
				"max_leads":          5000,
				"max_campaigns":      -1,
				"max_sequences":      -1,
				"max_funnels":        20,
				"max_users":          15,
				"ai_credits_monthly": 100,
				"max_integrations":   10,
				"max_tickets":        -1,
				"max_agents":         5,
			},
			features: []string{
				"contact_management", "lead_management", "unlimited_campaigns",
				"landing_pages", "email_sequences", "ai_chat_agent",
				"advanced_analytics", "shortlinks", "lead_scoring",
				"funnel_builder", "automation_workflows", "webhooks",
				"ticketing", "helpdesk", "sla_policies",
				"knowledge_base", "testimonials",
			},
		},
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("marketflow:PROFESSIONAL_YEARLY")),
			planCode:     "MARKETFLOW_PROFESSIONAL_YEARLY",
			name:         "MarketFlow Professional — Annual",
			description:  "Enterprise marketing + CRM: unlimited everything, white-label, API access, dedicated support. Save with annual billing.",
			billingCycle: "ANNUAL",
			price:        180000.0,
			tierOrder:    3,
			tierLimits: map[string]any{
				"max_contacts":       -1,
				"max_leads":          -1,
				"max_campaigns":      -1,
				"max_sequences":      -1,
				"max_funnels":        -1,
				"max_users":          -1,
				"ai_credits_monthly": -1,
				"max_integrations":   -1,
				"max_tickets":        -1,
				"max_agents":         -1,
			},
			features: []string{
				"contact_management", "lead_management", "unlimited_campaigns",
				"landing_pages", "email_sequences", "ai_chat_agent",
				"advanced_analytics", "shortlinks", "lead_scoring",
				"funnel_builder", "automation_workflows", "webhooks",
				"ticketing", "helpdesk", "sla_policies",
				"knowledge_base", "testimonials",
				"api_access", "white_label", "custom_integrations",
				"audit_trail", "priority_support", "dedicated_account_manager",
			},
		},

		// ── AI Credits top-up (add-on) ────────────────────────────────────────
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("marketflow:AI_CREDITS_100")),
			planCode:     "MARKETFLOW_AI_CREDITS_100",
			name:         "MarketFlow AI Credits — 100 pack",
			description:  "Top-up pack of 100 AI chat credits for MarketFlow. Each credit = 1 user question + AI response.",
			billingCycle: "ONE_TIME",
			price:        1000.0,
			tierOrder:    10,
			tierLimits:   map[string]any{"ai_credits_topup": 100},
			features:     []string{"ai_chat_agent"},
		},
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("marketflow:AI_CREDITS_500")),
			planCode:     "MARKETFLOW_AI_CREDITS_500",
			name:         "MarketFlow AI Credits — 500 pack",
			description:  "Top-up pack of 500 AI chat credits for MarketFlow. Best value for high-volume teams.",
			billingCycle: "ONE_TIME",
			price:        4000.0,
			tierOrder:    11,
			tierLimits:   map[string]any{"ai_credits_topup": 500},
			features:     []string{"ai_chat_agent"},
		},
	}

	for _, p := range plans {
		existing, err := tx.SubscriptionPlan.Get(ctx, p.id)
		if err != nil && !ent.IsNotFound(err) {
			return fmt.Errorf("lookup marketflow plan %s: %w", p.planCode, err)
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
			return fmt.Errorf("upsert marketflow plan %s: %w", p.planCode, err)
		}
		if err := seedPlanFeatures(ctx, tx, p.id, p.features); err != nil {
			return fmt.Errorf("seed features for marketflow plan %s: %w", p.planCode, err)
		}
		log.Printf("  marketflow plan: %s (%s, KES %.0f)", p.name, p.billingCycle, p.price)
	}
	return nil
}
