package subscriptions

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/featuredefinition"
	"github.com/bengobox/subscription-service/internal/ent/planfeature"
	"github.com/bengobox/subscription-service/internal/ent/product"
	"github.com/bengobox/subscription-service/internal/ent/productsubscription"
	"github.com/bengobox/subscription-service/internal/ent/subscriptionplan"
	"github.com/bengobox/subscription-service/internal/ent/tenantsubscription"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// CorePlatformProducts are activated for every tenant subscription whatever its plan: identity
// (auth/SSO), managing its own subscription, and notifications (every tenant sends email, SMS and
// WhatsApp through notifications-api). Every other default product comes from the plan itself
// (PlanDefaultProductCodes), so a treasury-only tenant never gets pos/inventory rows.
var CorePlatformProducts = []string{"auth", "subscription", "notifications"}

// planMetaProductKey is the subscription_plans.metadata key naming the product a standalone plan
// sells (e.g. {"product":"treasury"} on TREASURY_*).
const planMetaProductKey = "product"

// basicAccessService returns <svc> for a "basic_<svc>_access" integration grant. Those codes are
// catalogued under the shared "platform" tag, yet the plan carrying one uses that service through
// its integration paths (a PowerSuite plan takes payments through treasury and stock through
// inventory), so the service's product row belongs to the plan's defaults too.
func basicAccessService(featureCode string) (string, bool) {
	if !strings.HasPrefix(featureCode, "basic_") || !strings.HasSuffix(featureCode, "_access") {
		return "", false
	}
	svc := strings.TrimSuffix(strings.TrimPrefix(featureCode, "basic_"), "_access")
	return svc, svc != ""
}

// planDefaultProducts resolves the products a subscription on planID starts with, in a stable
// order: CorePlatformProducts first, then the plan's own service_tag, its metadata product, its
// basic_*_access integration services, and the distinct service_tag of every included feature.
// The shared "platform" tag and tags with no row in the products table are skipped. client may be
// a transactional client (tx.Client()).
func planDefaultProducts(ctx context.Context, client *ent.Client, planID uuid.UUID) ([]*ent.Product, error) {
	plan, err := client.SubscriptionPlan.Query().
		Where(subscriptionplan.IDEQ(planID)).
		WithFeatures(func(q *ent.PlanFeatureQuery) {
			q.Where(planfeature.IsIncludedEQ(true))
		}).
		Only(ctx)
	if err != nil {
		return nil, fmt.Errorf("load plan %s: %w", planID, err)
	}

	wanted := make([]string, 0, 8)
	seen := map[string]bool{}
	add := func(code string) {
		code = strings.TrimSpace(code)
		if code == "" || code == sharedServiceTag || seen[code] {
			return
		}
		seen[code] = true
		wanted = append(wanted, code)
	}
	for _, c := range CorePlatformProducts {
		add(c)
	}
	if plan.ServiceTag != nil {
		add(*plan.ServiceTag)
	}
	if p, ok := plan.Metadata[planMetaProductKey].(string); ok {
		add(p)
	}
	featureCodes := planFeatureCodes(plan)
	for _, fc := range featureCodes {
		if svc, ok := basicAccessService(fc); ok {
			add(svc)
		}
	}
	if len(featureCodes) > 0 {
		tags, err := client.FeatureDefinition.Query().
			Where(featuredefinition.FeatureCodeIn(featureCodes...)).
			Select(featuredefinition.FieldServiceTag).
			Strings(ctx)
		if err != nil {
			return nil, fmt.Errorf("query feature service tags: %w", err)
		}
		for _, t := range tags {
			add(t)
		}
	}

	rows, err := client.Product.Query().Where(product.CodeIn(wanted...)).All(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve default products: %w", err)
	}
	byCode := make(map[string]*ent.Product, len(rows))
	for _, p := range rows {
		byCode[p.Code] = p
	}
	out := make([]*ent.Product, 0, len(rows))
	for _, code := range wanted {
		if p, ok := byCode[code]; ok {
			out = append(out, p)
		}
	}
	return out, nil
}

