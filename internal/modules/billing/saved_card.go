package billing

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/bengobox/subscription-service/internal/ent"
)

// ChargeResult reports an automatic saved-card charge of the current period's invoice.
type ChargeResult struct {
	Attempted     bool   `json:"attempted"`
	InvoiceNumber string `json:"invoice_number,omitempty"`
	Status        string `json:"status,omitempty"`
	Reason        string `json:"reason,omitempty"`
}

// ChargeSavedCard collects the current period's subscription invoice from the card the tenant
// saved on an earlier Paystack payment (metadata paystack_auth_code, stored by the treasury
// payment consumer). The invoice is generated first when the invoice job has not done so yet
// (GenerateAndSend is idempotent per period). The charge is the same subscription intent the pay
// link uses, so the existing payment.succeeded path renews the subscription and treasury settles
// the invoice. One attempt per invoice per day (the reference carries the date, and treasury
// intents are idempotent per reference). Without a saved card nothing is attempted.
func (s *InvoiceService) ChargeSavedCard(ctx context.Context, sub *ent.TenantSubscription) (*ChargeResult, error) {
	authCode := stringMeta(sub.Metadata, "paystack_auth_code")
	if authCode == "" {
		return &ChargeResult{Reason: "no saved card"}, nil
	}
	inv, err := s.GenerateAndSend(ctx, sub, false)
	if err != nil {
		return nil, fmt.Errorf("generate invoice: %w", err)
	}
	if inv == nil || inv.InvoiceID == "" {
		return &ChargeResult{Reason: "no invoice for the period"}, nil
	}

	// Charge what is still owed on the invoice (a part payment or credit note may have reduced it).
	resp, err := s.treasury.Get(ctx, fmt.Sprintf("/api/v1/s2s/%s/invoices/%s", s.platformTenantID, inv.InvoiceID), s.headers())
	if err != nil || !resp.IsSuccess() {
		return nil, fmt.Errorf("read invoice %s: %w", inv.InvoiceNumber, err)
	}
	var due struct {
		AmountDue json.RawMessage `json:"amount_due"` // decimal: quoted or bare number
		Currency  string          `json:"currency"`
		Status    string          `json:"status"`
	}
	if err := json.Unmarshal(resp.Body, &due); err != nil {
		return nil, fmt.Errorf("decode invoice %s: %w", inv.InvoiceNumber, err)
	}
	amountDue, _ := strconv.ParseFloat(strings.Trim(string(due.AmountDue), `"`), 64)
	if amountDue <= 0 || due.Status == "paid" || due.Status == "void" || due.Status == "cancelled" {
		return &ChargeResult{InvoiceNumber: inv.InvoiceNumber, Reason: "nothing outstanding"}, nil
	}

	planCode := ""
	if sub.Edges.Plan != nil {
		planCode = sub.Edges.Plan.PlanCode
	}
	ref := invoicePayRef(sub, inv.InvoiceID) + "-C" + time.Now().UTC().Format("060102")
	req := subscriptionIntentRequest(sub, inv.InvoiceID, inv.InvoiceNumber, planCode, due.Currency, billingEmail(sub), amountDue, ref)
	req["authorization_code"] = authCode
	req["metadata"].(map[string]any)["collection"] = "saved_card"

	chargeResp, err := s.treasury.Post(ctx, fmt.Sprintf("/api/v1/s2s/%s/payments/intents/charge-saved-card", sub.TenantID), req, s.headers())
	if err != nil {
		return nil, fmt.Errorf("charge saved card: %w", err)
	}
	res := &ChargeResult{Attempted: true, InvoiceNumber: inv.InvoiceNumber}
	var out struct {
		Status string `json:"status"`
		Error  string `json:"error"`
	}
	_ = chargeResp.DecodeJSON(&out)
	if chargeResp.IsSuccess() {
		res.Status = out.Status
	} else {
		res.Status, res.Reason = "failed", out.Error
	}
	s.log.Info("subscription invoice saved-card charge",
		zap.String("tenant_id", sub.TenantID.String()), zap.String("invoice", inv.InvoiceNumber),
		zap.String("status", res.Status), zap.String("reason", res.Reason))
	return res, nil
}
