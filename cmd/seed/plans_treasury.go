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

// ── Treasury & Finance Plans ─────────────────────────────────────────────────
// Plans for the Treasury service — multi-currency wallet management, payment
// collection (Paystack, M-Pesa), escrow, bulk payouts, and financial reporting.
// Sold as a standalone service to tenants managing payment flows independently
// of the core ordering or ERP modules.

func seedTreasuryPlans(ctx context.Context, tx *ent.Tx) error {
	now := time.Now()
	serviceTag := "treasury"

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
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("treasury:STARTER")),
			planCode:     "TREASURY_STARTER",
			name:         "Treasury Starter",
			description:  "Collect payments via M-Pesa and Paystack with basic wallet management. Up to 10,000 transactions/month.",
			billingCycle: "MONTHLY",
			price:        1000.0,
			tierOrder:    1,
			tierLimits: map[string]any{
				"max_wallets":                3,
				"max_transactions_per_month": 10000,
				"max_payment_links":          20,
				"max_users":                  5,
				"max_currencies":             1,
			},
			// quotations/credit_notes/vouchers/treasury_approvals became REAL backend/UI
			// gates 2026-07-16 — standalone treasury tiers must grant what their tenants
			// already use (invoice_generation historically covered quotations too).
			features: []string{
				"wallet_management", "payment_collection",
				"paystack_integration", "payment_links", "transaction_reports",
				"invoice_generation", "basic_reconciliation", "customer_management",
				"basic_analytics", "quotations",
			},
		},
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("treasury:GROWTH")),
			planCode:     "TREASURY_GROWTH",
			name:         "Treasury Growth",
			description:  "Multi-currency wallets, bulk payouts, escrow management, and advanced financial reports. Up to 25,000 transactions/month.",
			billingCycle: "MONTHLY",
			price:        3500.0,
			tierOrder:    2,
			tierLimits: map[string]any{
				"max_wallets":                8,
				"max_transactions_per_month": 25000,
				"max_payment_links":          -1,
				"max_users":                  15,
				"max_currencies":             3,
				"max_bulk_payout_rows":       500,
			},
			features: []string{
				"wallet_management", "payment_collection", "mpesa_integration",
				"paystack_integration", "payment_links", "transaction_reports",
				"invoice_generation", "customer_management", "vendor_management",
				"ar_tracking", "ap_tracking", "ledger_posting", "tax_codes",
				"basic_reconciliation", "etims_integration",
				"advanced_analytics", "multi_currency", "bulk_payouts",
				"escrow_management", "payout_schedules", "reconciliation",
				"quotations", "credit_notes", "treasury_approvals", "smart_tax_compliance",
			},
		},
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("treasury:PROFESSIONAL")),
			planCode:     "TREASURY_PROFESSIONAL",
			name:         "Treasury Professional",
			description:  "Unlimited transactions and currencies. Full API access, webhooks, custom integrations, and priority support.",
			billingCycle: "MONTHLY",
			price:        8000.0,
			tierOrder:    3,
			tierLimits: map[string]any{
				"max_wallets":                -1,
				"max_transactions_per_month": -1,
				"max_payment_links":          -1,
				"max_users":                  -1,
				"max_currencies":             -1,
				"max_bulk_payout_rows":       -1,
			},
			features: []string{
				"wallet_management", "payment_collection", "mpesa_integration",
				"paystack_integration", "payment_links", "transaction_reports",
				"invoice_generation", "customer_management", "vendor_management",
				"ar_tracking", "ap_tracking", "ledger_posting", "tax_codes",
				"basic_reconciliation", "etims_integration",
				"advanced_analytics", "multi_currency", "bulk_payouts",
				"escrow_management", "payout_schedules", "reconciliation",
				"api_access", "webhooks", "custom_integrations",
				"audit_trail", "equity_payouts", "priority_support",
				"quotations", "credit_notes", "vouchers", "treasury_approvals", "smart_tax_compliance",
			},
		},
	}

	for _, p := range plans {
		existing, err := tx.SubscriptionPlan.Get(ctx, p.id)
		if err != nil && !ent.IsNotFound(err) {
			return fmt.Errorf("lookup treasury plan %s: %w", p.planCode, err)
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
			return fmt.Errorf("upsert treasury plan %s: %w", p.planCode, err)
		}
		if err := seedPlanFeatures(ctx, tx, p.id, p.features); err != nil {
			return fmt.Errorf("seed features for treasury plan %s: %w", p.planCode, err)
		}
		log.Printf("  treasury plan: %s (%s, KES %.0f)", p.name, p.billingCycle, p.price)
	}
	return nil
}
