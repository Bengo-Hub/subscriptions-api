package subscriptions

import (
	"context"
	"fmt"
	"time"

	serviceclient "github.com/Bengo-Hub/shared-service-client"
	"github.com/bengobox/subscription-service/internal/domain"
	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/planfeature"
	"github.com/bengobox/subscription-service/internal/ent/productsubscription"
	"github.com/bengobox/subscription-service/internal/ent/subscriptionplan"
	enttenant "github.com/bengobox/subscription-service/internal/ent/tenant"
	"github.com/bengobox/subscription-service/internal/ent/tenantsubscription"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// Service handles subscription lifecycle operations with state machine validation.
type Service struct {
	client         *ent.Client
	log            *zap.Logger
	treasuryClient *serviceclient.Client
	treasuryAPIKey string
	// platformTenantID is the platform owner tenant that issues subscription invoices.
	// It is exempt from subscriptions alongside the demo/codevertex tenants. See exemption.go.
	platformTenantID uuid.UUID
}

// New creates a subscription lifecycle service. platformTenantID is the operating
// platform-owner tenant UUID (PLATFORM_TENANT_ID); pass uuid.Nil to disable id-based
// exemption (slug-based exemption still applies).
func New(client *ent.Client, log *zap.Logger, treasuryClient *serviceclient.Client, treasuryAPIKey string, platformTenantID uuid.UUID) *Service {
	return &Service{
		client:           client,
		log:              log.Named("subscriptions.service"),
		treasuryClient:   treasuryClient,
		treasuryAPIKey:   treasuryAPIKey,
		platformTenantID: platformTenantID,
	}
}

// --- Input/Output types ---

// CreateInput defines the payload for creating a subscription.
type CreateInput struct {
	TenantID       uuid.UUID `json:"tenant_id"`
	PlanCode       string    `json:"plan_code"`
	BundleCode     string    `json:"bundle_code,omitempty"`
	TrialDays      int       `json:"trial_days,omitempty"`
	ReferredByCode string    `json:"referred_by_code,omitempty"` // Type-A referral: code of the referrer
	// TenantSlug/TenantName let the creator (auth-api S2S at signup, or the
	// auth.tenant.created consumer) seed the local tenant projection on demand,
	// avoiding an FK violation when the async tenant sync has not run yet.
	TenantSlug string `json:"tenant_slug,omitempty"`
	TenantName string `json:"tenant_name,omitempty"`
	// Terms & Conditions acceptance — required for tenant self-serve subscribe. The UI sends
	// the version it displayed plus accepted=true; the accepting user is stamped for audit.
	TermsVersion    string    `json:"terms_version,omitempty"`
	TermsAccepted   bool      `json:"terms_accepted,omitempty"`
	TermsAcceptedBy uuid.UUID `json:"terms_accepted_by,omitempty"`
}

// ChangePlanInput defines the payload for upgrading or downgrading.
type ChangePlanInput struct {
	TenantID    uuid.UUID `json:"tenant_id"`
	NewPlanCode string    `json:"new_plan_code"`
}

// CancelInput defines the payload for cancellation.
type CancelInput struct {
	TenantID uuid.UUID
	Reason   string
}

// RenewInput defines the payload for renewal.
type RenewInput struct {
	TenantID uuid.UUID
	PlanCode string // optional: renew on a different plan
}

// InitiateSubscriptionInput defines the payload for starting a subscription checkout.
type InitiateSubscriptionInput struct {
	TenantID  uuid.UUID `json:"tenant_id"`
	PlanCode  string    `json:"plan_code"`
	ReturnURL string    `json:"return_url,omitempty"` // Callback after payment
}

// InitiateSubscriptionResult contains payment intent info from Treasury.
type InitiateSubscriptionResult struct {
	IntentID         uuid.UUID       `json:"intent_id"`
	Status           string          `json:"status"`
	Amount           decimal.Decimal `json:"amount"`
	Currency         string          `json:"currency"`
	AuthorizationURL *string         `json:"authorization_url,omitempty"`
	InitiateURL      string          `json:"initiate_url,omitempty"`
}

// SubscriptionResult is returned from lifecycle operations.
type SubscriptionResult struct {
	ID                 uuid.UUID      `json:"id"`
	TenantID           uuid.UUID      `json:"tenant_id"`
	PlanCode           string         `json:"plan_code"`
	PlanName           string         `json:"plan_name"`
	Status             string         `json:"status"`
	BundleCode         *string        `json:"bundle_code,omitempty"`
	TrialEndsAt        *time.Time     `json:"trial_ends_at,omitempty"`
	CurrentPeriodStart time.Time      `json:"current_period_start"`
	CurrentPeriodEnd   time.Time      `json:"current_period_end"`
	CancelledAt        *time.Time     `json:"cancelled_at,omitempty"`
	CancelReason       *string        `json:"cancel_reason,omitempty"`
	Features           []string       `json:"features"`
	Limits             map[string]int `json:"limits"`
	// AccessStatus is derived gating state for clients: "active" (full access),
	// "grace" (past period end but within the grace window — still accessible, pay soon),
	// or "blocked" (expired/cancelled/suspended). GraceEndsAt is set while in grace.
	AccessStatus string     `json:"access_status"`
	GraceEndsAt  *time.Time `json:"grace_ends_at,omitempty"`
	// Scenario resolution — lets each service pick whichever plan scenario a tenant chose.
	// BillingCycle mirrors the plan (MONTHLY/QUARTERLY/ANNUAL/ONE_TIME).
	// BillingMode is the resolved scenario: "recurring" | "one_time" | "service_charge".
	// PlanType mirrors the plan's plan_type (TIERED/STANDALONE_SERVICE/BUNDLE/CUSTOM).
	// IsPerpetual is true for a paid ONE_TIME licence — non-expiring entitlement; auth
	// omits the JWT expiry for these so the subscription gate never blocks them.
	BillingCycle string `json:"billing_cycle"`
	BillingMode  string `json:"billing_mode"`
	PlanType     string `json:"plan_type,omitempty"`
	IsPerpetual  bool   `json:"is_perpetual"`
	// AllowOverage is the tenant's opt-in master switch for pay-as-you-go extra usage.
	// When true, metered throughput limits may be exceeded and the excess accrues as
	// OverageCharge billed on the next renewal.
	AllowOverage bool `json:"allow_overage"`
}

// --- State machine ---

// validTransitions maps current status to allowed next statuses.
var validTransitions = map[tenantsubscription.Status][]tenantsubscription.Status{
	tenantsubscription.StatusTRIAL:     {tenantsubscription.StatusACTIVE, tenantsubscription.StatusEXPIRED, tenantsubscription.StatusCANCELLED},
	tenantsubscription.StatusACTIVE:    {tenantsubscription.StatusACTIVE, tenantsubscription.StatusSUSPENDED, tenantsubscription.StatusCANCELLED, tenantsubscription.StatusEXPIRED},
	tenantsubscription.StatusSUSPENDED: {tenantsubscription.StatusACTIVE, tenantsubscription.StatusCANCELLED},
	tenantsubscription.StatusEXPIRED:   {tenantsubscription.StatusACTIVE},
	tenantsubscription.StatusCANCELLED: {tenantsubscription.StatusTRIAL, tenantsubscription.StatusACTIVE},
}

func canTransition(from, to tenantsubscription.Status) bool {
	allowed, ok := validTransitions[from]
	if !ok {
		return false
	}
	for _, s := range allowed {
		if s == to {
			return true
		}
	}
	return false
}

// --- Lifecycle operations ---

// GetSubscriptionResult is implemented in entitlements.go (composite resolution across the
// main plan + active per-product subscriptions).

// InitiateSubscription initiates the payment flow for a new or changed subscription.
func (s *Service) InitiateSubscription(ctx context.Context, in InitiateSubscriptionInput) (*InitiateSubscriptionResult, error) {
	// Demo + platform-owner tenants never own a subscription record.
	if err := s.guardExempt(ctx, in.TenantID); err != nil {
		return nil, err
	}

	// 1. Lookup plan
	plan, err := s.client.SubscriptionPlan.Query().
		Where(
			subscriptionplan.PlanCodeEQ(in.PlanCode),
			subscriptionplan.IsActiveEQ(true),
		).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("plan not found: %s", in.PlanCode)
		}
		return nil, fmt.Errorf("lookup plan: %w", err)
	}

	// 2. For free plans, activate directly without Treasury
	amount := decimal.NewFromFloat(plan.BasePrice)
	if amount.IsZero() {
		return s.activateFreePlan(ctx, in, plan)
	}

	req := map[string]any{
		"amount":         amount,
		"currency":       plan.Currency,
		"payment_method": "pending", // User selects method on Treasury-UI
		"reference_id":   fmt.Sprintf("SUB-%s-%d", in.TenantID.String()[:8], time.Now().Unix()),
		"reference_type": "subscription",
		"source_service": "subscriptions",
		"description":    fmt.Sprintf("Subscription for %s plan", plan.Name),
		"callback_url":   in.ReturnURL,
		"metadata": map[string]any{
			"tenant_id": in.TenantID.String(),
			"plan_code": plan.PlanCode,
		},
	}

	// 3. Call Treasury-API
	var treasuryHeaders map[string]string
	if s.treasuryAPIKey != "" {
		treasuryHeaders = map[string]string{"X-API-Key": s.treasuryAPIKey}
	}
	resp, err := s.treasuryClient.Post(ctx, fmt.Sprintf("/api/v1/%s/payments/intents", in.TenantID), req, treasuryHeaders)
	if err != nil {
		return nil, fmt.Errorf("treasury api error: %w", err)
	}

	if !resp.IsSuccess() {
		return nil, fmt.Errorf("treasury api failed with status %d: %s", resp.StatusCode, string(resp.Body))
	}

	var treasuryResp struct {
		IntentID         uuid.UUID       `json:"intent_id"`
		Status           string          `json:"status"`
		Amount           decimal.Decimal `json:"amount"`
		Currency         string          `json:"currency"`
		AuthorizationURL *string         `json:"authorization_url,omitempty"`
		InitiateURL      string          `json:"initiate_url,omitempty"`
	}
	if err := resp.DecodeJSON(&treasuryResp); err != nil {
		return nil, fmt.Errorf("decode treasury response: %w", err)
	}

	return &InitiateSubscriptionResult{
		IntentID:         treasuryResp.IntentID,
		Status:           treasuryResp.Status,
		Amount:           treasuryResp.Amount,
		Currency:         treasuryResp.Currency,
		AuthorizationURL: treasuryResp.AuthorizationURL,
		InitiateURL:      treasuryResp.InitiateURL,
	}, nil
}

