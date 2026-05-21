package plans

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/planfeature"
	"github.com/bengobox/subscription-service/internal/ent/product"
	"github.com/bengobox/subscription-service/internal/ent/subscriptionplan"
)

// EntRepository implements the Repository interface using Ent as the persistence layer.
type EntRepository struct {
	client *ent.Client
}

// NewEntRepository constructs an Ent-backed repository.
func NewEntRepository(client *ent.Client) *EntRepository {
	return &EntRepository{client: client}
}

// CreatePlan persists a new subscription plan.
func (r *EntRepository) CreatePlan(ctx context.Context, plan *SubscriptionPlan) error {
	if plan == nil {
		return errors.New("plans: nil plan provided")
	}

	tierLimits := plan.TierLimits
	if tierLimits == nil {
		tierLimits = make(map[string]any)
	}

	metadata := plan.Metadata
	if metadata == nil {
		metadata = make(map[string]any)
	}

	_, err := r.client.SubscriptionPlan.Create().
		SetID(plan.ID).
		SetPlanCode(plan.PlanCode).
		SetName(plan.Name).
		SetNillableDescription(&plan.Description).
		SetBillingCycle(plan.BillingCycle).
		SetBasePrice(plan.BasePrice).
		SetNillableOnetimeAllProductsPrice(plan.OnetimeAllProductsPrice).
		SetUseSumBasedPricing(plan.UseSumBasedPricing).
		SetCurrency(plan.Currency).
		SetIsActive(plan.IsActive).
		SetIsPublic(plan.IsPublic).
		SetTierOrder(plan.TierOrder).
		SetTierLimitsJSON(tierLimits).
		SetDiscountRules(plan.DiscountRules).
		SetNillableServiceTag(plan.ServiceTag).
		SetMetadata(metadata).
		Save(ctx)

	if err != nil {
		return fmt.Errorf("plans: create plan: %w", err)
	}

	return nil
}

// UpdatePlan updates an existing subscription plan.
func (r *EntRepository) UpdatePlan(ctx context.Context, plan *SubscriptionPlan) error {
	if plan == nil {
		return errors.New("plans: nil plan provided")
	}

	update := r.client.SubscriptionPlan.UpdateOneID(plan.ID)

	if plan.Name != "" {
		update = update.SetName(plan.Name)
	}
	if plan.Description != "" {
		update = update.SetNillableDescription(&plan.Description)
	}
	if plan.BillingCycle != "" {
		update = update.SetBillingCycle(plan.BillingCycle)
	}
	if plan.BasePrice >= 0 {
		update = update.SetBasePrice(plan.BasePrice)
	}
	update = update.SetNillableOnetimeAllProductsPrice(plan.OnetimeAllProductsPrice)
	update = update.SetUseSumBasedPricing(plan.UseSumBasedPricing)
	if plan.Currency != "" {
		update = update.SetCurrency(plan.Currency)
	}
	update = update.SetIsActive(plan.IsActive)
	update = update.SetIsPublic(plan.IsPublic)
	if plan.TierOrder > 0 {
		update = update.SetTierOrder(plan.TierOrder)
	}
	if plan.TierLimits != nil {
		update = update.SetTierLimitsJSON(plan.TierLimits)
	}
	if plan.Metadata != nil {
		update = update.SetMetadata(plan.Metadata)
	}
	if plan.DiscountRules != nil {
		update = update.SetDiscountRules(plan.DiscountRules)
	}
	update = update.SetNillableServiceTag(plan.ServiceTag)

	update = update.SetUpdatedAt(time.Now())

	_, err := update.Save(ctx)
	if err != nil {
		return fmt.Errorf("plans: update plan: %w", err)
	}

	return nil
}

// FindPlanByID retrieves a subscription plan by ID.
func (r *EntRepository) FindPlanByID(ctx context.Context, id uuid.UUID) (*SubscriptionPlan, error) {
	entPlan, err := r.client.SubscriptionPlan.Query().
		Where(subscriptionplan.ID(id)).
		WithFeatures().
		Only(ctx)

	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("plans: plan not found: %w", err)
		}
		return nil, fmt.Errorf("plans: find plan by id: %w", err)
	}

	return mapEntPlan(entPlan), nil
}