// PlanDefaultProductCodes is the product codes a subscription on planID is activated with: the
// core platform products plus every product the plan's features belong to (see
// planDefaultProducts). Signup, the zero-row guard and the backfill all use this one rule.
func PlanDefaultProductCodes(ctx context.Context, client *ent.Client, planID uuid.UUID) ([]string, error) {
	prods, err := planDefaultProducts(ctx, client, planID)
	if err != nil {
		return nil, err
	}
	codes := make([]string, 0, len(prods))
	for _, p := range prods {
		codes = append(codes, p.Code)
	}
	return codes, nil
}

// insertPlanDefaultProducts creates an active product row for each of the plan's default products,
// skipping any the subscription already has (ON CONFLICT DO NOTHING on the unique
// (tenant_subscription_id, product_code) index, so a concurrent insert cannot fail the caller's
// transaction). Returns how many rows it inserted.
func insertPlanDefaultProducts(ctx context.Context, client *ent.Client, tenantSubscriptionID, planID uuid.UUID, now time.Time) (int, error) {
	prods, err := planDefaultProducts(ctx, client, planID)
	if err != nil {
		return 0, err
	}
	inserted := 0
	for _, p := range prods {
		err := client.ProductSubscription.Create().
			SetTenantSubscriptionID(tenantSubscriptionID).
			SetProductID(p.ID).
			SetProductCode(p.Code).
			SetStatus(productsubscription.StatusActive).
			SetActivatedAt(now).
			OnConflictColumns(productsubscription.FieldTenantSubscriptionID, productsubscription.FieldProductCode).
			DoNothing().
			Exec(ctx)
		switch {
		case err == nil:
			inserted++
		case errors.Is(err, sql.ErrNoRows):
			// The row already exists: ent reports a DO NOTHING conflict as no returned id.
		default:
			return inserted, fmt.Errorf("activate default product %s: %w", p.Code, err)
		}
	}
	return inserted, nil
}

// EnsurePlanDefaultProducts gives a tenant subscription that has NO product rows yet its
// plan-derived defaults, and does nothing once it has any. Every path that adds a product row to an
// existing subscription calls it first: a pre-migration tenant with zero rows reads as "all
// products active" downstream (treasury-api fails open on an empty list), so its first row must
// never be a lone add-on such as whatsapp_messaging that suddenly makes treasury read as
// deactivated (hadia, 2026-11-12). Idempotent. Pass tx.Client() to run inside a transaction.
func EnsurePlanDefaultProducts(ctx context.Context, client *ent.Client, tenantSubscriptionID uuid.UUID) (int, error) {
	n, err := client.ProductSubscription.Query().
		Where(productsubscription.TenantSubscriptionIDEQ(tenantSubscriptionID)).
		Count(ctx)
	if err != nil {
		return 0, fmt.Errorf("count product subscriptions: %w", err)
	}
	if n > 0 {
		return 0, nil
	}
	sub, err := client.TenantSubscription.Get(ctx, tenantSubscriptionID)
	if err != nil {
		return 0, fmt.Errorf("load tenant subscription %s: %w", tenantSubscriptionID, err)
	}
	return insertPlanDefaultProducts(ctx, client, sub.ID, sub.PlanID, time.Now().UTC())
}

// EnsureDefaultProducts is EnsurePlanDefaultProducts on the service's own client, for callers in
// other packages (credits) that hold a *Service.
func (s *Service) EnsureDefaultProducts(ctx context.Context, tenantSubscriptionID uuid.UUID) error {
	n, err := EnsurePlanDefaultProducts(ctx, s.client, tenantSubscriptionID)
	if err != nil {
		return err
	}
	if n > 0 {
		s.log.Info("activated plan default products on a subscription that had none",
			zap.String("tenant_subscription_id", tenantSubscriptionID.String()), zap.Int("inserted", n))
	}
	return nil
}