// CreateSubscription provisions a new subscription for a tenant.
// ensureTenant lazily seeds the local tenant projection if it does not exist yet.
// auth is the source of truth; the full record arrives later via the auth.tenant
// sync, which upserts over this minimal row. Without a slug we cannot create a
// valid projection (slug is NOT NULL/unique), so we let the caller proceed and
// surface the underlying FK error. Constraint errors (concurrent create) are
// treated as success.
func (s *Service) ensureTenant(ctx context.Context, id uuid.UUID, slug, name string) error {
	if id == uuid.Nil {
		return nil
	}
	exists, err := s.client.Tenant.Query().Where(enttenant.IDEQ(id)).Exist(ctx)
	if err != nil {
		return fmt.Errorf("check tenant: %w", err)
	}
	if exists {
		return nil
	}
	if slug == "" {
		// Nothing we can seed with; the downstream FK will report the missing tenant.
		return nil
	}
	if name == "" {
		name = slug
	}
	if _, err := s.client.Tenant.Create().
		SetID(id).
		SetName(name).
		SetSlug(slug).
		SetStatus("active").
		SetSyncStatus("pending").
		Save(ctx); err != nil && !ent.IsConstraintError(err) {
		return err
	}
	s.log.Info("seeded tenant projection for subscription", zap.String("tenant_id", id.String()), zap.String("slug", slug))
	return nil
}

