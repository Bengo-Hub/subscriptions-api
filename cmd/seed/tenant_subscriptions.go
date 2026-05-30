package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/productsubscription"
	"github.com/bengobox/subscription-service/internal/ent/tenantsubscription"
	"github.com/bengobox/subscription-service/internal/modules/tenant"
)

func seedAllTenantSubscriptions(ctx context.Context, tx *ent.Tx, syncer *tenant.Syncer) error {
	// Clean slate: delete all existing subscription records before re-seeding.
	// This ensures no stale TRIAL/ACTIVE rows survive between seed runs.
	existingSubs, err := tx.TenantSubscription.Query().All(ctx)
	if err != nil {
		return fmt.Errorf("query existing subscriptions for cleanup: %w", err)
	}
	for _, sub := range existingSubs {
		// Delete child product subscriptions first (FK constraint)
		if _, err := tx.ProductSubscription.Delete().
			Where(productsubscription.TenantSubscriptionIDEQ(sub.ID)).
			Exec(ctx); err != nil {
			return fmt.Errorf("delete product subscriptions for %s: %w", sub.ID, err)
		}
		if err := tx.TenantSubscription.DeleteOne(sub).Exec(ctx); err != nil {
			return fmt.Errorf("delete subscription %s: %w", sub.ID, err)
		}
	}
	log.Printf("  cleaned %d existing subscription(s) for fresh seed", len(existingSubs))

	// Platform owner (codevertex) is intentionally excluded — the platform owner has
	// unrestricted access to all services without a subscription record.
	// All other tenants must have a subscription record for feature gating to work.

	type tenantDef struct {
		subID         uuid.UUID
		slug          string
		name          string
		plan          string
		bundleCode    string
		status        tenantsubscription.Status
		periodStart   time.Time
		periodEnd     time.Time
		extraProducts []string // product codes to activate beyond the bundle defaults
	}

	now := time.Now()
	trialEnd := now.Add(14 * 24 * time.Hour)

	tenantDefs := []tenantDef{
		{
			subID:       uuid.MustParse("30000000-0000-0000-0000-000000000002"),
			slug:        "mss",
			name:        "Masterspace Solutions",
			plan:        "ORDERING_GROWTH",
			bundleCode:  "ordering",
			status:      tenantsubscription.StatusTRIAL,
			periodStart: now,
			periodEnd:   trialEnd,
		},
		{
			subID:       uuid.MustParse("30000000-0000-0000-0000-000000000003"),
			slug:        "kura",
			name:        "Kenya Urban Roads Authority",
			plan:        "TRULOAD_STARTER",
			bundleCode:  "truload",
			status:      tenantsubscription.StatusTRIAL,
			periodStart: now,
			periodEnd:   trialEnd,
		},
		{
			subID:       uuid.MustParse("30000000-0000-0000-0000-000000000004"),
			slug:        "ultichange",
			name:        "UltiChange",
			plan:        "ORDERING_STARTER",
			bundleCode:  "ordering",
			status:      tenantsubscription.StatusTRIAL,
			periodStart: now,
			periodEnd:   trialEnd,
		},
		// codevertex-demo intentionally has NO subscription record.
		// All services bypass subscription gating for tenants with slug "codevertex-demo"
		// or the is_demo=true JWT claim. See subscription.go Get() for bypass logic.
	}

	planIDs := map[string]uuid.UUID{
		"ORDERING_STARTER":      uuid.NewSHA1(uuid.NameSpaceOID, []byte("plan:ORDERING_STARTER")),
		"ORDERING_GROWTH":       uuid.NewSHA1(uuid.NameSpaceOID, []byte("plan:ORDERING_GROWTH")),
		"ORDERING_PROFESSIONAL": uuid.NewSHA1(uuid.NameSpaceOID, []byte("plan:ORDERING_PROFESSIONAL")),
		"TRULOAD_STARTER":       uuid.NewSHA1(uuid.NameSpaceOID, []byte("truload:org:STARTER")),
		"TRULOAD_GROWTH":        uuid.NewSHA1(uuid.NameSpaceOID, []byte("truload:org:GROWTH")),
	}

	for _, td := range tenantDefs {
		tenantID, err := syncer.SyncTenant(ctx, td.slug)
		if err != nil {
			log.Printf("  [SKIP] tenant %q: %v", td.slug, err)
			continue
		}

		planID := planIDs[td.plan]

		meta := map[string]any{"seeded": true, "tenant_name": td.name, "tier": td.plan}
		b := tx.TenantSubscription.Create().
			SetID(td.subID).
			SetTenantID(tenantID).
			SetPlanID(planID).
			SetStatus(td.status).
			SetCurrentPeriodStart(td.periodStart).
			SetCurrentPeriodEnd(td.periodEnd).
			SetBundleCode(td.bundleCode).
			SetMetadata(meta)
		if td.status == tenantsubscription.StatusTRIAL {
			b = b.SetTrialEndsAt(td.periodEnd)
		}
		if _, err = b.Save(ctx); err != nil {
			return fmt.Errorf("create subscription for %s: %w", td.slug, err)
		}
		log.Printf("  subscription: %s → %s (%s, until %s)", td.name, td.plan, td.status, td.periodEnd.Format("2006-01-02"))

		// Activate bundle products
		switch td.bundleCode {
		case "truload":
			if err := activateTruLoadProducts(ctx, tx, td.subID, now); err != nil {
				return fmt.Errorf("activate truload products for %s: %w", td.slug, err)
			}
		case "ordering", "delivery": // "delivery" kept for backward-compat with existing prod rows
			if err := activateDeliveryProducts(ctx, tx, td.subID, now); err != nil {
				return fmt.Errorf("activate ordering products for %s: %w", td.slug, err)
			}
		default:
			if err := activateDeliveryProducts(ctx, tx, td.subID, now); err != nil {
				return fmt.Errorf("activate ordering products for %s: %w", td.slug, err)
			}
		}

		// Activate any extra products beyond the bundle
		for _, code := range td.extraProducts {
			if err := activateExtraProduct(ctx, tx, td.subID, code, now); err != nil {
				return fmt.Errorf("activate extra product %s for %s: %w", code, td.slug, err)
			}
		}
	}

	return nil
}

