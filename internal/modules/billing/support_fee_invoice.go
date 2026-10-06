package billing

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/supportfeecycle"
	"github.com/bengobox/subscription-service/internal/modules/subscriptions"
	"github.com/bengobox/subscription-service/internal/payref"
)

// GenerateAndSendSupportFeeInvoice creates a treasury invoice + Paystack pay link for one
// SupportFeeCycle (one billing period of a standard or special support agreement). A sibling to
// GenerateAndSend rather than a shared code path: a support charge is a flat per-period amount
// with no proration/setup-fee/wallet-credit logic, so
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
	if cycle.Status == supportfeecycle.StatusPAID || cycle.Status == supportfeecycle.StatusWAIVED {
		return nil, fmt.Errorf("support fee cycle is already %s", cycle.Status)
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
	agreement := cycle.Edges.Agreement
	if agreement == nil && cycle.AgreementID != nil {
		if a, err := s.orm.SupportAgreement.Get(ctx, *cycle.AgreementID); err == nil {
			agreement = a
			cycle.Edges.Agreement = a
		}
	}
	plan := cycle.Edges.SupportPlan
	if plan == nil && cycle.SupportPlanID != nil {
		if p, err := s.orm.SubscriptionPlan.Get(ctx, *cycle.SupportPlanID); err == nil {
			plan = p
			cycle.Edges.SupportPlan = p
		}
	}

	amount := subscriptions.EffectiveSupportPrice(cycle)
	currency := ""
	label := "Support"
	planCode := ""
	if agreement != nil {
		currency = agreement.Currency
		label = agreement.Name
	}
	if plan != nil {
		planCode = plan.PlanCode
		if currency == "" {
			currency = plan.Currency
		}
		if agreement == nil {
			label = plan.Name
		}
	}
	if currency == "" {
		currency = "KES"
	}
	period := cycle.PeriodStart.UTC().Format("02 Jan 2006")
	if cycle.PeriodEnd != nil {
		period += " to " + cycle.PeriodEnd.UTC().AddDate(0, 0, -1).Format("02 Jan 2006")
	}

	customerName := ""
	if sub.Edges.Tenant != nil {
		customerName = sub.Edges.Tenant.Name
	}
	customerEmail := billingEmail(sub)
	if agreement != nil {
		if be := stringMeta(agreement.Metadata, "billing_email"); be != "" {
			customerEmail = be
		}
	}
	now := time.Now().UTC()

	lines := []map[string]any{
		{
			"description": fmt.Sprintf("%s, %s", label, period),
			"quantity":    1,
			"unit_price":  amount,
			"tax_rate":    s.vatRate,
		},
	}
	total := amount * (1 + s.vatRate/100)

	// A personal agreement (the platform owner's own engagement) is invoiced off the company's
	// books: treasury posts nothing for it, keeps it out of every business report and collects
	// it only into the owner's personal PayHero channel (M-Pesa).
	personal := agreement != nil && subscriptions.IsPersonalAgreement(agreement.Metadata)

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
			"support_plan_code": planCode,
			"agreement_id":      uuidString(cycle.AgreementID),
			"cycle_number":      cycle.CycleNumber,
		},
	}
	if personal {
		invReq["metadata"].(map[string]any)["off_books"] = true
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
		"description":    fmt.Sprintf("%s %s", label, inv.InvoiceNumber),
		"customer_email": customerEmail,
		"metadata": map[string]any{
			"service":        "subscriptions",
			"entity_id":      cycle.ID.String(),
			"tenant_id":      cycle.TenantID.String(),
			"plan_code":      planCode,
			"invoice_id":     inv.ID,
			"invoice_number": inv.InvoiceNumber,
		},
	}
	if personal {
		intentReq["metadata"].(map[string]any)["off_books"] = true
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
			if personal {
				q.Set("gateways", "mpesa,payhero_offline") // the personal channel takes M-Pesa only
			}
			payURL = fmt.Sprintf("%s/pay?%s", s.treasuryUIBase, q.Encode())
		}
	} else {
		s.log.Warn("support fee invoice: payment intent creation failed; pay link will be invoice page", zap.Error(err))
	}
	if payURL == "" && inv.PublicToken != "" {
		payURL = fmt.Sprintf("%s/i/%s", s.treasuryUIBase, inv.PublicToken)
	}
	pdfURL, invoiceURL := publicInvoiceLinks(s.treasuryAPIBase, s.treasuryUIBase, inv.PublicToken)

	// 4. Persist markers, flip INVOICED, emit the notification event (→ email).
	meta := cloneMeta(cycle.Metadata)
	meta["last_invoice_id"] = inv.ID
	meta["last_invoice_number"] = inv.InvoiceNumber
	meta["last_invoice_due_date"] = dueKey
	meta["last_invoice_pay_url"] = payURL
	meta["last_invoice_pdf_url"] = pdfURL
	meta[MetaLastInvoiceURL] = invoiceURL
	meta["last_invoice_total"] = total
	meta["last_invoice_currency"] = currency

	tx, err := s.orm.Tx(ctx)
	if err != nil {
		return nil, fmt.Errorf("tx: %w", err)
	}
	upd := tx.SupportFeeCycle.UpdateOneID(cycle.ID).SetMetadata(meta)
	if cycle.Status == supportfeecycle.StatusPENDING {
		upd = upd.SetStatus(supportfeecycle.StatusINVOICED) // an OVERDUE re-issue stays OVERDUE
	}
	if _, err := upd.Save(ctx); err != nil {
		_ = tx.Rollback()
		return nil, fmt.Errorf("persist invoice markers: %w", err)
	}
	// Aggregate "subscription" so the subject (subscription.support_fee_invoice_generated) lands
	// in the subscription stream notifications-api consumes.
	s.svc.WriteOutboxEventPublic(ctx, tx, cycle.TenantID, "subscription", cycle.ID, "support_fee_invoice_generated", map[string]any{
		"tenant_id":      cycle.TenantID.String(),
		"support_plan":   planCode,
		"agreement_name": label,
		"period":         period,
		"amount":         total,
		"currency":       currency,
		"invoice_number": inv.InvoiceNumber,
		"due_date":       cycle.DueDate.UTC().Format(time.RFC3339),
		"pay_url":        payURL,
		"pdf_url":        pdfURL,
		"invoice_url":    invoiceURL,
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

func uuidString(id *uuid.UUID) string {
	if id == nil {
		return ""
	}
	return id.String()
}
