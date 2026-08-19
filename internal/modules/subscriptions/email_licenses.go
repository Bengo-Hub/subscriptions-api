package subscriptions

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/emaillicense"
	"github.com/bengobox/subscription-service/internal/ent/emailplan"
	"github.com/bengobox/subscription-service/internal/ent/product"
	"github.com/bengobox/subscription-service/internal/ent/productsubscription"
	"github.com/bengobox/subscription-service/internal/ent/tenantsubscription"
)

// emailLicensePurchaseDescription reads naturally for both the common
// single-seat case (a tenant admin adding exactly one new user — the same
// PurchaseEmailLicenses code path as a bulk buy, just quantity=1, per this
// session's explicit "single license purchase per user must be supported"
// requirement) and a bulk purchase.
func emailLicensePurchaseDescription(quantity int, planCode string) string {
	if quantity == 1 {
		return fmt.Sprintf("Email hosting: 1 x %s seat", planCode)
	}
	return fmt.Sprintf("Email hosting: %d x %s seats", quantity, planCode)
}

// EmailLicensePurchaseInput carries the plan/quantity for a new email-hosting
// license purchase intent.
type EmailLicensePurchaseInput struct {
	TenantID  uuid.UUID
	PlanCode  string
	Quantity  int
	ReturnURL string
}

// CreateEmailLicensePurchaseIntent creates a Treasury payment intent for N
// email-hosting seats at the given plan tier. Buying seats is a discrete,
// tenant-initiated, one-off paid action — mirrors AddonHandler.PurchaseAddon's
// pattern (addon.go) rather than provisioning immediately: licenses are only
// created once FulfillEmailLicensePurchase runs on confirmed payment, so a
// purchase that's never actually paid for never grants a license.
func (s *Service) CreateEmailLicensePurchaseIntent(ctx context.Context, in EmailLicensePurchaseInput) (map[string]any, error) {
	if s.treasuryClient == nil {
		return nil, fmt.Errorf("payment service unavailable")
	}

	plan, err := s.client.EmailPlan.Query().
		Where(emailplan.CodeEQ(in.PlanCode), emailplan.IsActive(true)).
		Only(ctx)
	if err != nil {
		return nil, fmt.Errorf("unknown or inactive plan_code %q", in.PlanCode)
	}

	headers := map[string]string{}
	if s.treasuryAPIKey != "" {
		headers["X-API-Key"] = s.treasuryAPIKey
	}

	req := map[string]any{
		"amount":         plan.PricePerUserMonthly * float64(in.Quantity),
		"currency":       "KES",
		"payment_method": "pending",
		"reference_id":   fmt.Sprintf("EMAILLIC-%s-%s-%d", in.TenantID.String()[:8], in.PlanCode, in.Quantity),
		"reference_type": "email_license_purchase",
		"source_service": "subscriptions",
		"description":    emailLicensePurchaseDescription(in.Quantity, in.PlanCode),
		"callback_url":   in.ReturnURL,
		// Read back at fulfillment time via GetIntent — plan_code/quantity
		// don't reliably survive the webhook/NATS notification's own wire
		// shape, but the stored intent's metadata is the authoritative
		// source (same read-back pattern treasury-api's own
		// CheckTransactionStatus uses for its Metadata).
		"metadata": map[string]any{
			"tenant_id": in.TenantID.String(),
			"plan_code": in.PlanCode,
			"quantity":  in.Quantity,
		},
	}

	resp, err := s.treasuryClient.Post(ctx, fmt.Sprintf("/api/v1/%s/payments/intents", in.TenantID), req, headers)
	if err != nil || !resp.IsSuccess() {
		return nil, fmt.Errorf("payment initiation failed: %w", err)
	}

	var treasuryResp map[string]any
	_ = resp.DecodeJSON(&treasuryResp)
	return treasuryResp, nil
}