// ZeroRowBackfillResult reports one BackfillZeroProductSubscriptions run.
type ZeroRowBackfillResult struct {
	Subscriptions int // ACTIVE/TRIAL subscriptions found with zero product rows
	Filled        int // of those, how many now have their defaults
	RowsInserted  int
	Failed        int
}

// BackfillZeroProductSubscriptions gives every ACTIVE or TRIAL tenant subscription (a subscription
// in its grace window is still ACTIVE) that has zero product rows its plan-derived defaults, one
// transaction per subscription so one bad plan cannot block the rest. Idempotent: a second run
// finds nothing to do. Wired into cmd/seed, which runs on every deploy.
func BackfillZeroProductSubscriptions(ctx context.Context, client *ent.Client, log *zap.Logger) (ZeroRowBackfillResult, error) {
	var res ZeroRowBackfillResult
	subs, err := client.TenantSubscription.Query().
		Where(
			tenantsubscription.StatusIn(tenantsubscription.StatusACTIVE, tenantsubscription.StatusTRIAL),
			tenantsubscription.Not(tenantsubscription.HasProductSubscriptions()),
		).
		All(ctx)
	if err != nil {
		return res, fmt.Errorf("query zero-row subscriptions: %w", err)
	}
	res.Subscriptions = len(subs)
	for _, sub := range subs {
		n, err := backfillOne(ctx, client, sub)
		if err != nil {
			res.Failed++
			log.Warn("zero-row product backfill failed",
				zap.String("tenant_id", sub.TenantID.String()), zap.Error(err))
			continue
		}
		res.Filled++
		res.RowsInserted += n
		log.Info("zero-row product backfill",
			zap.String("tenant_id", sub.TenantID.String()), zap.Int("inserted", n))
	}
	log.Info("zero-row product backfill done",
		zap.Int("subscriptions", res.Subscriptions), zap.Int("filled", res.Filled),
		zap.Int("rows_inserted", res.RowsInserted), zap.Int("failed", res.Failed))
	return res, nil
}

func backfillOne(ctx context.Context, client *ent.Client, sub *ent.TenantSubscription) (int, error) {
	tx, err := client.Tx(ctx)
	if err != nil {
		return 0, err
	}
	n, err := EnsurePlanDefaultProducts(ctx, tx.Client(), sub.ID)
	if err != nil {
		_ = tx.Rollback()
		return 0, err
	}
	return n, tx.Commit()
}

// grantsPlanProducts reports whether a subscription in this status should carry its plan's
// default products: ACTIVE (a grace window is still ACTIVE) and TRIAL.
func grantsPlanProducts(st tenantsubscription.Status) bool {
	return st == tenantsubscription.StatusACTIVE || st == tenantsubscription.StatusTRIAL
}

// MissingPlanDefaultProductCodes returns, in PlanDefaultProductCodes order, the plan-derived
// default products the subscription has NO row for at all. A row in any status counts as present,
// so a product the tenant deactivated is never reported (or re-created) here.
func MissingPlanDefaultProductCodes(ctx context.Context, client *ent.Client, tenantSubscriptionID, planID uuid.UUID) ([]string, error) {
	want, err := PlanDefaultProductCodes(ctx, client, planID)
	if err != nil {
		return nil, err
	}
	have, err := client.ProductSubscription.Query().
		Where(productsubscription.TenantSubscriptionIDEQ(tenantSubscriptionID)).
		Select(productsubscription.FieldProductCode).
		Strings(ctx)
	if err != nil {
		return nil, fmt.Errorf("query product codes: %w", err)
	}
	present := make(map[string]bool, len(have))
	for _, c := range have {
		present[c] = true
	}
	missing := make([]string, 0, len(want))
	for _, c := range want {
		if !present[c] {
			missing = append(missing, c)
		}
	}
	return missing, nil
}

