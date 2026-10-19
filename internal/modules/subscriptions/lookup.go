package subscriptions

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/tenantsubscription"
)

// SubscriptionByTenant returns the tenant's subscription row with its plan loaded, for modules
// that need entitlement checks outside the lifecycle service, such as the credits engine.
// ent.IsNotFound reports a tenant with no subscription.
func (s *Service) SubscriptionByTenant(ctx context.Context, tenantID uuid.UUID) (*ent.TenantSubscription, error) {
	sub, err := s.client.TenantSubscription.Query().
		Where(tenantsubscription.TenantIDEQ(tenantID)).
		WithPlan().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, err
		}
		return nil, fmt.Errorf("query subscription: %w", err)
	}
	return sub, nil
}
