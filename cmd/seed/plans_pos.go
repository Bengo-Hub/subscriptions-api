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
// License-based plans for POS — priced per device or per device group.
// These complement the pos-suite bundle plans (which cover the software subscription).
// Tenants choose either: subscription (monthly/yearly via pos-suite bundle) OR
// a per-device license (one-time or recurring device seat fee).

func seedPOSLicensePlans(ctx context.Context, tx *ent.Tx) error {
	now := time.Now()
	serviceTag := "pos"

	type planDef struct {
		id           uuid.UUID
		planCode     string
		name         string
		description  string
		billingCycle string
		price        float64
		tierOrder    int
		tierLimits   map[string]any
		features     []string
	}

	plans := []planDef{
		// ── Monthly device-seat subscriptions ─────────────────────────────
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("pos:device:1")),
			planCode:     "POS_DEVICE_1",
			name:         "POS — 1 Device",
			description:  "Monthly subscription for 1 POS terminal. Includes full POS software, daily reports, and receipt printing.",
			billingCycle: "MONTHLY",
			price:        800.0,
			tierOrder:    1,
			tierLimits:   map[string]any{"max_devices": 1, "max_cashiers": 2},
			features:     []string{"pos_terminal", "order_management", "receipt_printing", "daily_reports", "mpesa_pos"},
		},
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("pos:device:5")),
			planCode:     "POS_DEVICE_5",
			name:         "POS — 5 Devices",
			description:  "Monthly subscription for up to 5 POS terminals. Includes multi-cashier management and shift reporting.",
			billingCycle: "MONTHLY",
			price:        3500.0,
			tierOrder:    2,
			tierLimits:   map[string]any{"max_devices": 5, "max_cashiers": 10},
			features:     []string{"pos_terminal", "order_management", "receipt_printing", "daily_reports", "mpesa_pos", "multi_cashier", "shift_reports", "table_management"},
		},
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("pos:device:10")),
			planCode:     "POS_DEVICE_10",
			name:         "POS — 10 Devices",
			description:  "Monthly subscription for up to 10 POS terminals. Best for multi-outlet chains.",
			billingCycle: "MONTHLY",
			price:        6000.0,
			tierOrder:    3,
			tierLimits:   map[string]any{"max_devices": 10, "max_cashiers": -1},
			features:     []string{"pos_terminal", "order_management", "receipt_printing", "daily_reports", "mpesa_pos", "multi_cashier", "shift_reports", "table_management", "multi_outlet", "advanced_analytics"},
		},
		// ── Annual device-seat subscriptions ──────────────────────────────
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("pos:device:1:yearly")),
			planCode:     "POS_DEVICE_1_YEARLY",
			name:         "POS — 1 Device (Annual)",
			description:  "Annual subscription for 1 POS terminal. Save 2 months vs monthly.",
			billingCycle: "ANNUAL",
			price:        8000.0,
			tierOrder:    1,
			tierLimits:   map[string]any{"max_devices": 1, "max_cashiers": 2},
			features:     []string{"pos_terminal", "order_management", "receipt_printing", "daily_reports", "mpesa_pos"},
		},
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("pos:device:5:yearly")),
			planCode:     "POS_DEVICE_5_YEARLY",
			name:         "POS — 5 Devices (Annual)",
			description:  "Annual subscription for up to 5 POS terminals. Save 2 months vs monthly.",
			billingCycle: "ANNUAL",
			price:        35000.0,
			tierOrder:    2,
			tierLimits:   map[string]any{"max_devices": 5, "max_cashiers": 10},
			features:     []string{"pos_terminal", "order_management", "receipt_printing", "daily_reports", "mpesa_pos", "multi_cashier", "shift_reports", "table_management"},
		},
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("pos:device:10:yearly")),
			planCode:     "POS_DEVICE_10_YEARLY",
			name:         "POS — 10 Devices (Annual)",
			description:  "Annual subscription for up to 10 POS terminals. Save 2 months vs monthly.",
			billingCycle: "ANNUAL",
			price:        60000.0,
			tierOrder:    3,
			tierLimits:   map[string]any{"max_devices": 10, "max_cashiers": -1},
			features:     []string{"pos_terminal", "order_management", "receipt_printing", "daily_reports", "mpesa_pos", "multi_cashier", "shift_reports", "table_management", "multi_outlet", "advanced_analytics"},
		},
		// ── One-time per-device perpetual license ─────────────────────────
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("pos:license:per_device")),
			planCode:     "POS_LICENSE_PER_DEVICE",
			name:         "POS — Per Device Perpetual License",
			description:  "One-time perpetual license per POS device. Owns the software for that device indefinitely. Cloud sync and updates require an active subscription.",
			billingCycle: "ONE_TIME",
			price:        15000.0,
			tierOrder:    10,
			tierLimits:   map[string]any{"max_devices": 1, "max_cashiers": -1},
			features:     []string{"pos_terminal", "order_management", "receipt_printing", "daily_reports", "mpesa_pos", "multi_cashier", "shift_reports", "table_management"},
		},
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("pos:license:complete")),
			planCode:     "POS_LICENSE_COMPLETE",
			name:         "POS — Complete Perpetual License",
			description:  "One-time perpetual license for unlimited POS devices at a single outlet. Full feature set included.",
			billingCycle: "ONE_TIME",
			price:        120000.0,
			tierOrder:    11,
			tierLimits:   map[string]any{"max_devices": -1, "max_cashiers": -1},
			features:     []string{"pos_terminal", "order_management", "receipt_printing", "daily_reports", "mpesa_pos", "multi_cashier", "shift_reports", "table_management", "multi_outlet", "advanced_analytics", "api_access"},
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
				SetTierOrder(p.tierOrder).SetTierLimitsJSON(p.tierLimits).SetServiceTag(serviceTag).SetUpdatedAt(now).Save(ctx)
		} else {
			_, err = tx.SubscriptionPlan.Create().
				SetID(p.id).SetPlanCode(p.planCode).SetName(p.name).SetDescription(p.description).
				SetBillingCycle(p.billingCycle).SetPlanType(subscriptionplan.PlanTypeSTANDALONE_SERVICE).
				SetBasePrice(p.price).SetCurrency("KES").SetIsActive(true).SetIsPublic(true).
				SetTierOrder(p.tierOrder).SetTierLimitsJSON(p.tierLimits).SetServiceTag(serviceTag).
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
