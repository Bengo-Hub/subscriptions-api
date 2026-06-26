package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/subscriptionplan"
	"github.com/bengobox/subscription-service/internal/ent/tenantsubscription"
)

// ── ISP Billing Plan ─────────────────────────────────────────────────────────
// ONE pay-as-you-grow plan for ISP providers (covers hotspot + PPPoE), modeled on
// Centipid (centipidbilling.com) + the platform owner's pricing:
//
//	Monthly charge =
//	    500                                              (base, ALWAYS)
//	  + (monthly_hotspot_sales > 10,000 ? 3% × monthly_hotspot_sales : 0)
//	  + active_pppoe_subscribers × 35                    (ALWAYS, threshold-independent)
//
//	e.g. 12,000 hotspot sales → 500 + 3%×12,000 = KES 860 (+ PPPoE subscriber fees).
//
// NO feature/capacity limits — unlimited routers, users, customers, vouchers. The
// billing engine reads service_charge_percentage / service_charge_threshold /
// pppoe_per_subscriber_fee from tier_limits to assemble each monthly invoice.
// SMS/WhatsApp are prepaid credit bundles in notifications-api, not plan limits.
//
// All previously-seeded ISP plans (the hotspot/pppoe split + the short-lived flat
// tiers) are retired here and any active tenant subscriptions migrated onto this plan.

func ispBillingPlanID(key string) uuid.UUID {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte("isp_billing:"+key))
}

func seedISPBillingPlans(ctx context.Context, tx *ent.Tx) error {
	now := time.Now()
	serviceTag := "isp_billing"

	// All capabilities available (no tier gating).
	features := []string{
		"hotspot_management", "pppoe_management", "captive_portal", "radius_auth",
		"voucher_system", "bandwidth_limiting", "fup_management", "customer_management",
		"multi_router", "remote_winbox", "mpesa_integration", "invoice_generation",
		"realtime_notifications", "basic_analytics", "advanced_analytics",
		"performance_reports", "expense_tracking", "custom_domain", "white_label",
		"api_access", "custom_integrations", "priority_support", "audit_trail",
	}

	// No feature/capacity limits — only the usage-billing params.
	billingParams := map[string]any{
		"service_charge_percentage": 3,
		"service_charge_threshold":  10000,
		"pppoe_per_subscriber_fee":  35,
	}

	planID := ispBillingPlanID("STARTER")
	const (
		planCode = "ISP_BILLING_STARTER"
		planName = "ISP Billing"
		planDesc = "Pay-as-you-grow for ISP providers (hotspot + PPPoE). KES 500/month base; " +
			"+3% of hotspot revenue once monthly sales exceed KES 10,000; +KES 35 per active " +
			"PPPoE subscriber/month (always). Unlimited routers, users, customers & vouchers. " +
			"14-day free trial. SMS/WhatsApp via prepaid credits."
	)

	existing, err := tx.SubscriptionPlan.Get(ctx, planID)
	if err != nil && !ent.IsNotFound(err) {
		return fmt.Errorf("lookup isp_billing plan: %w", err)
	}
	if existing != nil {
		_, err = tx.SubscriptionPlan.UpdateOneID(planID).
			SetPlanCode(planCode).SetName(planName).SetDescription(planDesc).
			SetBillingCycle("MONTHLY").SetPlanType(subscriptionplan.PlanTypeSTANDALONE_SERVICE).
			SetBasePrice(500).SetCurrency("KES").SetIsActive(true).SetIsPublic(true).
			SetTierOrder(1).SetTierLimitsJSON(billingParams).SetServiceTag(serviceTag).SetFreeTrialDays(14).SetUpdatedAt(now).Save(ctx)
	} else {
		_, err = tx.SubscriptionPlan.Create().
			SetID(planID).SetPlanCode(planCode).SetName(planName).SetDescription(planDesc).
			SetBillingCycle("MONTHLY").SetPlanType(subscriptionplan.PlanTypeSTANDALONE_SERVICE).
			SetBasePrice(500).SetCurrency("KES").SetIsActive(true).SetIsPublic(true).
			SetTierOrder(1).SetTierLimitsJSON(billingParams).SetServiceTag(serviceTag).SetFreeTrialDays(14).
			SetCreatedAt(now).SetUpdatedAt(now).Save(ctx)
	}
	if err != nil {
		return fmt.Errorf("upsert isp_billing plan: %w", err)
	}
	if err := seedPlanFeatures(ctx, tx, planID, features); err != nil {
		return fmt.Errorf("seed features for isp_billing plan: %w", err)
	}
	log.Printf("  isp_billing plan: %s (MONTHLY, KES 500 base + 3%%>10k + KES 35/pppoe)", planName)

	// Retire every previous ISP plan (hotspot/pppoe split + the short-lived flat tiers).
	retiredKeys := []string{
		"PROFESSIONAL", "ENTERPRISE",
		"STARTER_YEARLY", "PROFESSIONAL_YEARLY", "ENTERPRISE_YEARLY",
		"HOTSPOT_STARTER", "HOTSPOT_PROFESSIONAL", "HOTSPOT_ENTERPRISE",
		"PPPOE_STARTER", "PPPOE_PROFESSIONAL", "PPPOE_ENTERPRISE",
		"HOTSPOT_STARTER_YEARLY", "HOTSPOT_PROFESSIONAL_YEARLY", "HOTSPOT_ENTERPRISE_YEARLY",
		"PPPOE_STARTER_YEARLY", "PPPOE_PROFESSIONAL_YEARLY", "PPPOE_ENTERPRISE_YEARLY",
	}
	var retiredIDs []uuid.UUID
	for _, k := range retiredKeys {
		id := ispBillingPlanID(k)
		if _, err := tx.SubscriptionPlan.Get(ctx, id); err != nil {
			if ent.IsNotFound(err) {
				continue
			}
			return fmt.Errorf("lookup retired isp_billing plan %s: %w", k, err)
		}
		retiredIDs = append(retiredIDs, id)
		if _, err := tx.SubscriptionPlan.UpdateOneID(id).
			SetIsActive(false).SetIsPublic(false).SetUpdatedAt(now).Save(ctx); err != nil {
			return fmt.Errorf("retire isp_billing plan %s: %w", k, err)
		}
	}

	// Migrate any active tenant subscriptions still on a retired ISP plan onto the
	// single new plan (one subscription per tenant — the unique index is preserved).
	if len(retiredIDs) > 0 {
		migrated, err := tx.TenantSubscription.Update().
			Where(tenantsubscription.PlanIDIn(retiredIDs...)).
			SetPlanID(planID).SetUpdatedAt(now).Save(ctx)
		if err != nil {
			return fmt.Errorf("migrate tenant subscriptions to new isp_billing plan: %w", err)
		}
		if migrated > 0 {
			log.Printf("  isp_billing: migrated %d tenant subscription(s) onto %s; retired %d old plan(s)", migrated, planCode, len(retiredIDs))
		}
	}
	return nil
}