func (s *Service) CreateSubscription(ctx context.Context, in CreateInput) (*SubscriptionResult, error) {
	// Demo + platform-owner tenants never own a subscription record.
	if err := s.guardExempt(ctx, in.TenantID); err != nil {
		return nil, err
	}

	// Subscription T&C must be accepted by the tenant before a self-serve subscription is
	// created. (Platform-owner admin assignment goes through a separate path and is exempt.)
	if !in.TermsAccepted || in.TermsVersion == "" {
		return nil, ErrTermsNotAccepted
	}

	// Ensure the local tenant projection exists before inserting the subscription.
	// At signup, auth-api calls this S2S immediately after creating the tenant —
	// before the async auth.tenant.created projection has run — so without this the
	// insert fails the tenant_subscriptions → tenants foreign key (SQLSTATE 23503).
	if err := s.ensureTenant(ctx, in.TenantID, in.TenantSlug, in.TenantName); err != nil {
		return nil, fmt.Errorf("ensure tenant projection: %w", err)
	}

	// Verify tenant doesn't already have a subscription
	exists, err := s.client.TenantSubscription.Query().
		Where(tenantsubscription.TenantIDEQ(in.TenantID)).
		Exist(ctx)
	if err != nil {
		return nil, fmt.Errorf("check existing subscription: %w", err)
	}
	if exists {
		return nil, fmt.Errorf("tenant already has a subscription")
	}

	// Lookup plan
	plan, err := s.client.SubscriptionPlan.Query().
		Where(
			subscriptionplan.PlanCodeEQ(in.PlanCode),
			subscriptionplan.IsActiveEQ(true),
		).
		WithFeatures(func(q *ent.PlanFeatureQuery) {
			q.Where(planfeature.IsIncludedEQ(true))
		}).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("plan not found: %s", in.PlanCode)
		}
		return nil, fmt.Errorf("lookup plan: %w", err)
	}

	now := time.Now().UTC()
	status := tenantsubscription.StatusTRIAL
	var trialEndsAt *time.Time
	periodEnd := now.AddDate(0, 1, 0) // 1 month default

	if in.TrialDays > 0 {
		t := now.Add(time.Duration(in.TrialDays) * 24 * time.Hour)
		trialEndsAt = &t
		periodEnd = t
	} else {
		status = tenantsubscription.StatusACTIVE
	}

	// Use a transaction for atomicity
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return nil, fmt.Errorf("start transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	create := tx.TenantSubscription.Create().
		SetTenantID(in.TenantID).
		SetPlanID(plan.ID).
		SetStatus(status).
		SetCurrentPeriodStart(now).
		SetCurrentPeriodEnd(periodEnd).
		// Every subscription gets its own shareable referral code on creation.
		SetReferralCode(generateReferralCode())

	if trialEndsAt != nil {
		create.SetTrialEndsAt(*trialEndsAt)
	}
	if in.BundleCode != "" {
		create.SetBundleCode(in.BundleCode)
	}
	// Snapshot the plan's one-time setup fee onto the subscription; billed once on the
	// first invoice (guarded by setup_fee_charged_at), never on renewal.
	if plan.SetupFee > 0 {
		create.SetSetupFeeAmount(plan.SetupFee)
	}

	// Record the T&C acceptance captured above (audit trail of version + who + when).
	create.SetTermsVersion(in.TermsVersion).SetTermsAcceptedAt(now)
	if in.TermsAcceptedBy != uuid.Nil {
		create.SetTermsAcceptedBy(in.TermsAcceptedBy)
	}
	// New subscriptions start active for dormancy purposes.
	create.SetLastActivityAt(now)

	// Type-A referral attribution: if the tenant signed up via a referral code, record the
	// referrer so they are credited when this tenant pays. A tenant cannot refer itself.
	if in.ReferredByCode != "" {
		if referrerID, ok := s.resolveReferrer(ctx, in.ReferredByCode); ok && referrerID != in.TenantID {
			create.SetReferredBy(referrerID)
		}
	}

	sub, err := create.Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("create subscription: %w", err)
	}

	// Publish outbox event
	s.writeOutboxEvent(ctx, tx, sub.TenantID, "subscription", sub.ID, "created", map[string]any{
		"tenant_id":   sub.TenantID.String(),
		"plan_code":   plan.PlanCode,
		"plan_name":   plan.Name,
		"status":      string(status),
		"bundle_code": in.BundleCode,
		"trial_days":  in.TrialDays,
		"notification": map[string]any{
			"target": "tenant_admin",
		},
	})

	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}

	return s.buildResult(sub, plan), nil
}

