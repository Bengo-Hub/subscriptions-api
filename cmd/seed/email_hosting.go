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

	// Pricing/feature model re-benchmarked 2026-08-19 against Zoho Mail's real,
	// current *non-WorkDrive* tiers (Mail Lite $1.00/user/mo, Mail Premium
	// $4.00/user/mo — verified live; Zoho's WorkDrive-bundled "Workplace"
	// tiers are a different, out-of-scope product family per the user's own
	// explicit instruction: price below Zoho's pure-mail tiers since we don't
	// offer WorkDrive/Writer/Sheet/Show/Cliq/Meeting yet). KES figures use
	// this repo's existing ~130 KES/USD approximation (see the original
	// 150≈$1.15 comment this replaces).
	//
	// Feature flags below are modeled on Zoho Mail Lite/Premium's real
	// published feature list (custom domain, Calendar, Directory/Contacts,
	// MFA, migration assistance, S/MIME, email retention & eDiscovery) — not
	// every Zoho flag maps to a real Stalwart capability yet. Two flags are
	// explicitly ROADMAP (tracked for entitlement/plan-comparison purposes,
	// NOT yet enforced anywhere in email-provisioner/mail-ui — do not gate a
	// UI feature on these until the underlying Stalwart mechanism is
	// confirmed, per this plan's own "probe before building" discipline):
	// smime_encryption (Zoho Premium's S/MIME — Stalwart's own S/MIME support
	// unconfirmed) and email_retention_archival (Zoho Premium's Email
	// Retention & eDiscovery — no Stalwart equivalent investigated per the
	// master plan's Part 8 finding). Zoho's "Huge Attachment" mechanism
	// (raising the effective attachment ceiling well past max_email_size_mb
	// via a special send path) is deliberately NOT modeled — a non-trivial
	// Stalwart-side mechanism of its own, out of scope for this pass.
	//
	// Rate-limit defaults per plan Part 6 (Stalwart queue.limiter.inbound Layer 1,
	// written into Stalwart's config by email-provisioner at license-assign time).
	plans := []planDef{
		{
			id:             uuid.NewSHA1(uuid.NameSpaceOID, []byte("email_plan:EMAIL_LITE")),
			code:           "EMAIL_LITE",
			name:           "Lite",
			description:    "Essential email hosting for individuals and small teams — custom domain, calendar, contacts, mobile access.",
			monthly:        120, // ~$0.92/mo — below Zoho Mail Lite's $1.00/mo
			yearly:         1152, // 20% off annual, matching Zoho's own annual discount rate
			storageGB:      2,
			maxAliases:     5,
			maxEmailSizeMB: 25,
			sortOrder:      1,
			features: map[string]any{
				"custom_domain":            true,
				"mfa_identity_management": true, // provided by platform SSO, not Stalwart-enforced per-plan
				"forwarding":               true,
				"autoresponder":            false,
				"calendar":                 false,
				"contacts":                 false,
				"shared_mailboxes":         false,
				"custom_sieve_filters":     false,
				"migration_assistance":     false,
				"smime_encryption":         false, // ROADMAP — see file header
				"email_retention_archival": false, // ROADMAP — see file header
				"priority_support":         false,
				"admin_delegation":         false,
				"max_daily_sends":          200,
				"max_hourly_sends":         30,
				"max_per_minute_sends":     10,
				"max_recipients_per_day":   500,
			},
		},
		{
			id:             uuid.NewSHA1(uuid.NameSpaceOID, []byte("email_plan:EMAIL_STANDARD")),
			code:           "EMAIL_STANDARD",
			name:           "Standard",
			description:    "Full-featured email with calendar, contacts, migration assistance, and custom filters for growing teams.",
			monthly:        280, // ~$2.15/mo — deliberately between Zoho's Lite/Premium; Zoho has no direct equivalent mid-tier
			yearly:         2688,
			storageGB:      5,
			maxAliases:     20,
			maxEmailSizeMB: 50,
			sortOrder:      2,
			features: map[string]any{
				"custom_domain":            true,
				"mfa_identity_management": true,
				"forwarding":               true,
				"autoresponder":            true,
				"calendar":                 true,
				"contacts":                 true,
				"shared_mailboxes":         false,
				"custom_sieve_filters":     true,
				"migration_assistance":     true,
				"smime_encryption":         false, // ROADMAP — see file header
				"email_retention_archival": false, // ROADMAP — see file header
				"priority_support":         false,
				"admin_delegation":         true,
				"max_daily_sends":          500,
				"max_hourly_sends":         60,
				"max_per_minute_sends":     15,
				"max_recipients_per_day":   1500,
			},
		},
		{
			id:             uuid.NewSHA1(uuid.NameSpaceOID, []byte("email_plan:EMAIL_PROFESSIONAL")),
			code:           "EMAIL_PROFESSIONAL",
			name:           "Professional",
			description:    "Unlimited aliases, shared mailboxes, and priority support for larger organizations — Zoho Premium feature parity minus WorkDrive.",
			monthly:        420, // ~$3.23/mo — below Zoho Mail Premium's $4.00/mo (we don't yet offer WorkDrive/S-MIME/retention)
			yearly:         4032,
			storageGB:      15,
			maxAliases:     -1, // unlimited
			maxEmailSizeMB: 100,
			sortOrder:      3,
			features: map[string]any{
				"custom_domain":            true,
				"mfa_identity_management": true,
				"forwarding":               true,
				"autoresponder":            true,
				"calendar":                 true,
				"contacts":                 true,
				"shared_mailboxes":         true,
				"custom_sieve_filters":     true,
				"migration_assistance":     true,
				"smime_encryption":         false, // ROADMAP — see file header; priced below Zoho Premium partly because this is unbuilt
				"email_retention_archival": false, // ROADMAP — see file header
				"priority_support":         true,
				"admin_delegation":         true,
				"max_daily_sends":          2000,
				"max_hourly_sends":         150,
				"max_per_minute_sends":     25,
				"max_recipients_per_day":   5000,
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
