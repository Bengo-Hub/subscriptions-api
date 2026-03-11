package plans

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// SubscriptionPlan represents a subscription plan entity.
type SubscriptionPlan struct {
	ID                      uuid.UUID
	PlanCode                string
	Name                    string
	Description             string
	BillingCycle            string
	BasePrice               float64
	OnetimeAllProductsPrice *float64
	UseSumBasedPricing      bool
	Currency                string
	IsActive                bool
	IsPublic                bool
	TierOrder               int
	TierLimits              map[string]any
	Metadata                map[string]any
	CreatedAt               time.Time
	UpdatedAt               time.Time
	Features                []*PlanFeature
	DiscountRules           []map[string]any
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
	ID          uuid.UUID
	PlanID      uuid.UUID
	FeatureCode string
	IsIncluded      bool
	LimitValue      *int
	OverageUnitPrice float64
	Metadata        map[string]any
	CreatedAt   time.Time
}

// Repository abstracts persistence for subscription plans.
type Repository interface {
	CreatePlan(ctx context.Context, plan *SubscriptionPlan) error
	UpdatePlan(ctx context.Context, plan *SubscriptionPlan) error
	FindPlanByID(ctx context.Context, id uuid.UUID) (*SubscriptionPlan, error)
	FindPlanByCode(ctx context.Context, code string) (*SubscriptionPlan, error)
	ListPlans(ctx context.Context, activeOnly bool) ([]*SubscriptionPlan, error)
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
