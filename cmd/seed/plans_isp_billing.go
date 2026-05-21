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
// that run hotspot or PPPoE networks and use the platform to manage customers,
// routers, billing, vouchers, and analytics.
//
// Two product lines mirror the internal PlatformSubscriptionTier model:
//   - HOTSPOT: captive-portal billing for cafés, hotels, public Wi-Fi
//   - PPPOE:   broadband ISP billing for PPPoE subscriber management
//
// Payments will be centralised through treasury-api once migration is complete.

func seedISPBillingPlans(ctx context.Context, tx *ent.Tx) error {
	now := time.Now()
	serviceTag := "isp_billing"

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
		// ── Hotspot Monthly Plans ────────────────────────────────────────────
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("isp_billing:HOTSPOT_STARTER")),
			planCode:     "ISP_HOTSPOT_STARTER",
			name:         "ISP Hotspot Starter",
			description:  "Manage 1 hotspot router with up to 50 active customers. Includes voucher system, captive portal, and SMS notifications.",
			billingCycle: "MONTHLY",
			price:        500.0,
			tierOrder:    1,
			tierLimits: map[string]any{
				"max_routers":           1,
				"max_customers":         50,
				"max_staff_users":       2,
				"max_sms_per_month":     100,
				"max_vouchers_per_month": 200,
			},
			features: []string{
				"hotspot_management", "captive_portal", "voucher_system",
				"customer_management", "bandwidth_limiting", "sms_notifications",
				"basic_analytics",
			},
		},
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("isp_billing:HOTSPOT_PROFESSIONAL")),
			planCode:     "ISP_HOTSPOT_PROFESSIONAL",
			name:         "ISP Hotspot Professional",
			description:  "Manage up to 5 routers with unlimited customers. Adds advanced analytics, custom domain, and M-Pesa integration.",
			billingCycle: "MONTHLY",
			price:        2000.0,
			tierOrder:    2,
			tierLimits: map[string]any{
				"max_routers":           5,
				"max_customers":         -1,
				"max_staff_users":       5,
				"max_sms_per_month":     500,
				"max_vouchers_per_month": -1,
			},
			features: []string{
				"hotspot_management", "captive_portal", "voucher_system",
				"customer_management", "bandwidth_limiting", "sms_notifications",
				"advanced_analytics", "custom_domain", "mpesa_integration",
				"multi_router", "performance_reports", "expense_tracking",
			},
		},
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("isp_billing:HOTSPOT_ENTERPRISE")),
			planCode:     "ISP_HOTSPOT_ENTERPRISE",
			name:         "ISP Hotspot Enterprise",
			description:  "Unlimited routers and customers. Full white-label, API access, custom integrations, and priority support.",
			billingCycle: "MONTHLY",
			price:        5000.0,
			tierOrder:    3,
			tierLimits: map[string]any{
				"max_routers":           -1,
				"max_customers":         -1,
				"max_staff_users":       -1,
				"max_sms_per_month":     -1,
				"max_vouchers_per_month": -1,
			},
			features: []string{
				"hotspot_management", "captive_portal", "voucher_system",
				"customer_management", "bandwidth_limiting", "sms_notifications",
				"advanced_analytics", "custom_domain", "mpesa_integration",
				"multi_router", "performance_reports", "expense_tracking",
				"white_label", "api_access", "custom_integrations",
				"priority_support", "audit_trail",
			},
		},

		// ── PPPoE Monthly Plans ──────────────────────────────────────────────
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("isp_billing:PPPOE_STARTER")),
			planCode:     "ISP_PPPOE_STARTER",
			name:         "ISP PPPoE Starter",
			description:  "Manage up to 50 PPPoE subscribers across 1 router. Includes RADIUS authentication, billing, and SMS alerts.",
			billingCycle: "MONTHLY",
			price:        1000.0,
			tierOrder:    4,
			tierLimits: map[string]any{
				"max_routers":       1,
				"max_customers":     50,
				"max_staff_users":   2,
				"max_sms_per_month": 100,
			},
			features: []string{
				"pppoe_management", "radius_auth", "customer_management",
				"bandwidth_limiting", "sms_notifications", "basic_analytics",
				"invoice_generation",
			},
		},
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("isp_billing:PPPOE_PROFESSIONAL")),
			planCode:     "ISP_PPPOE_PROFESSIONAL",
			name:         "ISP PPPoE Professional",
			description:  "Up to 250 PPPoE subscribers across 5 routers. Adds FUP management, advanced analytics, and M-Pesa integration.",
			billingCycle: "MONTHLY",
			price:        4000.0,
			tierOrder:    5,
			tierLimits: map[string]any{
				"max_routers":       5,
				"max_customers":     250,
				"max_staff_users":   5,
				"max_sms_per_month": 500,
			},
			features: []string{
				"pppoe_management", "radius_auth", "customer_management",
				"bandwidth_limiting", "sms_notifications", "advanced_analytics",
				"invoice_generation", "fup_management", "mpesa_integration",
				"multi_router", "performance_reports", "expense_tracking",
			},
		},
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("isp_billing:PPPOE_ENTERPRISE")),
			planCode:     "ISP_PPPOE_ENTERPRISE",
			name:         "ISP PPPoE Enterprise",
			description:  "Unlimited PPPoE subscribers and routers. Full white-label, API access, custom integrations, and priority support.",
			billingCycle: "MONTHLY",
			price:        9000.0,
			tierOrder:    6,
			tierLimits: map[string]any{
				"max_routers":       -1,
				"max_customers":     -1,
				"max_staff_users":   -1,
				"max_sms_per_month": -1,
			},
			features: []string{
				"pppoe_management", "radius_auth", "customer_management",
				"bandwidth_limiting", "sms_notifications", "advanced_analytics",
				"invoice_generation", "fup_management", "mpesa_integration",
				"multi_router", "performance_reports", "expense_tracking",
				"white_label", "api_access", "custom_integrations",
				"priority_support", "audit_trail",
			},
		},

		// ── Annual Plans (10-month pricing ≈ 16.7% discount) ────────────────
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("isp_billing:HOTSPOT_STARTER_YEARLY")),
			planCode:     "ISP_HOTSPOT_STARTER_YEARLY",
			name:         "ISP Hotspot Starter — Annual",
			description:  "1 router, up to 50 customers. Vouchers and captive portal. Save with annual billing.",
			billingCycle: "ANNUAL",
			price:        5000.0,
			tierOrder:    1,
			tierLimits: map[string]any{
				"max_routers":           1,
				"max_customers":         50,
				"max_staff_users":       2,
				"max_sms_per_month":     100,
				"max_vouchers_per_month": 200,
			},
			features: []string{
				"hotspot_management", "captive_portal", "voucher_system",
				"customer_management", "bandwidth_limiting", "sms_notifications",
				"basic_analytics",
			},
		},
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("isp_billing:HOTSPOT_PROFESSIONAL_YEARLY")),
			planCode:     "ISP_HOTSPOT_PROFESSIONAL_YEARLY",
			name:         "ISP Hotspot Professional — Annual",
			description:  "5 routers, unlimited customers, custom domain, M-Pesa. Save with annual billing.",
			billingCycle: "ANNUAL",
			price:        20000.0,
			tierOrder:    2,
			tierLimits: map[string]any{
				"max_routers":           5,
				"max_customers":         -1,
				"max_staff_users":       5,
				"max_sms_per_month":     500,
				"max_vouchers_per_month": -1,
			},
			features: []string{
				"hotspot_management", "captive_portal", "voucher_system",
				"customer_management", "bandwidth_limiting", "sms_notifications",
				"advanced_analytics", "custom_domain", "mpesa_integration",
				"multi_router", "performance_reports", "expense_tracking",
			},
		},
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("isp_billing:HOTSPOT_ENTERPRISE_YEARLY")),
			planCode:     "ISP_HOTSPOT_ENTERPRISE_YEARLY",
			name:         "ISP Hotspot Enterprise — Annual",
			description:  "Unlimited routers, white-label, API access, priority support. Save with annual billing.",
			billingCycle: "ANNUAL",
			price:        50000.0,
			tierOrder:    3,
			tierLimits: map[string]any{
				"max_routers":           -1,
				"max_customers":         -1,
				"max_staff_users":       -1,
				"max_sms_per_month":     -1,
				"max_vouchers_per_month": -1,
			},
			features: []string{
				"hotspot_management", "captive_portal", "voucher_system",
				"customer_management", "bandwidth_limiting", "sms_notifications",
				"advanced_analytics", "custom_domain", "mpesa_integration",
				"multi_router", "performance_reports", "expense_tracking",
				"white_label", "api_access", "custom_integrations",
				"priority_support", "audit_trail",
			},
		},
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("isp_billing:PPPOE_STARTER_YEARLY")),
			planCode:     "ISP_PPPOE_STARTER_YEARLY",
			name:         "ISP PPPoE Starter — Annual",
			description:  "1 router, 50 PPPoE subscribers, RADIUS auth, SMS alerts. Save with annual billing.",
			billingCycle: "ANNUAL",
			price:        10000.0,
			tierOrder:    4,
			tierLimits: map[string]any{
				"max_routers":       1,
				"max_customers":     50,
				"max_staff_users":   2,
				"max_sms_per_month": 100,
			},
			features: []string{
				"pppoe_management", "radius_auth", "customer_management",
				"bandwidth_limiting", "sms_notifications", "basic_analytics",
				"invoice_generation",
			},
		},
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("isp_billing:PPPOE_PROFESSIONAL_YEARLY")),
			planCode:     "ISP_PPPOE_PROFESSIONAL_YEARLY",
			name:         "ISP PPPoE Professional — Annual",
			description:  "5 routers, 250 subscribers, FUP management, M-Pesa. Save with annual billing.",
			billingCycle: "ANNUAL",
			price:        40000.0,
			tierOrder:    5,
			tierLimits: map[string]any{
				"max_routers":       5,
				"max_customers":     250,
				"max_staff_users":   5,
				"max_sms_per_month": 500,
			},
			features: []string{
				"pppoe_management", "radius_auth", "customer_management",
				"bandwidth_limiting", "sms_notifications", "advanced_analytics",
				"invoice_generation", "fup_management", "mpesa_integration",
				"multi_router", "performance_reports", "expense_tracking",
			},
		},
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("isp_billing:PPPOE_ENTERPRISE_YEARLY")),
			planCode:     "ISP_PPPOE_ENTERPRISE_YEARLY",
			name:         "ISP PPPoE Enterprise — Annual",
			description:  "Unlimited subscribers, white-label, API access, priority support. Save with annual billing.",
			billingCycle: "ANNUAL",
			price:        90000.0,
			tierOrder:    6,
			tierLimits: map[string]any{
				"max_routers":       -1,
				"max_customers":     -1,
				"max_staff_users":   -1,
				"max_sms_per_month": -1,
			},
			features: []string{
				"pppoe_management", "radius_auth", "customer_management",
				"bandwidth_limiting", "sms_notifications", "advanced_analytics",
				"invoice_generation", "fup_management", "mpesa_integration",
				"multi_router", "performance_reports", "expense_tracking",
				"white_label", "api_access", "custom_integrations",
				"priority_support", "audit_trail",
			},
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
			return fmt.Errorf("upsert isp_billing plan %s: %w", p.planCode, err)
		}
		if err := seedPlanFeatures(ctx, tx, p.id, p.features); err != nil {
			return fmt.Errorf("seed features for isp_billing plan %s: %w", p.planCode, err)
		}
		log.Printf("  isp_billing plan: %s (%s, KES %.0f)", p.name, p.billingCycle, p.price)
	}
	return nil
}
