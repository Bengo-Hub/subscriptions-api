package billing

import (
	"context"
	"fmt"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/productsubscription"
)

// tierRenewalLines returns one invoice line per recurring credit tier the tenant keeps on
// auto-renew, priced for every month of the billing period so the tier is paid in the same invoice
// as the plan. The matching grant happens when that invoice is paid. Retired tiers (the WhatsApp
// tiers, since 2026-11-12) are never renewed: they run to the end of their period.
func tierRenewalLines(ctx context.Context, orm *ent.Client, sub *ent.TenantSubscription, months int, vatRate float64) ([]map[string]any, float64, error) {
	if months <= 0 {
		months = 1
	}
	var lines []map[string]any
	var taxable float64
	seen := map[string]bool{}
	for _, code := range MeteredServiceCodes() {
		spec, _ := LookupMeteredService(code)
		if len(spec.OfferedTiers()) == 0 || spec.ProductCode == "" || seen[spec.ProductCode] {
			continue
		}
		// Services sharing one activation row bill its tier once.
		seen[spec.ProductCode] = true
		ps, err := orm.ProductSubscription.Query().
			Where(
				productsubscription.TenantSubscriptionIDEQ(sub.ID),
				productsubscription.ProductCodeEQ(spec.ProductCode),
				productsubscription.StatusEQ(productsubscription.StatusActive),
			).Only(ctx)
		if err != nil {
			if ent.IsNotFound(err) {
				continue
			}
			return nil, 0, fmt.Errorf("load %s activation: %w", spec.ProductCode, err)
		}
		if renew, ok := ps.Metadata[MetaTierAutoRenew].(bool); ok && !renew {
			continue
		}
		tierCode, _ := ps.Metadata[MetaTierCode].(string)
		tier, ok := spec.Tier(tierCode)
		if !ok || tier.Retired {
			continue
		}
		lines = append(lines, map[string]any{
			"description": fmt.Sprintf("%s %s monthly plan (%d month(s))", spec.Label, tier.Name, months),
			"quantity":    months,
			"unit_price":  tier.PriceKES,
			"tax_rate":    vatRate,
		})
		taxable += tier.PriceKES * float64(months)
	}
	return lines, taxable, nil
}
