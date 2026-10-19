package credits

import (
	"time"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/modules/billing"
)

// Metadata keys stored on the activation ProductSubscription of a tiered credit service.
const (
	MetaTierCode      = billing.MetaTierCode
	MetaTierPeriodEnd = billing.MetaTierPeriodEnd
	MetaTierAutoRenew = billing.MetaTierAutoRenew
)

// liveTier returns the tier a tenant has paid for and whether its paid period is still running.
func liveTier(spec billing.MeteredService, ps *ent.ProductSubscription, now time.Time) (billing.CreditTier, bool) {
	if ps == nil {
		return billing.CreditTier{}, false
	}
	code, _ := ps.Metadata[MetaTierCode].(string)
	tier, ok := spec.Tier(code)
	if !ok {
		return billing.CreditTier{}, false
	}
	end, _ := ps.Metadata[MetaTierPeriodEnd].(string)
	t, err := time.Parse(time.RFC3339, end)
	if err != nil || !t.After(now) {
		return billing.CreditTier{}, false
	}
	return tier, true
}
