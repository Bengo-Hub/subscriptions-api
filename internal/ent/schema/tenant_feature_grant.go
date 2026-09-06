package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// TenantFeatureGrant is a platform-admin-controlled grant of a single named
// feature_definitions code to one tenant, independent of that tenant's subscription
// plan. It exists because ProductSubscription/override_plan_id (see product_subscription.go)
// can only grant an entire additional PLAN as an overlay — there was previously no way to
// unlock exactly one feature (e.g. a pricing add-on) without also handing the tenant every
// other feature bundled into some plan.
//
// A grant only unlocks the feature CODE (folded into GetSubscriptionResult's composite
// features union, see entitlements.go's resolveTenantFeatureGrants); it does not itself turn
// anything on for end users. The owning service's own tenant-facing settings page still gates
// the actual on/off switch behind FeatureEnabled(code), exactly like the existing
// lots_batches/stock_alerts pattern in inventory-api's TenantInventoryConfig — granting makes
// the toggle available, the tenant's own admin still has to flip it.
type TenantFeatureGrant struct {
	ent.Schema
}

// Fields of the TenantFeatureGrant.
func (TenantFeatureGrant) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).
			Default(uuid.New).
			Immutable(),
		field.UUID("tenant_id", uuid.UUID{}).
			Comment("Tenant this grant applies to (raw tenant id, not FK'd through TenantSubscription -- a grant is independent of subscription/plan lifecycle)"),
		field.String("feature_code").
			NotEmpty().
			Comment("feature_definitions.feature_code being granted"),
		field.UUID("granted_by", uuid.UUID{}).
			Comment("Platform-admin user id (auth-service subject) who made this grant"),
		field.Time("granted_at").
			Default(time.Now).
			Comment("When the grant was made (or last re-granted after a revoke)"),
		field.Time("revoked_at").
			Optional().
			Nillable().
			Comment("Set when a platform admin revokes the grant; nil = currently active"),
		field.UUID("revoked_by", uuid.UUID{}).
			Optional().
			Nillable().
			Comment("Platform-admin user id who revoked the grant, if revoked"),
		field.String("notes").
			Optional().
			Comment("Free-text context for why this add-on was granted (support ticket, sales agreement, etc.)"),
		field.Time("created_at").
			Default(time.Now).
			Immutable(),
		field.Time("updated_at").
			Default(time.Now).
			UpdateDefault(time.Now),
	}
}

// Indexes of the TenantFeatureGrant.
func (TenantFeatureGrant) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("tenant_id"),
		index.Fields("feature_code"),
		// One row per (tenant, feature) -- grant/re-grant/revoke all upsert this same row
		// (mirrors ProductSubscription's tenant_subscription_id+product_code uniqueness).
		index.Fields("tenant_id", "feature_code").
			Unique(),
	}
}
