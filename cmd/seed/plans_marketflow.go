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

// ── MarketFlow (standalone) Plans ─────────────────────────────────────────────
//
// MarketFlow on its own, for a business that markets and sells without POS or inventory: a
// Forever Business Owner, a salon, a training centre. PowerSuite bundles keep MarketFlow
// included (psCRMBlock); the standalone MARKETFLOW_* family was retired on 2026-09-11 and comes
// back here on 2026-11-11, because a business signing up for MarketFlow alone (use case fbo)
// had no plan at all and was refused by marketflow-api's RequireServiceAccess("marketflow").
//
// Five tiers so every budget has a way in (owner, 2026-11-11): a free Starter, then KES 1,500,
// 3,500 (the existing marketflow product price), 8,500 and 15,000 a month. Each tier includes a
// monthly AI credit allowance (ai_credits_monthly) set well below the tier price at the loaded
// cost of about KES 4.8 a credit (.claude/plans/marketflow-ai-studio-bundle-pricing-2026-11-11.md);
// more credits come from bundles (billing/credit_services.go). Not tied to a use case, so every
// tenant sees them; auth-api's fbo use case trials on MARKETFLOW_STARTER.

// marketflowAIFeatures are the AI Studio outputs a tier may make (the codes marketflow-ai's
// registry checks before quoting a Creative Plan). Credits are charged per output on top.
func marketflowAIFeatures(tier int) []string {
	f := []string{"ai_static_ads", "ai_video_template"}
	if tier >= 2 {
		f = append(f, "ai_video_presenter")
	}
	if tier >= 3 {
		f = append(f, "ai_video_cinematic")
	}
	if tier >= 4 {
		f = append(f, "ai_video_presenter_premium", "ai_video_cinematic_premium")
	}
	return f
}

// marketflowPlanFeatures maps the five MarketFlow tiers onto the three CRM blocks PowerSuite uses,
// so one feature set stays the truth: Starter is CRM block 1; Lite adds the selling tools a small
// business needs first; Growth and Pro are block 2; Business is block 3.
func marketflowPlanFeatures(tier int) []string {
	var crm []string
	switch {
	case tier <= 1:
		crm = psCRMBlock(1)
	case tier == 2:
		crm = unionFeatures(psCRMBlock(1), []string{
			"landing_pages", "funnel_builder", "ai_chat_agent", "whatsapp_integration",
			"testimonials", "email_sequences", "paid_ads_launch",
		})
	case tier <= 4:
		crm = psCRMBlock(2)
	default:
		crm = psCRMBlock(3)
	}
	return unionFeatures(crm, marketflowAIFeatures(tier))
}

// marketflowPlanLimits: -1 is unlimited.
func marketflowPlanLimits(tier int) map[string]any {
	rows := map[int][7]int{
		//    contacts leads campaigns sequences funnels integrations ai_credits
		1: {500, 100, 1, 1, 2, 1, 10},
		2: {2000, 500, 3, 3, 5, 3, 50},
		3: {10000, 2000, 10, 10, 15, 5, 150},
		4: {50000, 10000, -1, -1, 50, 10, 400},
		5: {-1, -1, -1, -1, -1, -1, 1000},
	}
	r, ok := rows[tier]
	if !ok {
		r = rows[5]
	}
	return map[string]any{
		"max_contacts": r[0], "max_leads": r[1], "max_campaigns": r[2], "max_sequences": r[3],
		"max_funnels": r[4], "max_integrations": r[5], "ai_credits_monthly": r[6],
	}
}

type marketflowTier struct {
	code    string
	label   string
	tier    int
	monthly float64
	desc    string
}

