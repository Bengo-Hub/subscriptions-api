package plans

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// SubscriptionPlan represents a subscription plan entity.
type SubscriptionPlan struct {
	ID            uuid.UUID
	PlanCode      string
	Name          string
	Description   string
	BillingCycle  string
	BasePrice     float64
	Currency      string
	IsActive      bool
	IsPublic      bool
	TierOrder     int
	TierLimits    map[string]any
	Metadata      map[string]any
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// PlanFeature represents a plan feature mapping.
type PlanFeature struct {
	ID          uuid.UUID
	PlanID      uuid.UUID
	FeatureCode string
	IsIncluded  bool
	LimitValue  *int
	Metadata    map[string]any
	CreatedAt   time.Time
}

// Repository abstracts persistence for subscription plans.
type Repository interface {
	CreatePlan(ctx context.Context, plan *SubscriptionPlan) error
	UpdatePlan(ctx context.Context, plan *SubscriptionPlan) error
	FindPlanByID(ctx context.Context, id uuid.UUID) (*SubscriptionPlan, error)
	FindPlanByCode(ctx context.Context, code string) (*SubscriptionPlan, error)
	ListPlans(ctx context.Context, activeOnly bool) ([]*SubscriptionPlan, error)

	CreateFeature(ctx context.Context, feature *PlanFeature) error
	UpdateFeature(ctx context.Context, feature *PlanFeature) error
	FindFeatureByID(ctx context.Context, id uuid.UUID) (*PlanFeature, error)
	ListFeaturesByPlan(ctx context.Context, planID uuid.UUID) ([]*PlanFeature, error)
	DeleteFeature(ctx context.Context, id uuid.UUID) error
}

