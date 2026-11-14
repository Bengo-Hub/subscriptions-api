package billing

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/modules/subscriptions"
)

// metaProrationInvoices lists the upgrades already invoiced ("<from>:<to>:<period start>"), so a
// retried call never bills the same upgrade twice.
const metaProrationInvoices = "proration_invoice_keys"

// prorationKey identifies one upgrade within one billing period.
func prorationKey(from, to uuid.UUID, periodStart time.Time) string {
	return from.String() + ":" + to.String() + ":" + periodStart.UTC().Format(time.RFC3339)
}

// InvoiceProratedUpgrade bills the rest of the current period after an upgrade applied at once
// (owner, 2026-11-14): one line for the monthly price difference times the months of the period
// times the share of the period left (subscriptions.ProratedUpgradeCharge), VAT on top when the
// platform charges it, due at once. Paying it never renews the subscription: its ids go into
// plan_change_payment_refs like the scheduled plan-change invoice. The renewal itself is billed
// at the new plan by the normal renewal invoice (or ReconcileOpenRenewalInvoice when it is out).
func (s *InvoiceService) InvoiceProratedUpgrade(ctx context.Context, tenantID uuid.UUID, from, to *ent.SubscriptionPlan, charge subscriptions.ProratedCharge) error {
	if s.treasury == nil || charge.NetAmount <= 0 {
		return nil
	}
	sub, err := s.loadSub(ctx, tenantID)
	if err != nil {
		return err
	}
	key := prorationKey(from.ID, to.ID, charge.PeriodStart)
	var done []string
	if raw, ok := sub.Metadata[metaProrationInvoices].([]any); ok {
		for _, v := range raw {
			if k, ok := v.(string); ok {
				if k == key {
					return nil // already invoiced
				}
				done = append(done, k)
			}
		}
	}

	currency := to.Currency
	if currency == "" {
		currency = "KES"
	}
	total := charge.NetAmount * (1 + s.vatRate/100)
	lines := []map[string]any{{
		"description": fmt.Sprintf("Upgrade from %s to %s, %s to %s (%d month(s) x %s %.2f difference x %.0f%% of the period left)",
			from.Name, to.Name, charge.From.UTC().Format("02 Jan 2006"), charge.PeriodEnd.UTC().Format("02 Jan 2006"),
			charge.Months, currency, charge.MonthlyDiff, charge.Fraction*100),
		"quantity":   1,
		"unit_price": charge.NetAmount,
		"tax_rate":   s.vatRate,
	}}
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
		"due_date":       now.Format(time.RFC3339),
		"currency":       currency,
		"reference_id":   sub.ID.String(),
		"reference_type": "subscription",
		"lines":          lines,
		"metadata": map[string]any{
			"billed_tenant_id": sub.TenantID.String(),
			"plan_code":        to.PlanCode,
			"plan_change_from": from.PlanCode,
			"proration":        true,
			"period_end":       charge.PeriodEnd.UTC().Format(time.RFC3339),
		},
	}, s.headers())
	if err != nil || !resp.IsSuccess() {
		if err == nil {
			err = fmt.Errorf("HTTP %d", resp.StatusCode)
		}
		return fmt.Errorf("treasury create upgrade invoice: %w", err)
	}
	var inv struct {
		ID            string `json:"id"`
		InvoiceNumber string `json:"invoice_number"`
		PublicToken   string `json:"public_token"`
	}
	if err := json.Unmarshal(resp.Body, &inv); err != nil {
		return fmt.Errorf("decode upgrade invoice: %w", err)
	}
	if _, err := s.treasury.Post(ctx, fmt.Sprintf("/api/v1/s2s/%s/invoices/%s/send", s.platformTenantID, inv.ID), map[string]any{}, s.headers()); err != nil {
		s.log.Warn("upgrade invoice: send failed (continuing)", zap.String("invoice_id", inv.ID), zap.Error(err))
	}

	payRef := invoicePayRef(sub, inv.ID)
	refs := []string{inv.ID, payRef}
	intentResp, ierr := s.treasury.Post(ctx, fmt.Sprintf("/api/v1/s2s/%s/payments/intents", sub.TenantID),
		subscriptionIntentRequest(sub, inv.ID, inv.InvoiceNumber, to.PlanCode, currency, customerEmail, total, payRef), s.headers())
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

	meta := cloneMeta(sub.Metadata)
	keys := make([]any, 0, len(done)+1)
	for _, k := range done {
		keys = append(keys, k)
	}
	meta[metaProrationInvoices] = append(keys, key)
	meta[subscriptions.MetaPlanChangePaymentRefs] = subscriptions.AppendPlanChangePaymentRefs(meta, refs...)

	tx, err := s.orm.Tx(ctx)
	if err != nil {
		return fmt.Errorf("tx: %w", err)
	}
	if _, err := tx.TenantSubscription.UpdateOneID(sub.ID).SetMetadata(meta).Save(ctx); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("persist upgrade invoice markers: %w", err)
	}
	s.svc.WriteOutboxEventPublic(ctx, tx, sub.TenantID, "subscription", sub.ID, "invoice_generated", map[string]any{
		"tenant_id":      sub.TenantID.String(),
		"plan_code":      to.PlanCode,
		"amount":         total,
		"currency":       currency,
		"invoice_number": inv.InvoiceNumber,
		"due_date":       now.Format(time.RFC3339),
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
	s.log.Info("upgrade proration invoice raised", zap.String("tenant_id", tenantID.String()),
		zap.String("invoice_number", inv.InvoiceNumber), zap.String("from", from.PlanCode),
		zap.String("to", to.PlanCode), zap.Float64("amount", total))
	return nil
}
