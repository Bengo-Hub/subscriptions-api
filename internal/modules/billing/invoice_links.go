package billing

import (
	"fmt"
	"strings"
)

// MetaLastInvoiceURL is the subscription / support cycle metadata key holding the last invoice's
// public page (treasury-ui /i/{token}): the link the tenant opens to see and pay the invoice.
// Reminders reuse it, and WhatsApp sends it as the message's button.
const MetaLastInvoiceURL = "last_invoice_url"

// publicInvoiceLinks are an invoice's public PDF and public page, from its public token. Both are
// empty without a token.
func publicInvoiceLinks(treasuryAPIBase, treasuryUIBase, token string) (pdfURL, pageURL string) {
	if token == "" {
		return "", ""
	}
	return fmt.Sprintf("%s/api/v1/public/invoices/%s/pdf", strings.TrimRight(treasuryAPIBase, "/"), token),
		fmt.Sprintf("%s/i/%s", strings.TrimRight(treasuryUIBase, "/"), token)
}