// ChangePlan upgrades or downgrades a tenant's subscription plan.
func (s *Service) ChangePlan(ctx context.Context, in ChangePlanInput) (*SubscriptionResult, error) {
	// Demo + platform-owner tenants never own a subscription record.
	if err := s.guardExempt(ctx, in.TenantID); err != nil {
		return nil, err
	}

	sub, err := s.getSubscription(ctx, in.TenantID)
	if err != nil {
		return nil, err
	}

	// Only ACTIVE or TRIAL subscriptions can change plans
	if sub.Status != tenantsubscription.StatusACTIVE && sub.Status != tenantsubscription.StatusTRIAL {
		return nil, fmt.Errorf("cannot change plan in %s status", sub.Status)
	}

	// Lookup new plan
	newPlan, err := s.client.SubscriptionPlan.Query().
		Where(
			subscriptionplan.PlanCodeEQ(in.NewPlanCode),
			subscriptionplan.IsActiveEQ(true),
		).
		WithFeatures(func(q *ent.PlanFeatureQuery) {
			q.Where(planfeature.IsIncludedEQ(true))
		}).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("plan not found: %s", in.NewPlanCode)
		}
		return nil, fmt.Errorf("lookup plan: %w", err)
	}

	oldPlanID := sub.PlanID

	// Load old plan for proration calculation
	oldPlan, _ := s.client.SubscriptionPlan.Get(ctx, oldPlanID)

	// Calculate proration credit for mid-cycle upgrade
	var prorationCredit float64
	if oldPlan != nil && oldPlan.BasePrice > 0 && sub.CurrentPeriodEnd.After(time.Now().UTC()) {
		totalDays := sub.CurrentPeriodEnd.Sub(sub.CurrentPeriodStart).Hours() / 24
		remainingDays := sub.CurrentPeriodEnd.Sub(time.Now().UTC()).Hours() / 24
		if totalDays > 0 {
			prorationCredit = oldPlan.BasePrice * (remainingDays / totalDays)
		}
	}

	tx, err := s.client.Tx(ctx)
	if err != nil {
		return nil, fmt.Errorf("start transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	// Store proration credit in metadata for next renewal
	metadata := sub.Metadata
	if metadata == nil {
		metadata = map[string]any{}
	}
	if prorationCredit > 0 && newPlan.TierOrder > 0 && oldPlan != nil && newPlan.TierOrder > oldPlan.TierOrder {
		// Upgrade: store credit balance to reduce next renewal charge
		metadata["proration_credit"] = prorationCredit
		metadata["proration_credit_expires"] = sub.CurrentPeriodEnd.Format(time.RFC3339)
	} else if oldPlan != nil && newPlan.TierOrder < oldPlan.TierOrder {
		// Downgrade: credit carries to next renewal, clear any existing upgrade credit
		metadata["proration_credit"] = 0
	}

	sub, err = tx.TenantSubscription.UpdateOneID(sub.ID).
		SetPlanID(newPlan.ID).
		SetStatus(tenantsubscription.StatusACTIVE).
		SetMetadata(metadata).
		Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("update subscription plan: %w", err)
	}

	// Determine if upgrade or downgrade (reuse oldPlan already loaded for proration)
	direction := "changed"
	if newPlan.TierOrder > 0 && oldPlan != nil {
		if newPlan.TierOrder > oldPlan.TierOrder {
			direction = "upgraded"
		} else if newPlan.TierOrder < oldPlan.TierOrder {
			direction = "downgraded"
		}
	}

	// Include tenant_slug so downstream subscribers can invalidate keyed Redis caches
	tenantSlug := ""
	if t, err := tx.Tenant.Get(ctx, sub.TenantID); err == nil {
		tenantSlug = t.Slug
	}

	eventPayload := map[string]any{
		"tenant_id":     sub.TenantID.String(),
		"tenant_slug":   tenantSlug,
		"new_plan_code": newPlan.PlanCode,
		"new_plan_name": newPlan.Name,
		"service_tag": func() string {
			if newPlan.ServiceTag != nil {
				return *newPlan.ServiceTag
			}
			return ""
		}(),
		"old_plan_id": oldPlanID.String(),
		"direction":   direction,
		"notification": map[string]any{
			"target": "tenant_admin",
		},
	}
	// Directional event (subscription.upgraded / subscription.downgraded / subscription.changed)
	s.writeOutboxEvent(ctx, tx, sub.TenantID, "subscription", sub.ID, direction, eventPayload)
	// Consistent event for downstream services that subscribe to plan changes regardless of direction
	s.writeOutboxEvent(ctx, tx, sub.TenantID, "tenant", sub.TenantID, "subscription.updated", eventPayload)

	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}

	return s.buildResult(sub, newPlan), nil
}

// CancelSubscription cancels a tenant's subscription.
func (s *Service) CancelSubscription(ctx context.Context, in CancelInput) (*SubscriptionResult, error) {
	sub, err := s.getSubscription(ctx, in.TenantID)
	if err != nil {
		return nil, err
	}

	if !canTransition(sub.Status, tenantsubscription.StatusCANCELLED) {
		return nil, fmt.Errorf("cannot cancel from %s status", sub.Status)
	}

	now := time.Now().UTC()

	tx, err := s.client.Tx(ctx)
	if err != nil {
		return nil, fmt.Errorf("start transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	update := tx.TenantSubscription.UpdateOneID(sub.ID).
		SetStatus(tenantsubscription.StatusCANCELLED).
		SetCancelledAt(now)
	if in.Reason != "" {
		update.SetCancelReason(in.Reason)
	}

	sub, err = update.Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("cancel subscription: %w", err)
	}

	s.writeOutboxEvent(ctx, tx, sub.TenantID, "subscription", sub.ID, "cancelled", map[string]any{
		"tenant_id": sub.TenantID.String(),
		"reason":    in.Reason,
		"notification": map[string]any{
			"target": "tenant_admin",
		},
	})

	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}

	plan, _ := s.client.SubscriptionPlan.Query().
		Where(subscriptionplan.IDEQ(sub.PlanID)).
		WithFeatures(func(q *ent.PlanFeatureQuery) {
			q.Where(planfeature.IsIncludedEQ(true))
		}).
		Only(ctx)

	return s.buildResult(sub, plan), nil
}

