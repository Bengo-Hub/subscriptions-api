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

// ── POS Device License Plans ──────────────────────────────────────────────────
// Priced per device (monthly/annual) or as a one-time perpetual license.
// All plans include table management, shift reports, and KDS where applicable.

func seedPOSLicensePlans(ctx context.Context, tx *ent.Tx) error {
	now := time.Now()
	serviceTag := "pos"

	type planDef struct {
		id            uuid.UUID
		planCode      string
		name          string
		description   string
		billingCycle  string
		price         float64
		tierOrder     int
		freeTrialDays int
		tierLimits    map[string]any
		features      []string
	}

	basePOSFeatures := []string{
		"pos_terminal", "order_management", "receipt_printing", "daily_reports",
		"mpesa_pos", "shift_reports", "table_management", "kds", "offline_sync",
	}
	allPOSFeatures := append(append([]string{}, basePOSFeatures...), "multi_cashier")

	plans := []planDef{
		// ── Monthly device-seat subscriptions ──────────────────────────────
		{
			id:            uuid.NewSHA1(uuid.NameSpaceOID, []byte("pos:device:1")),
			planCode:      "POS_DEVICE_1",
			name:          "POS — 1 Device",
			description:   "Monthly subscription for 1 POS terminal. Full POS software, shift reporting, table management, and receipt printing.",
			billingCycle:  "MONTHLY",
			price:         800.0,
			tierOrder:     1,
			freeTrialDays: 30,
			tierLimits:    map[string]any{"max_devices": 1, "max_cashiers": 2, "max_tables": 20},
			features:      basePOSFeatures,
		},
		{
			id:            uuid.NewSHA1(uuid.NameSpaceOID, []byte("pos:device:5")),
			planCode:      "POS_DEVICE_5",
			name:          "POS — 5 Devices",
			description:   "Monthly subscription for up to 5 POS terminals. Multi-cashier, KDS, and advanced reporting.",
			billingCycle:  "MONTHLY",
			price:         3500.0,
			tierOrder:     2,
			freeTrialDays: 14,
			tierLimits:    map[string]any{"max_devices": 5, "max_cashiers": 10, "max_tables": 50},
			features:      allPOSFeatures,
		},
		{
			id:            uuid.NewSHA1(uuid.NameSpaceOID, []byte("pos:device:10")),
			planCode:      "POS_DEVICE_10",
			name:          "POS — 10 Devices",
			description:   "Monthly subscription for up to 10 POS terminals. Best for multi-outlet chains.",
			billingCycle:  "MONTHLY",
			price:         6000.0,
			tierOrder:     3,
			freeTrialDays: 14,
			tierLimits:    map[string]any{"max_devices": 10, "max_cashiers": -1, "max_tables": -1, "max_rooms": 100, "max_conference_events": -1},
			features:      append(allPOSFeatures, "multi_outlet", "advanced_analytics", "hotel_module", "conference_events", "happy_hour"),
		},
		// ── Annual device-seat subscriptions ───────────────────────────────
		{
			id:            uuid.NewSHA1(uuid.NameSpaceOID, []byte("pos:device:1:yearly")),
			planCode:      "POS_DEVICE_1_YEARLY",
			name:          "POS — 1 Device (Annual)",
			description:   "Annual subscription for 1 POS terminal. Save 2 months vs monthly.",
			billingCycle:  "ANNUAL",
			price:         8000.0,
			tierOrder:     1,
			freeTrialDays: 30,
			tierLimits:    map[string]any{"max_devices": 1, "max_cashiers": 2, "max_tables": 20},
			features:      basePOSFeatures,
		},
		{
			id:            uuid.NewSHA1(uuid.NameSpaceOID, []byte("pos:device:5:yearly")),
			planCode:      "POS_DEVICE_5_YEARLY",
			name:          "POS — 5 Devices (Annual)",
			description:   "Annual subscription for up to 5 POS terminals. Save 2 months vs monthly.",
			billingCycle:  "ANNUAL",
			price:         35000.0,
			tierOrder:     2,
			freeTrialDays: 14,
			tierLimits:    map[string]any{"max_devices": 5, "max_cashiers": 10, "max_tables": 50},
			features:      allPOSFeatures,
		},
		{
			id:            uuid.NewSHA1(uuid.NameSpaceOID, []byte("pos:device:10:yearly")),
			planCode:      "POS_DEVICE_10_YEARLY",
			name:          "POS — 10 Devices (Annual)",
			description:   "Annual subscription for up to 10 POS terminals. Save 2 months vs monthly.",
			billingCycle:  "ANNUAL",
			price:         60000.0,
			tierOrder:     3,
			freeTrialDays: 14,
			tierLimits:    map[string]any{"max_devices": 10, "max_cashiers": -1, "max_tables": -1, "max_rooms": 100, "max_conference_events": -1},
			features:      append(allPOSFeatures, "multi_outlet", "advanced_analytics", "hotel_module", "conference_events", "happy_hour"),
		},
		// ── One-time perpetual licenses ─────────────────────────────────────
		{
			id:            uuid.NewSHA1(uuid.NameSpaceOID, []byte("pos:license:per_device")),
			planCode:      "POS_LICENSE_PER_DEVICE",
			name:          "POS — Per Device Perpetual License",
			description:   "One-time license per POS device. Cloud sync and updates require an active subscription.",
			billingCycle:  "ONE_TIME",
			price:         15000.0,
			tierOrder:     10,
			freeTrialDays: 0,
			tierLimits:    map[string]any{"max_devices": 1, "max_cashiers": -1, "max_tables": 50},
			features:      allPOSFeatures,
		},
		{
			id:            uuid.NewSHA1(uuid.NameSpaceOID, []byte("pos:license:complete")),
			planCode:      "POS_LICENSE_COMPLETE",
			name:          "POS — Complete Perpetual License",
			description:   "One-time license for unlimited POS devices at a single outlet. Full feature set.",
			billingCycle:  "ONE_TIME",
			price:         120000.0,
			tierOrder:     11,
			freeTrialDays: 0,
			tierLimits:    map[string]any{"max_devices": -1, "max_cashiers": -1, "max_tables": -1, "max_rooms": -1, "max_conference_events": -1},
			features:      append(allPOSFeatures, "multi_outlet", "advanced_analytics", "hotel_module", "conference_events", "happy_hour", "api_access"),
		},
	}

	for _, p := range plans {
		existing, err := tx.SubscriptionPlan.Get(ctx, p.id)
		if err != nil && !ent.IsNotFound(err) {
			return fmt.Errorf("lookup pos license plan %s: %w", p.planCode, err)
		}
		if existing != nil {
			_, err = tx.SubscriptionPlan.UpdateOneID(p.id).
				SetPlanCode(p.planCode).SetName(p.name).SetDescription(p.description).
				SetBillingCycle(p.billingCycle).SetPlanType(subscriptionplan.PlanTypeSTANDALONE_SERVICE).
				SetBasePrice(p.price).SetCurrency("KES").SetIsActive(true).SetIsPublic(true).
				SetTierOrder(p.tierOrder).SetFreeTrialDays(p.freeTrialDays).
				SetTierLimitsJSON(p.tierLimits).SetServiceTag(serviceTag).SetUpdatedAt(now).Save(ctx)
		} else {
			_, err = tx.SubscriptionPlan.Create().
				SetID(p.id).SetPlanCode(p.planCode).SetName(p.name).SetDescription(p.description).
				SetBillingCycle(p.billingCycle).SetPlanType(subscriptionplan.PlanTypeSTANDALONE_SERVICE).
				SetBasePrice(p.price).SetCurrency("KES").SetIsActive(true).SetIsPublic(true).
				SetTierOrder(p.tierOrder).SetFreeTrialDays(p.freeTrialDays).
				SetTierLimitsJSON(p.tierLimits).SetServiceTag(serviceTag).
				SetCreatedAt(now).SetUpdatedAt(now).Save(ctx)
		}
		if err != nil {
			return fmt.Errorf("upsert pos license plan %s: %w", p.planCode, err)
		}
		if err := seedPlanFeatures(ctx, tx, p.id, p.features); err != nil {
			return fmt.Errorf("seed features for pos license plan %s: %w", p.planCode, err)
		}
		log.Printf("  pos license plan: %s (%s, KES %.0f)", p.name, p.billingCycle, p.price)
	}
	return nil
}

