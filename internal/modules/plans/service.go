package plans

import (
	"context"
	"fmt"

	sharedcache "github.com/Bengo-Hub/cache"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// Service handles business logic for subscription plans.
type Service struct {
	log   *zap.Logger
	repo  Repository
	cache *sharedcache.Aside
}

// NewService creates a new plans service.
func NewService(log *zap.Logger, repo Repository) *Service {
	return &Service{
		log:  log.Named("plans.service"),
		repo: repo,
	}
}

// SetCache injects the cache helper (optional; caching is skipped if nil).
func (s *Service) SetCache(c *sharedcache.Aside) {
	s.cache = c
}

// GetPlanWithPrice retrieves a plan and calculates its dynamic price if necessary.
func (s *Service) GetPlanWithPrice(ctx context.Context, id uuid.UUID) (*SubscriptionPlan, error) {
	plan, err := s.repo.FindPlanByID(ctx, id)
	if err != nil {
		return nil, err
	}

	return s.calculatePrice(ctx, plan)
}

// GetPlanByCodeWithPrice retrieves a plan by code and calculates its dynamic price.
func (s *Service) GetPlanByCodeWithPrice(ctx context.Context, code string) (*SubscriptionPlan, error) {
	plan, err := s.repo.FindPlanByCode(ctx, code)
	if err != nil {
		return nil, err
	}

	return s.calculatePrice(ctx, plan)
}

// ListPlansWithPrices retrieves all plans with their calculated prices (cached 1 min).
func (s *Service) ListPlansWithPrices(ctx context.Context, activeOnly bool, serviceTag *string) ([]*SubscriptionPlan, error) {
	serviceTagStr := "all"
	if serviceTag != nil {
		serviceTagStr = *serviceTag
	}
	key := sharedcache.Key("sub", "plans", fmt.Sprintf("active=%v,svc=%s", activeOnly, serviceTagStr))
	fetch := func(ctx context.Context) ([]*SubscriptionPlan, error) {
		plans, err := s.repo.ListPlans(ctx, activeOnly, serviceTag)
		if err != nil {
			return nil, err
		}

		for i, plan := range plans {
			calculated, err := s.calculatePrice(ctx, plan)
			if err != nil {
				s.log.Error("failed to calculate price for plan", zap.String("plan_code", plan.PlanCode), zap.Error(err))
				continue
			}
			plans[i] = calculated
		}

		return plans, nil
	}
	return sharedcache.GetOrSet(ctx, s.cache, key, sharedcache.TTLReference, fetch)
}

// calculatePrice performs the SUM-based logic if use_sum_based_pricing is true.
func (s *Service) calculatePrice(ctx context.Context, plan *SubscriptionPlan) (*SubscriptionPlan, error) {
	if !plan.UseSumBasedPricing {
		return plan, nil
	}

	// If a override price is set for custom plans, use it
	if plan.OnetimeAllProductsPrice != nil && *plan.OnetimeAllProductsPrice > 0 {
		plan.BasePrice = *plan.OnetimeAllProductsPrice
		return plan, nil
	}

	// Otherwise, sum the prices of all products associated with the features
	var totalPrice float64
	for _, f := range plan.Features {
		if !f.IsIncluded {
			continue
		}

		// Look up product by feature code (convention: feature_code == product_code)
		product, err := s.repo.FindProductByCode(ctx, f.FeatureCode)
		if err != nil {
			// If not found, it might be a pure UI feature or non-billable service, skip
			continue
		}

		// Skip platform services as they are free
		if product.IsPlatform || product.IsBaseService {
			continue
		}

		// Add price based on billing cycle
		switch plan.BillingCycle {
		case "ANNUAL":
			totalPrice += product.YearlyPrice
		case "ONE_TIME":
			totalPrice += product.OnetimePrice
		default:
			totalPrice += product.MonthlyPrice
		}
	}

	if totalPrice > 0 {
		plan.BasePrice = totalPrice
	}

	return s.applyDiscountRules(ctx, plan)
}

// applyDiscountRules evaluates the dynamic discount rules for a plan.
func (s *Service) applyDiscountRules(ctx context.Context, plan *SubscriptionPlan) (*SubscriptionPlan, error) {
	if len(plan.DiscountRules) == 0 {
		return plan, nil
	}

	originalPrice := plan.BasePrice
	var totalDiscount float64

	for _, rule := range plan.DiscountRules {
		ruleType, _ := rule["type"].(string)
		switch ruleType {
		case "YEARLY":
			// Yearly discount applies if billing cycle is ANNUAL
			if plan.BillingCycle == "ANNUAL" {
				if percentage, ok := rule["percentage"].(float64); ok {
					totalDiscount += (originalPrice * (percentage / 100))
				}
			}
		case "LOYALTY":
			// Loyalty discount (conceptually applies after a certain period, but here we can just note it)
			// For simplicity, we apply it if the plan is marked as eligible in metadata or similar
			// In a real scenario, this would check tenant history
			if percentage, ok := rule["percentage"].(float64); ok {
				totalDiscount += (originalPrice * (percentage / 100))
			}
		case "NEW_CUSTOMER":
			// New customer discount (percentage off first month/year)
			if percentage, ok := rule["percentage"].(float64); ok {
				totalDiscount += (originalPrice * (percentage / 100))
			}
		}
	}

	plan.BasePrice = originalPrice - totalDiscount
	if plan.BasePrice < 0 {
		plan.BasePrice = 0
	}

	return plan, nil
}

// Pass-through methods to repository
func (s *Service) CreatePlan(ctx context.Context, plan *SubscriptionPlan) error {
	return s.repo.CreatePlan(ctx, plan)
}

func (s *Service) UpdatePlan(ctx context.Context, plan *SubscriptionPlan) error {
	return s.repo.UpdatePlan(ctx, plan)
}

func (s *Service) DeletePlan(ctx context.Context, id uuid.UUID) error {
	return s.repo.DeletePlan(ctx, id)
}