// RenewSubscription renews an expired or cancelled subscription.
func (s *Service) RenewSubscription(ctx context.Context, in RenewInput) (*SubscriptionResult, error) {
	sub, err := s.getSubscription(ctx, in.TenantID)
	if err != nil {
		return nil, err
	}

	if !canTransition(sub.Status, tenantsubscription.StatusACTIVE) {
		return nil, fmt.Errorf("cannot renew from %s status", sub.Status)
	}

	// Use current plan or switch if specified
	planID := sub.PlanID
	if in.PlanCode != "" {
		newPlan, err := s.client.SubscriptionPlan.Query().
			Where(
				subscriptionplan.PlanCodeEQ(in.PlanCode),
				subscriptionplan.IsActiveEQ(true),
			).Only(ctx)
		if err != nil {
			return nil, fmt.Errorf("plan not found: %s", in.PlanCode)
		}
		planID = newPlan.ID
	}

	now := time.Now().UTC()
	// Pay-early stacking: extend from the later of now and the current period end so a
	// tenant who pays before expiry keeps their remaining paid days instead of losing them.
	base := now
	if sub.CurrentPeriodEnd.After(now) {
		base = sub.CurrentPeriodEnd
	}
	var periodEnd time.Time
	switch sub.BillingCycle {
	case tenantsubscription.BillingCycleANNUAL:
		periodEnd = base.AddDate(1, 0, 0)
	case tenantsubscription.BillingCycleQUARTERLY:
		periodEnd = base.AddDate(0, 3, 0)
	default: // MONTHLY and ONE_TIME
		periodEnd = base.AddDate(0, 1, 0)
	}

	// Clear grace + invoice/reminder markers now that payment has been received.
	cleanedMeta := make(map[string]any, len(sub.Metadata))
	for k, v := range sub.Metadata {
		switch k {
		case "grace_until", "last_grace_reminder_date":
			// drop — grace ended on payment
		default:
			cleanedMeta[k] = v
		}
	}

	tx, err := s.client.Tx(ctx)
	if err != nil {
		return nil, fmt.Errorf("start transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	sub, err = tx.TenantSubscription.UpdateOneID(sub.ID).
		SetPlanID(planID).
		SetStatus(tenantsubscription.StatusACTIVE).
		SetCurrentPeriodStart(now).
		SetCurrentPeriodEnd(periodEnd).
		SetMetadata(cleanedMeta).
		ClearCancelledAt().
		ClearCancelReason().
		ClearTrialEndsAt().
		Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("renew subscription: %w", err)
	}

	s.writeOutboxEvent(ctx, tx, sub.TenantID, "subscription", sub.ID, "renewed", map[string]any{
		"tenant_id": sub.TenantID.String(),
		"notification": map[string]any{
			"target": "tenant_admin",
		},
	})

	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}

	plan, _ := s.client.SubscriptionPlan.Query().
		Where(subscriptionplan.IDEQ(sub.PlanID)).
		WithFeatures(func(q *ent.PlanFeatureQuery) {
			q.Where(planfeature.IsIncludedEQ(true))
		}).
		Only(ctx)

	return s.buildResult(sub, plan), nil
}

// SwitchPlanByID changes the plan for a subscription identified by its UUID.
// Delegates to ChangePlan after resolving the tenant from the subscription record.
func (s *Service) SwitchPlanByID(ctx context.Context, subscriptionID uuid.UUID, newPlanCode string) (*SubscriptionResult, error) {
	sub, err := s.client.TenantSubscription.Get(ctx, subscriptionID)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("subscription not found")
		}
		return nil, fmt.Errorf("lookup subscription: %w", err)
	}
	return s.ChangePlan(ctx, ChangePlanInput{
		TenantID:    sub.TenantID,
		NewPlanCode: newPlanCode,
	})
}

// ActivateProduct enables a product subscription within a tenant subscription.
func (s *Service) ActivateProduct(ctx context.Context, tenantID uuid.UUID, productCode string) error {
	return s.AssignProductPlan(ctx, tenantID, productCode, "")
}