// retireLegacyPOSPlans deactivates the old per-device/seat POS plans (POS_DEVICE_* and the
// POS_LICENSE_* perpetual licenses). They are superseded by the industry product-line tiers
// (POS_HOSP_*/POS_DUKA_*/POS_DAWA_*). Deactivated (is_active=false, is_public=false) rather than
// hard-deleted so any tenant still pointed at one keeps resolving; they just drop out of the
// public catalog. Idempotent.
func retireLegacyPOSPlans(ctx context.Context, tx *ent.Tx) error {
	legacy := []string{
		"POS_DEVICE_1", "POS_DEVICE_5", "POS_DEVICE_10",
		"POS_DEVICE_1_YEARLY", "POS_DEVICE_5_YEARLY", "POS_DEVICE_10_YEARLY",
		"POS_LICENSE_PER_DEVICE", "POS_LICENSE_COMPLETE",
	}
	n, err := tx.SubscriptionPlan.Update().
		Where(subscriptionplan.PlanCodeIn(legacy...), subscriptionplan.IsActiveEQ(true)).
		SetIsActive(false).
		SetIsPublic(false).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("retire legacy pos plans: %w", err)
	}
	if n > 0 {
		log.Printf("  ✓ Retired %d legacy POS device/license plans (superseded by POS_HOSP/DUKA/DAWA tiers)", n)
	}
	return nil
}