func activateTruLoadProducts(ctx context.Context, tx *ent.Tx, subID uuid.UUID, now time.Time) error {
	truloadProducts := []struct {
		id   uuid.UUID
		code string
	}{
		{uuid.MustParse("10000000-0000-0000-0000-000000000050"), "truload"},
		{uuid.MustParse("10000000-0000-0000-0000-000000000012"), "treasury"},
	}

	for _, p := range truloadProducts {
		existingPS, err := tx.ProductSubscription.Query().
			Where(
				productsubscription.TenantSubscriptionIDEQ(subID),
				productsubscription.ProductCodeEQ(p.code),
			).
			First(ctx)
		if err != nil && !ent.IsNotFound(err) {
			return fmt.Errorf("lookup product subscription %s: %w", p.code, err)
		}

		if existingPS != nil {
			_, err = tx.ProductSubscription.UpdateOne(existingPS).
				SetProductID(p.id).
				SetStatus(productsubscription.StatusActive).
				SetActivatedAt(now).
				Save(ctx)
			if err != nil {
				return fmt.Errorf("update product subscription %s: %w", p.code, err)
			}
		} else {
			_, err = tx.ProductSubscription.Create().
				SetTenantSubscriptionID(subID).
				SetProductCode(p.code).
				SetProductID(p.id).
				SetStatus(productsubscription.StatusActive).
				SetActivatedAt(now).
				Save(ctx)
			if err != nil {
				return fmt.Errorf("create product subscription %s: %w", p.code, err)
			}
		}
	}

	return nil
}

