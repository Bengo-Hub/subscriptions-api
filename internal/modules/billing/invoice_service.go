package billing

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"time"

	serviceclient "github.com/Bengo-Hub/shared-service-client"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/customaddon"
	"github.com/bengobox/subscription-service/internal/ent/subscriptioncredit"
	"github.com/bengobox/subscription-service/internal/ent/subscriptioncredittransaction"
	"github.com/bengobox/subscription-service/internal/ent/tenantsubscription"
	"github.com/bengobox/subscription-service/internal/modules/subscriptions"
	"github.com/bengobox/subscription-service/internal/payref"
)

// InvoiceService generates platform→tenant subscription invoices in treasury-api and
// drives the durable pay link + email. It owns the billing-event side; treasury owns the
// invoice document, PDF and ledger. Reused by the scheduled job and the manual
// platform-owner endpoints.
type InvoiceService struct {
	log              *zap.Logger
	orm              *ent.Client
	svc              *subscriptions.Service // for outbox event writes
	treasury         *serviceclient.Client
	ispBillingClient *serviceclient.Client // isp-billing S2S (exact active PPPoE count); may be nil
	apiKey           string
	platformTenantID string
	treasuryUIBase   string // e.g. https://books.codevertexitsolutions.com (for /pay)
	treasuryAPIBase  string // e.g. https://booksapi.codevertexitsolutions.com (for public PDF)
	vatRate          float64
	overage          *OverageService
}

// NewInvoiceService constructs the subscription invoice service.
func NewInvoiceService(log *zap.Logger, orm *ent.Client, svc *subscriptions.Service, treasury, ispBillingClient *serviceclient.Client, apiKey, platformTenantID, treasuryUIBase, treasuryAPIBase string, vatRate float64) *InvoiceService {
	return &InvoiceService{
		log:              log.Named("billing.invoice"),
		orm:              orm,
		svc:              svc,
		treasury:         treasury,
		ispBillingClient: ispBillingClient,
		apiKey:           apiKey,
		platformTenantID: platformTenantID,
		treasuryUIBase:   treasuryUIBase,
		treasuryAPIBase:  treasuryAPIBase,
		vatRate:          vatRate,
		overage:          NewOverageService(log, orm),
	}
}

// InvoiceResult summarizes a generated subscription invoice.
type InvoiceResult struct {
	InvoiceID     string  `json:"invoice_id"`
	InvoiceNumber string  `json:"invoice_number"`
	Amount        float64 `json:"amount"`
	Currency      string  `json:"currency"`
	PayURL        string  `json:"pay_url"`
	PDFURL        string  `json:"pdf_url"`
	Skipped       bool    `json:"skipped"` // true when an invoice for this period already exists
}

func (s *InvoiceService) headers() map[string]string {
	h := map[string]string{}
	if s.apiKey != "" {
		h["X-API-Key"] = s.apiKey
	}
	return h
}

