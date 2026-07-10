package billing

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/coupon"
	"github.com/bengobox/subscription-service/internal/ent/subscriptioncredit"
	"github.com/bengobox/subscription-service/internal/ent/tenantsubscription"
	"github.com/bengobox/subscription-service/internal/modules/subscriptions"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// CouponService validates and redeems platform-issued subscription discount codes.
// These codes apply to tenant subscription fees only, not customer food-order PromoCode (ordering-backend).
type CouponService struct {
	log    *zap.Logger
	orm    *ent.Client
	credit *CreditService
}

// NewCouponService creates a new CouponService.
func NewCouponService(log *zap.Logger, orm *ent.Client, credit *CreditService) *CouponService {
	return &CouponService{
		log:    log.Named("billing.coupon"),
		orm:    orm,
		credit: credit,
	}
}

// ValidateCouponResult holds the computed discount without applying it.
type ValidateCouponResult struct {
	CouponID      uuid.UUID
	Code          string
	Type          coupon.Type
	DiscountKes   int    // computed KES discount for fixed_kes/percentage; 0 for free_months
	FreeMonths    int    // for free_months type
	Description   string
}

// ValidateCoupon checks whether a coupon code is valid for the given plan and price,
// and returns the computed discount. Does NOT apply or consume the coupon.
// billingCycleMonths is the number of months the tenant is being billed for in one
// invoice (1 for MONTHLY, 6 for SEMI_ANNUAL, 12 for ANNUAL — see BillingCycleMonths).
func (s *CouponService) ValidateCoupon(ctx context.Context, code, planCode string, planPriceKes float64, billingCycleMonths int) (*ValidateCouponResult, error) {
	c, err := s.fetchActive(ctx, code)
	if err != nil {
		return nil, err
	}

	if err := s.checkApplicability(c, planCode, planPriceKes); err != nil {
		return nil, err
	}

	result := &ValidateCouponResult{
		CouponID:    c.ID,
		Code:        c.Code,
		Type:        c.Type,
		Description: c.Name,
	}

	switch c.Type {
	case coupon.TypePercentage:
		result.DiscountKes = int(planPriceKes*c.Value/100) * evergreenMonths(c, billingCycleMonths)
	case coupon.TypeFixedKes:
		result.DiscountKes = int(c.Value) * evergreenMonths(c, billingCycleMonths)
	case coupon.TypeFreeMonths:
		result.FreeMonths = int(c.Value)
	}

	return result, nil
}

// evergreenMonths returns the discount multiplier for a per-month coupon amount:
// a coupon with no valid_until (an "evergreen" code) has no natural expiry to bound
// it to a single month, so it applies to every month covered by the invoice being
// billed (e.g. a KES 1,000 evergreen coupon on a SEMI_ANNUAL invoice discounts all
// 6 months, not just one). Coupons with a valid_until remain single-application
// (multiplier 1) — they are promotional, one-off discounts, not a recurring rate cut.
func evergreenMonths(c *ent.Coupon, billingCycleMonths int) int {
	if billingCycleMonths <= 0 {
		billingCycleMonths = 1
	}
	if c.ValidUntil == nil {
		return billingCycleMonths
	}
	return 1
}