func activateDeliveryProducts(ctx context.Context, tx *ent.Tx, subID uuid.UUID, now time.Time) error {
	deliveryProducts := []struct {
		id   uuid.UUID
		code string
	}{
		{uuid.MustParse("10000000-0000-0000-0000-000000000010"), "ordering"},
		{uuid.MustParse("10000000-0000-0000-0000-000000000011"), "logistics"},
		{uuid.MustParse("10000000-0000-0000-0000-000000000012"), "treasury"},
		{uuid.MustParse("10000000-0000-0000-0000-000000000021"), "storefront"},
	}

	for _, p := range deliveryProducts {
		existingPS, err := tx.ProductSubscription.Query().
			Where(
				productsubscription.TenantSubscriptionIDEQ(subID),
				productsubscription.ProductCodeEQ(p.code),
			).
			First(ctx)
		if err != nil && !ent.IsNotFound(err) {
			return fmt.Errorf("lookup product subscription %s: %w", p.code, err)
		}

		if existingPS != nil {
			_, err = tx.ProductSubscription.UpdateOne(existingPS).
				SetProductID(p.id).
				SetStatus(productsubscription.StatusActive).
				SetActivatedAt(now).
				Save(ctx)
			if err != nil {
				return fmt.Errorf("update product subscription %s: %w", p.code, err)
			}
		} else {
			_, err = tx.ProductSubscription.Create().
				SetTenantSubscriptionID(subID).
				SetProductCode(p.code).
				SetProductID(p.id).
				SetStatus(productsubscription.StatusActive).
				SetActivatedAt(now).
				Save(ctx)
			if err != nil {
				return fmt.Errorf("create product subscription %s: %w", p.code, err)
			}
		}
	}

	return nil
}

// productCodeToID maps well-known product codes to their seeded UUIDs.
var productCodeToID = map[string]uuid.UUID{
	"auth":                  uuid.MustParse("10000000-0000-0000-0000-000000000001"),
	"notifications":         uuid.MustParse("10000000-0000-0000-0000-000000000002"),
	"subscription":          uuid.MustParse("10000000-0000-0000-0000-000000000003"),
	"ordering":              uuid.MustParse("10000000-0000-0000-0000-000000000010"),
	"logistics":             uuid.MustParse("10000000-0000-0000-0000-000000000011"),
	"treasury":              uuid.MustParse("10000000-0000-0000-0000-000000000012"),
	"pos":                   uuid.MustParse("10000000-0000-0000-0000-000000000020"),
	"storefront":            uuid.MustParse("10000000-0000-0000-0000-000000000021"),
	"truload":               uuid.MustParse("10000000-0000-0000-0000-000000000050"),
	"marketflow":            uuid.MustParse("10000000-0000-0000-0000-000000000040"),
	"inventory":             uuid.MustParse("10000000-0000-0000-0000-000000000060"),
	"erp":                   uuid.MustParse("10000000-0000-0000-0000-000000000070"),
}

// activateExtraProduct upserts a single product subscription on top of an existing bundle.
func activateExtraProduct(ctx context.Context, tx *ent.Tx, subID uuid.UUID, code string, now time.Time) error {
	productID, ok := productCodeToID[code]
	if !ok {
		return fmt.Errorf("unknown product code %q in activateExtraProduct", code)
	}

	existing, err := tx.ProductSubscription.Query().
		Where(
			productsubscription.TenantSubscriptionIDEQ(subID),
			productsubscription.ProductCodeEQ(code),
		).
		First(ctx)
	if err != nil && !ent.IsNotFound(err) {
		return fmt.Errorf("lookup extra product %s: %w", code, err)
	}

	if existing != nil {
		_, err = tx.ProductSubscription.UpdateOne(existing).
			SetProductID(productID).
			SetStatus(productsubscription.StatusActive).
			SetActivatedAt(now).
			Save(ctx)
	} else {
		_, err = tx.ProductSubscription.Create().
			SetTenantSubscriptionID(subID).
			SetProductCode(code).
			SetProductID(productID).
			SetStatus(productsubscription.StatusActive).
			SetActivatedAt(now).
			Save(ctx)
	}
	if err != nil {
		return fmt.Errorf("upsert extra product %s: %w", code, err)
	}
	log.Printf("    extra product activated: %s", code)
	return nil
}
