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

// ── Treasury (standalone) Plans ───────────────────────────────────────────────
//
// Treasury on its own, for a tenant that needs payments, invoicing and the ledger but none of
// POS, inventory, ordering or ERP: a gift registry (Hadia) or a property platform that bills
// through treasury (Maskani). SSO and notifications come with every subscription and are never
// sold separately. Prices set 2026-11-06: Starter KES 4,000, Growth KES 8,000, Professional
// KES 15,000 a month. Not tied to a use case (use_case stays empty), so every tenant sees them.
//
// Escrow (holding customers' money for beneficiaries) starts at Growth, which is the plan Hadia's
// MSA price maps to. Hadia's longer try-out is set per tenant (trial_ends_at) when the plan is
// assigned; the plan's own free trial stays 14 days like every other family.

// treasuryPlanFeatures returns the standalone treasury features for a tier (1 to 3).
func treasuryPlanFeatures(tier int) []string {
	// Invoicing, the ledger and collections by M-Pesa and card from the first tier: without them
	// treasury alone is of no use.
	f := unionFeatures(psTreasuryBlock(2, true), []string{"mpesa_integration", "paystack_integration"})
	if tier >= 2 {
		f = unionFeatures(f, psTreasuryBlock(3, true), []string{"bulk_payouts", "payout_schedules"})
	}
	if tier >= 3 {
		f = unionFeatures(f, []string{"multi_currency", "equity_payouts", "api_access", "webhooks", "custom_integrations"})
	}
	return f
}

// treasuryPlanLimits: -1 is unlimited. Growth and Professional do not cap wallets or payment
// links, since an escrow tenant opens one wallet per beneficiary (one per registry on Hadia).
func treasuryPlanLimits(tier int) map[string]any {
	switch tier {
	case 1:
		return map[string]any{"max_users": 3, "max_wallets": 3, "max_payment_links": 50, "max_currencies": 1,
			"max_bulk_payout_rows": 0, "max_transactions_per_month": 2000}
	case 2:
		return map[string]any{"max_users": 10, "max_wallets": -1, "max_payment_links": -1, "max_currencies": 1,
			"max_bulk_payout_rows": 1000, "max_transactions_per_month": -1}
	default:
		return map[string]any{"max_users": 25, "max_wallets": -1, "max_payment_links": -1, "max_currencies": 5,
			"max_bulk_payout_rows": -1, "max_transactions_per_month": -1}
	}
}

type treasuryTier struct {
	code    string
	label   string
	tier    int
	monthly float64
	desc    string
}

var treasuryTiers = []treasuryTier{
	{code: "TREASURY_STARTER", label: "Treasury Starter", tier: 1, monthly: 4000,
		desc: "Payments and accounting on their own: M-Pesa and card collection, payment links, invoices and quotations, customer and vendor ledgers, tax codes and KRA eTIMS, for up to 3 users."},
	{code: "TREASURY_GROWTH", label: "Treasury Growth", tier: 2, monthly: 8000,
		desc: "Adds escrow wallets, bulk and scheduled payouts, payment and journal vouchers, full bank reconciliation, approvals, audit trail, cash forecasting and BI reports, for up to 10 users."},
	{code: "TREASURY_PROFESSIONAL", label: "Treasury Professional", tier: 3, monthly: 15000,
		desc: "Adds multiple currencies, equity and royalty payouts, API access, webhooks and custom integrations, for up to 25 users."},
}

// seedTreasuryPlans upserts the three monthly standalone treasury plans.
func seedTreasuryPlans(ctx context.Context, tx *ent.Tx) error {
	now := time.Now()
	for _, t := range treasuryTiers {
		id := uuid.NewSHA1(uuid.NameSpaceOID, []byte("plan:"+t.code))
		feats := treasuryPlanFeatures(t.tier)
		limits := treasuryPlanLimits(t.tier)
		meta := map[string]any{"product": "treasury"}

		existing, err := tx.SubscriptionPlan.Get(ctx, id)
		if err != nil && !ent.IsNotFound(err) {
			return fmt.Errorf("lookup treasury plan %s: %w", t.code, err)
		}
		if existing != nil {
			_, err = tx.SubscriptionPlan.UpdateOneID(id).
				SetPlanCode(t.code).SetName(t.label).SetDescription(t.desc).
				SetBillingCycle("MONTHLY").SetPlanType(subscriptionplan.PlanTypeTIERED).
				SetBasePrice(t.monthly).SetSetupFee(0).SetCurrency("KES").
				SetIsActive(true).SetIsPublic(true).
				SetTierOrder(t.tier).SetFreeTrialDays(14).ClearUseCase().
				SetTierLimitsJSON(limits).SetServiceTag("treasury").SetMetadata(meta).SetUpdatedAt(now).Save(ctx)
		} else {
			_, err = tx.SubscriptionPlan.Create().
				SetID(id).SetPlanCode(t.code).SetName(t.label).SetDescription(t.desc).
				SetBillingCycle("MONTHLY").SetPlanType(subscriptionplan.PlanTypeTIERED).
				SetBasePrice(t.monthly).SetSetupFee(0).SetCurrency("KES").
				SetIsActive(true).SetIsPublic(true).
				SetTierOrder(t.tier).SetFreeTrialDays(14).
				SetTierLimitsJSON(limits).SetServiceTag("treasury").SetMetadata(meta).
				SetCreatedAt(now).SetUpdatedAt(now).Save(ctx)
		}
		if err != nil {
			return fmt.Errorf("upsert treasury plan %s: %w", t.code, err)
		}
		if err := seedPlanFeatures(ctx, tx, id, feats); err != nil {
			return fmt.Errorf("seed features for %s: %w", t.code, err)
		}
		log.Printf("  treasury plan: %s (MONTHLY, KES %.0f, %d features)", t.label, t.monthly, len(feats))
	}
	return nil
}