// RedeemCoupon validates the coupon, converts it to a SubscriptionCreditTransaction,
// and credits the tenant's wallet. Returns the credit amount added (KES).
//
// Discount exclusivity: a subscription whose one-time setup fee was waived by a 6+ month
// billing period gets no OTHER *one-off promotional* discount — bounded (valid_until set)
// coupons are rejected for those tenants so a structural waiver can't be stacked with a
// limited-time promo. Evergreen coupons (no valid_until, e.g. a standing small-business
// rate) are exempt from this exclusivity: they represent an ongoing per-month rate cut,
// not a stackable bonus, so they combine with the setup-fee waiver by design.
func (s *CouponService) RedeemCoupon(ctx context.Context, tenantID uuid.UUID, code, planCode string, planPriceKes float64) (int, error) {
	c, err := s.fetchActive(ctx, code)
	if err != nil {
		return 0, err
	}

	if err := s.checkApplicability(c, planCode, planPriceKes); err != nil {
		return 0, err
	}

	billingCycleMonths := 1
	if sub, serr := s.orm.TenantSubscription.Query().
		Where(tenantsubscription.TenantIDEQ(tenantID)).
		Only(ctx); serr == nil {
		if c.ValidUntil != nil {
			waived, _ := sub.Metadata[subscriptions.MetaSetupFeeWaived].(bool)
			if waived || subscriptions.CycleWaivesSetupFee(string(sub.BillingCycle)) {
				return 0, fmt.Errorf("coupons cannot be combined with the 6+ month setup-fee waiver")
			}
		}
		if m := subscriptions.BillingCycleMonths(string(sub.BillingCycle)); m > 0 {
			billingCycleMonths = m
		}
	}

	// Compute credit value. An evergreen coupon (no valid_until) has no natural
	// per-use expiry, so it is treated as a recurring per-month rate cut and applies
	// to every month the tenant is being billed for in this cycle (see evergreenMonths).
	var creditKes int
	switch c.Type {
	case coupon.TypePercentage:
		creditKes = int(planPriceKes*c.Value/100) * evergreenMonths(c, billingCycleMonths)
	case coupon.TypeFixedKes:
		creditKes = int(c.Value) * evergreenMonths(c, billingCycleMonths)
	case coupon.TypeFreeMonths:
		// free_months: credit = full plan price × months
		creditKes = int(planPriceKes) * int(c.Value)
	}

	if creditKes <= 0 {
		return 0, fmt.Errorf("coupon yields zero value for this plan")
	}

	// Increment used_count atomically
	updated, err := s.orm.Coupon.UpdateOneID(c.ID).
		SetUsedCount(c.UsedCount + 1).
		Save(ctx)
	if err != nil {
		return 0, fmt.Errorf("consume coupon: %w", err)
	}

	// Add credit to wallet
	if err := s.credit.AddCredits(ctx, tenantID, creditKes, "coupon_redeemed", c.ID, "coupon"); err != nil {
		// Roll back used_count increment on credit failure
		_, _ = s.orm.Coupon.UpdateOneID(c.ID).SetUsedCount(updated.UsedCount - 1).Save(ctx)
		return 0, fmt.Errorf("add credits: %w", err)
	}

	s.log.Info("coupon redeemed",
		zap.String("tenant_id", tenantID.String()),
		zap.String("code", code),
		zap.Int("credit_kes", creditKes),
	)
	return creditKes, nil
}

// fetchActive loads a coupon by code, checking it is active and not expired/exhausted.
func (s *CouponService) fetchActive(ctx context.Context, code string) (*ent.Coupon, error) {
	c, err := s.orm.Coupon.Query().
		Where(
			coupon.CodeEQ(strings.ToUpper(strings.TrimSpace(code))),
			coupon.IsActiveEQ(true),
		).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("coupon not found or inactive")
		}
		return nil, fmt.Errorf("lookup coupon: %w", err)
	}

	now := time.Now()
	if c.ValidFrom.After(now) {
		return nil, fmt.Errorf("coupon is not yet valid")
	}
	if c.ValidUntil != nil && c.ValidUntil.Before(now) {
		return nil, fmt.Errorf("coupon has expired")
	}
	if c.MaxUses >= 0 && c.UsedCount >= c.MaxUses {
		return nil, fmt.Errorf("coupon usage limit reached")
	}

	return c, nil
}

// checkApplicability validates the coupon against plan and price constraints.
func (s *CouponService) checkApplicability(c *ent.Coupon, planCode string, planPriceKes float64) error {
	if planPriceKes < c.MinPlanPrice {
		return fmt.Errorf("plan price KES %.0f is below the minimum KES %.0f for this coupon", planPriceKes, c.MinPlanPrice)
	}

	if len(c.ApplicablePlanCodes) > 0 {
		found := false
		for _, pc := range c.ApplicablePlanCodes {
			if strings.EqualFold(pc, planCode) {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("coupon is not valid for plan %s", planCode)
		}
	}

	return nil
}

// GetByCode returns a coupon by code for display purposes (no validation side effects).
func (s *CouponService) GetByCode(ctx context.Context, code string) (*ent.Coupon, error) {
	c, err := s.orm.Coupon.Query().
		Where(coupon.CodeEQ(strings.ToUpper(strings.TrimSpace(code)))).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("coupon not found")
		}
		return nil, err
	}
	return c, nil
}

// walletExists checks whether a credit wallet exists for the tenant.
func walletExists(ctx context.Context, orm *ent.Client, tenantID uuid.UUID) bool {
	exists, _ := orm.SubscriptionCredit.Query().
		Where(subscriptioncredit.TenantIDEQ(tenantID)).
		Exist(ctx)
	return exists
}
