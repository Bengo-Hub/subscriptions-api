package main

import (
	"context"
	"fmt"
	"log"

	"github.com/google/uuid"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/product"
)

// ── Email Hosting (Stalwart) ─────────────────────────────────────────────────
// Zoho-Mail-like per-seat email hosting product. See
// .claude/plans/codevertex-email-hosting-service-plan.md Parts 3/4/6.

func seedEmailHostingProduct(ctx context.Context, tx *ent.Tx) error {
	id := uuid.NewSHA1(uuid.NameSpaceOID, []byte("product:email-hosting"))

	existing, err := tx.Product.Get(ctx, id)
	if err != nil && !ent.IsNotFound(err) {
		return fmt.Errorf("lookup email-hosting product: %w", err)
	}

	if existing != nil {
		if _, err := tx.Product.UpdateOneID(id).
			SetCode("email-hosting").
			SetName("Email Hosting").
			SetDescription("Per-user hosted email mailboxes (webmail, IMAP/SMTP, CalDAV/CardDAV on Standard+) — priced per license, not flat.").
			SetCategory(product.CategoryProduct).
			SetStatus(product.StatusActive).
			SetIsPlatform(false).
			SetIsBaseService(false).
			SetMonthlyPrice(0).
			SetYearlyPrice(0).
			SetOnetimePrice(0).
			SetIncludedInBundle(false).
			SetSortOrder(80).
			Save(ctx); err != nil {
			return fmt.Errorf("update email-hosting product: %w", err)
		}
	} else {
		if _, err := tx.Product.Create().
			SetID(id).
			SetCode("email-hosting").
			SetName("Email Hosting").
			SetDescription("Per-user hosted email mailboxes (webmail, IMAP/SMTP, CalDAV/CardDAV on Standard+) — priced per license, not flat.").
			SetCategory(product.CategoryProduct).
			SetStatus(product.StatusActive).
			SetIsPlatform(false).
			SetIsBaseService(false).
			SetMonthlyPrice(0).
			SetYearlyPrice(0).
			SetOnetimePrice(0).
			SetIncludedInBundle(false).
			SetSortOrder(80).
			Save(ctx); err != nil {
			return fmt.Errorf("create email-hosting product: %w", err)
		}
	}

	log.Println("  product: Email Hosting (email-hosting)")
	return nil
}

func seedEmailPlans(ctx context.Context, tx *ent.Tx) error {
	type planDef struct {
		id            uuid.UUID
		code          string
		name          string
		description   string
		monthly       float64
		yearly        float64
		storageGB     int
		maxAliases    int
		maxEmailSizeMB int
		sortOrder     int
		features      map[string]any
	}

	// Rate-limit defaults per plan Part 6 (Stalwart queue.limiter.inbound Layer 1,
	// written into Stalwart's config by email-provisioner at license-assign time).
	plans := []planDef{
		{
			id:             uuid.NewSHA1(uuid.NameSpaceOID, []byte("email_plan:EMAIL_LITE")),
			code:           "EMAIL_LITE",
			name:           "Lite",
			description:    "Essential email hosting for individuals and small teams.",
			monthly:        150,
			yearly:         1620, // ~10% discount vs 12x monthly
			storageGB:      2,
			maxAliases:     5,
			maxEmailSizeMB: 25,
			sortOrder:      1,
			features: map[string]any{
				"forwarding":            true,
				"autoresponder":         false,
				"calendar":              false,
				"contacts":              false,
				"shared_mailboxes":      false,
				"custom_sieve_filters":  false,
				"priority_support":      false,
				"admin_delegation":      false,
				"max_daily_sends":       200,
				"max_hourly_sends":      30,
				"max_per_minute_sends":  10,
				"max_recipients_per_day": 500,
			},
		},
		{
			id:             uuid.NewSHA1(uuid.NameSpaceOID, []byte("email_plan:EMAIL_STANDARD")),
			code:           "EMAIL_STANDARD",
			name:           "Standard",
			description:    "Full-featured email with calendar, contacts, and custom filters for growing teams.",
			monthly:        350,
			yearly:         3780,
			storageGB:      5,
			maxAliases:     20,
			maxEmailSizeMB: 50,
			sortOrder:      2,
			features: map[string]any{
				"forwarding":            true,
				"autoresponder":         true,
				"calendar":              true,
				"contacts":              true,
				"shared_mailboxes":      false,
				"custom_sieve_filters":  true,
				"priority_support":      false,
				"admin_delegation":      true,
				"max_daily_sends":       500,
				"max_hourly_sends":      60,
				"max_per_minute_sends":  15,
				"max_recipients_per_day": 1500,
			},
		},
		{
			id:             uuid.NewSHA1(uuid.NameSpaceOID, []byte("email_plan:EMAIL_PROFESSIONAL")),
			code:           "EMAIL_PROFESSIONAL",
			name:           "Professional",
			description:    "Unlimited aliases, shared mailboxes, and priority support for larger organizations.",
			monthly:        750,
			yearly:         8100,
			storageGB:      15,
			maxAliases:     -1, // unlimited
			maxEmailSizeMB: 100,
			sortOrder:      3,
			features: map[string]any{
				"forwarding":            true,
				"autoresponder":         true,
				"calendar":              true,
				"contacts":              true,
				"shared_mailboxes":      true,
				"custom_sieve_filters":  true,
				"priority_support":      true,
				"admin_delegation":      true,
				"max_daily_sends":       2000,
				"max_hourly_sends":      150,
				"max_per_minute_sends":  25,
				"max_recipients_per_day": 5000,
			},
		},
	}

	for _, p := range plans {
		existing, err := tx.EmailPlan.Get(ctx, p.id)
		if err != nil && !ent.IsNotFound(err) {
			return fmt.Errorf("lookup email plan %s: %w", p.code, err)
		}

		if existing != nil {
			if _, err := tx.EmailPlan.UpdateOneID(p.id).
				SetCode(p.code).
				SetName(p.name).
				SetDescription(p.description).
				SetPricePerUserMonthly(p.monthly).
				SetPricePerUserYearly(p.yearly).
				SetStoragePerUserGB(p.storageGB).
				SetMaxAliases(p.maxAliases).
				SetMaxEmailSizeMB(p.maxEmailSizeMB).
				SetFeaturesJSON(p.features).
				SetIsActive(true).
				SetIsPublic(true).
				SetSortOrder(p.sortOrder).
				Save(ctx); err != nil {
				return fmt.Errorf("update email plan %s: %w", p.code, err)
			}
		} else {
			if _, err := tx.EmailPlan.Create().
				SetID(p.id).
				SetCode(p.code).
				SetName(p.name).
				SetDescription(p.description).
				SetPricePerUserMonthly(p.monthly).
				SetPricePerUserYearly(p.yearly).
				SetStoragePerUserGB(p.storageGB).
				SetMaxAliases(p.maxAliases).
				SetMaxEmailSizeMB(p.maxEmailSizeMB).
				SetFeaturesJSON(p.features).
				SetIsActive(true).
				SetIsPublic(true).
				SetSortOrder(p.sortOrder).
				Save(ctx); err != nil {
				return fmt.Errorf("create email plan %s: %w", p.code, err)
			}
		}

		log.Printf("  email plan: %s (%s) — KES %.0f/user/mo", p.name, p.code, p.monthly)
	}

	return nil
}
