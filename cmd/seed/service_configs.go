package main

import (
	"context"
	"fmt"
	"log"

	"github.com/google/uuid"

	"github.com/bengobox/subscription-service/internal/ent"
)

// ── Service Configs ─────────────────────────────────────────────────────────

func seedServiceConfigs(ctx context.Context, tx *ent.Tx) error {
	type scDef struct {
		configKey   string
		configValue string
		configType  string
		description string
		isSecret    bool
	}

	configs := []scDef{
		{
			configKey:   "subscriptions.trial_days",
			configValue: "14",
			configType:  "int",
			description: "Default number of trial days for new subscriptions",
		},
		{
			configKey:   "subscriptions.max_plans_per_tenant",
			configValue: "1",
			configType:  "int",
			description: "Maximum active subscription plans per tenant",
		},
		{
			configKey:   "subscriptions.grace_period_days",
			configValue: "7",
			configType:  "int",
			description: "Days after expiration before access is revoked",
		},
		{
			configKey:   "subscriptions.auto_renew_default",
			configValue: "true",
			configType:  "bool",
			description: "Whether new subscriptions auto-renew by default",
		},
		{
			configKey:   "subscriptions.usage_reporting_interval_seconds",
			configValue: "300",
			configType:  "int",
			description: "Minimum interval between usage reports from the same service",
		},
		{
			configKey:   "subscriptions.feature_cache_ttl_seconds",
			configValue: "60",
			configType:  "int",
			description: "TTL for feature entitlement cache in Redis",
		},
		{
			configKey:   "subscriptions.rbac_enabled",
			configValue: "true",
			configType:  "bool",
			description: "Whether RBAC enforcement is active",
		},
	}

	for _, c := range configs {
		id := uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf("sc::%s", c.configKey)))
		exists, _ := tx.ServiceConfig.Get(ctx, id)
		if exists != nil {
			continue
		}
		_, err := tx.ServiceConfig.Create().
			SetID(id).
			SetConfigKey(c.configKey).
			SetConfigValue(c.configValue).
			SetConfigType(c.configType).
			SetDescription(c.description).
			SetIsSecret(c.isSecret).
			Save(ctx)
		if err != nil {
			return fmt.Errorf("create service config %s: %w", c.configKey, err)
		}
	}

	log.Println("  ✓ Service configs seeded")
	return nil
}
