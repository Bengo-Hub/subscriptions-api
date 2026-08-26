package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/product"
	"github.com/bengobox/subscription-service/internal/ent/subscriptionplan"
)

// ── eTIMS API Access (external, non-tenant API consumers) ──────────────────
//
// Distinct from the existing "etims_integration" FEATURE bundled into onboarded
// tenants' regular treasury/POS plans (TREASURY_GROWTH etc. already grant it,
// unchanged by this file) — this is a standalone PRODUCT for external companies
// consuming treasury-api's /api/v1/external/etims/* API directly, with no other
// Codevertex SaaS relationship. See .claude/plans/etims-api-monetization-and-
// security-2026-08-18.md.
//
// Pricing is a prepaid TOKEN BUCKET (internal/modules/billing/token_wallet.go +
// api_token_costs.go), not the old ReportUsage/OverageCharge monthly-soft-cap pipeline —
// etims_transactions was removed from overage_eligibility.go's meteredMetrics when this shipped.
// included_tokens is each plan's monthly grant (added to the wallet, accumulating, every
// renewal — see TokenWalletService.GrantTokens); token_price_kes is the price per token when a
// subscriber tops up ad hoc between renewals. Both are derived from the ORIGINAL per-transaction
// figures (500/2,000/10,000 included; KES 800/600/400 per 100 over) scaled by
// ApiTokenCostTransmit=10 (the token weight of a sales/credit-note/stock-io transmission — the
// one call type this was already metering 1:1) so a subscriber's sales-transmission capacity is
// UNCHANGED in KES terms; cheaper lookup/write calls, previously unmetered entirely, are now
// metered too but at a much lower weight — a strict improvement, not a new cost.
//
// The one-time ASSISTED-integration fee (35k/85k/180k KES, only when Codevertex's
// team does the setup instead of the client's own developers) is deliberately
// NOT a plan setup_fee here — that field fires automatically for every
// subscriber to a plan, but assisted-vs-self-serve is a per-request choice
// orthogonal to which usage tier they pick. It's applied instead via the
// existing CustomAddon mechanism (billing_cycle=one_time), created by a
// platform admin once an IntegrationRequest is approved as assisted.

func seedEtimsAPIProduct(ctx context.Context, tx *ent.Tx) error {
	id := uuid.NewSHA1(uuid.NameSpaceOID, []byte("product:etims-api-access"))

	const description = "Standalone KRA eTIMS fiscalization API for external companies integrating their own systems directly -- device/item registration, sale/credit-note transmission, sandbox certification checklist. Priced by monthly transaction volume, not bundled into any other plan."

	existing, err := tx.Product.Get(ctx, id)
	if err != nil && !ent.IsNotFound(err) {
		return fmt.Errorf("lookup etims-api-access product: %w", err)
	}

	if existing != nil {
		if _, err := tx.Product.UpdateOneID(id).
			SetName("eTIMS API Access").
			SetDescription(description).
			SetCategory(product.CategoryProduct).
			SetStatus(product.StatusActive).
			SetIsPlatform(false).
			SetIsBaseService(false).
			SetSortOrder(81).
			Save(ctx); err != nil {
			return fmt.Errorf("update etims-api-access product: %w", err)
		}
	} else {
		if _, err := tx.Product.Create().
			SetID(id).
			SetCode("etims-api-access").
			SetName("eTIMS API Access").
			SetDescription(description).
			SetCategory(product.CategoryProduct).
			SetStatus(product.StatusActive).
			SetIsPlatform(false).
			SetIsBaseService(false).
			SetMonthlyPrice(0).
			SetYearlyPrice(0).
			SetOnetimePrice(0).
			SetIncludedInBundle(false).
			SetSortOrder(81).
			Save(ctx); err != nil {
			return fmt.Errorf("create etims-api-access product: %w", err)
		}
	}

	log.Println("  product: eTIMS API Access (etims-api-access)")
	return nil
}