// AssignProductPlan activates a per-product subscription for a tenant and, when planCode is
// non-empty, points it at that plan via override_plan_id so the plan's features/limits are
// merged into the tenant's COMPOSITE entitlements (multi-use-case: e.g. POS main plan + a
// TruLoad product line). Emits tenant.subscription.updated so caches/JWTs refresh.
func (s *Service) AssignProductPlan(ctx context.Context, tenantID uuid.UUID, productCode, planCode string) error {
	// Demo + platform-owner tenants never own a subscription record.
	if err := s.guardExempt(ctx, tenantID); err != nil {
		return err
	}

	sub, err := s.getSubscription(ctx, tenantID)
	if err != nil {
		return err
	}
	if sub.Status != tenantsubscription.StatusACTIVE && sub.Status != tenantsubscription.StatusTRIAL {
		return fmt.Errorf("cannot activate products in %s status", sub.Status)
	}

	// Resolve the override plan (if a plan code was supplied) and its owning product.
	var overridePlanID *uuid.UUID
	if planCode != "" {
		plan, perr := s.client.SubscriptionPlan.Query().
			Where(subscriptionplan.PlanCodeEQ(planCode), subscriptionplan.IsActiveEQ(true)).
			Only(ctx)
		if perr != nil {
			if ent.IsNotFound(perr) {
				return fmt.Errorf("plan not found: %s", planCode)
			}
			return fmt.Errorf("lookup plan: %w", perr)
		}
		overridePlanID = &plan.ID
	}

	tx, err := s.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("start transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	existing, qerr := tx.ProductSubscription.Query().
		Where(
			productsubscription.TenantSubscriptionIDEQ(sub.ID),
			productsubscription.ProductCodeEQ(productCode),
		).
		Only(ctx)
	switch {
	case qerr == nil:
		upd := tx.ProductSubscription.UpdateOneID(existing.ID).
			SetStatus(productsubscription.StatusActive).
			ClearDeactivatedAt()
		if overridePlanID != nil {
			upd = upd.SetOverridePlanID(*overridePlanID)
		}
		if _, err = upd.Save(ctx); err != nil {
			return fmt.Errorf("update product subscription: %w", err)
		}
	case ent.IsNotFound(qerr):
		create := tx.ProductSubscription.Create().
			SetTenantSubscriptionID(sub.ID).
			SetProductCode(productCode).
			SetStatus(productsubscription.StatusActive).
			SetActivatedAt(time.Now().UTC())
		if overridePlanID != nil {
			create = create.SetOverridePlanID(*overridePlanID)
		}
		if _, err = create.Save(ctx); err != nil {
			return fmt.Errorf("create product subscription: %w", err)
		}
	default:
		err = fmt.Errorf("check product subscription: %w", qerr)
		return err
	}

	s.emitSubscriptionUpdatedTx(ctx, tx, sub, "product_activated")
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// DeactivateProduct disables a product subscription and refreshes composite entitlements.
func (s *Service) DeactivateProduct(ctx context.Context, tenantID uuid.UUID, productCode string) error {
	sub, err := s.getSubscription(ctx, tenantID)
	if err != nil {
		return err
	}

	existing, err := s.client.ProductSubscription.Query().
		Where(
			productsubscription.TenantSubscriptionIDEQ(sub.ID),
			productsubscription.ProductCodeEQ(productCode),
		).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return fmt.Errorf("product subscription not found: %s", productCode)
		}
		return fmt.Errorf("lookup product subscription: %w", err)
	}

	tx, err := s.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("start transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	now := time.Now().UTC()
	if err = tx.ProductSubscription.UpdateOneID(existing.ID).
		SetStatus(productsubscription.StatusInactive).
		SetNillableDeactivatedAt(&now).
		Exec(ctx); err != nil {
		return fmt.Errorf("deactivate product subscription: %w", err)
	}

	s.emitSubscriptionUpdatedTx(ctx, tx, sub, "product_deactivated")
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// emitSubscriptionUpdatedTx writes the tenant.subscription.updated outbox event (with the
// tenant slug so downstream subscribers can invalidate slug-keyed Redis caches) inside tx.
// Used after product-subscription changes so auth re-mints JWTs with the new composite set.
func (s *Service) emitSubscriptionUpdatedTx(ctx context.Context, tx *ent.Tx, sub *ent.TenantSubscription, direction string) {
	tenantSlug := ""
	if t, terr := tx.Tenant.Get(ctx, sub.TenantID); terr == nil {
		tenantSlug = t.Slug
	}
	s.writeOutboxEvent(ctx, tx, sub.TenantID, "tenant", sub.TenantID, "subscription.updated", map[string]any{
		"tenant_id":   sub.TenantID.String(),
		"tenant_slug": tenantSlug,
		"direction":   direction,
	})
}

// ListProducts returns all product subscriptions for a tenant.
func (s *Service) ListProducts(ctx context.Context, tenantID uuid.UUID) ([]*ent.ProductSubscription, error) {
	sub, err := s.getSubscription(ctx, tenantID)
	if err != nil {
		return nil, err
	}

	return s.client.ProductSubscription.Query().
		Where(productsubscription.TenantSubscriptionIDEQ(sub.ID)).
		All(ctx)
}

// ListSubscriptions returns all tenant subscriptions (Admin only).
func (s *Service) ListSubscriptions(ctx context.Context) ([]*SubscriptionResult, error) {
	subs, err := s.client.TenantSubscription.Query().
		WithPlan(func(q *ent.SubscriptionPlanQuery) {
			q.WithFeatures()
		}).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("list subscriptions: %w", err)
	}

	results := make([]*SubscriptionResult, len(subs))
	for i, sub := range subs {
		results[i] = s.buildResult(sub, sub.Edges.Plan)
	}
	return results, nil
}

// ServiceSubscriptionEntry describes a tenant's subscription status for one service tag.
type ServiceSubscriptionEntry struct {
	ServiceTag       string     `json:"service_tag"`
	Status           string     `json:"status"` // "ACTIVE", "TRIAL", "EXPIRED", "CANCELLED", "NONE"
	PlanCode         *string    `json:"plan_code,omitempty"`
	PlanName         *string    `json:"plan_name,omitempty"`
	CurrentPeriodEnd *time.Time `json:"current_period_end,omitempty"`
}

// ServiceSubscriptionsResult is returned by GetServiceSubscriptions.
type ServiceSubscriptionsResult struct {
	TenantID     uuid.UUID                  `json:"tenant_id"`
	Subscription *SubscriptionResult        `json:"subscription,omitempty"`
	Services     []ServiceSubscriptionEntry `json:"services"`
}

// GetServiceSubscriptions returns a per-service-tag subscription view for a tenant.
// Each billable service tag is listed; only the tag matching the current plan is ACTIVE.
func (s *Service) GetServiceSubscriptions(ctx context.Context, tenantID uuid.UUID) (*ServiceSubscriptionsResult, error) {
	sub, err := s.client.TenantSubscription.Query().
		Where(tenantsubscription.TenantIDEQ(tenantID)).
		WithPlan(func(q *ent.SubscriptionPlanQuery) {
			q.WithFeatures(func(fq *ent.PlanFeatureQuery) {
				fq.Where(planfeature.IsIncludedEQ(true))
			})
		}).
		Only(ctx)
	if err != nil && !ent.IsNotFound(err) {
		return nil, fmt.Errorf("query subscription: %w", err)
	}

	result := &ServiceSubscriptionsResult{TenantID: tenantID}

	// Composite: a tenant is ACTIVE for every service tag covered by its main plan AND by
	// any active per-product subscription's override plan (multi-use-case tenants).
	activeTags := map[string]bool{}
	if sub != nil && sub.Edges.Plan != nil {
		result.Subscription = s.buildResult(sub, sub.Edges.Plan)
		activeTags = s.activeServiceTags(ctx, sub, sub.Edges.Plan)
	}

	services := make([]ServiceSubscriptionEntry, 0, len(domain.AllServiceTags))
	for _, tag := range domain.AllServiceTags {
		entry := ServiceSubscriptionEntry{ServiceTag: tag, Status: "NONE"}
		if sub != nil && activeTags[tag] {
			entry.Status = string(sub.Status)
			if sub.Edges.Plan != nil {
				code := sub.Edges.Plan.PlanCode
				name := sub.Edges.Plan.Name
				entry.PlanCode = &code
				entry.PlanName = &name
			}
			entry.CurrentPeriodEnd = &sub.CurrentPeriodEnd
		}
		services = append(services, entry)
	}
	result.Services = services
	return result, nil
}

// ExpiringSubscription represents a subscription nearing expiry.
type ExpiringSubscription struct {
	TenantID         uuid.UUID `json:"tenant_id"`
	PlanCode         string    `json:"plan_code"`
	PlanName         string    `json:"plan_name"`
	CurrentPeriodEnd time.Time `json:"current_period_end"`
	DaysRemaining    int       `json:"days_remaining"`
}

// ListExpiring returns ACTIVE subscriptions expiring within the given number of days.
func (s *Service) ListExpiring(ctx context.Context, days int) ([]ExpiringSubscription, error) {
	now := time.Now().UTC()
	deadline := now.AddDate(0, 0, days)

	subs, err := s.client.TenantSubscription.Query().
		Where(
			tenantsubscription.StatusEQ(tenantsubscription.StatusACTIVE),
			tenantsubscription.CurrentPeriodEndGTE(now),
			tenantsubscription.CurrentPeriodEndLTE(deadline),
		).
		WithPlan().
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("list expiring subscriptions: %w", err)
	}

	results := make([]ExpiringSubscription, 0, len(subs))
	for _, sub := range subs {
		daysLeft := int(sub.CurrentPeriodEnd.Sub(now).Hours() / 24)
		es := ExpiringSubscription{
			TenantID:         sub.TenantID,
			CurrentPeriodEnd: sub.CurrentPeriodEnd,
			DaysRemaining:    daysLeft,
		}
		if sub.Edges.Plan != nil {
			es.PlanCode = sub.Edges.Plan.PlanCode
			es.PlanName = sub.Edges.Plan.Name
		}
		results = append(results, es)
	}
	return results, nil
}

