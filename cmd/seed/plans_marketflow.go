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
// 3,500 (the existing marketflow product price), 8,500 and 15,000 a month. Paid tiers include a
// monthly AI credit allowance (ai_credits_monthly, owner 2026-11-14, replacing the 2026-11-11 "no
// plan includes AI credits"); more AI work is paid from bundles (billing/credit_services.go). A
// plan also sets the free chat turns a day (chat_turns_daily). Only MarketFlow plans include AI
// credits: PowerSuite plans never do. Not tied to a use case, so every tenant sees them; auth-api's
// fbo use case trials on MARKETFLOW_STARTER.

// Included AI credits a month per MarketFlow tier, set on 2026-11-15 from the owner's chosen
// option (Lite 50, Growth 150, Pro 400, Business 1,000); margin reasoning in
// .claude/plans/billing-ai-credits-plan-audit-2026-11-14.md. Rule used: at the worst
// case of about KES 5 of provider cost per credit (marketflow-ai registry), a fully used allowance
// costs at most 35% of the plan price net of the 3% payment fee, and the plan price per included
// credit (about KES 15) stays above the dearest bundle price (KES 12.5), so a plan is never a
// cheaper way to buy credits. Starter is free, so it carries no allowance (0 = the key is left
// out, never written as 0, which several services read as unlimited).
const (
	mfAICreditsStarter  = 0
	mfAICreditsLite     = 50
	mfAICreditsGrowth   = 150
	mfAICreditsPro      = 400
	mfAICreditsBusiness = 1000
)

// marketflowAICredits is the included AI credits a month for a tier (0: none).
func marketflowAICredits(tier int) int {
	switch tier {
	case 1:
		return mfAICreditsStarter
	case 2:
		return mfAICreditsLite
	case 3:
		return mfAICreditsGrowth
	case 4:
		return mfAICreditsPro
	default:
		return mfAICreditsBusiness
	}
}

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
		// Free tier (owner, 2026-11-11): the public page, contacts, leads and short links only;
		// campaigns, funnels, the AI chat agent and the rest start at Lite.
		return unionFeatures([]string{"contact_management", "lead_management", "shortlinks", "profile_pages"}, marketflowAIFeatures(tier))
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
	f := unionFeatures(crm, marketflowAIFeatures(tier))
	// A business's own domain for its MarketFlow page and funnels (owner, 2026-11-11: Pro and up).
	// The domains themselves are managed centrally in SSO (auth-api tenant domains).
	if tier >= 4 {
		f = unionFeatures(f, []string{"marketflow_custom_domain"})
	}
	// Autopilot (owner, 2026-11-14): the agents from Lite up (how many is max_autopilot_agents);
	// auto-publish after a veto window from Pro up. Below Pro every draft waits for approval.
	if tier >= 2 {
		f = unionFeatures(f, []string{"marketflow_autopilot"})
	}
	if tier >= 4 {
		f = unionFeatures(f, []string{"autopilot_auto_publish"})
	}
	return f
}

// marketflowAutopilotAgents is max_autopilot_agents per tier (owner, 2026-11-14): Lite 1,
// Growth 3, Pro 10, Business unlimited (-1). Starter has no Autopilot, so it gets no key.
var marketflowAutopilotAgents = map[int]int{2: 1, 3: 3, 4: 10, 5: -1}

// marketflowPlanLimits: -1 is unlimited. ai_credits_monthly is the included AI credit allowance
// (marketflowAICredits, refilled each month by the credits job; left out on the free tier).
// chat_turns_daily is the free chat allowance a day before chat turns cost credits (marketflow-ai
// chatallowance reads it); the free tier gets the fewest.
func marketflowPlanLimits(tier int) map[string]any {
	rows := map[int][7]int{
		//    contacts leads campaigns sequences funnels integrations chat_turns_daily
		1: {200, 50, 1, 1, 1, 1, 10}, // never 0: several services read 0 as unlimited
		2: {2000, 500, 3, 3, 5, 3, 30},
		3: {10000, 2000, 10, 10, 15, 5, 60},
		4: {50000, 10000, -1, -1, 50, 10, 120},
		5: {-1, -1, -1, -1, -1, -1, 300},
	}
	r, ok := rows[tier]
	if !ok {
		r = rows[5]
	}
	limits := map[string]any{
		"max_contacts": r[0], "max_leads": r[1], "max_campaigns": r[2], "max_sequences": r[3],
		"max_funnels": r[4], "max_integrations": r[5], "chat_turns_daily": r[6],
	}
	if n := marketflowAICredits(tier); n > 0 {
		limits["ai_credits_monthly"] = n
	}
	if n, ok := marketflowAutopilotAgents[tier]; ok {
		limits["max_autopilot_agents"] = n
	} else if tier > 5 {
		limits["max_autopilot_agents"] = marketflowAutopilotAgents[5]
	}
	return limits
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
		desc: "Free: your public profile page, contacts and leads, branded short links and AI poster and template video ads, with 10 free chat turns a day. AI work uses credits you buy."},
	{code: "MARKETFLOW_LITE", label: "MarketFlow Lite", tier: 2, monthly: 1500,
		desc: fmt.Sprintf("Adds campaigns, funnels and landing pages, the AI chat assistant on your page and WhatsApp, follow-up sequences, testimonials, Meta ad boosts and AI presenter videos, with %d AI credits a month and 30 free chat turns a day. Buy more credits any time.", mfAICreditsLite)},
	{code: "MARKETFLOW_GROWTH", label: "MarketFlow Growth", tier: 3, monthly: 3500,
		desc: fmt.Sprintf("The full marketing team: lead scoring, deal pipeline, automations, helpdesk, webhooks and cinematic videos, with %d AI credits a month and 60 free chat turns a day. Buy more credits any time.", mfAICreditsGrowth)},
	{code: "MARKETFLOW_PRO", label: "MarketFlow Pro", tier: 4, monthly: 8500,
		desc: fmt.Sprintf("Unlimited campaigns and sequences, premium presenter and cinematic videos, your own domain for your page and funnels, more leads and integrations, with %d AI credits a month and 120 free chat turns a day. Buy more credits any time.", mfAICreditsPro)},
	{code: "MARKETFLOW_BUSINESS", label: "MarketFlow Business", tier: 5, monthly: 15000,
		desc: fmt.Sprintf("For agencies and businesses with several brands: no limits, white label, your own domain and a dedicated account manager, with %d AI credits a month and 300 free chat turns a day. Buy more credits any time.", mfAICreditsBusiness)},
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
