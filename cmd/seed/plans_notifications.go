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

// ── Notifications Plans ───────────────────────────────────────────────────────
// Notifications-service handles multi-channel delivery: email, SMS, push, WhatsApp.
// Email is rate-limited by plan; SMS/push/WhatsApp follow their own cost models.

func seedNotificationsPlans(ctx context.Context, tx *ent.Tx) error {
	now := time.Now()
	serviceTag := "notifications"

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
		// ── Monthly plans ────────────────────────────────────────────────────
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("notifications:STARTER")),
			planCode:     "NOTIFICATIONS_STARTER",
			name:         "Notifications Starter",
			description:  "Essential multi-channel notifications: up to 5,000 emails/month, push notifications, and basic SMS.",
			billingCycle: "MONTHLY",
			price:        1500.0,
			tierOrder:    1,
			tierLimits: map[string]any{
				"max_emails_monthly": 5000,
				"max_sms_monthly":    500,
				"max_push_monthly":   10000,
				"max_channels":       3,
				"max_templates":      20,
			},
			features: []string{
				"email_notifications", "sms_notifications", "push_notifications",
				"notification_templates", "delivery_tracking",
			},
		},
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("notifications:GROWTH")),
			planCode:     "NOTIFICATIONS_GROWTH",
			name:         "Notifications Growth",
			description:  "Full multi-channel suite: 25,000 emails/month, WhatsApp, priority delivery queues, and advanced analytics.",
			billingCycle: "MONTHLY",
			price:        4500.0,
			tierOrder:    2,
			tierLimits: map[string]any{
				"max_emails_monthly": 25000,
				"max_sms_monthly":    2500,
				"max_push_monthly":   100000,
				"max_channels":       5,
				"max_templates":      100,
			},
			features: []string{
				"email_notifications", "sms_notifications", "push_notifications",
				"whatsapp_notifications", "notification_templates", "delivery_tracking",
				"advanced_analytics", "priority_queue", "webhook_delivery",
			},
		},
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("notifications:PROFESSIONAL")),
			planCode:     "NOTIFICATIONS_PROFESSIONAL",
			name:         "Notifications Professional",
			description:  "Enterprise notifications: unlimited emails, all channels, custom webhooks, audit trail, and dedicated queue.",
			billingCycle: "MONTHLY",
			price:        9500.0,
			tierOrder:    3,
			tierLimits: map[string]any{
				"max_emails_monthly": -1,
				"max_sms_monthly":    -1,
				"max_push_monthly":   -1,
				"max_channels":       -1,
				"max_templates":      -1,
			},
			features: []string{
				"email_notifications", "sms_notifications", "push_notifications",
				"whatsapp_notifications", "notification_templates", "delivery_tracking",
				"advanced_analytics", "priority_queue", "webhook_delivery",
				"audit_trail", "custom_channels", "api_access", "dedicated_queue",
			},
		},

		// ── Annual plans (10-month pricing) ──────────────────────────────────
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("notifications:STARTER_YEARLY")),
			planCode:     "NOTIFICATIONS_STARTER_YEARLY",
			name:         "Notifications Starter — Annual",
			description:  "Essential multi-channel notifications with annual billing savings.",
			billingCycle: "ANNUAL",
			price:        15000.0,
			tierOrder:    1,
			tierLimits: map[string]any{
				"max_emails_monthly": 5000,
				"max_sms_monthly":    500,
				"max_push_monthly":   10000,
				"max_channels":       3,
				"max_templates":      20,
			},
			features: []string{
				"email_notifications", "sms_notifications", "push_notifications",
				"notification_templates", "delivery_tracking",
			},
		},
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("notifications:GROWTH_YEARLY")),
			planCode:     "NOTIFICATIONS_GROWTH_YEARLY",
			name:         "Notifications Growth — Annual",
			description:  "Full multi-channel suite with WhatsApp and advanced analytics. Save with annual billing.",
			billingCycle: "ANNUAL",
			price:        45000.0,
			tierOrder:    2,
			tierLimits: map[string]any{
				"max_emails_monthly": 25000,
				"max_sms_monthly":    2500,
				"max_push_monthly":   100000,
				"max_channels":       5,
				"max_templates":      100,
			},
			features: []string{
				"email_notifications", "sms_notifications", "push_notifications",
				"whatsapp_notifications", "notification_templates", "delivery_tracking",
				"advanced_analytics", "priority_queue", "webhook_delivery",
			},
		},
		{
			id:           uuid.NewSHA1(uuid.NameSpaceOID, []byte("notifications:PROFESSIONAL_YEARLY")),
			planCode:     "NOTIFICATIONS_PROFESSIONAL_YEARLY",
			name:         "Notifications Professional — Annual",
			description:  "Enterprise notifications: unlimited everything, all channels, dedicated queue. Save with annual billing.",
			billingCycle: "ANNUAL",
			price:        95000.0,
			tierOrder:    3,
			tierLimits: map[string]any{
				"max_emails_monthly": -1,
				"max_sms_monthly":    -1,
				"max_push_monthly":   -1,
				"max_channels":       -1,
				"max_templates":      -1,
			},
			features: []string{
				"email_notifications", "sms_notifications", "push_notifications",
				"whatsapp_notifications", "notification_templates", "delivery_tracking",
				"advanced_analytics", "priority_queue", "webhook_delivery",
				"audit_trail", "custom_channels", "api_access", "dedicated_queue",
			},
		},
	}

	for _, p := range plans {
		existing, err := tx.SubscriptionPlan.Get(ctx, p.id)
		if err != nil && !ent.IsNotFound(err) {
			return fmt.Errorf("lookup notifications plan %s: %w", p.planCode, err)
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
			return fmt.Errorf("upsert notifications plan %s: %w", p.planCode, err)
		}
		if err := seedPlanFeatures(ctx, tx, p.id, p.features); err != nil {
			return fmt.Errorf("seed features for notifications plan %s: %w", p.planCode, err)
		}
		log.Printf("  notifications plan: %s (%s, KES %.0f)", p.name, p.billingCycle, p.price)
	}
	return nil
}
