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
			name: "Codevertex Ordering",
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
		// The former "pos-suite" (id …0002) and generic "powersuite" (id …0003) bundles were
		// SUPERSEDED by the three per-use-case PowerSuite bundles below (same stable ids reused
		// so the upsert reworks the rows in place; docs/subscription-plans/). Yearly price is
		// 12× monthly — the billing period is a per-subscription choice with a ≥6-month
		// setup-fee waiver, never a discounted separate plan row.
		{
			id:          uuid.MustParse("20000000-0000-0000-0000-000000000002"),
			code:        "powersuite-hospitality",
			name:        "PowerSuite Hospitality",
			description: "Hotels, restaurants, bars & cafes: POS + KDS + tables, inventory & recipes, treasury, online ordering, logistics, CRM and ERP in one suite.",
			products: []schema.BundleProduct{
				{ProductCode: "pos"},
				{ProductCode: "inventory"},
				{ProductCode: "treasury"},
				{ProductCode: "ordering"},
				{ProductCode: "logistics"},
				{ProductCode: "marketflow"},
				{ProductCode: "erp"},
			},
			tiers: []schema.BundleTier{
				{PlanCode: "POWERSUITE_HOSP_BASIC", MonthlyPrice: 2500, YearlyPrice: 30000},
				{PlanCode: "POWERSUITE_HOSP_PRO", MonthlyPrice: 4000, YearlyPrice: 48000},
				{PlanCode: "POWERSUITE_HOSP_GOLD", MonthlyPrice: 6500, YearlyPrice: 78000},
			},
			sortOrder: 2,
		},
		{
			id:          uuid.MustParse("20000000-0000-0000-0000-000000000003"),
			code:        "powersuite-retail",
			name:        "PowerSuite Retail (Duka)",
			description: "Shops, supermarkets, hardware & boutiques: barcode POS, inventory & procurement, warranties, treasury, online ordering, logistics, CRM and ERP.",
			products: []schema.BundleProduct{
				{ProductCode: "pos"},
				{ProductCode: "inventory"},
				{ProductCode: "treasury"},
				{ProductCode: "ordering"},
				{ProductCode: "logistics"},
				{ProductCode: "marketflow"},
				{ProductCode: "erp"},
			},
			tiers: []schema.BundleTier{
				{PlanCode: "POWERSUITE_DUKA_BASIC", MonthlyPrice: 2500, YearlyPrice: 30000},
				{PlanCode: "POWERSUITE_DUKA_PRO", MonthlyPrice: 4500, YearlyPrice: 54000},
				{PlanCode: "POWERSUITE_DUKA_GOLD", MonthlyPrice: 8500, YearlyPrice: 102000},
			},
			sortOrder: 3,
		},
		{
			id:          uuid.MustParse("20000000-0000-0000-0000-000000000004"),
			code:        "library",
			name:        "Codevertex Library",
			description: "Standalone library/ILS: catalog & OPAC, circulation, holds, members, fines, and e-book lending. Treasury included for fines, membership fees, and e-book sales.",
			products: []schema.BundleProduct{
				{ProductCode: "library"},
				{ProductCode: "treasury"},
			},
			tiers: []schema.BundleTier{
				{PlanCode: "LIBRARY_STARTER", MonthlyPrice: 1500, YearlyPrice: 16500},
				{PlanCode: "LIBRARY_GROWTH", MonthlyPrice: 4000, YearlyPrice: 44000},
				{PlanCode: "LIBRARY_PROFESSIONAL", MonthlyPrice: 9000, YearlyPrice: 99000},
			},
			sortOrder: 4,
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