// buildLines assembles itemized invoice lines (base plan + pending overages + active
// addons − available credits) and returns the lines, the net total, the currency, and
// the wallet credit amount applied (caller must actually deduct this from the tenant's
// SubscriptionCredit balance once the invoice is confirmed created — this function only
// computes, it does not mutate the wallet).
func (s *InvoiceService) buildLines(ctx context.Context, sub *ent.TenantSubscription) ([]map[string]any, float64, string, float64, error) {
	if sub.Edges.Plan == nil {
		plan, err := s.orm.TenantSubscription.QueryPlan(sub).Only(ctx)
		if err != nil {
			return nil, 0, "", 0, fmt.Errorf("load plan: %w", err)
		}
		sub.Edges.Plan = plan
	}
	plan := sub.Edges.Plan
	currency := plan.Currency
	if currency == "" {
		currency = "KES"
	}

	lines := make([]map[string]any, 0, 4)
	var taxable float64 // sum of VAT-eligible line subtotals (base + overage + addons)

	// Base plan line (VAT applied exclusive per the platform-seeded VAT-16 default).
	// base_price is per MONTH; the line bills the tenant's chosen billing period
	// (quantity = months: MONTHLY=1, SEMI_ANNUAL=6, ANNUAL=12) — no automatic discount.
	months := subscriptions.BillingCycleMonths(string(sub.BillingCycle))
	if months <= 0 {
		months = 1
	}
	lines = append(lines, map[string]any{
		"description": fmt.Sprintf("Subscription: %s (%s — %d month(s) × %s %.2f)", plan.Name, sub.BillingCycle, months, currency, plan.BasePrice),
		"quantity":    months,
		"unit_price":  plan.BasePrice,
		"tax_rate":    s.vatRate,
	})
	taxable += plan.BasePrice * float64(months)

	// ISP Billing usage charges (service_tag "isp_billing"): threshold-gated service
	// charge on monthly hotspot sales + per-active-PPPoE-subscriber fee, read from the
	// plan's tier_limits. No-op for every non-ISP plan (guarded by IsISPBillingPlan), so
	// existing billing is unchanged. The base_price line above already covers the KES 500
	// base. VAT is applied exclusive, mirroring the other taxable lines.
	if IsISPBillingPlan(plan) {
		ispLines, err := s.computeISPUsageLines(ctx, sub, plan)
		if err != nil {
			// Don't fail the whole invoice on a usage-aggregation error — log and bill the
			// deterministic base. The next cycle reconciles once the source recovers.
			s.log.Warn("invoice: ISP usage charge computation failed; billing base only", zap.Error(err))
		}
		for _, il := range ispLines {
			lines = append(lines, map[string]any{
				"description": il.Description,
				"quantity":    il.Quantity,
				"unit_price":  il.UnitPrice,
				"tax_rate":    s.vatRate,
			})
			taxable += il.Subtotal
		}
	}

	// One-time setup/onboarding fee — billed once on the first invoice only.
	// Guarded by setup_fee_charged_at (nil = not yet charged); never recurs on renewal.
	// Waived entirely (never on the invoice) for billing periods of 6+ months.
	if setupFeeDue(sub) {
		lines = append(lines, map[string]any{
			"description": fmt.Sprintf("One-time setup fee: %s", plan.Name),
			"quantity":    1,
			"unit_price":  sub.SetupFeeAmount,
			"tax_rate":    s.vatRate,
		})
		taxable += sub.SetupFeeAmount
	}

	// Pending overage charges
	overages, err := s.overage.ListPendingByTenant(ctx, sub.TenantID)
	if err != nil {
		s.log.Warn("invoice: overage query failed", zap.Error(err))
	}
	for _, o := range overages {
		lines = append(lines, map[string]any{
			"description": fmt.Sprintf("Overage: %s (%.0f units)", o.MetricType, o.UnitsOver),
			"quantity":    o.UnitsOver,
			"unit_price":  o.UnitPriceKes,
			"tax_rate":    s.vatRate,
		})
		taxable += o.UnitsOver * o.UnitPriceKes
	}

	// Active custom addons
	addons, err := s.orm.CustomAddon.Query().
		Where(customaddon.TenantIDEQ(sub.TenantID), customaddon.StatusEQ(customaddon.StatusActive)).
		All(ctx)
	if err != nil {
		s.log.Warn("invoice: addon query failed", zap.Error(err))
	}
	for _, a := range addons {
		lines = append(lines, map[string]any{
			"description": a.Name,
			"quantity":    a.Quantity,
			"unit_price":  a.UnitPriceKes,
			"tax_rate":    s.vatRate,
		})
		taxable += float64(a.UnitPriceKes * a.Quantity)
	}

	// Grand total = taxable + VAT (exclusive).
	total := taxable * (1 + s.vatRate/100)

	// Apply available credits post-tax (untaxed line, floored so total never goes negative).
	// The wallet itself is deducted by the caller once the invoice is confirmed created —
	// see creditApplied below and its use in GenerateAndSend.
	var creditApplied float64
	if credit, err := s.orm.SubscriptionCredit.Query().
		Where(subscriptioncredit.TenantIDEQ(sub.TenantID)).Only(ctx); err == nil && credit.BalanceKes > 0 {
		apply := float64(credit.BalanceKes)
		if apply > total {
			apply = total
		}
		if apply > 0 {
			lines = append(lines, map[string]any{
				"description": "Loyalty credit applied",
				"quantity":    1,
				"unit_price":  -apply,
				"tax_rate":    0,
			})
			total -= apply
			creditApplied = apply
		}
	}

	return lines, total, currency, creditApplied, nil
}