// FindPlanByCode retrieves a subscription plan by plan code.
func (r *EntRepository) FindPlanByCode(ctx context.Context, code string) (*SubscriptionPlan, error) {
	entPlan, err := r.client.SubscriptionPlan.Query().
		Where(subscriptionplan.PlanCode(code)).
		WithFeatures().
		Only(ctx)

	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("plans: plan not found: %w", err)
		}
		return nil, fmt.Errorf("plans: find plan by code: %w", err)
	}

	return mapEntPlan(entPlan), nil
}

// ListPlans retrieves all subscription plans, optionally filtering by active status and service tag.
func (r *EntRepository) ListPlans(ctx context.Context, activeOnly bool, serviceTag *string) ([]*SubscriptionPlan, error) {
	query := r.client.SubscriptionPlan.Query()

	if activeOnly {
		query = query.Where(subscriptionplan.IsActive(true))
	}
	if serviceTag != nil {
		query = query.Where(subscriptionplan.ServiceTag(*serviceTag))
	}

	entPlans, err := query.
		Order(subscriptionplan.ByTierOrder()).
		WithFeatures().
		All(ctx)

	if err != nil {
		return nil, fmt.Errorf("plans: list plans: %w", err)
	}

	plans := make([]*SubscriptionPlan, len(entPlans))
	for i, entPlan := range entPlans {
		plans[i] = mapEntPlan(entPlan)
	}

	return plans, nil
}

// DeletePlan removes a subscription plan.
func (r *EntRepository) DeletePlan(ctx context.Context, id uuid.UUID) error {
	err := r.client.SubscriptionPlan.DeleteOneID(id).Exec(ctx)
	if err != nil {
		return fmt.Errorf("plans: delete plan: %w", err)
	}
	return nil
}

// CreateFeature persists a new plan feature.
func (r *EntRepository) CreateFeature(ctx context.Context, feature *PlanFeature) error {
	if feature == nil {
		return errors.New("plans: nil feature provided")
	}

	metadata := feature.Metadata
	if metadata == nil {
		metadata = make(map[string]any)
	}

	create := r.client.PlanFeature.Create().
		SetID(feature.ID).
		SetPlanID(feature.PlanID).
		SetFeatureCode(feature.FeatureCode).
		SetIsIncluded(feature.IsIncluded).
		SetOverageUnitPrice(feature.OverageUnitPrice).
		SetMetadata(metadata)

	if feature.LimitValue != nil {
		create = create.SetLimitValue(*feature.LimitValue)
	}

	_, err := create.Save(ctx)
	if err != nil {
		return fmt.Errorf("plans: create feature: %w", err)
	}

	return nil
}

// UpdateFeature updates an existing plan feature.
func (r *EntRepository) UpdateFeature(ctx context.Context, feature *PlanFeature) error {
	if feature == nil {
		return errors.New("plans: nil feature provided")
	}

	update := r.client.PlanFeature.UpdateOneID(feature.ID)

	if feature.FeatureCode != "" {
		update = update.SetFeatureCode(feature.FeatureCode)
	}
	update = update.SetIsIncluded(feature.IsIncluded)
	update = update.SetOverageUnitPrice(feature.OverageUnitPrice)
	if feature.LimitValue != nil {
		update = update.SetLimitValue(*feature.LimitValue)
	} else {
		update = update.ClearLimitValue()
	}
	if feature.Metadata != nil {
		update = update.SetMetadata(feature.Metadata)
	}

	_, err := update.Save(ctx)
	if err != nil {
		return fmt.Errorf("plans: update feature: %w", err)
	}

	return nil
}

// FindFeatureByID retrieves a plan feature by ID.
func (r *EntRepository) FindFeatureByID(ctx context.Context, id uuid.UUID) (*PlanFeature, error) {
	entFeature, err := r.client.PlanFeature.Query().
		Where(planfeature.ID(id)).
		Only(ctx)

	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("plans: feature not found: %w", err)
		}
		return nil, fmt.Errorf("plans: find feature by id: %w", err)
	}

	return mapEntFeature(entFeature), nil
}

// ListFeaturesByPlan retrieves all features for a given plan.
func (r *EntRepository) ListFeaturesByPlan(ctx context.Context, planID uuid.UUID) ([]*PlanFeature, error) {
	entFeatures, err := r.client.PlanFeature.Query().
		Where(planfeature.PlanID(planID)).
		All(ctx)

	if err != nil {
		return nil, fmt.Errorf("plans: list features by plan: %w", err)
	}

	features := make([]*PlanFeature, len(entFeatures))
	for i, entFeature := range entFeatures {
		features[i] = mapEntFeature(entFeature)
	}

	return features, nil
}

