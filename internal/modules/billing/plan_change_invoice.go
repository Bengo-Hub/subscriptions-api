package billing

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/modules/subscriptions"
)

// Metadata markers for the plan-change invoice of the current cycle.
const (
	// MetaLastInvoicePlanID is the plan the last renewal invoice billed.
	MetaLastInvoicePlanID = "last_invoice_plan_id"

	metaPlanChangeInvoiceID     = "plan_change_invoice_id"
	metaPlanChangeInvoiceNumber = "plan_change_invoice_number"
	metaPlanChangePeriodStart   = "plan_change_invoice_period_start"
	metaPlanChangePlanID        = "plan_change_invoice_plan_id"
)

// ReconcileOpenRenewalInvoice keeps the coming renewal billed at the right plan after a plan
// change is scheduled for it, or cancelled, once its renewal invoice is already out.
//
// The renewal invoice is never voided and raised again: it may already carry wallet credit,
// overage lines and the one-time setup fee, and its emailed pay link stays payable. Instead the
// price difference for the coming period goes on a separate plan-change invoice. A cancelled or
// replaced schedule voids that plan-change invoice. Paying it never renews the subscription
// (subscriptions.IsPlanChangePayment). A scheduled downgrade leaves the invoice as it is.
//
// With no renewal invoice open for the current cycle there is nothing to do: the invoice job bills
// the scheduled plan when it raises one.
func (s *InvoiceService) ReconcileOpenRenewalInvoice(ctx context.Context, tenantID uuid.UUID) error {
	if s.treasury == nil {
		return nil
	}
	sub, err := s.loadSub(ctx, tenantID)
	if err != nil {
		return err
	}
	startKey := sub.CurrentPeriodStart.UTC().Format(time.RFC3339)
	endKey := sub.CurrentPeriodEnd.UTC().Format(time.RFC3339)
	billing, err := subscriptions.BillingPlanFor(ctx, s.orm, sub)
	if err != nil {
		return err
	}

	meta := cloneMeta(sub.Metadata)
	changed := false
	if adjID := stringMeta(meta, metaPlanChangeInvoiceID); adjID != "" {
		if stringMeta(meta, metaPlanChangePeriodStart) == startKey && stringMeta(meta, metaPlanChangePlanID) == billing.ID.String() {
			return nil // already billed for this plan and cycle
		}
		if stringMeta(meta, metaPlanChangePeriodStart) == startKey {
			if err := s.voidInvoice(ctx, adjID); err != nil {
				return fmt.Errorf("void plan-change invoice %s: %w", adjID, err)
			}
			s.log.Info("plan-change invoice voided", zap.String("tenant_id", tenantID.String()), zap.String("invoice_id", adjID))
		}
		for _, k := range []string{metaPlanChangeInvoiceID, metaPlanChangeInvoiceNumber, metaPlanChangePeriodStart, metaPlanChangePlanID} {
			delete(meta, k)
		}
		changed = true
	}

	openRenewal := stringMeta(meta, "last_invoice_id") != "" &&
		stringMeta(meta, "last_invoice_period_start") == startKey &&
		stringMeta(meta, "last_invoice_period_end") == endKey
	invoicedPlanID := sub.PlanID
	if id, perr := uuid.Parse(stringMeta(meta, MetaLastInvoicePlanID)); perr == nil {
		invoicedPlanID = id
	}

	var diff float64
	var invoiced *ent.SubscriptionPlan
	if openRenewal && invoicedPlanID != billing.ID {
		if invoiced, err = s.orm.SubscriptionPlan.Get(ctx, invoicedPlanID); err != nil {
			return fmt.Errorf("load invoiced plan: %w", err)
		}
		diff = subscriptions.EffectivePrice(sub, billing) - subscriptions.EffectivePrice(sub, invoiced)
	}
	if diff <= 0 {
		if openRenewal && invoiced != nil {
			s.log.Info("plan change to a cheaper plan after the renewal invoice went out: invoice kept",
				zap.String("tenant_id", tenantID.String()), zap.String("invoiced_plan", invoiced.PlanCode), zap.String("plan", billing.PlanCode))
		}
		if changed {
			return s.saveMeta(ctx, sub, meta)
		}
		return nil
	}

	months := subscriptions.BillingCycleMonths(string(sub.BillingCycle))
	if months <= 0 {
		months = 1
	}
	currency := billing.Currency
	if currency == "" {
		currency = "KES"
	}
	lines := []map[string]any{{
		"description": fmt.Sprintf("Plan change from %s to %s from %s (%d month(s) x %s %.2f difference)",
			invoiced.Name, billing.Name, sub.CurrentPeriodEnd.UTC().Format("02 Jan 2006"), months, currency, diff),
		"quantity":   months,
		"unit_price": diff,
		"tax_rate":   s.vatRate,
	}}
	total := diff * float64(months) * (1 + s.vatRate/100)

	customerName := ""
	if sub.Edges.Tenant != nil {
		customerName = sub.Edges.Tenant.Name
	}
	customerEmail := billingEmail(sub)
	now := time.Now().UTC()
	resp, err := s.treasury.Post(ctx, fmt.Sprintf("/api/v1/s2s/%s/invoices", s.platformTenantID), map[string]any{
		"customer_name":  customerName,
		"customer_email": customerEmail,
		"invoice_type":   "subscription",
		"invoice_date":   now.Format(time.RFC3339),
		"due_date":       endKey,
		"currency":       currency,
		"reference_id":   sub.ID.String(),
		"reference_type": "subscription",
		"lines":          lines,
		"metadata": map[string]any{
			"billed_tenant_id": sub.TenantID.String(),
			"plan_code":        billing.PlanCode,
			"plan_change_from": invoiced.PlanCode,
			"period_start":     endKey,
		},
	}, s.headers())
	if err != nil || !resp.IsSuccess() {
		if err == nil {
			err = fmt.Errorf("HTTP %d", resp.StatusCode)
		}
		return fmt.Errorf("treasury create plan-change invoice: %w", err)
	}
	var inv struct {
		ID            string `json:"id"`
		InvoiceNumber string `json:"invoice_number"`
		PublicToken   string `json:"public_token"`
	}
	if err := json.Unmarshal(resp.Body, &inv); err != nil {
		return fmt.Errorf("decode plan-change invoice: %w", err)
	}
	if _, err := s.treasury.Post(ctx, fmt.Sprintf("/api/v1/s2s/%s/invoices/%s/send", s.platformTenantID, inv.ID), map[string]any{}, s.headers()); err != nil {
		s.log.Warn("plan-change invoice: send failed (continuing)", zap.String("invoice_id", inv.ID), zap.Error(err))
	}

	// Paid like any subscription invoice (same GL and settlement routing in treasury); the ids
	// below are what make the payment consumer settle it without renewing the subscription.
	payRef := invoicePayRef(sub, inv.ID)
	refs := []string{inv.ID, payRef}
	intentResp, ierr := s.treasury.Post(ctx, fmt.Sprintf("/api/v1/s2s/%s/payments/intents", sub.TenantID),
		subscriptionIntentRequest(sub, inv.ID, inv.InvoiceNumber, billing.PlanCode, currency, customerEmail, total, payRef), s.headers())
	if ierr == nil && intentResp.IsSuccess() {
		var ir struct {
			IntentID string `json:"intent_id"`
		}
		if json.Unmarshal(intentResp.Body, &ir) == nil && ir.IntentID != "" {
			refs = append(refs, ir.IntentID)
		}
	}
	pdfURL, invoiceURL := publicInvoiceLinks(s.treasuryAPIBase, s.treasuryUIBase, inv.PublicToken)
	payURL := ""
	if inv.PublicToken != "" {
		payURL = fmt.Sprintf("%s/i/%s", s.treasuryUIBase, inv.PublicToken)
	}

	meta[metaPlanChangeInvoiceID] = inv.ID
	meta[metaPlanChangeInvoiceNumber] = inv.InvoiceNumber
	meta[metaPlanChangePeriodStart] = startKey
	meta[metaPlanChangePlanID] = billing.ID.String()
	meta[subscriptions.MetaPlanChangePaymentRefs] = subscriptions.AppendPlanChangePaymentRefs(meta, refs...)

	tx, err := s.orm.Tx(ctx)
	if err != nil {
		return fmt.Errorf("tx: %w", err)
	}
	if _, err := tx.TenantSubscription.UpdateOneID(sub.ID).SetMetadata(meta).Save(ctx); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("persist plan-change invoice markers: %w", err)
	}
	s.svc.WriteOutboxEventPublic(ctx, tx, sub.TenantID, "subscription", sub.ID, "invoice_generated", map[string]any{
		"tenant_id":      sub.TenantID.String(),
		"plan_code":      billing.PlanCode,
		"amount":         total,
		"currency":       currency,
		"invoice_number": inv.InvoiceNumber,
		"due_date":       endKey,
		"pay_url":        payURL,
		"pdf_url":        pdfURL,
		"invoice_url":    invoiceURL,
		"notification": map[string]any{
			"target":          "tenant_admin",
			"recipient_email": customerEmail,
		},
	})
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	s.log.Info("plan-change invoice raised", zap.String("tenant_id", tenantID.String()),
		zap.String("invoice_number", inv.InvoiceNumber), zap.String("from", invoiced.PlanCode),
		zap.String("to", billing.PlanCode), zap.Float64("amount", total))
	return nil
}

