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

// ── Maskani (property platform) Plans ─────────────────────────────────────────
//
// Source: Maskani SRDD 25.3 (private docs repo, shaba-village-proposal). Four monthly tiers
// measured by units under management. Each tier includes the Maskani licence plus the ERP and
// treasury subscription at the same tier, so features are the union of the maskani module
// switches, erpFeatures(tier) and a treasury block; limits are erpLimits(tier) (employees and
// staff users match the SRDD table exactly) plus max_units. Owners, occupants, guards and vendor
// supervisors are portal users and never count as staff users.
//
// Tier differences come from the reused ERP set: Growth adds budgeting, approval workflows, asset
// management and BI reports; Professional adds API access, custom workflows, audit trail and
// priority support. Enterprise is unlimited and quoted. There is no plan setup fee: implementation
// is quoted per project (setup_fees.go has no maskani entry). Later modules (leasing, commercial,
// portfolios) come with every tier at no extra charge, marketplace listings from Growth; maskani-api
// still hides modules that are not released yet.

// maskaniCoreModules are the release 1 modules, in every tier.
func maskaniCoreModules() []string {
	return []string{
		"maskani_properties", "maskani_billing", "maskani_utilities", "maskani_sales", "maskani_estate",
		"maskani_maintenance", "maskani_providers", "maskani_gate", "maskani_staff", "maskani_communication",
		"maskani_leasing", "maskani_commercial", "maskani_portfolios",
	}
}

// maskaniTreasuryBlock: bills are treasury invoices from the first tier, collected by M-Pesa
// (paybill and STK) and card, so invoicing and the ledger are never gated below Growth here.
func maskaniTreasuryBlock(tier int) []string {
	base := unionFeatures(psTreasuryBlock(2, true), []string{"mpesa_integration", "paystack_integration"})
	if tier >= 2 {
		base = unionFeatures(base, psTreasuryBlock(3, true))
	}
	return base
}

func maskaniTierFeatures(tier int) []string {
	f := unionFeatures(maskaniCoreModules(), erpFeatures(tier), maskaniTreasuryBlock(tier))
	if tier >= 2 {
		f = append(f, "maskani_marketplace")
	}
	return unionFeatures(f)
}

func maskaniTierLimits(tier int) map[string]any {
	units := map[int]int{1: 150, 2: 400, 3: 1000}
	maxUnits, ok := units[tier]
	if !ok {
		maxUnits = -1
	}
	return mergeLimits(erpLimits(tier), map[string]any{"max_units": maxUnits})
}

type maskaniTier struct {
	code    string
	label   string
	tier    int
	monthly float64 // 0 with custom_quote for Enterprise
	desc    string
}

var maskaniTiers = []maskaniTier{
	{code: "MASKANI_STARTER", label: "Maskani Starter", tier: 1, monthly: 10000,
		desc: "Property management for up to 150 units: register, billing and collections, sales and instalments, owner portal, gate, vendors, work orders and notices, with ERP payroll and treasury included."},
	{code: "MASKANI_GROWTH", label: "Maskani Growth", tier: 2, monthly: 20000,
		desc: "Up to 400 units with service charge budget against actual, multi-level approvals, asset register and BI reports. Recommended for most estates."},
	{code: "MASKANI_PROFESSIONAL", label: "Maskani Professional", tier: 3, monthly: 35000,
		desc: "Up to 1,000 units across several properties or phases, with API access, custom workflows, extended audit trail and priority support."},
	{code: "MASKANI_ENTERPRISE", label: "Maskani Enterprise", tier: 4, monthly: 0,
		desc: "Unlimited units, staff and properties, scoped and priced to your organisation. Contact sales for a quote."},
}

// seedMaskaniPlans upserts the four monthly Maskani plans.
func seedMaskaniPlans(ctx context.Context, tx *ent.Tx) error {
	now := time.Now()
	for _, t := range maskaniTiers {
		id := uuid.NewSHA1(uuid.NameSpaceOID, []byte("plan:"+t.code))
		feats := maskaniTierFeatures(t.tier)
		limits := maskaniTierLimits(t.tier)
		trial := 14
		meta := map[string]any{"product": "maskani"}
		if t.monthly == 0 {
			trial = 0
			meta["custom_quote"] = true
		}

		existing, err := tx.SubscriptionPlan.Get(ctx, id)
		if err != nil && !ent.IsNotFound(err) {
			return fmt.Errorf("lookup maskani plan %s: %w", t.code, err)
		}
		if existing != nil {
			_, err = tx.SubscriptionPlan.UpdateOneID(id).
				SetPlanCode(t.code).SetName(t.label).SetDescription(t.desc).
				SetBillingCycle("MONTHLY").SetPlanType(subscriptionplan.PlanTypeTIERED).
				SetBasePrice(t.monthly).SetSetupFee(0).SetCurrency("KES").
				SetIsActive(true).SetIsPublic(true).
				SetTierOrder(t.tier).SetFreeTrialDays(trial).
				SetTierLimitsJSON(limits).SetServiceTag("maskani").SetMetadata(meta).SetUpdatedAt(now).Save(ctx)
		} else {
			_, err = tx.SubscriptionPlan.Create().
				SetID(id).SetPlanCode(t.code).SetName(t.label).SetDescription(t.desc).
				SetBillingCycle("MONTHLY").SetPlanType(subscriptionplan.PlanTypeTIERED).
				SetBasePrice(t.monthly).SetSetupFee(0).SetCurrency("KES").
				SetIsActive(true).SetIsPublic(true).
				SetTierOrder(t.tier).SetFreeTrialDays(trial).
				SetTierLimitsJSON(limits).SetServiceTag("maskani").SetMetadata(meta).
				SetCreatedAt(now).SetUpdatedAt(now).Save(ctx)
		}
		if err != nil {
			return fmt.Errorf("upsert maskani plan %s: %w", t.code, err)
		}
		if err := seedPlanFeatures(ctx, tx, id, feats); err != nil {
			return fmt.Errorf("seed features for %s: %w", t.code, err)
		}
		log.Printf("  maskani plan: %s (MONTHLY, KES %.0f, %d features)", t.label, t.monthly, len(feats))
	}
	return nil
}