// EnsureMissingPlanDefaultProducts gives an ACTIVE or TRIAL subscription a row for every
// plan-derived default product it has no row for, whether or not it already has other rows. It
// only inserts (insertPlanDefaultProducts, ON CONFLICT DO NOTHING): an existing row of any status
// is left as it is, so a product the tenant deactivated stays deactivated and nothing is ever
// removed. Plan changes call it with the subscription already on its new plan, so the new plan's
// products appear (mccl, on a library plan, had pos/inventory/treasury rows but no library row).
// Other statuses are skipped. Idempotent; pass tx.Client() to run inside a transaction.
func EnsureMissingPlanDefaultProducts(ctx context.Context, client *ent.Client, tenantSubscriptionID uuid.UUID) (int, error) {
	sub, err := client.TenantSubscription.Get(ctx, tenantSubscriptionID)
	if err != nil {
		return 0, fmt.Errorf("load tenant subscription %s: %w", tenantSubscriptionID, err)
	}
	if !grantsPlanProducts(sub.Status) {
		return 0, nil
	}
	return insertPlanDefaultProducts(ctx, client, sub.ID, sub.PlanID, time.Now().UTC())
}

// MissingPlanDefaults is one subscription the missing-defaults backfill found short of rows.
type MissingPlanDefaults struct {
	TenantID       uuid.UUID
	TenantSlug     string
	SubscriptionID uuid.UUID
	PlanCode       string
	Codes          []string // plan-derived codes with no row, which the run inserts (or would)
}

// MissingDefaultsBackfillResult reports one BackfillMissingPlanDefaultProducts run.
type MissingDefaultsBackfillResult struct {
	Applied       bool // false: dry run, nothing written
	Subscriptions int  // ACTIVE/TRIAL subscriptions checked
	Missing       []MissingPlanDefaults
	RowsInserted  int
	Failed        int
}

// BackfillMissingPlanDefaultProducts finds every ACTIVE or TRIAL subscription missing a row for
// one of its plan-derived default products (BackfillZeroProductSubscriptions covers only
// subscriptions with zero rows). With apply false it is a dry run: it logs, per tenant, the codes
// it would insert and writes nothing. With apply true it inserts them through
// EnsureMissingPlanDefaultProducts, one transaction per subscription. Existing rows are never
// changed or removed.
func BackfillMissingPlanDefaultProducts(ctx context.Context, client *ent.Client, log *zap.Logger, apply bool) (MissingDefaultsBackfillResult, error) {
	res := MissingDefaultsBackfillResult{Applied: apply}
	subs, err := client.TenantSubscription.Query().
		Where(tenantsubscription.StatusIn(tenantsubscription.StatusACTIVE, tenantsubscription.StatusTRIAL)).
		WithTenant().
		WithPlan().
		All(ctx)
	if err != nil {
		return res, fmt.Errorf("query active subscriptions: %w", err)
	}
	res.Subscriptions = len(subs)
	for _, sub := range subs {
		codes, err := MissingPlanDefaultProductCodes(ctx, client, sub.ID, sub.PlanID)
		if err != nil {
			res.Failed++
			log.Warn("missing plan products check failed", zap.String("tenant_id", sub.TenantID.String()), zap.Error(err))
			continue
		}
		if len(codes) == 0 {
			continue
		}
		m := MissingPlanDefaults{TenantID: sub.TenantID, SubscriptionID: sub.ID, Codes: codes}
		if sub.Edges.Tenant != nil {
			m.TenantSlug = sub.Edges.Tenant.Slug
		}
		if sub.Edges.Plan != nil {
			m.PlanCode = sub.Edges.Plan.PlanCode
		}
		res.Missing = append(res.Missing, m)
		fields := []zap.Field{
			zap.String("tenant_id", m.TenantID.String()), zap.String("tenant_slug", m.TenantSlug),
			zap.String("plan_code", m.PlanCode), zap.Strings("codes", codes),
		}
		if !apply {
			log.Info("missing plan products (dry run, nothing inserted)", fields...)
			continue
		}
		n, err := ensureMissingOne(ctx, client, sub.ID)
		if err != nil {
			res.Failed++
			log.Warn("missing plan products insert failed", append(fields, zap.Error(err))...)
			continue
		}
		res.RowsInserted += n
		log.Info("missing plan products inserted", append(fields, zap.Int("inserted", n))...)
	}
	log.Info("missing plan products backfill done", zap.Bool("applied", apply),
		zap.Int("subscriptions", res.Subscriptions), zap.Int("missing", len(res.Missing)),
		zap.Int("rows_inserted", res.RowsInserted), zap.Int("failed", res.Failed))
	return res, nil
}

