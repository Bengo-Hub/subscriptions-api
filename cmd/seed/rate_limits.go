package main

import (
	"context"
	"fmt"
	"log"

	"github.com/google/uuid"

	"github.com/bengobox/subscription-service/internal/ent"
)

// ── Rate Limit Configs ──────────────────────────────────────────────────────

func seedRateLimitConfigs(ctx context.Context, tx *ent.Tx) error {
	type rlDef struct {
		serviceName       string
		keyType           string
		endpointPattern   string
		requestsPerWindow int
		windowSeconds     int
		burstMultiplier   float64
		description       string
	}

	configs := []rlDef{
		{
			serviceName:       "subscriptions-api",
			keyType:           "ip",
			endpointPattern:   "*",
			requestsPerWindow: 120,
			windowSeconds:     60,
			burstMultiplier:   2.0,
			description:       "Default IP-based rate limit for subscriptions-api",
		},
		{
			serviceName:       "subscriptions-api",
			keyType:           "tenant",
			endpointPattern:   "*",
			requestsPerWindow: 300,
			windowSeconds:     60,
			burstMultiplier:   1.5,
			description:       "Default tenant-based rate limit for subscriptions-api",
		},
		{
			serviceName:       "subscriptions-api",
			keyType:           "user",
			endpointPattern:   "*",
			requestsPerWindow: 60,
			windowSeconds:     60,
			burstMultiplier:   1.5,
			description:       "Default user-based rate limit for subscriptions-api",
		},
		{
			serviceName:       "subscriptions-api",
			keyType:           "endpoint",
			endpointPattern:   "/api/v1/subscription",
			requestsPerWindow: 30,
			windowSeconds:     60,
			burstMultiplier:   1.0,
			description:       "Rate limit for subscription creation/modification",
		},
		{
			serviceName:       "subscriptions-api",
			keyType:           "endpoint",
			endpointPattern:   "/api/v1/usage/report",
			requestsPerWindow: 600,
			windowSeconds:     60,
			burstMultiplier:   3.0,
			description:       "Higher limit for usage reporting (called by other microservices)",
		},
		// treasury-api's external eTIMS API — first real cross-service consumer of this table
		// (via subscriptions-api's GET /tenants/{id}/rate-limit). A tenant on an ETIMS_API_*
		// plan resolves its limit from tier_limits_json["api_requests_per_minute"] instead (see
		// cmd/seed/etims_api.go); these rows are the fallback for any other tenant/pattern.
		{
			serviceName:       "treasury-api",
			keyType:           "tenant",
			endpointPattern:   "/api/v1/external/etims/*",
			requestsPerWindow: 60,
			windowSeconds:     60,
			burstMultiplier:   1.5,
			description:       "Fallback per-tenant rate limit for the external eTIMS API when no plan-tier limit resolves",
		},
		{
			serviceName:       "treasury-api",
			keyType:           "tenant",
			endpointPattern:   "*",
			requestsPerWindow: 60,
			windowSeconds:     60,
			burstMultiplier:   1.5,
			description:       "Default tenant-based rate limit for treasury-api's external API surface",
		},
	}

	for _, c := range configs {
		id := uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf("rl:%s:%s:%s", c.serviceName, c.keyType, c.endpointPattern)))
		exists, _ := tx.RateLimitConfig.Get(ctx, id)
		if exists != nil {
			continue
		}
		_, err := tx.RateLimitConfig.Create().
			SetID(id).
			SetServiceName(c.serviceName).
			SetKeyType(c.keyType).
			SetEndpointPattern(c.endpointPattern).
			SetRequestsPerWindow(c.requestsPerWindow).
			SetWindowSeconds(c.windowSeconds).
			SetBurstMultiplier(c.burstMultiplier).
			SetIsActive(true).
			SetDescription(c.description).
			Save(ctx)
		if err != nil {
			return fmt.Errorf("create rate limit config %s/%s/%s: %w", c.serviceName, c.keyType, c.endpointPattern, err)
		}
	}

	log.Println("  ✓ Rate limit configs seeded")
	return nil
}