// GetEmailLicensePurchaseIntentMetadata reads back a Treasury payment
// intent's own stored metadata to recover plan_code/quantity at fulfillment
// time. Neither the Treasury webhook body nor the treasury.payment.succeeded
// NATS event reliably carries arbitrary intent metadata (no generic
// Metadata/quantity field in either wire shape) — the intent itself is the
// authoritative source, same read-back pattern treasury-api's own
// CheckTransactionStatus uses for intent.Metadata.
func (s *Service) GetEmailLicensePurchaseIntentMetadata(ctx context.Context, tenantID, intentID uuid.UUID) (planCode string, quantity int, err error) {
	if s.treasuryClient == nil {
		return "", 0, fmt.Errorf("payment service unavailable")
	}
	headers := map[string]string{}
	if s.treasuryAPIKey != "" {
		headers["X-API-Key"] = s.treasuryAPIKey
	}

	resp, err := s.treasuryClient.Get(ctx, fmt.Sprintf("/api/v1/%s/payments/intents/%s", tenantID, intentID), headers)
	if err != nil || !resp.IsSuccess() {
		return "", 0, fmt.Errorf("fetch payment intent %s: %w", intentID, err)
	}

	var intent struct {
		Metadata map[string]any `json:"metadata"`
	}
	if err := resp.DecodeJSON(&intent); err != nil {
		return "", 0, fmt.Errorf("decode payment intent: %w", err)
	}

	planCode, quantity, ok := parsePlanCodeAndQuantity(intent.Metadata)
	if !ok {
		return "", 0, fmt.Errorf("payment intent %s has no valid plan_code/quantity in metadata", intentID)
	}
	return planCode, quantity, nil
}

// parsePlanCodeAndQuantity extracts plan_code/quantity from a decoded
// payment intent's metadata map. A pure function (no I/O) so the JSON-number
// vs. int decoding edge case can be unit-tested without a real Treasury
// call: encoding/json always decodes a bare number into float64 for a
// map[string]any target, but a caller could plausibly hand this an
// already-typed int too (e.g. from a Go-side construction in a test), so
// both are handled explicitly rather than assuming one shape.
func parsePlanCodeAndQuantity(metadata map[string]any) (planCode string, quantity int, ok bool) {
	planCode, _ = metadata["plan_code"].(string)
	switch q := metadata["quantity"].(type) {
	case float64:
		quantity = int(q)
	case int:
		quantity = q
	}
	return planCode, quantity, planCode != "" && quantity > 0
}

// FulfillEmailLicensePurchase provisions N AVAILABLE EmailLicense rows for a
// tenant, denormalizing storage/features from the given plan. Extracted from
// what used to be EmailLicenseHandler.PurchaseEmailLicenses's own transaction
// so both the Treasury webhook and the treasury.payment.succeeded NATS
// consumer can call it once payment is actually confirmed — callers are
// responsible for their own idempotency check (the intent ID) before calling
// this, since calling it twice for the same intent would double-provision.
func (s *Service) FulfillEmailLicensePurchase(ctx context.Context, tenantID uuid.UUID, planCode string, quantity int) ([]*ent.EmailLicense, error) {
	if quantity <= 0 {
		return nil, fmt.Errorf("quantity must be positive, got %d", quantity)
	}

	plan, err := s.client.EmailPlan.Query().
		Where(emailplan.CodeEQ(planCode), emailplan.IsActive(true)).
		Only(ctx)
	if err != nil {
		return nil, fmt.Errorf("unknown or inactive plan_code %q: %w", planCode, err)
	}

	tenantSub, err := s.client.TenantSubscription.Query().
		Where(tenantsubscription.TenantIDEQ(tenantID)).
		Only(ctx)
	if err != nil {
		return nil, fmt.Errorf("tenant has no active subscription: %w", err)
	}

	tx, err := s.client.Tx(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}

	prodSub, err := tx.ProductSubscription.Query().
		Where(productsubscription.TenantSubscriptionID(tenantSub.ID), productsubscription.ProductCode("email-hosting")).
		Only(ctx)
	if ent.IsNotFound(err) {
		emailProduct, perr := tx.Product.Query().Where(product.CodeEQ("email-hosting")).Only(ctx)
		if perr != nil {
			_ = tx.Rollback()
			return nil, fmt.Errorf("email-hosting product not seeded: %w", perr)
		}
		prodSub, err = tx.ProductSubscription.Create().
			SetTenantSubscriptionID(tenantSub.ID).
			SetProductCode("email-hosting").
			SetProductID(emailProduct.ID).
			Save(ctx)
	}
	if err != nil {
		_ = tx.Rollback()
		return nil, fmt.Errorf("resolve email-hosting product subscription: %w", err)
	}

	created := make([]*ent.EmailLicense, 0, quantity)
	for i := 0; i < quantity; i++ {
		lic, err := tx.EmailLicense.Create().
			SetTenantSubscriptionID(tenantSub.ID).
			SetProductSubscriptionID(prodSub.ID).
			SetEmailPlanID(plan.ID).
			SetStatus("AVAILABLE").
			SetStorageQuotaGB(plan.StoragePerUserGB).
			SetFeaturesJSON(plan.FeaturesJSON).
			Save(ctx)
		if err != nil {
			_ = tx.Rollback()
			return nil, fmt.Errorf("create email license: %w", err)
		}
		created = append(created, lic)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit purchase licenses: %w", err)
	}

	return created, nil
}

