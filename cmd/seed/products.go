package main

import (
	"context"
	"fmt"
	"log"

	"github.com/google/uuid"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/product"
)

// ── Products ─────────────────────────────────────────────────────────────────

func seedProducts(ctx context.Context, tx *ent.Tx) error {
	type productDef struct {
		id            uuid.UUID
		code          string
		name          string
		description   string
		category      product.Category
		isPlatform    bool
		isBaseService bool
		monthlyPrice  float64
		yearlyPrice   float64
		onetimePrice  float64
		dependencies  []string
		sortOrder     int
	}

	products := []productDef{
		// Platform products — always included, no extra cost
		{
			id:            uuid.MustParse("10000000-0000-0000-0000-000000000001"),
			code:          "auth",
			name:          "Authentication & SSO",
			description:   "User authentication, multi-factor auth, OAuth 2.0, OpenID Connect, session management, and single sign-on across all BengoBox services.",
			category:      product.CategoryPlatform,
			isPlatform:    true,
			isBaseService: true,
			monthlyPrice:  0,
			yearlyPrice:   0,
			onetimePrice:  0,
			sortOrder:     1,
		},
		{
			id:            uuid.MustParse("10000000-0000-0000-0000-000000000002"),
			code:          "notifications",
			name:          "Notifications",
			description:   "Multi-channel notification delivery: email (SMTP/SendGrid), SMS (Africa's Talking), and push notifications with template management.",
			category:      product.CategoryPlatform,
			isPlatform:    true,
			isBaseService: true,
			monthlyPrice:  0,
			yearlyPrice:   0,
			onetimePrice:  0,
			sortOrder:     2,
		},
		{
			id:            uuid.MustParse("10000000-0000-0000-0000-000000000003"),
			code:          "subscription",
			name:          "Subscription Management",
			description:   "Plan management, billing lifecycle, feature entitlements, usage tracking, and tier enforcement.",
			category:      product.CategoryPlatform,
			isPlatform:    true,
			isBaseService: true,
			monthlyPrice:  0,
			yearlyPrice:   0,
			onetimePrice:  0,
			sortOrder:     3,
		},

		// Core products — subscribable individually or via bundles
		{
			id:           uuid.MustParse("10000000-0000-0000-0000-000000000010"),
			code:         "ordering",
			name:         "Online Ordering",
			description:  "Customer-facing ordering portal (web + PWA), menu management, cart & checkout, order lifecycle tracking, and M-Pesa payment integration.",
			category:     product.CategoryProduct,
			monthlyPrice: 1500,
			yearlyPrice:  15000,
			onetimePrice: 100000,
			sortOrder:    10,
		},
		{
			id:           uuid.MustParse("10000000-0000-0000-0000-000000000011"),
			code:         "logistics",
			name:         "Delivery & Logistics",
			description:  "Rider management, delivery assignment, real-time tracking, route optimization, and rider mobile app.",
			category:     product.CategoryProduct,
			monthlyPrice: 1500,
			yearlyPrice:  15000,
			onetimePrice: 80000,
			dependencies: []string{"ordering"},
			sortOrder:    11,
		},
		{
			id:            uuid.MustParse("10000000-0000-0000-0000-000000000012"),
			code:          "treasury",
			name:          "Payments & Invoicing",
			description:   "Payment processing, M-Pesa integration, invoicing, receipts, financial reporting, and revenue tracking.",
			category:      product.CategoryProduct,
			isBaseService: true,
			monthlyPrice:  1000,
			yearlyPrice:   10000,
			onetimePrice:  80000,
			sortOrder:     12,
		},

		// Add-on products
		{
			id:           uuid.MustParse("10000000-0000-0000-0000-000000000020"),
			code:         "pos",
			name:         "Point of Sale",
			description:  "In-store point-of-sale terminal integration, walk-in order management, and cash register support.",
			category:     product.CategoryAddOn,
			monthlyPrice: 2000,
			yearlyPrice:  20000,
			onetimePrice: 120000,
			dependencies: []string{"treasury"},
			sortOrder:    20,
		},
		{
			id:           uuid.MustParse("10000000-0000-0000-0000-000000000021"),
			code:         "storefront",
			name:         "Website & Storefront",
			description:  "Branded website with menu display, business info, SEO, custom domain, and social media integration.",
			category:     product.CategoryAddOn,
			monthlyPrice: 500,
			yearlyPrice:  5000,
			onetimePrice: 80000,
			sortOrder:    21,
		},

		// Integration add-ons — purchased per-tenant, enable premium features
		{
			id:           uuid.MustParse("10000000-0000-0000-0000-000000000030"),
			code:         "google_maps",
			name:         "Google Maps Integration",
			description:  "Google Maps API for live delivery tracking with accurate ETAs, geocoding, and route visualization. Default: OpenStreetMap (free).",
			category:     product.CategoryAddOn,
			monthlyPrice: 500,
			yearlyPrice:  5000,
			onetimePrice: 80000,
			sortOrder:    30,
		},
		{
			id:           uuid.MustParse("10000000-0000-0000-0000-000000000031"),
			code:         "paystack_gateway",
			name:         "Paystack Payment Gateway",
			description:  "Paystack card and mobile money payments. Enables Visa/Mastercard, bank transfers, and USSD payments alongside M-Pesa.",
			category:     product.CategoryAddOn,
			monthlyPrice: 300,
			yearlyPrice:  3000,
			onetimePrice: 80000,
			sortOrder:    31,
		},
		{
			id:           uuid.MustParse("10000000-0000-0000-0000-000000000032"),
			code:         "sms_credits",
			name:         "SMS Credit Pack",
			description:  "Bulk SMS credits for order notifications, OTP verification, and marketing. Base plan includes 100 SMS/month; this adds 500 extra/month.",
			category:     product.CategoryAddOn,
			monthlyPrice: 200,
			yearlyPrice:  2000,
			onetimePrice: 80000,
			sortOrder:    32,
		},
		{
			id:           uuid.MustParse("10000000-0000-0000-0000-000000000033"),
			code:         "premium_support",
			name:         "Premium Support",
			description:  "Dedicated support channel with 4-hour SLA, priority issue resolution, and custom integrations assistance.",
			category:     product.CategoryAddOn,
			monthlyPrice: 1000,
			yearlyPrice:  10000,
			onetimePrice: 80000,
			sortOrder:    33,
		},

		// TruLoad — Intelligent weighbridge platform
		{
			id:            uuid.MustParse("10000000-0000-0000-0000-000000000050"),
			code:          "truload",
			name:          "TruLoad Weighbridge",
			description:   "Multi-tenant weighbridge platform for axle-load enforcement (KURA/KeNHA) and commercial weighing (factories, logistics, mining, agriculture). Includes TruConnect scale integration, case management, prosecution, invoicing, PDF tickets, and reporting.",
			category:      product.CategoryProduct,
			isBaseService: false,
			monthlyPrice:  3500,
			yearlyPrice:   38500,
			onetimePrice:  150000,
			sortOrder:     50,
		},

		// MarketFlow — AI marketing automation platform
		{
			id:            uuid.MustParse("10000000-0000-0000-0000-000000000040"),
			code:          "marketflow",
			name:          "MarketFlow AI Marketing",
			description:   "AI-powered marketing automation: chatbots, ad campaigns, landing funnels, nurture sequences, lead management, and conversion tracking.",
			category:      product.CategoryProduct,
			isBaseService: false,
			monthlyPrice:  3500,
			yearlyPrice:   35000,
			onetimePrice:  0,
			sortOrder:     40,
		},
		{
			id:           uuid.MustParse("10000000-0000-0000-0000-000000000041"),
			code:         "marketflow_ai_credits",
			name:         "MarketFlow AI Credits",
			description:  "Top-up pack of 100 AI chat credits for MarketFlow. Each credit = 1 user question + AI response. Base plan includes 5 free/month (Starter) or 100/month (Growth).",
			category:     product.CategoryAddOn,
			monthlyPrice: 1000,
			yearlyPrice:  10000,
			onetimePrice: 0,
			sortOrder:    41,
		},

		// Inventory Management
		{
			id:           uuid.MustParse("10000000-0000-0000-0000-000000000060"),
			code:         "inventory",
			name:         "Inventory Management",
			description:  "Real-time stock tracking, supplier management, purchase orders, low-stock alerts, batch/expiry tracking, and multi-warehouse support.",
			category:     product.CategoryProduct,
			monthlyPrice: 500,
			yearlyPrice:  5500,
			onetimePrice: 80000,
			sortOrder:    60,
		},

		// ERP Suite
		{
			id:            uuid.MustParse("10000000-0000-0000-0000-000000000070"),
			code:          "erp",
			name:          "ERP Suite",
			description:   "Integrated enterprise resource planning: HR, payroll, procurement, asset management, budgeting, and financial reporting.",
			category:      product.CategoryProduct,
			isBaseService: false,
			monthlyPrice:  2000,
			yearlyPrice:   22000,
			onetimePrice:  150000,
			sortOrder:     70,
		},
		// Library Management
		{
			id:            uuid.MustParse("10000000-0000-0000-0000-000000000071"),
			code:          "library",
			name:          "Library Management",
			description:   "Standalone library/ILS: catalog & OPAC, circulation (checkout/return/renew), holds, members, fines, e-book lending, barcode, stocktake, and reports.",
			category:      product.CategoryProduct,
			isBaseService: false,
			monthlyPrice:  1500,
			yearlyPrice:   16500,
			onetimePrice:  150000,
			sortOrder:     71,
		},
	}

	for _, p := range products {
		existing, err := tx.Product.Get(ctx, p.id)
		if err != nil && !ent.IsNotFound(err) {
			return fmt.Errorf("lookup product %s: %w", p.code, err)
		}

		if existing != nil {
			builder := tx.Product.UpdateOneID(p.id).
				SetCode(p.code).
				SetName(p.name).
				SetDescription(p.description).
				SetCategory(p.category).
				SetStatus(product.StatusActive).
				SetIsPlatform(p.isPlatform).
				SetIsBaseService(p.isBaseService).
				SetMonthlyPrice(p.monthlyPrice).
				SetYearlyPrice(p.yearlyPrice).
				SetOnetimePrice(p.onetimePrice).
				SetIncludedInBundle(true).
				SetSortOrder(p.sortOrder)
			if len(p.dependencies) > 0 {
				builder = builder.SetDependencies(p.dependencies)
			} else {
				builder = builder.ClearDependencies()
			}
			if _, err := builder.Save(ctx); err != nil {
				return fmt.Errorf("update product %s: %w", p.code, err)
			}
		} else {
			builder := tx.Product.Create().
				SetID(p.id).
				SetCode(p.code).
				SetName(p.name).
				SetDescription(p.description).
				SetCategory(p.category).
				SetStatus(product.StatusActive).
				SetIsPlatform(p.isPlatform).
				SetIsBaseService(p.isBaseService).
				SetMonthlyPrice(p.monthlyPrice).
				SetYearlyPrice(p.yearlyPrice).
				SetOnetimePrice(p.onetimePrice).
				SetIncludedInBundle(true).
				SetSortOrder(p.sortOrder)
			if len(p.dependencies) > 0 {
				builder = builder.SetDependencies(p.dependencies)
			}
			if _, err := builder.Save(ctx); err != nil {
				return fmt.Errorf("create product %s: %w", p.code, err)
			}
		}

		log.Printf("  product: %s (%s)", p.name, p.code)
	}

	return nil
}
