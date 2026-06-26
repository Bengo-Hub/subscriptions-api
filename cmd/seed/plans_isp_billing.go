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

// ── ISP Billing Plans ────────────────────────────────────────────────────────
// Plans for the ISP Billing SaaS product — sold to ISP providers (businesses)
// that run hotspot AND/OR PPPoE networks and use the platform to manage
// customers, routers, billing, vouchers, and analytics.
//
// Pricing is the single source of truth in shared-docs/CODEVERTEX-PRICING-MODEL.md
// §6: ONE "ISP Billing" service line with three tiers —
//   Starter 3,500 · Professional 8,000 · Enterprise 16,000 (KES / month).
// Annual = 10× monthly (≈16.7% discount). A single product line covers both
// hotspot and PPPoE (the ISP provider's org type decides which features they use).
//
// SMS/WhatsApp are NOT plan limits/features — they are prepaid credit bundles in
// notifications-api (§8), available to any tenant regardless of tier. So the plans
// carry no max_sms_per_month / sms_notifications.
//
// The earlier split (ISP_HOTSPOT_* / ISP_PPPOE_*) is retired below (marked
// inactive + non-public) so existing rows don't keep showing in the catalog.

func ispBillingPlanID(key string) uuid.UUID {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte("isp_billing:"+key))
}