// ExpireDueLicenses transitions every EmailLicense whose expires_at has
// passed to EXPIRED and publishes the matching license.expired event —
// closing plan Part 5's T5 gap: ExpireEmailLicense (the HTTP handler) only
// ever ran on an explicit admin call, so a license with a real expires_at
// set never actually expired on its own. Intended to be called periodically
// by a StartEmailLicenseExpiryJob-style background job (internal/jobs),
// mirroring this package's other lifecycle sweeps (dormancy.go, grace.go).
// Terminal/inert statuses (EXPIRED, DELETED) are excluded so a re-run is a
// safe no-op for rows already handled.
func (s *Service) ExpireDueLicenses(ctx context.Context, log *zap.Logger) (int, error) {
	now := time.Now().UTC()
	due, err := s.client.EmailLicense.Query().
		Where(
			emaillicense.ExpiresAtNotNil(),
			emaillicense.ExpiresAtLT(now),
			emaillicense.StatusNotIn("EXPIRED", "DELETED"),
		).
		All(ctx)
	if err != nil {
		return 0, fmt.Errorf("query due licenses: %w", err)
	}

	expired := 0
	for _, lic := range due {
		tx, err := s.client.Tx(ctx)
		if err != nil {
			log.Warn("expire-due: begin tx failed", zap.String("license_id", lic.ID.String()), zap.Error(err))
			continue
		}

		tenantSub, err := tx.TenantSubscription.Get(ctx, lic.TenantSubscriptionID)
		if err != nil {
			_ = tx.Rollback()
			log.Warn("expire-due: resolve tenant failed", zap.String("license_id", lic.ID.String()), zap.Error(err))
			continue
		}

		updated, err := tx.EmailLicense.UpdateOneID(lic.ID).SetStatus("EXPIRED").Save(ctx)
		if err != nil {
			_ = tx.Rollback()
			log.Warn("expire-due: transition failed", zap.String("license_id", lic.ID.String()), zap.Error(err))
			continue
		}

		var assignedEmail *string
		if updated.AssignedToEmail != nil {
			assignedEmail = updated.AssignedToEmail
		}
		s.WriteOutboxEventPublic(ctx, tx, tenantSub.TenantID, "email", lic.ID, "license.expired", map[string]any{
			"license_id":        lic.ID.String(),
			"assigned_to_email": assignedEmail,
			"expires_at":        lic.ExpiresAt.Format(time.RFC3339),
		})

		if err := tx.Commit(); err != nil {
			log.Warn("expire-due: commit failed", zap.String("license_id", lic.ID.String()), zap.Error(err))
			continue
		}
		expired++
	}
	return expired, nil
}
