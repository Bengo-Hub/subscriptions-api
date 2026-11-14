package credits

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/apitokenwallet"
	"github.com/bengobox/subscription-service/internal/ent/tenantsubscription"
	"github.com/bengobox/subscription-service/internal/modules/billing"
)

// SyncPlanAllowance moves the included monthly allowance (AI credits) to the plan a subscription
// just moved to (owner, 2026-11-14: included credits follow the plan in force):
//   - a plan that grants more tops the allowance up by the difference now, so credits already
//     used this month stay used;
//   - from a plan that granted none (a stale balance is replaced) or an unlimited one, the
//     allowance is set to the new plan's amount;
//   - a plan that grants less lowers the allowance to the new amount when it is above it
//     (downgrades take effect at the renewal, so this runs then);
//   - a plan that grants none clears it.
//
// Bought bundles are never touched. A paid trial gets no allowance (allowanceLimit), so nothing
// is granted while the subscription is TRIAL. Each move is keyed by subscription, both plans
// and the month, so a retry changes nothing and switching back and forth within a month cannot
// collect a second top-up for the same move.
func (s *Service) SyncPlanAllowance(ctx context.Context, sub *ent.TenantSubscription, from, to *ent.SubscriptionPlan) error {
	if sub == nil || from == nil || to == nil || from.ID == to.ID {
		return nil
	}
	if sub.Status != tenantsubscription.StatusACTIVE {
		return nil
	}
	window := time.Now().UTC().Format("2006-01")
	seen := map[string]bool{}
	for _, code := range billing.MeteredServiceCodes() {
		spec, _ := billing.LookupMeteredService(code)
		if spec.PlanQuotaKey == "" || spec.QuotaTag == "" || seen[spec.QuotaTag] {
			continue
		}
		seen[spec.QuotaTag] = true
		oldL, newL := limitOf(from, spec.PlanQuotaKey), limitOf(to, spec.PlanQuotaKey)
		key := fmt.Sprintf("plan:%s:%s:%s:%s", sub.ID, from.ID, to.ID, window)
		desc := fmt.Sprintf("%s included allowance: %s to %s", spec.Label, from.Name, to.Name)
		bal, err := s.wallets.GetBalance(ctx, sub.TenantID, spec.QuotaTag)
		if err != nil {
			return err
		}
		switch {
		case newL < 0:
			// Unlimited: Coverage reads the plan, the wallet is not used.
		case newL == 0:
			if bal.Balance > 0 {
				if _, err := s.wallets.SetQuota(ctx, sub.TenantID, spec.QuotaTag, 0, key, desc); err != nil {
					return err
				}
			}
		case oldL <= 0:
			if _, err := s.wallets.SetQuota(ctx, sub.TenantID, spec.QuotaTag, newL, key, desc); err != nil {
				return err
			}
		case newL > oldL:
			if _, _, err := s.wallets.Credit(ctx, billing.CreditRequest{
				TenantID: sub.TenantID, ServiceTag: spec.QuotaTag, Units: newL - oldL, Action: "grant",
				Key: key, RefType: "plan_change", Description: desc,
			}); err != nil {
				return err
			}
		case newL < oldL && bal.Balance > newL:
			if _, err := s.wallets.SetQuota(ctx, sub.TenantID, spec.QuotaTag, newL, key, desc); err != nil {
				return err
			}
		}
	}
	return nil
}

// ClearIncludedAIAllowanceKey is the idempotency key of the one-time fix below: a wallet already
// cleared by it is never written again.
const ClearIncludedAIAllowanceKey = "fix:clear-included-ai-allowance:2026-11-14"

// IncludedAllowanceFix is one tenant the fix found.
type IncludedAllowanceFix struct {
	TenantID uuid.UUID
	Slug     string
	PlanCode string
	Balance  int64
}

// IncludedAllowanceFixResult reports one ClearUngrantedAIAllowances run.
type IncludedAllowanceFixResult struct {
	Applied bool
	Found   []IncludedAllowanceFix
	Cleared int
	Failed  int
}

// ClearUngrantedAIAllowances is the one-time fix for the included AI credits PowerSuite tenants
// still held after b66d2a0 (2026-11-04) stopped PowerSuite plans granting them: the November
// refill ran on 2026-11-01, before that change, and its month key keeps a later run from zeroing
// them. It finds every positive included AI allowance (ai_credits_quota) whose tenant's plan
// grants none (no ai_credits_monthly, which is every PowerSuite plan) and, when apply is true,
// sets it to 0. Each clear writes one ledger row (action adjustment, ref_type quota_reset,
// idempotency key ClearIncludedAIAllowanceKey) and is logged. Bought credits (the ai_credits
// bundle wallet) are never read or written. With apply false it only logs what it would clear.
func ClearUngrantedAIAllowances(ctx context.Context, orm *ent.Client, wallets *billing.TokenWalletService, log *zap.Logger, apply bool) (IncludedAllowanceFixResult, error) {
	res := IncludedAllowanceFixResult{Applied: apply}
	spec, ok := billing.LookupMeteredService(billing.ServiceAI)
	if !ok || spec.QuotaTag == "" {
		return res, fmt.Errorf("ai credit service is not registered")
	}
	rows, err := orm.ApiTokenWallet.Query().
		Where(apitokenwallet.ServiceTagEQ(spec.QuotaTag), apitokenwallet.BalanceGT(0)).
		All(ctx)
	if err != nil {
		return res, fmt.Errorf("query included AI allowances: %w", err)
	}
	for _, w := range rows {
		fix := IncludedAllowanceFix{TenantID: w.TenantID, Balance: w.Balance}
		if t, terr := orm.Tenant.Get(ctx, w.TenantID); terr == nil {
			fix.Slug = t.Slug
		}
		sub, serr := orm.TenantSubscription.Query().
			Where(tenantsubscription.TenantIDEQ(w.TenantID)).
			WithPlan().
			Only(ctx)
		switch {
		case serr == nil:
			if sub.Edges.Plan != nil {
				fix.PlanCode = sub.Edges.Plan.PlanCode
			}
			if limitOf(sub.Edges.Plan, spec.PlanQuotaKey) != 0 {
				continue // the plan grants an allowance: the monthly refill owns it
			}
		case ent.IsNotFound(serr):
			// No subscription: no plan grants it either.
		default:
			res.Failed++
			log.Warn("clear included AI allowance: subscription lookup failed", zap.String("tenant_id", w.TenantID.String()), zap.Error(serr))
			continue
		}
		res.Found = append(res.Found, fix)
		if !apply {
			log.Info("clear included AI allowance (dry run)", zap.String("tenant", fix.Slug),
				zap.String("plan", fix.PlanCode), zap.Int64("balance", fix.Balance))
			continue
		}
		applied, err := wallets.SetQuota(ctx, w.TenantID, spec.QuotaTag, 0, ClearIncludedAIAllowanceKey,
			"Included AI credits cleared: the plan grants none (one-time fix 2026-11-14)")
		if err != nil {
			res.Failed++
			log.Warn("clear included AI allowance failed", zap.String("tenant", fix.Slug), zap.Error(err))
			continue
		}
		if applied {
			res.Cleared++
		}
		log.Info("clear included AI allowance", zap.String("tenant", fix.Slug),
			zap.String("plan", fix.PlanCode), zap.Int64("cleared", fix.Balance), zap.Bool("applied", applied))
	}
	return res, nil
}