// setupFeeDue reports whether the one-time setup/installation fee must be billed on the
// next invoice: a positive uncharged snapshot AND a billing period too short to waive it
// (< 6 months). Single source of truth shared by buildLines and the mark-charged step so
// the fee is only ever marked charged when it was actually billed.
func setupFeeDue(sub *ent.TenantSubscription) bool {
	return sub.SetupFeeAmount > 0 &&
		sub.SetupFeeChargedAt == nil &&
		!subscriptions.CycleWaivesSetupFee(string(sub.BillingCycle))
}

// billingEmail resolves the tenant billing email from subscription metadata.
func billingEmail(sub *ent.TenantSubscription) string {
	if sub.Metadata != nil {
		if be, ok := sub.Metadata["billing_email"].(string); ok && be != "" {
			return be
		}
	}
	return ""
}

// GenerateAndSend creates a subscription invoice in treasury (owned by the platform
// tenant, customer = billed tenant), posts it (AR journal), creates a durable Paystack
// pay link via a subscription-referenced payment intent, emits the invoice_generated
// outbox event (→ email), records markers and marks overages invoiced.
//
// Idempotent: unless force, it skips if an invoice for the current period was already
// generated (tracked in subscription metadata).
func (s *InvoiceService) GenerateAndSend(ctx context.Context, sub *ent.TenantSubscription, force bool) (*InvoiceResult, error) {
	if s.treasury == nil {
		return nil, fmt.Errorf("treasury client not configured")
	}
	periodKey := sub.CurrentPeriodEnd.UTC().Format(time.RFC3339)

	// Idempotency guard (skippable with force=true for manual regenerate)
	if !force && sub.Metadata != nil {
		if last, ok := sub.Metadata["last_invoice_period_end"].(string); ok && last == periodKey {
			return &InvoiceResult{
				InvoiceID:     stringMeta(sub.Metadata, "last_invoice_id"),
				InvoiceNumber: stringMeta(sub.Metadata, "last_invoice_number"),
				PayURL:        stringMeta(sub.Metadata, "last_invoice_pay_url"),
				PDFURL:        stringMeta(sub.Metadata, "last_invoice_pdf_url"),
				Skipped:       true,
			}, nil
		}
	}

	lines, total, currency, creditApplied, err := s.buildLines(ctx, sub)
	if err != nil {
		return nil, err
	}

	// Resolve customer (billed tenant) snapshot
	customerName := ""
	if sub.Edges.Tenant != nil {
		customerName = sub.Edges.Tenant.Name
	} else if t, err := s.orm.TenantSubscription.QueryTenant(sub).Only(ctx); err == nil {
		customerName = t.Name
	}
	customerEmail := billingEmail(sub)
	now := time.Now().UTC()
	planCode := ""
	if sub.Edges.Plan != nil {
		planCode = sub.Edges.Plan.PlanCode
	}

	// 1. Create the invoice under the PLATFORM tenant (issuer); customer = billed tenant.
	invReq := map[string]any{
		"customer_name":  customerName,
		"customer_email": customerEmail,
		"invoice_type":   "subscription",
		"invoice_date":   now.Format(time.RFC3339),
		"due_date":       sub.CurrentPeriodEnd.UTC().Format(time.RFC3339),
		"currency":       currency,
		"reference_id":   sub.ID.String(),
		"reference_type": "subscription",
		"lines":          lines,
		"metadata": map[string]any{
			"billed_tenant_id": sub.TenantID.String(),
			"plan_code":        planCode,
			"period_start":     sub.CurrentPeriodStart.UTC().Format(time.RFC3339),
			"period_end":       periodKey,
		},
	}
	resp, err := s.treasury.Post(ctx, fmt.Sprintf("/api/v1/s2s/%s/invoices", s.platformTenantID), invReq, s.headers())
	if err != nil || !resp.IsSuccess() {
		return nil, fmt.Errorf("treasury create invoice failed: %w", err)
	}
	var inv struct {
		ID            string `json:"id"`
		InvoiceNumber string `json:"invoice_number"`
		PublicToken   string `json:"public_token"`
	}
	if err := json.Unmarshal(resp.Body, &inv); err != nil {
		return nil, fmt.Errorf("decode invoice: %w", err)
	}

	// Mark the one-time setup fee as charged so it is never billed again (renewals skip it).
	// Uses the same setupFeeDue rule as buildLines: a waived fee (6+ month period) was NOT
	// billed above and must stay uncharged/waived rather than be marked charged.
	if setupFeeDue(sub) {
		chargedAt := now
		if _, err := s.orm.TenantSubscription.UpdateOneID(sub.ID).SetSetupFeeChargedAt(chargedAt).Save(ctx); err != nil {
			s.log.Warn("invoice: failed to mark setup fee charged", zap.String("subscription_id", sub.ID.String()), zap.Error(err))
		} else {
			sub.SetupFeeChargedAt = &chargedAt
		}
	}

	// 2. Send it (status→sent, posts AR journal). No email is emitted by treasury for
	//    subscription invoices — subscriptions drives the email below.
	if _, err := s.treasury.Post(ctx, fmt.Sprintf("/api/v1/s2s/%s/invoices/%s/send", s.platformTenantID, inv.ID), map[string]any{}, s.headers()); err != nil {
		s.log.Warn("invoice: send failed (continuing)", zap.String("invoice_id", inv.ID), zap.Error(err))
	}

	// 3. Create a subscription-referenced payment intent so paying the link renews the
	//    subscription via the existing payment.succeeded consumer, and mints a fresh
	//    Paystack session on each visit (durable link).
	payURL := ""
	// Service-identifiable Paystack reference (SUB-{slug}-{hex}); the subscription UUID travels in
	// metadata.entity_id. Safe: the renewal consumer reconciles by tenant_id + plan_code, and the
	// invoice is settled via metadata.invoice_id — neither depends on this reference_id value.
	payRef := payref.Build("SUB", "", sub.TenantID, sub.ID)
	intentReq := map[string]any{
		"reference_id":   payRef,
		"reference_type": "subscription",
		"payment_method": "pending",
		"currency":       currency,
		"amount":         total,
		"source_service": "subscriptions",
		"description":    fmt.Sprintf("Subscription invoice %s", inv.InvoiceNumber),
		"customer_email": customerEmail,
		"metadata": map[string]any{
			"service":        "subscriptions",
			"entity_id":      sub.ID.String(),
			"tenant_id":      sub.TenantID.String(),
			"plan_code":      planCode,
			"invoice_id":     inv.ID,
			"invoice_number": inv.InvoiceNumber,
		},
	}
	intentResp, err := s.treasury.Post(ctx, fmt.Sprintf("/api/v1/s2s/%s/payments/intents", sub.TenantID), intentReq, s.headers())
	if err == nil && intentResp.IsSuccess() {
		var ir struct {
			IntentID    string `json:"intent_id"`
			InitiateURL string `json:"initiate_url"`
		}
		if json.Unmarshal(intentResp.Body, &ir) == nil {
			q := url.Values{}
			q.Set("tenant", sub.TenantID.String())
			q.Set("amount", fmt.Sprintf("%.2f", total))
			q.Set("currency", currency)
			q.Set("reference_id", payRef)
			q.Set("reference_type", "subscription")
			q.Set("invoice_number", inv.InvoiceNumber)
			if customerEmail != "" {
				q.Set("email", customerEmail)
			}
			if ir.InitiateURL != "" {
				q.Set("initiate_url", ir.InitiateURL)
			}
			if ir.IntentID != "" {
				q.Set("intent_id", ir.IntentID)
			}
			payURL = fmt.Sprintf("%s/pay?%s", s.treasuryUIBase, q.Encode())
		}
	} else {
		s.log.Warn("invoice: payment intent creation failed; pay link will be invoice page", zap.Error(err))
	}
	if payURL == "" && inv.PublicToken != "" {
		// Fallback: public invoice page (still mints a fresh session on Pay).
		payURL = fmt.Sprintf("%s/i/%s", s.treasuryUIBase, inv.PublicToken)
	}
	pdfURL := ""
	if inv.PublicToken != "" {
		pdfURL = fmt.Sprintf("%s/api/v1/public/invoices/%s/pdf", s.treasuryAPIBase, inv.PublicToken)
	}

	// 4. Persist markers + emit the invoice_generated outbox event (→ notifications email).
	meta := cloneMeta(sub.Metadata)
	meta["last_invoice_id"] = inv.ID
	meta["last_invoice_number"] = inv.InvoiceNumber
	meta["last_invoice_period_end"] = periodKey
	meta["last_invoice_pay_url"] = payURL
	meta["last_invoice_pdf_url"] = pdfURL
	meta["last_invoice_total"] = total
	meta["last_invoice_currency"] = currency

	tx, err := s.orm.Tx(ctx)
	if err != nil {
		return nil, fmt.Errorf("tx: %w", err)
	}
	if _, err := tx.TenantSubscription.UpdateOneID(sub.ID).SetMetadata(meta).Save(ctx); err != nil {
		_ = tx.Rollback()
		return nil, fmt.Errorf("persist invoice markers: %w", err)
	}
	// Actually consume the wallet credit that was applied as a line above — buildLines only
	// computes the discount, it never mutates the wallet, so this deduction must happen once
	// the invoice is confirmed created (never before: an earlier treasury failure above returns
	// without ever reaching here, leaving the wallet untouched for the next attempt).
	if creditApplied > 0 {
		if wallet, werr := tx.SubscriptionCredit.Query().
			Where(subscriptioncredit.TenantIDEQ(sub.TenantID)).Only(ctx); werr == nil {
			applyInt := int(creditApplied)
			if _, err := tx.SubscriptionCredit.UpdateOneID(wallet.ID).
				SetBalanceKes(wallet.BalanceKes - applyInt).
				SetUpdatedAt(now).
				Save(ctx); err != nil {
				_ = tx.Rollback()
				return nil, fmt.Errorf("deduct applied credit: %w", err)
			}
			invoiceID, _ := uuid.Parse(inv.ID)
			refType := "invoice"
			if _, err := tx.SubscriptionCreditTransaction.Create().
				SetTenantID(sub.TenantID).
				SetCreditID(wallet.ID).
				SetType(subscriptioncredittransaction.TypeAutoApplied).
				SetAmountKes(-applyInt).
				SetNillableRefID(&invoiceID).
				SetNillableRefType(&refType).
				Save(ctx); err != nil {
				_ = tx.Rollback()
				return nil, fmt.Errorf("record applied credit transaction: %w", err)
			}
		} else {
			s.log.Warn("invoice: credit line was applied but wallet not found for deduction", zap.String("tenant_id", sub.TenantID.String()))
		}
	}
	s.svc.WriteOutboxEventPublic(ctx, tx, sub.TenantID, "subscription", sub.ID, "invoice_generated", map[string]any{
		"tenant_id":      sub.TenantID.String(),
		"plan_code":      planCode,
		"amount":         total,
		"currency":       currency,
		"invoice_number": inv.InvoiceNumber,
		"due_date":       sub.CurrentPeriodEnd.UTC().Format(time.RFC3339),
		"pay_url":        payURL,
		"pdf_url":        pdfURL,
		"notification": map[string]any{
			"target":          "tenant_admin",
			"recipient_email": customerEmail,
		},
	})
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}

	// 5. Mark pending overages as invoiced (best-effort).
	if err := s.overage.MarkInvoiced(ctx, sub.TenantID); err != nil {
		s.log.Warn("invoice: mark overages invoiced failed", zap.Error(err))
	}

	s.log.Info("subscription invoice generated",
		zap.String("tenant_id", sub.TenantID.String()),
		zap.String("invoice_number", inv.InvoiceNumber),
		zap.Float64("amount", total),
	)
	return &InvoiceResult{
		InvoiceID:     inv.ID,
		InvoiceNumber: inv.InvoiceNumber,
		Amount:        total,
		Currency:      currency,
		PayURL:        payURL,
		PDFURL:        pdfURL,
	}, nil
}

