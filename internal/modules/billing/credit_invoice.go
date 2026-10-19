package billing

import (
	"context"
	"fmt"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/productsubscription"
)

// tierRenewalLines returns one invoice line per recurring credit tier the tenant keeps on
// auto-renew (WhatsApp), priced for every month of the billing period so the tier is paid in the
// same invoice as the plan. The matching grant happens when that invoice is paid.
func tierRenewalLines(ctx context.Context, orm *ent.Client, sub *ent.TenantSubscription, months int, vatRate float64) ([]map[string]any, float64, error) {
	if months <= 0 {
		months = 1
	}
	var lines []map[string]any
	var taxable float64
	for _, code := range MeteredServiceCodes() {
		spec, _ := LookupMeteredService(code)
		if len(spec.Tiers) == 0 || spec.ProductCode == "" {
			continue
		}
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
		if !ok {
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
