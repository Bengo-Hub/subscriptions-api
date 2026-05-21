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

// ── TruLoad Org-Level Plans ──────────────────────────────────────────────────
// Plans for TruLoad tenant organisations (weighbridge operators).
// Three tiers: Starter, Growth, Professional — each with MONTHLY + ANNUAL billing.
// Plus a ONE_TIME license plan for outright purchase.
// Feature codes match the TruLoad feature gate matrix in the backend.

func seedTruLoadOrgPlans(ctx context.Context, tx *ent.Tx) error {
	now := time.Now()
	serviceTag := "truload"

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
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("truload:org:STARTER")),
			planCode:     "TRULOAD_STARTER",
			name:         "TruLoad Starter",
			description:  "Essential weighbridge management for single-station operations. Commercial weighing, enforcement, invoicing, PDF tickets, and core reporting.",
			billingCycle: "MONTHLY",
			planType:     subscriptionplan.PlanTypeTIERED,
			price:        2500.0,
			tierOrder:    1,
			tierLimits: map[string]any{
				"max_stations":        1,
				"max_users":           5,
				"max_weighings_month": 500,
			},
			features: []string{
				"commercial_weighing",
				"enforcement_weighing",
				"invoicing",
				"reporting",
				"tare_management",
				"tolerance_settings",
				"cargo_types",
			},
		},
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("truload:org:GROWTH")),
			planCode:     "TRULOAD_GROWTH",
			name:         "TruLoad Growth",
			description:  "Multi-station support with transporter portal and case management. Adds prosecution tracking and transporter self-service portal access.",
			billingCycle: "MONTHLY",
			planType:     subscriptionplan.PlanTypeTIERED,
			price:        6000.0,
			tierOrder:    2,
			tierLimits: map[string]any{
				"max_stations":        5,
				"max_users":           20,
				"max_weighings_month": 3000,
			},
			features: []string{
				"commercial_weighing",
				"enforcement_weighing",
				"invoicing",
				"reporting",
				"tare_management",
				"tolerance_settings",
				"cargo_types",
				"case_management",
				"prosecution",
				"transporter_portal",
			},
		},
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("truload:org:PROFESSIONAL")),
			planCode:     "TRULOAD_PROFESSIONAL",
			name:         "TruLoad Professional",
			description:  "Full-featured weighbridge platform for large operators and government agencies. Unlimited stations, API/webhook access, advanced analytics, and priority support.",
			billingCycle: "MONTHLY",
			planType:     subscriptionplan.PlanTypeTIERED,
			price:        12500.0,
			tierOrder:    3,
			tierLimits: map[string]any{
				"max_stations":        -1,
				"max_users":           -1,
				"max_weighings_month": -1,
			},
			features: []string{
				"commercial_weighing",
				"enforcement_weighing",
				"invoicing",
				"reporting",
				"tare_management",
				"tolerance_settings",
				"cargo_types",
				"case_management",
				"prosecution",
				"transporter_portal",
				"api_webhooks",
			},
		},

		// ── Annual Plans (≈10 months pricing) ───────────────────────────
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("truload:org:STARTER_YEARLY")),
			planCode:     "TRULOAD_STARTER_YEARLY",
			name:         "TruLoad Starter — Annual",
			description:  "Essential weighbridge management. Save with annual billing.",
			billingCycle: "ANNUAL",
			planType:     subscriptionplan.PlanTypeTIERED,
			price:        27500.0,
			tierOrder:    1,
			tierLimits: map[string]any{
				"max_stations":        1,
				"max_users":           5,
				"max_weighings_month": 500,
			},
			features: []string{
				"commercial_weighing",
				"enforcement_weighing",
				"invoicing",
				"reporting",
				"tare_management",
				"tolerance_settings",
				"cargo_types",
			},
		},
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("truload:org:GROWTH_YEARLY")),
			planCode:     "TRULOAD_GROWTH_YEARLY",
			name:         "TruLoad Growth — Annual",
			description:  "Multi-station with transporter portal and case management. Save with annual billing.",
			billingCycle: "ANNUAL",
			planType:     subscriptionplan.PlanTypeTIERED,
			price:        66000.0,
			tierOrder:    2,
			tierLimits: map[string]any{
				"max_stations":        5,
				"max_users":           20,
				"max_weighings_month": 3000,
			},
			features: []string{
				"commercial_weighing",
				"enforcement_weighing",
				"invoicing",
				"reporting",
				"tare_management",
				"tolerance_settings",
				"cargo_types",
				"case_management",
				"prosecution",
				"transporter_portal",
			},
		},
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("truload:org:PROFESSIONAL_YEARLY")),
			planCode:     "TRULOAD_PROFESSIONAL_YEARLY",
			name:         "TruLoad Professional — Annual",
			description:  "Full-featured weighbridge platform. Unlimited capacity, API access, and priority support. Save with annual billing.",
			billingCycle: "ANNUAL",
			planType:     subscriptionplan.PlanTypeTIERED,
			price:        137500.0,
			tierOrder:    3,
			tierLimits: map[string]any{
				"max_stations":        -1,
				"max_users":           -1,
				"max_weighings_month": -1,
			},
			features: []string{
				"commercial_weighing",
				"enforcement_weighing",
				"invoicing",
				"reporting",
				"tare_management",
				"tolerance_settings",
				"cargo_types",
				"case_management",
				"prosecution",
				"transporter_portal",
				"api_webhooks",
			},
		},

		// ── License Plan (ONE_TIME perpetual purchase) ───────────────────
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("truload:org:LICENSE")),
			planCode:     "TRULOAD_LICENSE",
			name:         "TruLoad License",
			description:  "Perpetual software license for TruLoad. One-time purchase. All features included. Excludes ongoing cloud hosting and support.",
			billingCycle: "ONE_TIME",
			planType:     subscriptionplan.PlanTypeTIERED,
			price:        150000.0,
			tierOrder:    4,
			tierLimits: map[string]any{
				"max_stations":        -1,
				"max_users":           -1,
				"max_weighings_month": -1,
			},
			features: []string{
				"commercial_weighing",
				"enforcement_weighing",
				"invoicing",
				"reporting",
				"tare_management",
				"tolerance_settings",
				"cargo_types",
				"case_management",
				"prosecution",
				"transporter_portal",
				"api_webhooks",
			},
		},
	}

	for _, p := range plans {
		existing, err := tx.SubscriptionPlan.Get(ctx, p.id)
		if err != nil && !ent.IsNotFound(err) {
			return fmt.Errorf("lookup truload org plan %s: %w", p.planCode, err)
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
				return fmt.Errorf("update truload org plan %s: %w", p.planCode, err)
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
				return fmt.Errorf("create truload org plan %s: %w", p.planCode, err)
			}
		}

		if err := seedPlanFeatures(ctx, tx, p.id, p.features); err != nil {
			return fmt.Errorf("seed features for truload org plan %s: %w", p.planCode, err)
		}

		log.Printf("  truload org plan: %s (%s, %s, KES %.0f)", p.name, p.planCode, p.billingCycle, p.price)
	}

	return nil
}

