package plans

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// SubscriptionPlan represents a subscription plan entity.
type SubscriptionPlan struct {
	ID                      uuid.UUID        `json:"id"`
	PlanCode                string           `json:"planCode"`
	Name                    string           `json:"name"`
	Description             string           `json:"description"`
	BillingCycle            string           `json:"billingCycle"`
	BasePrice               float64          `json:"basePrice"`
	SetupFee                float64          `json:"setupFee"`
	OnetimeAllProductsPrice *float64         `json:"onetimeAllProductsPrice,omitempty"`
	UseSumBasedPricing      bool             `json:"useSumBasedPricing"`
	Currency                string           `json:"currency"`
	IsActive                bool             `json:"isActive"`
	IsPublic                bool             `json:"isPublic"`
	TierOrder               int              `json:"tierOrder"`
	TierLimits              map[string]any   `json:"tierLimits"`
	FreeTrialDays           int              `json:"freeTrialDays"`
	Metadata                map[string]any   `json:"metadata,omitempty"`
	ServiceTag              *string          `json:"serviceTag,omitempty"`
	PlanType                string           `json:"planType,omitempty"`
	CreatedAt               time.Time        `json:"createdAt"`
	UpdatedAt               time.Time        `json:"updatedAt"`
	Features                []*PlanFeature   `json:"features"`
	DiscountRules           []map[string]any `json:"discountRules,omitempty"`
}

// Product represents a subscribable product.
type Product struct {
	ID            uuid.UUID
	Code          string
	Name          string
	Description   string
	Category      string
	IsPlatform    bool // if true, this product is a platform
	IsBaseService bool // if true, this product is a base service
	MonthlyPrice  float64
	YearlyPrice   float64
	OnetimePrice  float64
	Metadata      map[string]any
}

// PlanFeature represents a plan feature mapping.
type PlanFeature struct {
	ID               uuid.UUID      `json:"id"`
	PlanID           uuid.UUID      `json:"planId"`
	FeatureCode      string         `json:"featureCode"`
	IsIncluded       bool           `json:"isIncluded"`
	LimitValue       *int           `json:"limitValue,omitempty"`
	OverageUnitPrice float64        `json:"overageUnitPrice"`
	Metadata         map[string]any `json:"metadata,omitempty"`
	CreatedAt        time.Time      `json:"createdAt"`
}

// Repository abstracts persistence for subscription plans.
type Repository interface {
	CreatePlan(ctx context.Context, plan *SubscriptionPlan) error
	UpdatePlan(ctx context.Context, plan *SubscriptionPlan) error
	FindPlanByID(ctx context.Context, id uuid.UUID) (*SubscriptionPlan, error)
	FindPlanByCode(ctx context.Context, code string) (*SubscriptionPlan, error)
	ListPlans(ctx context.Context, activeOnly bool, serviceTag *string) ([]*SubscriptionPlan, error)
	DeletePlan(ctx context.Context, id uuid.UUID) error

	CreateFeature(ctx context.Context, feature *PlanFeature) error
	UpdateFeature(ctx context.Context, feature *PlanFeature) error
	FindFeatureByID(ctx context.Context, id uuid.UUID) (*PlanFeature, error)
	ListFeaturesByPlan(ctx context.Context, planID uuid.UUID) ([]*PlanFeature, error)
	DeleteFeature(ctx context.Context, id uuid.UUID) error

	// Product methods
	FindProductByCode(ctx context.Context, code string) (*Product, error)
	ListProducts(ctx context.Context) ([]*Product, error)
}