func ensureMissingOne(ctx context.Context, client *ent.Client, subID uuid.UUID) (int, error) {
	tx, err := client.Tx(ctx)
	if err != nil {
		return 0, err
	}
	n, err := EnsureMissingPlanDefaultProducts(ctx, tx.Client(), subID)
	if err != nil {
		_ = tx.Rollback()
		return 0, err
	}
	return n, tx.Commit()
}

// EntitlementCheck is the result of checking whether a tenant may self-activate a product.
type EntitlementCheck struct {
	Entitled bool `json:"entitled"`
	// MinPlanCode/MinPlanName/MinPlanTier name the cheapest plan that would unlock this
	// product, when NOT entitled — lets the UI prompt a specific upgrade rather than a
	// generic "upgrade your plan" message. Nil when Entitled is true or no catalogued plan
	// grants the product (nothing to recommend).
	MinPlanCode *string `json:"min_plan_code,omitempty"`
	MinPlanName *string `json:"min_plan_name,omitempty"`
	MinPlanTier *int    `json:"min_plan_tier,omitempty"`
}

// IsEntitledToProduct reports whether tenantID's current composite entitlements (base plan +
// already-active product overlays) already grant at least one feature tagged with productCode's
// service_tag — i.e. whether self-activating this product would be turning on something they're
// already paying for, not granting something new. A product with no catalogued
// FeatureDefinition rows at all (nothing gates it) fails OPEN, matching this platform's
// consistent "uncatalogued → don't block" entitlement posture (see isFeatureUnlocked in
// shared-ui-lib's feature-gate.tsx for the client-side twin of this rule).
func (s *Service) IsEntitledToProduct(ctx context.Context, tenantID uuid.UUID, productCode string) (EntitlementCheck, error) {
	defs, err := s.client.FeatureDefinition.Query().
		Where(featuredefinition.ServiceTagEQ(productCode)).
		All(ctx)
	if err != nil {
		return EntitlementCheck{}, fmt.Errorf("query feature definitions for product %s: %w", productCode, err)
	}
	if len(defs) == 0 {
		return EntitlementCheck{Entitled: true}, nil
	}

	result, err := s.GetSubscriptionResult(ctx, tenantID)
	if err != nil {
		return EntitlementCheck{}, err
	}
	if result.Exempt {
		return EntitlementCheck{Entitled: true}, nil
	}

	have := make(map[string]struct{}, len(result.Features))
	for _, f := range result.Features {
		have[f] = struct{}{}
	}
	for _, d := range defs {
		if _, ok := have[d.FeatureCode]; ok {
			return EntitlementCheck{Entitled: true}, nil
		}
	}

	// Not entitled: find the cheapest active plan that includes any of this product's features,
	// reusing the same minUnlockingPlans computation the /features/catalog endpoint uses for its
	// "upgrade to X" prompts, so the two surfaces never disagree about which plan to recommend.
	min, minErr := s.minUnlockingPlanForFeatures(ctx, defs)
	if minErr != nil {
		s.log.Warn("entitlement check: failed to resolve min unlocking plan", zap.Error(minErr))
		return EntitlementCheck{Entitled: false}, nil
	}
	if min == nil {
		return EntitlementCheck{Entitled: false}, nil
	}
	return EntitlementCheck{
		Entitled:    false,
		MinPlanCode: &min.PlanCode,
		MinPlanName: &min.Name,
		MinPlanTier: &min.TierOrder,
	}, nil
}