func seedISPBillingPlans(ctx context.Context, tx *ent.Tx) error {
	now := time.Now()
	serviceTag := "isp_billing"

	// Feature sets are cumulative (Starter ⊂ Professional ⊂ Enterprise).
	starterFeatures := []string{
		"hotspot_management", "pppoe_management", "captive_portal", "voucher_system",
		"radius_auth", "customer_management", "bandwidth_limiting", "basic_analytics",
		"invoice_generation", "mpesa_integration",
	}
	professionalFeatures := append(append([]string{}, starterFeatures...),
		"advanced_analytics", "custom_domain", "multi_router", "performance_reports",
		"expense_tracking", "fup_management",
	)
	enterpriseFeatures := append(append([]string{}, professionalFeatures...),
		"white_label", "api_access", "custom_integrations", "priority_support", "audit_trail",
	)

	starterLimits := map[string]any{
		"max_routers": 2, "max_customers": 200, "max_users": 3, "max_vouchers_per_month": -1,
	}
	professionalLimits := map[string]any{
		"max_routers": 10, "max_customers": 2000, "max_users": 15, "max_vouchers_per_month": -1,
	}
	enterpriseLimits := map[string]any{
		"max_routers": -1, "max_customers": -1, "max_users": -1, "max_vouchers_per_month": -1,
	}

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
		// ── Monthly ──────────────────────────────────────────────────────────
		{
			id:           ispBillingPlanID("STARTER"),
			planCode:     "ISP_BILLING_STARTER",
			name:         "ISP Billing Starter",
			description:  "For small ISPs: up to 2 routers and 200 hotspot/PPPoE customers. Captive portal, vouchers, RADIUS, M-Pesa billing, and basic analytics.",
			billingCycle: "MONTHLY",
			price:        3500.0,
			tierOrder:    1,
			tierLimits:   starterLimits,
			features:     starterFeatures,
		},
		{
			id:           ispBillingPlanID("PROFESSIONAL"),
			planCode:     "ISP_BILLING_PROFESSIONAL",
			name:         "ISP Billing Professional",
			description:  "For growing ISPs: up to 10 routers and 2,000 customers. Adds advanced analytics, custom domain, FUP management, and performance reports.",
			billingCycle: "MONTHLY",
			price:        8000.0,
			tierOrder:    2,
			tierLimits:   professionalLimits,
			features:     professionalFeatures,
		},
		{
			id:           ispBillingPlanID("ENTERPRISE"),
			planCode:     "ISP_BILLING_ENTERPRISE",
			name:         "ISP Billing Enterprise",
			description:  "Unlimited routers and customers. Full white-label, API access, custom integrations, audit trail, and priority support.",
			billingCycle: "MONTHLY",
			price:        16000.0,
			tierOrder:    3,
			tierLimits:   enterpriseLimits,
			features:     enterpriseFeatures,
		},

		// ── Annual (10× monthly ≈ 16.7% discount) ─────────────────────────────
		{
			id:           ispBillingPlanID("STARTER_YEARLY"),
			planCode:     "ISP_BILLING_STARTER_YEARLY",
			name:         "ISP Billing Starter — Annual",
			description:  "Up to 2 routers and 200 customers. Captive portal, vouchers, RADIUS, M-Pesa billing. Save with annual billing.",
			billingCycle: "ANNUAL",
			price:        35000.0,
			tierOrder:    1,
			tierLimits:   starterLimits,
			features:     starterFeatures,
		},
		{
			id:           ispBillingPlanID("PROFESSIONAL_YEARLY"),
			planCode:     "ISP_BILLING_PROFESSIONAL_YEARLY",
			name:         "ISP Billing Professional — Annual",
			description:  "Up to 10 routers and 2,000 customers. Advanced analytics, custom domain, FUP management. Save with annual billing.",
			billingCycle: "ANNUAL",
			price:        80000.0,
			tierOrder:    2,
			tierLimits:   professionalLimits,
			features:     professionalFeatures,
		},
		{
			id:           ispBillingPlanID("ENTERPRISE_YEARLY"),
			planCode:     "ISP_BILLING_ENTERPRISE_YEARLY",
			name:         "ISP Billing Enterprise — Annual",
			description:  "Unlimited routers and customers. White-label, API access, priority support. Save with annual billing.",
			billingCycle: "ANNUAL",
			price:        160000.0,
			tierOrder:    3,
			tierLimits:   enterpriseLimits,
			features:     enterpriseFeatures,
		},
	}

	for _, p := range plans {
		existing, err := tx.SubscriptionPlan.Get(ctx, p.id)
		if err != nil && !ent.IsNotFound(err) {
			return fmt.Errorf("lookup isp_billing plan %s: %w", p.planCode, err)
		}
		if existing != nil {
			_, err = tx.SubscriptionPlan.UpdateOneID(p.id).
				SetPlanCode(p.planCode).SetName(p.name).SetDescription(p.description).
				SetBillingCycle(p.billingCycle).SetPlanType(subscriptionplan.PlanTypeSTANDALONE_SERVICE).
				SetBasePrice(p.price).SetCurrency("KES").SetIsActive(true).SetIsPublic(true).
				SetTierOrder(p.tierOrder).SetTierLimitsJSON(p.tierLimits).SetServiceTag(serviceTag).SetFreeTrialDays(14).SetUpdatedAt(now).Save(ctx)
		} else {
			_, err = tx.SubscriptionPlan.Create().
				SetID(p.id).SetPlanCode(p.planCode).SetName(p.name).SetDescription(p.description).
				SetBillingCycle(p.billingCycle).SetPlanType(subscriptionplan.PlanTypeSTANDALONE_SERVICE).
				SetBasePrice(p.price).SetCurrency("KES").SetIsActive(true).SetIsPublic(true).
				SetTierOrder(p.tierOrder).SetTierLimitsJSON(p.tierLimits).SetServiceTag(serviceTag).SetFreeTrialDays(14).
				SetCreatedAt(now).SetUpdatedAt(now).Save(ctx)
		}
		if err != nil {
			return fmt.Errorf("upsert isp_billing plan %s: %w", p.planCode, err)
		}
		if err := seedPlanFeatures(ctx, tx, p.id, p.features); err != nil {
			return fmt.Errorf("seed features for isp_billing plan %s: %w", p.planCode, err)
		}
		log.Printf("  isp_billing plan: %s (%s, KES %.0f)", p.name, p.billingCycle, p.price)
	}

	// Retire the previous hotspot/pppoe split plans (superseded by the unified
	// ISP Billing line). Mark inactive + non-public so they drop out of the
	// catalog while preserving any historical subscriptions referencing them.
	retired := []string{
		"HOTSPOT_STARTER", "HOTSPOT_PROFESSIONAL", "HOTSPOT_ENTERPRISE",
		"PPPOE_STARTER", "PPPOE_PROFESSIONAL", "PPPOE_ENTERPRISE",
		"HOTSPOT_STARTER_YEARLY", "HOTSPOT_PROFESSIONAL_YEARLY", "HOTSPOT_ENTERPRISE_YEARLY",
		"PPPOE_STARTER_YEARLY", "PPPOE_PROFESSIONAL_YEARLY", "PPPOE_ENTERPRISE_YEARLY",
	}
	for _, key := range retired {
		id := ispBillingPlanID(key)
		if _, err := tx.SubscriptionPlan.Get(ctx, id); err != nil {
			if ent.IsNotFound(err) {
				continue
			}
			return fmt.Errorf("lookup retired isp_billing plan %s: %w", key, err)
		}
		if _, err := tx.SubscriptionPlan.UpdateOneID(id).
			SetIsActive(false).SetIsPublic(false).SetUpdatedAt(now).Save(ctx); err != nil {
			return fmt.Errorf("retire isp_billing plan %s: %w", key, err)
		}
		log.Printf("  isp_billing plan retired (inactive): %s", key)
	}
	return nil
}
