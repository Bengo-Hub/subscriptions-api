package main

import (
	"context"
	"fmt"
	"log"

	"github.com/google/uuid"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/bundle"
	"github.com/bengobox/subscription-service/internal/ent/schema"
)

// ── Bundles ──────────────────────────────────────────────────────────────────
// Curated product combinations with tiered pricing.
// The "delivery" bundle covers ordering + logistics + treasury + storefront tiers.

func seedBundles(ctx context.Context, tx *ent.Tx) error {
	type bundleDef struct {
		id          uuid.UUID
		code        string
		name        string
		description string
		products    []schema.BundleProduct
		tiers       []schema.BundleTier
		isDefault   bool
		sortOrder   int
	}

	bundles := []bundleDef{
		{
			id:   uuid.MustParse("20000000-0000-0000-0000-000000000001"),
			code: "ordering",
			name: "BengoBox Ordering",
			description: "Complete food delivery platform: online ordering, logistics, payments, and storefront. " +
				"Pricing matches the Urban Cafe Food Delivery System Inception Report tiers.",
			products: []schema.BundleProduct{
				{ProductCode: "ordering"},
				{ProductCode: "logistics"},
				{ProductCode: "treasury"},
				{ProductCode: "storefront"},
			},
			tiers: []schema.BundleTier{
				{PlanCode: "ORDERING_STARTER", MonthlyPrice: 2500, YearlyPrice: 27500},
				{PlanCode: "ORDERING_GROWTH", MonthlyPrice: 6000, YearlyPrice: 66000},
				{PlanCode: "ORDERING_PROFESSIONAL", MonthlyPrice: 12500, YearlyPrice: 137500},
			},
			isDefault: true,
			sortOrder: 1,
		},
		{
			id:          uuid.MustParse("20000000-0000-0000-0000-000000000002"),
			code:        "pos-suite",
			name:        "BengoBox POS",
			description: "In-store point of sale with payment processing. Ideal for walk-in restaurants and cafes.",
			products: []schema.BundleProduct{
				{ProductCode: "pos"},
				{ProductCode: "treasury"},
			},
			tiers: []schema.BundleTier{
				{PlanCode: "ORDERING_STARTER", MonthlyPrice: 2000, YearlyPrice: 22000},
				{PlanCode: "ORDERING_GROWTH", MonthlyPrice: 4500, YearlyPrice: 49500},
				{PlanCode: "ORDERING_PROFESSIONAL", MonthlyPrice: 8000, YearlyPrice: 88000},
			},
			sortOrder: 2,
		},
		{
			id:          uuid.MustParse("20000000-0000-0000-0000-000000000003"),
			code:        "complete",
			name:        "BengoBox Complete",
			description: "Everything included: online ordering, delivery logistics, POS, payments, and storefront. Best value for full-service operations.",
			products: []schema.BundleProduct{
				{ProductCode: "ordering"},
				{ProductCode: "logistics"},
				{ProductCode: "treasury"},
				{ProductCode: "pos"},
				{ProductCode: "storefront"},
			},
			tiers: []schema.BundleTier{
				{PlanCode: "ORDERING_STARTER", MonthlyPrice: 4000, YearlyPrice: 44000},
				{PlanCode: "ORDERING_GROWTH", MonthlyPrice: 9500, YearlyPrice: 104500},
				{PlanCode: "ORDERING_PROFESSIONAL", MonthlyPrice: 18000, YearlyPrice: 198000},
			},
			sortOrder: 3,
		},
	}

	for _, b := range bundles {
		existing, err := tx.Bundle.Get(ctx, b.id)
		if err != nil && !ent.IsNotFound(err) {
			return fmt.Errorf("lookup bundle %s: %w", b.code, err)
		}

		if existing != nil {
			_, err = tx.Bundle.UpdateOneID(b.id).
				SetCode(b.code).
				SetName(b.name).
				SetDescription(b.description).
				SetProducts(b.products).
				SetTiers(b.tiers).
				SetDiscountType(bundle.DiscountTypeNone).
				SetIsActive(true).
				SetIsDefault(b.isDefault).
				SetSortOrder(b.sortOrder).
				Save(ctx)
			if err != nil {
				return fmt.Errorf("update bundle %s: %w", b.code, err)
			}
		} else {
			_, err = tx.Bundle.Create().
				SetID(b.id).
				SetCode(b.code).
				SetName(b.name).
				SetDescription(b.description).
				SetProducts(b.products).
				SetTiers(b.tiers).
				SetDiscountType(bundle.DiscountTypeNone).
				SetIsActive(true).
				SetIsDefault(b.isDefault).
				SetSortOrder(b.sortOrder).
				Save(ctx)
			if err != nil {
				return fmt.Errorf("create bundle %s: %w", b.code, err)
			}
		}

		log.Printf("  bundle: %s (%s, %d products)", b.name, b.code, len(b.products))
	}

	return nil
}