// activateFreePlan provisions a free plan subscription without going through Treasury.
func (s *Service) activateFreePlan(ctx context.Context, in InitiateSubscriptionInput, plan *ent.SubscriptionPlan) (*InitiateSubscriptionResult, error) {
	now := time.Now().UTC()
	periodEnd := now.AddDate(0, 1, 0)

	tx, err := s.client.Tx(ctx)
	if err != nil {
		return nil, fmt.Errorf("start transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	// Upsert: update if exists, create if not
	existing, err := tx.TenantSubscription.Query().
		Where(tenantsubscription.TenantIDEQ(in.TenantID)).
		Only(ctx)
	if err != nil && !ent.IsNotFound(err) {
		return nil, fmt.Errorf("query existing subscription: %w", err)
	}

	if existing != nil {
		_, err = tx.TenantSubscription.UpdateOneID(existing.ID).
			SetPlanID(plan.ID).
			SetStatus(tenantsubscription.StatusACTIVE).
			SetCurrentPeriodStart(now).
			SetCurrentPeriodEnd(periodEnd).
			ClearCancelledAt().
			ClearCancelReason().
			Save(ctx)
	} else {
		_, err = tx.TenantSubscription.Create().
			SetTenantID(in.TenantID).
			SetPlanID(plan.ID).
			SetStatus(tenantsubscription.StatusACTIVE).
			SetCurrentPeriodStart(now).
			SetCurrentPeriodEnd(periodEnd).
			Save(ctx)
	}
	if err != nil {
		return nil, fmt.Errorf("provision free subscription: %w", err)
	}

	s.writeOutboxEvent(ctx, tx, in.TenantID, "subscription", in.TenantID, "activated", map[string]any{
		"tenant_id": in.TenantID.String(),
		"plan_code": plan.PlanCode,
		"plan_name": plan.Name,
		"status":    "ACTIVE",
		"free_plan": true,
	})

	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}

	zero := decimal.Zero
	status := "active"
	return &InitiateSubscriptionResult{
		IntentID: in.TenantID, // reuse tenant ID as a stable token for free plans
		Status:   status,
		Amount:   zero,
		Currency: plan.Currency,
	}, nil
}

// --- Helpers ---

func (s *Service) getSubscription(ctx context.Context, tenantID uuid.UUID) (*ent.TenantSubscription, error) {
	sub, err := s.client.TenantSubscription.Query().
		Where(tenantsubscription.TenantIDEQ(tenantID)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("subscription not found for tenant")
		}
		return nil, fmt.Errorf("query subscription: %w", err)
	}
	return sub, nil
}