var marketflowTiers = []marketflowTier{
	{code: "MARKETFLOW_STARTER", label: "MarketFlow Starter", tier: 1, monthly: 0,
		desc: "Free: your public profile page, contacts and leads, one campaign, branded short links, AI poster and template video ads, and 10 AI credits a month."},
	{code: "MARKETFLOW_LITE", label: "MarketFlow Lite", tier: 2, monthly: 1500,
		desc: "Adds funnels and landing pages, the AI chat assistant on your page and WhatsApp, follow-up sequences, testimonials, Meta ad boosts and AI presenter videos, with 50 AI credits a month."},
	{code: "MARKETFLOW_GROWTH", label: "MarketFlow Growth", tier: 3, monthly: 3500,
		desc: "The full marketing team: lead scoring, deal pipeline, automations, helpdesk, webhooks and cinematic videos, for growing businesses, with 150 AI credits a month."},
	{code: "MARKETFLOW_PRO", label: "MarketFlow Pro", tier: 4, monthly: 8500,
		desc: "Unlimited campaigns and sequences, premium presenter and cinematic videos, more leads and integrations, with 400 AI credits a month."},
	{code: "MARKETFLOW_BUSINESS", label: "MarketFlow Business", tier: 5, monthly: 15000,
		desc: "For agencies and businesses with several brands: no limits, white label and a dedicated account manager, with 1,000 AI credits a month."},
}

// seedMarketflowPlans upserts the five monthly standalone MarketFlow plans.
func seedMarketflowPlans(ctx context.Context, tx *ent.Tx) error {
	now := time.Now()
	for _, t := range marketflowTiers {
		id := uuid.NewSHA1(uuid.NameSpaceOID, []byte("plan:"+t.code))
		feats := marketflowPlanFeatures(t.tier)
		limits := marketflowPlanLimits(t.tier)
		meta := map[string]any{"product": "marketflow"}
		// The free tier needs no trial; the paid tiers keep the 14 days every family has.
		trial := 14
		if t.monthly == 0 {
			trial = 0
			meta["free_tier"] = true
		}

		existing, err := tx.SubscriptionPlan.Get(ctx, id)
		if err != nil && !ent.IsNotFound(err) {
			return fmt.Errorf("lookup marketflow plan %s: %w", t.code, err)
		}
		if existing != nil {
			_, err = tx.SubscriptionPlan.UpdateOneID(id).
				SetPlanCode(t.code).SetName(t.label).SetDescription(t.desc).
				SetBillingCycle("MONTHLY").SetPlanType(subscriptionplan.PlanTypeTIERED).
				SetBasePrice(t.monthly).SetSetupFee(0).SetCurrency("KES").
				SetIsActive(true).SetIsPublic(true).
				SetTierOrder(t.tier).SetFreeTrialDays(trial).ClearUseCase().
				SetTierLimitsJSON(limits).SetServiceTag("marketflow").SetMetadata(meta).SetUpdatedAt(now).Save(ctx)
		} else {
			_, err = tx.SubscriptionPlan.Create().
				SetID(id).SetPlanCode(t.code).SetName(t.label).SetDescription(t.desc).
				SetBillingCycle("MONTHLY").SetPlanType(subscriptionplan.PlanTypeTIERED).
				SetBasePrice(t.monthly).SetSetupFee(0).SetCurrency("KES").
				SetIsActive(true).SetIsPublic(true).
				SetTierOrder(t.tier).SetFreeTrialDays(trial).
				SetTierLimitsJSON(limits).SetServiceTag("marketflow").SetMetadata(meta).
				SetCreatedAt(now).SetUpdatedAt(now).Save(ctx)
		}
		if err != nil {
			return fmt.Errorf("upsert marketflow plan %s: %w", t.code, err)
		}
		if err := seedPlanFeatures(ctx, tx, id, feats); err != nil {
			return fmt.Errorf("seed features for %s: %w", t.code, err)
		}
		log.Printf("  marketflow plan: %s (MONTHLY, KES %.0f, %d features)", t.label, t.monthly, len(feats))
	}
	return nil
}

// retiredMarketflowCodes are the pre-2026-09-11 MarketFlow codes that stay retired (their
// _YEARLY rows are caught by the suffix rule); STARTER and GROWTH are reused by the new family.
var retiredMarketflowCodes = map[string]bool{"MARKETFLOW_PROFESSIONAL": true}