// ── TruLoad Transporter Portal Plans ────────────────────────────────────────
// Separate plan tier for transporters (data consumers) accessing the TruLoad
// transporter portal. Cheaper than org-level plans; priced for self-service.
// Features mirror the plan matrix in the TruLoad commercial weighing spec.

func seedTruLoadTransporterPlans(ctx context.Context, tx *ent.Tx) error {
	now := time.Now()
	serviceTag := "truload"

	type planDef struct {
		id           uuid.UUID
		planCode     string
		name         string
		description  string
		billingCycle string
		price        float64
		tierOrder    int
		tierLimits   map[string]any
		features     []featureDef
	}

	plans := []planDef{
		// ── Monthly Plans ────────────────────────────────────────────────
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("truload:plan:TRANSPORTER_BASIC")),
			planCode:     "TRANSPORTER_BASIC",
			name:         "Transporter Basic",
			description:  "View your own weight tickets (90 days history). 1 user, 1 site, email notifications, PDF ticket downloads.",
			billingCycle: "MONTHLY",
			price:        500.0,
			tierOrder:    1,
			tierLimits: map[string]any{
				"max_users":    1,
				"max_sites":    1,
				"history_days": 90,
			},
			features: []featureDef{
				{code: "portal_access"},
				{code: "ticket_download"},
				{code: "email_notifications"},
				{code: "history_days", limitValue: 90},
				{code: "max_users", limitValue: 1},
			},
		},
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("truload:plan:TRANSPORTER_STANDARD")),
			planCode:     "TRANSPORTER_STANDARD",
			name:         "Transporter Standard",
			description:  "Full weighing history, 5 users, multi-site access, CSV export, driver reports, vehicle trends, SMS+email notifications.",
			billingCycle: "MONTHLY",
			price:        1500.0,
			tierOrder:    2,
			tierLimits: map[string]any{
				"max_users":    5,
				"max_sites":    -1, // unlimited
				"history_days": -1, // unlimited
			},
			features: []featureDef{
				{code: "portal_access"},
				{code: "ticket_download"},
				{code: "email_notifications"},
				{code: "sms_notifications"},
				{code: "multi_site_access"},
				{code: "data_export"},
				{code: "driver_reports"},
				{code: "vehicle_trends"},
				{code: "consignment_tracking"},
				{code: "max_users", limitValue: 5},
			},
		},
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("truload:plan:TRANSPORTER_PREMIUM")),
			planCode:     "TRANSPORTER_PREMIUM",
			name:         "Transporter Premium",
			description:  "Unlimited users, all sites, API access, advanced analytics, consignment tracking, webhooks, and bulk data export.",
			billingCycle: "MONTHLY",
			price:        3500.0,
			tierOrder:    3,
			tierLimits: map[string]any{
				"max_users":    -1, // unlimited
				"max_sites":    -1, // unlimited
				"history_days": -1, // unlimited
			},
			features: []featureDef{
				{code: "portal_access"},
				{code: "ticket_download"},
				{code: "email_notifications"},
				{code: "sms_notifications"},
				{code: "multi_site_access"},
				{code: "data_export"},
				{code: "driver_reports"},
				{code: "vehicle_trends"},
				{code: "consignment_tracking"},
				{code: "api_access"},
				{code: "analytics"},
				{code: "webhooks"},
			},
		},

		// ── Annual Plans (10 months pricing ≈ 16.7% discount) ───────────
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("truload:plan:TRANSPORTER_BASIC_YEARLY")),
			planCode:     "TRANSPORTER_BASIC_YEARLY",
			name:         "Transporter Basic — Annual",
			description:  "View your own weight tickets (90 days). 1 user, 1 site. Save with annual billing.",
			billingCycle: "ANNUAL",
			price:        5000.0,
			tierOrder:    1,
			tierLimits: map[string]any{
				"max_users":    1,
				"max_sites":    1,
				"history_days": 90,
			},
			features: []featureDef{
				{code: "portal_access"},
				{code: "ticket_download"},
				{code: "email_notifications"},
				{code: "history_days", limitValue: 90},
				{code: "max_users", limitValue: 1},
			},
		},
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("truload:plan:TRANSPORTER_STANDARD_YEARLY")),
			planCode:     "TRANSPORTER_STANDARD_YEARLY",
			name:         "Transporter Standard — Annual",
			description:  "Full history, 5 users, multi-site, CSV export, driver reports, vehicle trends. Save with annual billing.",
			billingCycle: "ANNUAL",
			price:        15000.0,
			tierOrder:    2,
			tierLimits: map[string]any{
				"max_users":    5,
				"max_sites":    -1,
				"history_days": -1,
			},
			features: []featureDef{
				{code: "portal_access"},
				{code: "ticket_download"},
				{code: "email_notifications"},
				{code: "sms_notifications"},
				{code: "multi_site_access"},
				{code: "data_export"},
				{code: "driver_reports"},
				{code: "vehicle_trends"},
				{code: "consignment_tracking"},
				{code: "max_users", limitValue: 5},
			},
		},
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("truload:plan:TRANSPORTER_PREMIUM_YEARLY")),
			planCode:     "TRANSPORTER_PREMIUM_YEARLY",
			name:         "Transporter Premium — Annual",
			description:  "Unlimited users, all sites, API access, analytics, webhooks, bulk export. Save with annual billing.",
			billingCycle: "ANNUAL",
			price:        35000.0,
			tierOrder:    3,
			tierLimits: map[string]any{
				"max_users":    -1,
				"max_sites":    -1,
				"history_days": -1,
			},
			features: []featureDef{
				{code: "portal_access"},
				{code: "ticket_download"},
				{code: "email_notifications"},
				{code: "sms_notifications"},
				{code: "multi_site_access"},
				{code: "data_export"},
				{code: "driver_reports"},
				{code: "vehicle_trends"},
				{code: "consignment_tracking"},
				{code: "api_access"},
				{code: "analytics"},
				{code: "webhooks"},
			},
		},
	}

	for _, p := range plans {
		existing, err := tx.SubscriptionPlan.Get(ctx, p.id)
		if err != nil && !ent.IsNotFound(err) {
			return fmt.Errorf("lookup transporter plan %s: %w", p.planCode, err)
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
				SetTierLimitsJSON(p.tierLimits).
				SetServiceTag(serviceTag).
				SetUpdatedAt(now).
				Save(ctx)
			if err != nil {
				return fmt.Errorf("update transporter plan %s: %w", p.planCode, err)
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
				SetTierLimitsJSON(p.tierLimits).
				SetServiceTag(serviceTag).
				SetCreatedAt(now).
				SetUpdatedAt(now).
				Save(ctx)
			if err != nil {
				return fmt.Errorf("create transporter plan %s: %w", p.planCode, err)
			}
		}

		if err := seedPlanFeaturesWithLimits(ctx, tx, p.id, p.features); err != nil {
			return fmt.Errorf("seed features for transporter plan %s: %w", p.planCode, err)
		}

		log.Printf("  transporter plan: %s (%s, %s, KES %.0f)", p.name, p.planCode, p.billingCycle, p.price)
	}

	return nil
}