// SetAllowOverage flips the tenant's opt-in extra-usage master switch and publishes a
// tenant.subscription.updated event so auth-api refreshes the cached claim. Returns the
// rebuilt subscription result.
func (s *Service) SetAllowOverage(ctx context.Context, tenantID uuid.UUID, enabled bool) (*SubscriptionResult, error) {
	sub, err := s.getSubscription(ctx, tenantID)
	if err != nil {
		return nil, err
	}

	tx, err := s.client.Tx(ctx)
	if err != nil {
		return nil, fmt.Errorf("start transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	upd := tx.TenantSubscription.UpdateOneID(sub.ID).SetAllowOverage(enabled)
	if enabled {
		upd = upd.SetOverageEnabledAt(time.Now().UTC())
	}
	sub, err = upd.Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("update allow_overage: %w", err)
	}

	tenantSlug := ""
	if t, terr := tx.Tenant.Get(ctx, sub.TenantID); terr == nil {
		tenantSlug = t.Slug
	}
	eventPayload := map[string]any{
		"tenant_id":     sub.TenantID.String(),
		"tenant_slug":   tenantSlug,
		"allow_overage": enabled,
		"direction":     "changed",
	}
	s.writeOutboxEvent(ctx, tx, sub.TenantID, "tenant", sub.TenantID, "subscription.updated", eventPayload)

	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}

	return s.GetSubscriptionResult(ctx, tenantID)
}

func (s *Service) buildResult(sub *ent.TenantSubscription, plan *ent.SubscriptionPlan) *SubscriptionResult {
	result := &SubscriptionResult{
		ID:                 sub.ID,
		TenantID:           sub.TenantID,
		Status:             string(sub.Status),
		BundleCode:         sub.BundleCode,
		TrialEndsAt:        sub.TrialEndsAt,
		CurrentPeriodStart: sub.CurrentPeriodStart,
		CurrentPeriodEnd:   sub.CurrentPeriodEnd,
		CancelledAt:        sub.CancelledAt,
		CancelReason:       sub.CancelReason,
		Features:           []string{},
		Limits:             map[string]int{},
		AllowOverage:       sub.AllowOverage,
	}

	// Derive access status (grace keeps access alive while past-due but within window).
	result.AccessStatus = "blocked"
	switch sub.Status {
	case tenantsubscription.StatusACTIVE, tenantsubscription.StatusTRIAL:
		result.AccessStatus = "active"
		if gu, ok := sub.Metadata["grace_until"].(string); ok && gu != "" {
			if t, err := time.Parse(time.RFC3339, gu); err == nil {
				tu := t.UTC()
				result.GraceEndsAt = &tu
				if time.Now().UTC().Before(tu) {
					result.AccessStatus = "grace"
				}
			}
		}
	}

	// Default scenario: recurring subscription gated by period end / grace.
	result.BillingMode = "recurring"

	if plan != nil {
		result.PlanCode = plan.PlanCode
		result.PlanName = plan.Name
		result.BillingCycle = plan.BillingCycle
		result.PlanType = string(plan.PlanType)
		if plan.Edges.Features != nil {
			for _, f := range plan.Edges.Features {
				result.Features = append(result.Features, f.FeatureCode)
			}
		}
		if plan.TierLimitsJSON != nil {
			for k, v := range plan.TierLimitsJSON {
				if intVal, ok := v.(float64); ok {
					result.Limits[k] = int(intVal)
				} else if intVal, ok := v.(int); ok {
					result.Limits[k] = intVal
				}
			}
		}

		// One-time licence → perpetual entitlement: never expires. Force active access
		// regardless of the stored period end so the gate and JWT treat it as permanent.
		if plan.BillingCycle == "ONE_TIME" {
			result.BillingMode = "one_time"
			result.IsPerpetual = true
			if sub.Status != tenantsubscription.StatusCANCELLED && sub.Status != tenantsubscription.StatusSUSPENDED {
				result.AccessStatus = "active"
				result.GraceEndsAt = nil
			}
		}
	}

	return result
}

// WriteOutboxEventPublic is the exported wrapper for use by handlers in other packages.
func (s *Service) WriteOutboxEventPublic(ctx context.Context, tx *ent.Tx, tenantID uuid.UUID, aggregateType string, aggregateID uuid.UUID, eventType string, data map[string]any) {
	s.writeOutboxEvent(ctx, tx, tenantID, aggregateType, aggregateID, eventType, data)
}

func (s *Service) writeOutboxEvent(ctx context.Context, tx *ent.Tx, tenantID uuid.UUID, aggregateType string, aggregateID uuid.UUID, eventType string, data map[string]any) {
	payload := map[string]any{
		"id":             uuid.New().String(),
		"tenant_id":      tenantID.String(),
		"aggregate_type": aggregateType,
		"aggregate_id":   aggregateID.String(),
		"event_type":     eventType,
		"payload":        data,
		"timestamp":      time.Now().UTC().Format(time.RFC3339),
		"version":        "1.0",
	}

	if err := tx.OutboxEvent.Create().
		SetTenantID(tenantID).
		SetAggregateType(aggregateType).
		SetAggregateID(aggregateID).
		SetEventType(eventType).
		SetPayload(payload).
		SetStatus("PENDING").
		SetAttempts(0).
		Exec(ctx); err != nil {
		s.log.Warn("failed to write outbox event", zap.String("event_type", eventType), zap.Error(err))
	}
}