// voidInvoice cancels an unpaid platform invoice. Treasury only refuses a fully paid one, so a
// part payment is checked here: money already taken on a plan-change invoice is never voided away
// (the error leaves the markers in place for a manual refund or credit).
func (s *InvoiceService) voidInvoice(ctx context.Context, invoiceID string) error {
	resp, err := s.treasury.Get(ctx, fmt.Sprintf("/api/v1/s2s/%s/invoices/%s", s.platformTenantID, invoiceID), s.headers())
	if err != nil || !resp.IsSuccess() {
		return fmt.Errorf("read invoice: %w", err)
	}
	var inv struct {
		AmountPaid json.RawMessage `json:"amount_paid"` // decimal: quoted or bare number
		Status     string          `json:"status"`
	}
	if err := json.Unmarshal(resp.Body, &inv); err != nil {
		return fmt.Errorf("decode invoice: %w", err)
	}
	if inv.Status == "cancelled" || inv.Status == "void" {
		return nil
	}
	if paid, _ := strconv.ParseFloat(strings.Trim(string(inv.AmountPaid), `"`), 64); paid > 0 || inv.Status == "paid" {
		return fmt.Errorf("invoice already has %.2f paid on it", paid)
	}
	resp, err = s.treasury.Post(ctx, fmt.Sprintf("/api/v1/s2s/%s/invoices/%s/void", s.platformTenantID, invoiceID), map[string]any{}, s.headers())
	if err != nil {
		return err
	}
	if !resp.IsSuccess() {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(resp.Body))
	}
	return nil
}

func (s *InvoiceService) saveMeta(ctx context.Context, sub *ent.TenantSubscription, meta map[string]any) error {
	if _, err := s.orm.TenantSubscription.UpdateOneID(sub.ID).SetMetadata(meta).Save(ctx); err != nil {
		return fmt.Errorf("save invoice markers: %w", err)
	}
	return nil
}
