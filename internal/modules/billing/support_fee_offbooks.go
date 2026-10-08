package billing

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/bengobox/subscription-service/internal/ent/supportfeecycle"
)

// MetaCycleOffBooks marks a support charge whose invoice treasury holds off the company's books.
const MetaCycleOffBooks = "off_books"

// MoveOpenSupportChargesOffBooks takes an agreement's already-invoiced, unpaid charges off the
// company's books once the agreement is collected personally. A new charge is raised off-books at
// creation; this covers the ones raised before the switch, which otherwise stayed in revenue and AR
// (boi's Dedicated Support Engineer invoice INV-261008-000032, 2026-10-08). Each invoice keeps its
// number; its pay link is narrowed to M-Pesa, the personal channel's only rail. Returns how many
// moved; a charge treasury refuses (already part-paid, or fiscalised) is reported in the error and
// the rest still move.
func (s *InvoiceService) MoveOpenSupportChargesOffBooks(ctx context.Context, agreementID uuid.UUID) (int, error) {
	if s.treasury == nil {
		return 0, fmt.Errorf("treasury client not configured")
	}
	cycles, err := s.orm.SupportFeeCycle.Query().
		Where(
			supportfeecycle.AgreementIDEQ(agreementID),
			supportfeecycle.StatusIn(supportfeecycle.StatusINVOICED, supportfeecycle.StatusOVERDUE),
		).
		All(ctx)
	if err != nil {
		return 0, fmt.Errorf("load open support charges: %w", err)
	}
	moved := 0
	var errs []error
	for _, c := range cycles {
		invID := stringMeta(c.Metadata, "last_invoice_id")
		if invID == "" {
			continue
		}
		if v, _ := c.Metadata[MetaCycleOffBooks].(bool); v {
			continue
		}
		resp, err := s.treasury.Post(ctx, fmt.Sprintf("/api/v1/s2s/%s/invoices/%s/move-off-books", s.platformTenantID, invID), map[string]any{}, s.headers())
		if err != nil || !resp.IsSuccess() {
			detail := ""
			if err == nil {
				detail = fmt.Sprintf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(resp.Body)))
			} else {
				detail = err.Error()
			}
			errs = append(errs, fmt.Errorf("%s: %s", stringMeta(c.Metadata, "last_invoice_number"), detail))
			continue
		}
		meta := cloneMeta(c.Metadata)
		meta[MetaCycleOffBooks] = true
		if pay := personalPayURL(stringMeta(meta, "last_invoice_pay_url")); pay != "" {
			meta["last_invoice_pay_url"] = pay
		}
		if err := s.orm.SupportFeeCycle.UpdateOneID(c.ID).SetMetadata(meta).Exec(ctx); err != nil {
			errs = append(errs, fmt.Errorf("%s: moved in treasury but not recorded: %w", stringMeta(c.Metadata, "last_invoice_number"), err))
			continue
		}
		moved++
		s.log.Info("support charge moved off the books",
			zap.String("cycle_id", c.ID.String()), zap.String("invoice", stringMeta(c.Metadata, "last_invoice_number")))
	}
	return moved, errors.Join(errs...)
}

// personalPayURL narrows a treasury /pay link to the rails a personal channel takes (M-Pesa).
// Any other link (the public invoice page) runs its own checkout from the invoice and is kept.
func personalPayURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || strings.TrimRight(u.Path, "/") != "/pay" {
		return ""
	}
	q := u.Query()
	q.Set("gateways", "mpesa,payhero_offline")
	u.RawQuery = q.Encode()
	return u.String()
}