// GenerateForTenant loads the tenant's subscription (with plan + tenant edges) and
// generates an invoice. Used by the manual platform-owner endpoint.
func (s *InvoiceService) GenerateForTenant(ctx context.Context, tenantID uuid.UUID, force bool) (*InvoiceResult, error) {
	sub, err := s.loadSub(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	return s.GenerateAndSend(ctx, sub, force)
}

func (s *InvoiceService) loadSub(ctx context.Context, tenantID uuid.UUID) (*ent.TenantSubscription, error) {
	sub, err := s.orm.TenantSubscription.Query().
		Where(tenantsubscription.TenantIDEQ(tenantID)).
		WithPlan().
		WithTenant().
		Only(ctx)
	if err != nil {
		return nil, fmt.Errorf("load subscription: %w", err)
	}
	return sub, nil
}

// ResendLast re-emits the email for the most recently generated invoice (fresh pay link
// still works — it re-initiates a Paystack session on visit) without creating a new
// invoice. Used by the manual platform-owner "resend" action.
func (s *InvoiceService) ResendLast(ctx context.Context, tenantID uuid.UUID) (*InvoiceResult, error) {
	sub, err := s.loadSub(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	invNo := stringMeta(sub.Metadata, "last_invoice_number")
	if invNo == "" {
		return nil, fmt.Errorf("no invoice to resend; generate one first")
	}
	payURL := stringMeta(sub.Metadata, "last_invoice_pay_url")
	pdfURL := stringMeta(sub.Metadata, "last_invoice_pdf_url")
	currency, _ := sub.Metadata["last_invoice_currency"].(string)
	total, _ := sub.Metadata["last_invoice_total"].(float64)
	planCode := ""
	if sub.Edges.Plan != nil {
		planCode = sub.Edges.Plan.PlanCode
	}

	tx, err := s.orm.Tx(ctx)
	if err != nil {
		return nil, fmt.Errorf("tx: %w", err)
	}
	s.svc.WriteOutboxEventPublic(ctx, tx, sub.TenantID, "subscription", sub.ID, "invoice_generated", map[string]any{
		"tenant_id":      sub.TenantID.String(),
		"plan_code":      planCode,
		"amount":         total,
		"currency":       currency,
		"invoice_number": invNo,
		"due_date":       sub.CurrentPeriodEnd.UTC().Format(time.RFC3339),
		"pay_url":        payURL,
		"pdf_url":        pdfURL,
		"notification": map[string]any{
			"target":          "tenant_admin",
			"recipient_email": billingEmail(sub),
		},
	})
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return &InvoiceResult{
		InvoiceID:     stringMeta(sub.Metadata, "last_invoice_id"),
		InvoiceNumber: invNo,
		Amount:        total,
		Currency:      currency,
		PayURL:        payURL,
		PDFURL:        pdfURL,
	}, nil
}

// FetchInvoicePDF proxies the treasury public/S2S invoice PDF so the subscriptions UI can
// download it with only a subscriptions JWT (no treasury auth).
func (s *InvoiceService) FetchInvoicePDF(ctx context.Context, invoiceID string) ([]byte, error) {
	if s.treasury == nil {
		return nil, fmt.Errorf("treasury client not configured")
	}
	resp, err := s.treasury.Get(ctx, fmt.Sprintf("/api/v1/s2s/%s/invoices/%s/pdf", s.platformTenantID, invoiceID), s.headers())
	if err != nil {
		return nil, err
	}
	if !resp.IsSuccess() {
		return nil, fmt.Errorf("treasury pdf returned status %d", resp.StatusCode)
	}
	return resp.Body, nil
}

// LastInvoiceFor returns a summary of the tenant's most recent subscription invoice from
// stored markers, for the subscriptions UI. Returns nil if none.
func (s *InvoiceService) LastInvoiceFor(ctx context.Context, tenantID uuid.UUID) (*InvoiceResult, error) {
	sub, err := s.loadSub(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	invNo := stringMeta(sub.Metadata, "last_invoice_number")
	if invNo == "" {
		return nil, nil
	}
	currency, _ := sub.Metadata["last_invoice_currency"].(string)
	total, _ := sub.Metadata["last_invoice_total"].(float64)
	return &InvoiceResult{
		InvoiceID:     stringMeta(sub.Metadata, "last_invoice_id"),
		InvoiceNumber: invNo,
		Amount:        total,
		Currency:      currency,
		PayURL:        stringMeta(sub.Metadata, "last_invoice_pay_url"),
		PDFURL:        stringMeta(sub.Metadata, "last_invoice_pdf_url"),
	}, nil
}

func stringMeta(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	s, _ := m[key].(string)
	return s
}

func cloneMeta(m map[string]any) map[string]any {
	out := make(map[string]any, len(m)+5)
	for k, v := range m {
		out[k] = v
	}
	return out
}