// ActivateMyProduct is the tenant-self-service entry point: activates productCode within the
// tenant's OWN current plan (never an override plan — self-service can only turn on what's
// already entitled, never grant a new plan) after confirming entitlement. Returns the same
// EntitlementCheck the caller would have gotten from IsEntitledToProduct so a 403 response can
// carry the upgrade recommendation without a second lookup.
func (s *Service) ActivateMyProduct(ctx context.Context, tenantID uuid.UUID, productCode string) (EntitlementCheck, error) {
	check, err := s.IsEntitledToProduct(ctx, tenantID, productCode)
	if err != nil {
		return EntitlementCheck{}, err
	}
	if !check.Entitled {
		return check, nil
	}
	if err := s.AssignProductPlan(ctx, tenantID, productCode, ""); err != nil {
		return EntitlementCheck{}, err
	}
	return check, nil
}

// minUnlockingPlan is the cheapest active plan (by tier, then price) that includes at least one
// of the given feature definitions.
type minUnlockingPlan struct {
	PlanCode  string
	Name      string
	TierOrder int
}

func (s *Service) minUnlockingPlanForFeatures(ctx context.Context, defs []*ent.FeatureDefinition) (*minUnlockingPlan, error) {
	codes := make(map[string]struct{}, len(defs))
	for _, d := range defs {
		codes[d.FeatureCode] = struct{}{}
	}

	plans, err := s.client.SubscriptionPlan.Query().
		Where(subscriptionplan.IsActive(true)).
		WithFeatures().
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("query plans: %w", err)
	}

	var best *minUnlockingPlan
	var bestPrice = float64(0)
	for _, p := range plans {
		for _, pf := range p.Edges.Features {
			if !pf.IsIncluded {
				continue
			}
			if _, ok := codes[pf.FeatureCode]; !ok {
				continue
			}
			if best == nil || p.TierOrder < best.TierOrder || (p.TierOrder == best.TierOrder && p.BasePrice < bestPrice) {
				best = &minUnlockingPlan{PlanCode: p.PlanCode, Name: p.Name, TierOrder: p.TierOrder}
				bestPrice = p.BasePrice
			}
			break
		}
	}
	return best, nil
}

// ActiveProductCodes returns the product_code of every currently-active ProductSubscription for
// tenantID, for JWT claim enrichment (auth-api mints these into the access token so the
// app-switcher can show only activated apps without an extra network call per page load).
func (s *Service) ActiveProductCodes(ctx context.Context, tenantID uuid.UUID) ([]string, error) {
	sub, err := s.getSubscription(ctx, tenantID)
	if err != nil {
		// No subscription (exempt tenant, or none provisioned yet) → no product claims, not an error.
		return nil, nil //nolint:nilerr
	}
	return s.activeProductCodesForSub(ctx, sub.ID)
}

// activeProductCodesForSub is the shared core of ActiveProductCodes, for callers (like
// GetSubscriptionResult) that already hold the TenantSubscription and would otherwise re-query it.
func (s *Service) activeProductCodesForSub(ctx context.Context, tenantSubscriptionID uuid.UUID) ([]string, error) {
	rows, err := s.client.ProductSubscription.Query().
		Where(
			productsubscription.TenantSubscriptionIDEQ(tenantSubscriptionID),
			productsubscription.StatusEQ(productsubscription.StatusActive),
		).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("query active product subscriptions: %w", err)
	}
	codes := make([]string, 0, len(rows))
	for _, r := range rows {
		codes = append(codes, r.ProductCode)
	}
	return codes, nil
}