func seedEtimsAPIPlans(ctx context.Context, tx *ent.Tx) error {
	now := time.Now()
	serviceTag := "etims_api"

	type planDef struct {
		id          uuid.UUID
		planCode    string
		name        string
		description string
		price       float64
		tierOrder   int
		includedTx  int
		tierLimits  map[string]any
		features    []string
		isPublic    bool
	}

	plans := []planDef{
		{
			id:          uuid.NewSHA1(uuid.NameSpaceOID, []byte("etims_api:BASIC")),
			planCode:    "ETIMS_API_BASIC",
			name:        "eTIMS API Basic",
			description: "5,000 tokens/month included (~500 sales transmissions, or more if you mostly use cheaper lookup/write calls), then KES 0.80/token top-up.",
			price:       4999.0,
			tierOrder:   1,
			includedTx:  500,
			tierLimits: map[string]any{
				"etims_transactions_per_month":      500,  // display-only: sales-equivalent capacity at the transmit weight
				"overage_etims_price_per_100_month": 800,  // display-only: legacy per-100-transaction framing
				"included_tokens":                   5000, // authoritative: monthly token grant (500 * ApiTokenCostTransmit)
				"token_price_kes":                   0.8,  // authoritative: KES per token on ad-hoc top-up
				"api_requests_per_minute":           60,
			},
			features: []string{"etims_api_access"},
			isPublic: true,
		},
		{
			id:          uuid.NewSHA1(uuid.NameSpaceOID, []byte("etims_api:GROWTH")),
			planCode:    "ETIMS_API_GROWTH",
			name:        "eTIMS API Growth",
			description: "20,000 tokens/month included (~2,000 sales transmissions, or more if you mostly use cheaper lookup/write calls), then KES 0.60/token top-up.",
			price:       12999.0,
			tierOrder:   2,
			includedTx:  2000,
			tierLimits: map[string]any{
				"etims_transactions_per_month":      2000,
				"overage_etims_price_per_100_month": 600,
				"included_tokens":                   20000,
				"token_price_kes":                   0.6,
				"api_requests_per_minute":           150,
			},
			features: []string{"etims_api_access"},
			isPublic: true,
		},
		{
			id:          uuid.NewSHA1(uuid.NameSpaceOID, []byte("etims_api:SCALE")),
			planCode:    "ETIMS_API_SCALE",
			name:        "eTIMS API Scale",
			description: "100,000 tokens/month included (~10,000 sales transmissions, or more if you mostly use cheaper lookup/write calls), then KES 0.40/token top-up.",
			price:       29999.0,
			tierOrder:   3,
			includedTx:  10000,
			tierLimits: map[string]any{
				"etims_transactions_per_month":      10000,
				"overage_etims_price_per_100_month": 400,
				"included_tokens":                   100000,
				"token_price_kes":                   0.4,
				"api_requests_per_minute":           400,
			},
			features: []string{"etims_api_access"},
			isPublic: true,
		},
		// ETIMS_API_BUNDLED — the PowerSuite/POS/Duka/Dawa cross-sell tier: zero monthly base
		// fee, same token economics as Basic (5,000 included tokens/month, KES 0.80/token
		// top-up), for a tenant that ALREADY pays for bundled etims_integration on their main
		// plan and just wants external-API access for a downstream integration on top. NOT
		// public (is_public: false) — self-serve POST /subscription would let anyone claim it
		// as a free-standing plan, defeating the pricing model. Only reachable via the admin
		// AssignProductPlan path (POST /admin/tenants/{id}/products), which enforces the
		// eligibility check (tenant's composite entitlements must already include
		// etims_integration) in service.go's AssignProductPlan before attaching it.
		{
			id:          uuid.NewSHA1(uuid.NameSpaceOID, []byte("etims_api:BUNDLED")),
			planCode:    "ETIMS_API_BUNDLED",
			name:        "eTIMS API (PowerSuite bundle)",
			description: "For tenants who already pay for eTIMS on their main plan: no extra monthly fee for external API access, 5,000 tokens/month included, then KES 0.80/token top-up.",
			price:       0,
			tierOrder:   0,
			includedTx:  500,
			tierLimits: map[string]any{
				"included_tokens":         5000,
				"token_price_kes":         0.8,
				"api_requests_per_minute": 60,
			},
			features: []string{"etims_api_access"},
			isPublic: false,
		},
	}

	for _, p := range plans {
		existing, err := tx.SubscriptionPlan.Get(ctx, p.id)
		if err != nil && !ent.IsNotFound(err) {
			return fmt.Errorf("lookup etims_api plan %s: %w", p.planCode, err)
		}
		if existing != nil {
			_, err = tx.SubscriptionPlan.UpdateOneID(p.id).
				SetPlanCode(p.planCode).SetName(p.name).SetDescription(p.description).
				SetBillingCycle("MONTHLY").SetPlanType(subscriptionplan.PlanTypeSTANDALONE_SERVICE).
				SetBasePrice(p.price).SetCurrency("KES").SetIsActive(true).SetIsPublic(p.isPublic).
				SetTierOrder(p.tierOrder).SetTierLimitsJSON(p.tierLimits).SetServiceTag(serviceTag).SetUpdatedAt(now).Save(ctx)
		} else {
			_, err = tx.SubscriptionPlan.Create().
				SetID(p.id).SetPlanCode(p.planCode).SetName(p.name).SetDescription(p.description).
				SetBillingCycle("MONTHLY").SetPlanType(subscriptionplan.PlanTypeSTANDALONE_SERVICE).
				SetBasePrice(p.price).SetCurrency("KES").SetIsActive(true).SetIsPublic(p.isPublic).
				SetTierOrder(p.tierOrder).SetTierLimitsJSON(p.tierLimits).SetServiceTag(serviceTag).
				SetCreatedAt(now).SetUpdatedAt(now).Save(ctx)
		}
		if err != nil {
			return fmt.Errorf("upsert etims_api plan %s: %w", p.planCode, err)
		}
		if err := seedPlanFeatures(ctx, tx, p.id, p.features); err != nil {
			return fmt.Errorf("seed features for etims_api plan %s: %w", p.planCode, err)
		}
		log.Printf("  etims_api plan: %s (MONTHLY, KES %.0f, %d tx incl.)", p.name, p.price, p.includedTx)
	}
	return nil
}
