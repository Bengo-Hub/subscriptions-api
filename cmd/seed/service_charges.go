package main

import (
	"context"
	"fmt"
	"log"

	"github.com/google/uuid"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/servicechargeplan"
)

// ── Service Charge Plans ─────────────────────────────────────────────────────

func seedServiceChargePlans(ctx context.Context, tx *ent.Tx) error {
	type scPlan struct {
		code        string
		name        string
		description string
		chargeType  servicechargeplan.ChargeType
		chargeValue float64
		minCharge   *float64
		maxCharge   *float64
		services    []string
		isDefault   bool
	}

	minCharge50 := 50.0
	minCharge5 := 5.0
	minCharge2 := 2.0
	maxCharge5000 := 5000.0
	maxCharge2000 := 2000.0
	maxCharge500 := 500.0
	maxCharge250 := 250.0

	plans := []scPlan{
		{
			code:        "SC_ORDERING_5PCT",
			name:        "Ordering 5% Service Charge",
			description: "Platform takes 5% of each online order transaction amount. Suitable for small tenants who prefer no monthly subscription fee.",
			chargeType:  servicechargeplan.ChargeTypePERCENTAGE,
			chargeValue: 5.0,
			minCharge:   &minCharge50,
			maxCharge:   &maxCharge5000,
			services:    []string{"ordering"},
			isDefault:   true,
		},
		{
			code:        "SC_ORDERING_3PCT",
			name:        "Ordering 3% Service Charge",
			description: "Reduced 3% service charge for high-volume ordering tenants.",
			chargeType:  servicechargeplan.ChargeTypePERCENTAGE,
			chargeValue: 3.0,
			minCharge:   &minCharge50,
			maxCharge:   &maxCharge5000,
			services:    []string{"ordering"},
		},
		{
			code:        "SC_LOGISTICS_7PCT",
			name:        "Logistics 7% Service Charge",
			description: "Platform takes 7% of each delivery fee processed through logistics service.",
			chargeType:  servicechargeplan.ChargeTypePERCENTAGE,
			chargeValue: 7.0,
			services:    []string{"logistics"},
			isDefault:   true,
		},
		{
			code:        "SC_POS_2PCT",
			name:        "POS 2% Service Charge",
			description: "Platform takes 2% of each POS transaction.",
			chargeType:  servicechargeplan.ChargeTypePERCENTAGE,
			chargeValue: 2.0,
			maxCharge:   &maxCharge2000,
			services:    []string{"pos"},
			isDefault:   true,
		},
		{
			// Codevertex Flex — the pay-as-you-grow entry tier for micro-enterprises that
			// can't commit to a monthly subscription (kiosks, stalls, new businesses). 1.5%
			// of each sale, floored at KES 2, capped at KES 250 per transaction — the 1.5% rate
			// is aligned with the ~1.5% M-Pesa till rate merchants already understand. No monthly
			// fee. Tenants on this plan are restricted to platform-collectable online rails
			// (no cash/offline) so the commission is netted at settlement. Applies to all POS
			// lines (POS/Duka/Dawa). See the dormancy clause in the pricing doc (>60 days idle).
			code:        "SC_POS_FLEX",
			name:        "Codevertex Flex — POS Service Charge",
			description: "Pay-as-you-grow for micro shops, kiosks & stalls: 1.5% of each sale, minimum KES 2, capped at KES 250 per transaction. No monthly subscription. One-time setup KES 5,000. Online payment methods only.",
			chargeType:  servicechargeplan.ChargeTypePERCENTAGE,
			chargeValue: 1.5,
			minCharge:   &minCharge2,
			maxCharge:   &maxCharge250,
			services:    []string{"pos"},
		},
		{
			code:        "SC_UNIVERSAL_FLAT_50",
			name:        "Universal KES 50 Flat Fee",
			description: "Flat KES 50 per transaction regardless of amount. Applies to any service.",
			chargeType:  servicechargeplan.ChargeTypeFIXED_PER_TRANSACTION,
			chargeValue: 50.0,
			services:    nil, // any service
		},
		{
			code:        "SC_TRULOAD_10PCT",
			name:        "TruLoad 10% Service Charge",
			description: "Platform takes 10% of each weighing/overload fine transaction processed through TruLoad.",
			chargeType:  servicechargeplan.ChargeTypePERCENTAGE,
			chargeValue: 10.0,
			services:    []string{"truload"},
			isDefault:   true,
		},
		{
			code:        "SC_MARKETFLOW_ADS",
			name:        "MarketFlow Ads Service Fee",
			description: "Platform fee on ad campaign budgets processed through MarketFlow. 5% of ads budget, KES 5 minimum, KES 500 max per campaign. Configurable from /platform/billing.",
			chargeType:  servicechargeplan.ChargeTypePERCENTAGE,
			chargeValue: 5.0,
			minCharge:   &minCharge5,
			maxCharge:   &maxCharge500,
			services:    []string{"marketflow"},
			isDefault:   true,
		},
		{
			code:        "SC_MARKETFLOW_AI_CREDIT",
			name:        "MarketFlow AI Credit Fee",
			description: "Per-credit fee when purchasing AI chat credits beyond the plan allowance. KES 10 per credit.",
			chargeType:  servicechargeplan.ChargeTypeFIXED_PER_TRANSACTION,
			chargeValue: 10.0,
			services:    []string{"marketflow"},
		},
	}

	for _, p := range plans {
		planID := uuid.NewSHA1(uuid.NameSpaceURL, []byte("bengobox:sc_plan:"+p.code))

		existing, err := tx.ServiceChargePlan.Query().Where(servicechargeplan.Code(p.code)).Only(ctx)
		if err == nil {
			upd := tx.ServiceChargePlan.UpdateOneID(existing.ID).
				SetName(p.name).
				SetDescription(p.description).
				SetChargeType(p.chargeType).
				SetChargeValue(p.chargeValue).
				SetIsDefault(p.isDefault).
				SetNillableMinCharge(p.minCharge).
				SetNillableMaxCharge(p.maxCharge)
			if p.services != nil {
				upd.SetApplicableServices(p.services)
			}
			if _, err := upd.Save(ctx); err != nil {
				return fmt.Errorf("update service charge plan %s: %w", p.code, err)
			}
			continue
		}
		if !ent.IsNotFound(err) {
			return fmt.Errorf("lookup service charge plan %s: %w", p.code, err)
		}

		builder := tx.ServiceChargePlan.Create().
			SetID(planID).
			SetCode(p.code).
			SetName(p.name).
			SetDescription(p.description).
			SetChargeType(p.chargeType).
			SetChargeValue(p.chargeValue).
			SetIsActive(true).
			SetIsDefault(p.isDefault).
			SetNillableMinCharge(p.minCharge).
			SetNillableMaxCharge(p.maxCharge)
		if p.services != nil {
			builder.SetApplicableServices(p.services)
		}
		if _, err := builder.Save(ctx); err != nil {
			return fmt.Errorf("create service charge plan %s: %w", p.code, err)
		}
	}

	// Deprecate the earlier 1% micro charge (SC_POS_MICRO_1PCT) — superseded by SC_POS_FLEX
	// (1.5% / min KES 10 / cap KES 250). Deactivate rather than delete so any historical
	// reference stays resolvable. Idempotent: a no-op once the row is already inactive/absent.
	if old, err := tx.ServiceChargePlan.Query().Where(servicechargeplan.Code("SC_POS_MICRO_1PCT")).Only(ctx); err == nil {
		if old.IsActive {
			if _, err := tx.ServiceChargePlan.UpdateOneID(old.ID).SetIsActive(false).Save(ctx); err != nil {
				return fmt.Errorf("deactivate SC_POS_MICRO_1PCT: %w", err)
			}
			log.Println("  ✓ Deprecated SC_POS_MICRO_1PCT (superseded by SC_POS_FLEX)")
		}
	} else if !ent.IsNotFound(err) {
		return fmt.Errorf("lookup SC_POS_MICRO_1PCT: %w", err)
	}

	// Deprecate SC_ISP_BILLING — ISP billing is NOT a per-transaction service charge.
	// It's a base plan (KES 500/mo) + threshold-gated 3% above KES 10k monthly sales
	// + per-PPPoE-subscriber fee, all computed by the billing engine from the
	// ISP_BILLING_STARTER plan's tier_limits (cmd/seed/plans_isp_billing.go).
	if old, err := tx.ServiceChargePlan.Query().Where(servicechargeplan.Code("SC_ISP_BILLING")).Only(ctx); err == nil {
		if old.IsActive {
			if _, err := tx.ServiceChargePlan.UpdateOneID(old.ID).SetIsActive(false).SetIsDefault(false).Save(ctx); err != nil {
				return fmt.Errorf("deactivate SC_ISP_BILLING: %w", err)
			}
			log.Println("  ✓ Deprecated SC_ISP_BILLING (ISP billing moved to base-plan + usage params)")
		}
	} else if !ent.IsNotFound(err) {
		return fmt.Errorf("lookup SC_ISP_BILLING: %w", err)
	}

	log.Println("  ✓ Service charge plans seeded")
	return nil
}