// DeleteFeature removes a plan feature.
func (r *EntRepository) DeleteFeature(ctx context.Context, id uuid.UUID) error {
	err := r.client.PlanFeature.DeleteOneID(id).Exec(ctx)
	if err != nil {
		return fmt.Errorf("plans: delete feature: %w", err)
	}

	return nil
}

// FindProductByCode retrieves a product by its code.
func (r *EntRepository) FindProductByCode(ctx context.Context, code string) (*Product, error) {
	entProduct, err := r.client.Product.Query().
		Where(product.Code(code)).
		Only(ctx)

	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("plans: product not found: %w", err)
		}
		return nil, fmt.Errorf("plans: find product by code: %w", err)
	}

	return mapEntProduct(entProduct), nil
}

// ListProducts retrieves all products.
func (r *EntRepository) ListProducts(ctx context.Context) ([]*Product, error) {
	entProducts, err := r.client.Product.Query().
		Order(ent.Asc(product.FieldSortOrder)).
		All(ctx)

	if err != nil {
		return nil, fmt.Errorf("plans: list products: %w", err)
	}

	products := make([]*Product, len(entProducts))
	for i, entProduct := range entProducts {
		products[i] = mapEntProduct(entProduct)
	}

	return products, nil
}

// mapEntPlan converts an Ent SubscriptionPlan entity to a domain SubscriptionPlan.
func mapEntPlan(entPlan *ent.SubscriptionPlan) *SubscriptionPlan {
	plan := &SubscriptionPlan{
		ID:           entPlan.ID,
		PlanCode:     entPlan.PlanCode,
		Name:         entPlan.Name,
		BillingCycle: entPlan.BillingCycle,
		BasePrice:    entPlan.BasePrice,
		OnetimeAllProductsPrice: entPlan.OnetimeAllProductsPrice,
		UseSumBasedPricing:     entPlan.UseSumBasedPricing,
		Currency:     entPlan.Currency,
		IsActive:     entPlan.IsActive,
		IsPublic:     entPlan.IsPublic,
		TierOrder:    entPlan.TierOrder,
		TierLimits:   entPlan.TierLimitsJSON,
		Metadata:     entPlan.Metadata,
		ServiceTag:   entPlan.ServiceTag,
		CreatedAt:    entPlan.CreatedAt,
		UpdatedAt:    entPlan.UpdatedAt,
		Description:  entPlan.Description,
		DiscountRules: entPlan.DiscountRules,
	}

	if entPlan.Edges.Features != nil {
		plan.Features = make([]*PlanFeature, len(entPlan.Edges.Features))
		for i, f := range entPlan.Edges.Features {
			plan.Features[i] = mapEntFeature(f)
		}
	}

	if plan.TierLimits == nil {
		plan.TierLimits = make(map[string]any)
	}
	if plan.Metadata == nil {
		plan.Metadata = make(map[string]any)
	}

	return plan
}

// mapEntFeature converts an Ent PlanFeature entity to a domain PlanFeature.
func mapEntFeature(entFeature *ent.PlanFeature) *PlanFeature {
	feature := &PlanFeature{
		ID:          entFeature.ID,
		PlanID:      entFeature.PlanID,
		FeatureCode: entFeature.FeatureCode,
		IsIncluded:  entFeature.IsIncluded,
		OverageUnitPrice: entFeature.OverageUnitPrice,
		Metadata:    entFeature.Metadata,
		CreatedAt:   entFeature.CreatedAt,
	}

	if entFeature.LimitValue > 0 {
		limitValue := entFeature.LimitValue
		feature.LimitValue = &limitValue
	}

	if feature.Metadata == nil {
		feature.Metadata = make(map[string]any)
	}

	return feature
}

// mapEntProduct converts an Ent Product entity to a domain Product.
func mapEntProduct(entProduct *ent.Product) *Product {
	return &Product{
		ID:            entProduct.ID,
		Code:          entProduct.Code,
		Name:          entProduct.Name,
		Description:   entProduct.Description,
		Category:      string(entProduct.Category),
		IsPlatform:    entProduct.IsPlatform,
		IsBaseService: entProduct.IsBaseService,
		MonthlyPrice:  entProduct.MonthlyPrice,
		YearlyPrice:   entProduct.YearlyPrice,
		OnetimePrice:  entProduct.OnetimePrice,
		Metadata:      entProduct.Metadata,
	}
}
