package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/coupon"
	"github.com/google/uuid"
)

func seedCoupons(ctx context.Context, tx *ent.Tx) error {
	type couponDef struct {
		code               string
		name               string
		description        string
		couponType         coupon.Type
		value              float64
		applicablePlanCodes []string
		minPlanPrice       float64
		maxUses            int
		validDays          int // days from now; -1 = no expiry
	}

	adminUserID := uuid.MustParse("00000000-0000-0000-0000-000000000001") // platform seed admin

	defs := []couponDef{
		{
			code:        "WELCOME20",
			name:        "Welcome 20% Off",
			description: "20% discount on your first month's subscription. Valid for new tenants.",
			couponType:  coupon.TypePercentage,
			value:       20,
			maxUses:     -1, // one per tenant enforced by business logic
			validDays:   365,
		},
		{
			code:        "ANNUAL10",
			name:        "Annual Plan 10% Off",
			description: "10% discount on any annual subscription plan.",
			couponType:  coupon.TypePercentage,
			value:       10,
			applicablePlanCodes: []string{
				"ORDERING_STARTER_YEARLY", "ORDERING_GROWTH_YEARLY", "ORDERING_PROFESSIONAL_YEARLY",
				"POS_SUITE_STARTER_YEARLY", "POS_SUITE_GROWTH_YEARLY", "POS_SUITE_PROFESSIONAL_YEARLY",
				"POWERSUITE_STARTER_YEARLY", "POWERSUITE_GROWTH_YEARLY", "POWERSUITE_PROFESSIONAL_YEARLY",
			},
			maxUses:   -1,
			validDays: 365,
		},
		{
			code:        "DEMO_FREE",
			name:        "30 Days Free",
			description: "One free month on any plan. Used for demo and pilot tenants.",
			couponType:  coupon.TypeFreeMonths,
			value:       1, // 1 free month
			maxUses:     50,
			validDays:   180,
		},
		{
			code:         "STARTER500",
			name:         "KES 500 Off Starter",
			description:  "KES 500 credit on any Starter plan.",
			couponType:   coupon.TypeFixedKes,
			value:        500,
			maxUses:      100,
			validDays:    90,
			minPlanPrice: 2000,
		},
		{
			code:        "GROWTH1000",
			name:        "KES 1,000 Off Growth",
			description: "KES 1,000 credit on any Growth plan.",
			couponType:  coupon.TypeFixedKes,
			value:       1000,
			maxUses:     50,
			validDays:   90,
			minPlanPrice: 4000,
		},
	}

	for _, d := range defs {
		code := strings.ToUpper(strings.TrimSpace(d.code))
		id := uuid.NewSHA1(uuid.NameSpaceOID, []byte("coupon:"+code))

		var validUntil *time.Time
		if d.validDays > 0 {
			t := time.Now().Add(time.Duration(d.validDays) * 24 * time.Hour)
			validUntil = &t
		}

		existing, err := tx.Coupon.Get(ctx, id)
		if err != nil && !ent.IsNotFound(err) {
			return fmt.Errorf("lookup coupon %s: %w", code, err)
		}

		if existing != nil {
			upd := tx.Coupon.UpdateOneID(id).
				SetName(d.name).
				SetType(d.couponType).
				SetValue(d.value).
				SetMinPlanPrice(d.minPlanPrice).
				SetMaxUses(d.maxUses).
				SetIsActive(false).
				SetNillableValidUntil(validUntil).
				SetCreatedBy(adminUserID)
			if len(d.applicablePlanCodes) > 0 {
				upd.SetApplicablePlanCodes(d.applicablePlanCodes)
			}
			if _, err := upd.Save(ctx); err != nil {
				return fmt.Errorf("update coupon %s: %w", code, err)
			}
		} else {
			create := tx.Coupon.Create().
				SetID(id).
				SetCode(code).
				SetName(d.name).
				SetType(d.couponType).
				SetValue(d.value).
				SetMinPlanPrice(d.minPlanPrice).
				SetMaxUses(d.maxUses).
				SetIsActive(false).
				SetValidFrom(time.Now()).
				SetNillableValidUntil(validUntil).
				SetCreatedBy(adminUserID)
			if len(d.applicablePlanCodes) > 0 {
				create.SetApplicablePlanCodes(d.applicablePlanCodes)
			}
			if _, err := create.Save(ctx); err != nil {
				return fmt.Errorf("create coupon %s: %w", code, err)
			}
		}

		log.Printf("  coupon: %s (%s, %.0f)", code, d.couponType, d.value)
	}

	return nil
}
