package billing

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"time"

	"go.uber.org/zap"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/supportfeecycle"
	"github.com/bengobox/subscription-service/internal/modules/subscriptions"
	"github.com/bengobox/subscription-service/internal/payref"
)

// GenerateAndSendSupportFeeInvoice creates a treasury invoice + Paystack pay link for one
// annual SupportFeeCycle. A sibling to GenerateAndSend rather than a shared code path: a
// support fee is a flat annual amount with no proration/setup-fee/wallet-credit logic, so
// routing it through GenerateAndSend's buildLines (tightly coupled to *ent.TenantSubscription's
// recurring-billing shape) would risk regressing the live subscription-invoicing path for no
// benefit. This method reuses the same already-wired treasury/Paystack client fields on
// InvoiceService (s.treasury, s.apiKey, s.platformTenantID, s.treasuryUIBase, s.treasuryAPIBase,
// s.vatRate, s.headers()) and the package-local billingEmail/stringMeta/cloneMeta helpers
// (invoice_service.go, same package).
//
// Idempotent: unless force, skips a cycle already invoiced for its current due_date (tracked in
// cycle.Metadata["last_invoice_due_date"]).
func (s *InvoiceService) GenerateAndSendSupportFeeInvoice(ctx context.Context, cycle *ent.SupportFeeCycle, force bool) (*InvoiceResult, error) {
	if s.treasury == nil {
		return nil, fmt.Errorf("treasury client not configured")
	}
	dueKey := cycle.DueDate.UTC().Format(time.RFC3339)

	if !force && cycle.Metadata != nil {
		if last, ok := cycle.Metadata["last_invoice_due_date"].(string); ok && last == dueKey {
			return &InvoiceResult{
				InvoiceID:     stringMeta(cycle.Metadata, "last_invoice_id"),
				InvoiceNumber: stringMeta(cycle.Metadata, "last_invoice_number"),
				PayURL:        stringMeta(cycle.Metadata, "last_invoice_pay_url"),
				PDFURL:        stringMeta(cycle.Metadata, "last_invoice_pdf_url"),
				Skipped:       true,
			}, nil
		}
	}

	sub := cycle.Edges.TenantSubscription
	if sub == nil {
		var err error
		sub, err = s.orm.SupportFeeCycle.QueryTenantSubscription(cycle).WithTenant().Only(ctx)
		if err != nil {
			return nil, fmt.Errorf("load tenant subscription: %w", err)
		}
		cycle.Edges.TenantSubscription = sub
	}
	plan := cycle.Edges.SupportPlan
	if plan == nil {
		var err error
		plan, err = s.orm.SupportFeeCycle.QuerySupportPlan(cycle).Only(ctx)
		if err != nil {
			return nil, fmt.Errorf("load support plan: %w", err)
		}
		cycle.Edges.SupportPlan = plan
	}

	amount := subscriptions.EffectiveSupportPrice(cycle)
	currency := plan.Currency
	if currency == "" {
		currency = "KES"
	}

	customerName := ""
	if sub.Edges.Tenant != nil {
		customerName = sub.Edges.Tenant.Name
	}
	customerEmail := billingEmail(sub)
	now := time.Now().UTC()

	lines := []map[string]any{
		{
			"description": fmt.Sprintf("Annual support — %s (cycle %d)", plan.Name, cycle.CycleNumber),
			"quantity":    1,
			"unit_price":  amount,
			"tax_rate":    s.vatRate,
		},
	}
	total := amount * (1 + s.vatRate/100)

	// 1. Create the invoice under the PLATFORM tenant (issuer); customer = the license tenant.
	invReq := map[string]any{
		"customer_name":  customerName,
		"customer_email": customerEmail,
		"invoice_type":   "support_fee",
		"invoice_date":   now.Format(time.RFC3339),
		"due_date":       cycle.DueDate.UTC().Format(time.RFC3339),
		"currency":       currency,
		"reference_id":   cycle.ID.String(),
		"reference_type": "support_fee_cycle",
		"lines":          lines,
		"metadata": map[string]any{
			"billed_tenant_id":  cycle.TenantID.String(),
			"support_plan_code": plan.PlanCode,
			"cycle_number":      cycle.CycleNumber,
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

	// 2. Send it (status→sent, posts AR journal). No email from treasury — subscriptions
	// drives the email below via the invoice_generated-shaped outbox event.
	if _, err := s.treasury.Post(ctx, fmt.Sprintf("/api/v1/s2s/%s/invoices/%s/send", s.platformTenantID, inv.ID), map[string]any{}, s.headers()); err != nil {
		s.log.Warn("support fee invoice: send failed (continuing)", zap.String("invoice_id", inv.ID), zap.Error(err))
	}

	// 3. Create a support-fee-referenced payment intent so paying the link marks the cycle
	// PAID via the payment.succeeded consumer's support_fee_cycle branch.
	payURL := ""
	payRef := payref.Build("SUPFEE", "", cycle.TenantID, cycle.ID)
	intentReq := map[string]any{
		"reference_id":   payRef,
		"reference_type": "support_fee_cycle",
		"payment_method": "pending",
		"currency":       currency,
		"amount":         total,
		"source_service": "subscriptions",
		"description":    fmt.Sprintf("Annual support fee %s", inv.InvoiceNumber),
		"customer_email": customerEmail,
		"metadata": map[string]any{
			"service":        "subscriptions",
			"entity_id":      cycle.ID.String(),
			"tenant_id":      cycle.TenantID.String(),
			"plan_code":      plan.PlanCode,
			"invoice_id":     inv.ID,
			"invoice_number": inv.InvoiceNumber,
		},
	}
	intentResp, err := s.treasury.Post(ctx, fmt.Sprintf("/api/v1/s2s/%s/payments/intents", cycle.TenantID), intentReq, s.headers())
	if err == nil && intentResp.IsSuccess() {
		var ir struct {
			IntentID    string `json:"intent_id"`
			InitiateURL string `json:"initiate_url"`
		}
		if json.Unmarshal(intentResp.Body, &ir) == nil {
			q := url.Values{}
			q.Set("tenant", cycle.TenantID.String())
			q.Set("amount", fmt.Sprintf("%.2f", total))
			q.Set("currency", currency)
			q.Set("reference_id", payRef)
			q.Set("reference_type", "support_fee_cycle")
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
		s.log.Warn("support fee invoice: payment intent creation failed; pay link will be invoice page", zap.Error(err))
	}
	if payURL == "" && inv.PublicToken != "" {
		payURL = fmt.Sprintf("%s/i/%s", s.treasuryUIBase, inv.PublicToken)
	}
	pdfURL := ""
	if inv.PublicToken != "" {
		pdfURL = fmt.Sprintf("%s/api/v1/public/invoices/%s/pdf", s.treasuryAPIBase, inv.PublicToken)
	}

	// 4. Persist markers, flip INVOICED, emit the notification event (→ email).
	meta := cloneMeta(cycle.Metadata)
	meta["last_invoice_id"] = inv.ID
	meta["last_invoice_number"] = inv.InvoiceNumber
	meta["last_invoice_due_date"] = dueKey
	meta["last_invoice_pay_url"] = payURL
	meta["last_invoice_pdf_url"] = pdfURL
	meta["last_invoice_total"] = total
	meta["last_invoice_currency"] = currency

	tx, err := s.orm.Tx(ctx)
	if err != nil {
		return nil, fmt.Errorf("tx: %w", err)
	}
	if _, err := tx.SupportFeeCycle.UpdateOneID(cycle.ID).
		SetStatus(supportfeecycle.StatusINVOICED).
		SetMetadata(meta).
		Save(ctx); err != nil {
		_ = tx.Rollback()
		return nil, fmt.Errorf("persist invoice markers: %w", err)
	}
	s.svc.WriteOutboxEventPublic(ctx, tx, cycle.TenantID, "support_fee_cycle", cycle.ID, "support_fee_invoice_generated", map[string]any{
		"tenant_id":      cycle.TenantID.String(),
		"support_plan":   plan.PlanCode,
		"amount":         total,
		"currency":       currency,
		"invoice_number": inv.InvoiceNumber,
		"due_date":       cycle.DueDate.UTC().Format(time.RFC3339),
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

	s.log.Info("support fee invoice generated",
		zap.String("tenant_id", cycle.TenantID.String()),
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
