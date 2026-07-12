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

// seedBundleOneTimePlans seeds the PowerSuite PERPETUAL (one-time) license tiers.
//
// A one-time license is billing_cycle=ONE_TIME → resolved by the subscription service
// to billing_mode="one_time", is_perpetual=true, non-expiring (never gate-blocked). Each
// tier grants EXACTLY its recurring PowerSuite equivalent's features/limits (via the shared
// powerSuiteFeatures/powerSuiteLimits) so gating is uniform between the monthly plan and the
// license, and each tier is a proper superset of the one below. Tier 3 = full access across
// all covered services (Professional feature set, all operational limits unlimited).
//
// Prices (KES, one-off) + inline setup/installation fees (charged once, on top of the license):
//
//	Tier 1  45,000 + 10,000 setup
//	Tier 2 120,000 + 15,000 setup
//	Tier 3 150,000 + 20,000 setup
//
// setup_fees.go skips ONE_TIME plans, so the fee is set inline here (mirrors the POS product-line
// and Library one-time license pattern).
func seedBundleOneTimePlans(ctx context.Context, tx *ent.Tx) error {
	now := time.Now()

	type planDef struct {
		id          uuid.UUID
		planCode    string
		name        string
		description string
		price       float64
		tierOrder   int
		setupFee    float64
		tierLimits  map[string]any
		features    []string
	}

	plans := []planDef{
		{
			id:          uuid.NewSHA1(uuid.NameSpaceOID, []byte("plan:POWERSUITE_STARTER_ONE_TIME")),
			planCode:    "POWERSUITE_STARTER_ONE_TIME",
			name:        "PowerSuite Starter — Perpetual License",
			description: "One-time purchase of PowerSuite at starter level: online ordering, POS, logistics, inventory, payments, CRM, and storefront for a single outlet. Excludes ongoing cloud hosting and support.",
			price:       45000.0,
			tierOrder:   1,
			setupFee:    10000.0,
			tierLimits:  powerSuiteLimits(1),
			features:    powerSuiteFeatures(1),
		},
		{
			id:          uuid.NewSHA1(uuid.NameSpaceOID, []byte("plan:POWERSUITE_GROWTH_ONE_TIME")),
			planCode:    "POWERSUITE_GROWTH_ONE_TIME",
			name:        "PowerSuite Growth — Perpetual License",
			description: "One-time purchase of PowerSuite at growth level: multi-outlet ordering, advanced analytics, route optimisation, multi-warehouse, multi-currency, CRM automation, ticketing, and HR management.",
			price:       120000.0,
			tierOrder:   2,
			setupFee:    15000.0,
			tierLimits:  powerSuiteLimits(2),
			features:    powerSuiteFeatures(2),
		},
		{
			id:          uuid.NewSHA1(uuid.NameSpaceOID, []byte("plan:POWERSUITE_PROFESSIONAL_ONE_TIME")),
			planCode:    "POWERSUITE_PROFESSIONAL_ONE_TIME",
			name:        "PowerSuite Professional — Perpetual License",
			description: "One-time purchase of PowerSuite at enterprise level — full access across every service: unlimited outlets, hotel module, API access, barcode scanning, audit trail, CRM automation, full ERP, and priority support.",
			price:       150000.0,
			tierOrder:   3,
			setupFee:    20000.0,
			tierLimits:  powerSuiteLimits(3),
			features:    powerSuiteFeatures(3),
		},
	}

	for _, p := range plans {
		existing, err := tx.SubscriptionPlan.Get(ctx, p.id)
		if err != nil && !ent.IsNotFound(err) {
			return fmt.Errorf("lookup one-time plan %s: %w", p.planCode, err)
		}
		if existing != nil {
			_, err = tx.SubscriptionPlan.UpdateOneID(p.id).
				SetPlanCode(p.planCode).
				SetName(p.name).
				SetDescription(p.description).
				SetBillingCycle("ONE_TIME").
				SetPlanType(subscriptionplan.PlanTypeTIERED).
				SetBasePrice(p.price).
				SetSetupFee(p.setupFee).
				SetCurrency("KES").
				SetIsActive(true).
				SetIsPublic(true).
				SetTierOrder(p.tierOrder).
				SetFreeTrialDays(0).
				SetTierLimitsJSON(p.tierLimits).
				SetServiceTag("ordering").
				SetUpdatedAt(now).
				Save(ctx)
		} else {
			_, err = tx.SubscriptionPlan.Create().
				SetID(p.id).
				SetPlanCode(p.planCode).
				SetName(p.name).
				SetDescription(p.description).
				SetBillingCycle("ONE_TIME").
				SetPlanType(subscriptionplan.PlanTypeTIERED).
				SetBasePrice(p.price).
				SetSetupFee(p.setupFee).
				SetCurrency("KES").
				SetIsActive(true).
				SetIsPublic(true).
				SetTierOrder(p.tierOrder).
				SetFreeTrialDays(0).
				SetTierLimitsJSON(p.tierLimits).
				SetServiceTag("ordering").
				SetCreatedAt(now).
				SetUpdatedAt(now).
				Save(ctx)
		}
		if err != nil {
			return fmt.Errorf("upsert one-time plan %s: %w", p.planCode, err)
		}
		if err := seedPlanFeatures(ctx, tx, p.id, p.features); err != nil {
			return fmt.Errorf("seed features for one-time plan %s: %w", p.planCode, err)
		}
		log.Printf("  powersuite license: %s (ONE_TIME, KES %.0f + %.0f setup)", p.name, p.price, p.setupFee)
	}

	return nil
}
