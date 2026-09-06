package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	authclient "github.com/Bengo-Hub/shared-auth-client"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/serviceconfig"
	"github.com/bengobox/subscription-service/internal/ent/subscriptionplan"
	enttenant "github.com/bengobox/subscription-service/internal/ent/tenant"
	"github.com/bengobox/subscription-service/internal/ent/tenantsubscription"
	"github.com/bengobox/subscription-service/internal/modules/billing"
	"github.com/bengobox/subscription-service/internal/modules/subscriptions"
)

// PlatformHandler handles platform admin endpoints.
type PlatformHandler struct {
	log            *zap.Logger
	client         *ent.Client
	featureHandler *FeatureHandler
	invoiceSvc     *billing.InvoiceService
	subSvc         *subscriptions.Service
}

// NewPlatformHandler creates a new platform handler.
func NewPlatformHandler(log *zap.Logger, client *ent.Client, featureHandler *FeatureHandler) *PlatformHandler {
	return &PlatformHandler{
		log:            log.Named("platform.handler"),
		client:         client,
		featureHandler: featureHandler,
	}
}

// WithInvoiceService wires the subscription invoice service for manual generation/resend.
func (h *PlatformHandler) WithInvoiceService(svc *billing.InvoiceService) {
	h.invoiceSvc = svc
}

// WithSubscriptionService wires the subscription service so admin assignment can honor
// the demo/platform tenant exemption (those tenants must never own a subscription).
func (h *PlatformHandler) WithSubscriptionService(svc *subscriptions.Service) {
	h.subSvc = svc
}

// GetPlatformStats godoc
// @Summary Get platform statistics (admin)
// @Description Returns aggregated platform stats: total plans, subscriptions, MRR. Requires platform owner.
// @Tags Platform
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{}
// @Failure 403 {object} map[string]string
// @Router /platform/stats [get]
func (h *PlatformHandler) GetPlatformStats(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	totalPlans, _ := h.client.SubscriptionPlan.Query().Count(ctx)
	activePlans, _ := h.client.SubscriptionPlan.Query().
		Where(subscriptionplan.IsActive(true)).
		Count(ctx)
	totalSubscriptions, _ := h.client.TenantSubscription.Query().Count(ctx)
	activeSubscriptions, _ := h.client.TenantSubscription.Query().
		Where(tenantsubscription.StatusEQ(tenantsubscription.StatusACTIVE)).
		Count(ctx)
	trialingCount, _ := h.client.TenantSubscription.Query().
		Where(tenantsubscription.StatusEQ(tenantsubscription.StatusTRIAL)).
		Count(ctx)
	churnedCount, _ := h.client.TenantSubscription.Query().
		Where(tenantsubscription.StatusIn(
			tenantsubscription.StatusCANCELLED,
			tenantsubscription.StatusEXPIRED,
		)).
		Count(ctx)

	// MRR: sum base prices for ACTIVE subscriptions only
	activeSubs, err := h.client.TenantSubscription.Query().
		Where(tenantsubscription.StatusEQ(tenantsubscription.StatusACTIVE)).
		WithPlan().
		All(ctx)
	mrr := 0.0
	if err == nil {
		for _, s := range activeSubs {
			if s.Edges.Plan != nil {
				mrr += s.Edges.Plan.BasePrice
			}
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"totalPlans":          totalPlans,
		"activePlans":         activePlans,
		"totalSubscriptions":  totalSubscriptions,
		"activeSubscriptions": activeSubscriptions,
		"trialingCount":       trialingCount,
		"churnedCount":        churnedCount,
		"mrr":                 mrr,
		"currency":            "KES",
	})
}

// ListAllSubscriptions godoc
// @Summary List all subscriptions (admin)
// @Description Returns paginated list of all tenant subscriptions. Requires platform owner.
// @Tags Platform
// @Produce json
// @Security BearerAuth
// @Param page query int false "Page number (default 1)"
// @Param pageSize query int false "Page size (default 20)"
// @Param status query string false "Filter by status (ACTIVE, TRIAL, EXPIRED, etc.)"
// @Success 200 {object} map[string]interface{}
// @Failure 403 {object} map[string]string
// @Router /admin/subscriptions [get]
func (h *PlatformHandler) ListAllSubscriptions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("pageSize"))
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize

	query := h.client.TenantSubscription.Query().
		WithPlan().
		WithTenant()

	if status := r.URL.Query().Get("status"); status != "" {
		// Accept both uppercase (ACTIVE) and lowercase (active) from the frontend
		query = query.Where(tenantsubscription.StatusEQ(tenantsubscription.Status(strings.ToUpper(status))))
	}

	total, err := query.Clone().Count(ctx)
	if err != nil {
		h.log.Error("failed to count subscriptions", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	subs, err := query.
		Offset(offset).
		Limit(pageSize).
		Order(ent.Desc(tenantsubscription.FieldCreatedAt)).
		All(ctx)
	if err != nil {
		h.log.Error("failed to list subscriptions", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	type subRow struct {
		ID               string  `json:"id"`
		TenantID         string  `json:"tenantId"`
		TenantName       string  `json:"tenantName"`
		TenantSlug       string  `json:"tenantSlug"`
		PlanCode         string  `json:"planCode"`
		PlanName         string  `json:"planName"`
		PlanTier         string  `json:"planTier"`
		Status           string  `json:"status"`
		StartDate        string  `json:"startDate"`
		CurrentPeriodEnd string  `json:"currentPeriodEnd"`
		MonthlyRevenue   float64 `json:"monthlyRevenue"`
		Currency         string  `json:"currency"`
	}

	searchTerm := strings.ToLower(r.URL.Query().Get("search"))

	data := make([]subRow, 0, len(subs))
	for _, s := range subs {
		tenantName := ""
		tenantSlug := ""
		if s.Edges.Tenant != nil {
			tenantName = s.Edges.Tenant.Name
			tenantSlug = s.Edges.Tenant.Slug
		}

		// Apply search filter (tenant name or slug contains search term)
		if searchTerm != "" {
			if !strings.Contains(strings.ToLower(tenantName), searchTerm) &&
				!strings.Contains(strings.ToLower(tenantSlug), searchTerm) {
				continue
			}
		}

		planCode := ""
		planName := ""
		planTier := ""
		monthlyRevenue := 0.0
		currency := "KES"
		if s.Edges.Plan != nil {
			planCode = s.Edges.Plan.PlanCode
			planName = s.Edges.Plan.Name
			monthlyRevenue = s.Edges.Plan.BasePrice
			currency = s.Edges.Plan.Currency
			planTier = derivePlanTier(s.Edges.Plan.PlanCode, s.Edges.Plan.TierOrder)
		}

		data = append(data, subRow{
			ID:               s.ID.String(),
			TenantID:         s.TenantID.String(),
			TenantName:       tenantName,
			TenantSlug:       tenantSlug,
			PlanCode:         planCode,
			PlanName:         planName,
			PlanTier:         planTier,
			Status:           strings.ToUpper(string(s.Status)),
			StartDate:        s.CurrentPeriodStart.Format("2006-01-02T15:04:05Z"),
			CurrentPeriodEnd: s.CurrentPeriodEnd.Format("2006-01-02T15:04:05Z"),
			MonthlyRevenue:   monthlyRevenue,
			Currency:         currency,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"data":     data,
		"total":    total,
		"page":     page,
		"pageSize": pageSize,
	})
}

// AssignPlanToTenant godoc
// @Summary Assign subscription plan to a tenant (admin)
// @Description Creates or updates a tenant's subscription to the given plan. Requires platform owner.
// @Tags Platform
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param tenant_id path string true "Tenant UUID"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /admin/tenants/{tenant_id}/subscription [post]
func (h *PlatformHandler) AssignPlanToTenant(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	tenantIDStr := chi.URLParam(r, "tenant_id")
	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid tenant_id"})
		return
	}

	var body struct {
		PlanCode     string `json:"planCode"`
		StartTrial   bool   `json:"startTrial"`
		BillingCycle string `json:"billingCycle"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if body.PlanCode == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "planCode is required"})
		return
	}
	// Validate an explicitly-supplied cycle up front (ResolveAssignedCycle re-normalizes below).
	if _, err := subscriptions.NormalizeBillingCycle(body.BillingCycle); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	// Demo + platform-owner tenants stay exempt from billing/feature-gating no matter what
	// happens below (GetSubscriptionResult/exemptResult always return the full-access exempt
	// values for them, unconditionally). 2026-09-02: this endpoint no longer no-ops for an
	// exempt tenant — a platform admin can now deliberately assign a plan to one purely for
	// CLASSIFICATION (facility_type -> hospital-ui's adaptive nav, plan name for display; see
	// exemptResult's own doc comment). This is safe specifically because this handler is the
	// admin-direct data-assignment path (a plain TenantSubscription row write below, no payment/
	// billing call anywhere in this function) — the self-serve CreateSubscription/
	// InitiateSubscription paths (which DO touch real payment flows) remain fully blocked by
	// guardExempt for exempt tenants, untouched by this change.
	isExempt := h.subSvc != nil && h.subSvc.IsExemptTenant(ctx, tenantID)

	plan, err := h.client.SubscriptionPlan.Query().
		Where(subscriptionplan.PlanCode(body.PlanCode)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "plan not found"})
			return
		}
		h.log.Error("failed to find plan", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	status := tenantsubscription.StatusACTIVE
	if body.StartTrial {
		status = tenantsubscription.StatusTRIAL
	}

	now := time.Now()

	// When starting a trial, set trial_ends_at from the plan's free_trial_days so
	// the trial actually expires (otherwise it never ends — a free-forever leak).
	var trialEndsPtr *time.Time
	if body.StartTrial && plan.FreeTrialDays > 0 {
		te := now.AddDate(0, 0, plan.FreeTrialDays)
		trialEndsPtr = &te
	}

	// Check if subscription already exists for tenant
	existing, err := h.client.TenantSubscription.Query().
		Where(tenantsubscription.TenantID(tenantID)).
		Only(ctx)
	if err != nil && !ent.IsNotFound(err) {
		h.log.Error("failed to query existing subscription", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	// Resolve the cycle to STORE: admin's explicit request wins, else inherit the plan's own
	// cycle. This is the fix for a ONE_TIME (perpetual) plan being silently stored as MONTHLY
	// with a finite ~1-month current_period_end — it must inherit ONE_TIME and be perpetual.
	cycle := subscriptions.ResolveAssignedCycle(body.BillingCycle, plan.BillingCycle)
	periodEnd := subscriptions.ResolvePeriodEnd(now, string(cycle))

	var sub interface{}
	if existing != nil {
		// Update existing subscription. Always (re)set billing_cycle to the resolved cycle so
		// switching a tenant onto a ONE_TIME plan corrects a previously-stored recurring cycle.
		updated, err := h.client.TenantSubscription.UpdateOneID(existing.ID).
			SetPlanID(plan.ID).
			SetStatus(status).
			SetBillingCycle(cycle).
			SetCurrentPeriodStart(now).
			SetCurrentPeriodEnd(periodEnd).
			SetNillableTrialEndsAt(trialEndsPtr).
			Save(ctx)
		if err != nil {
			h.log.Error("failed to update tenant subscription", zap.Error(err))
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to update subscription"})
			return
		}
		sub = updated
	} else {
		// Create new subscription
		created, err := h.client.TenantSubscription.Create().
			SetTenantID(tenantID).
			SetPlanID(plan.ID).
			SetStatus(status).
			SetBillingCycle(cycle).
			SetCurrentPeriodStart(now).
			SetCurrentPeriodEnd(periodEnd).
			SetNillableTrialEndsAt(trialEndsPtr).
			Save(ctx)
		if err != nil {
			h.log.Error("failed to create tenant subscription", zap.Error(err))
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create subscription"})
			return
		}
		sub = created
	}

	if h.featureHandler != nil {
		h.featureHandler.InvalidateCache(ctx, tenantID)
	}

	if isExempt {
		h.log.Info("assigned plan to an exempt tenant for classification only — billing/feature-gating unaffected",
			zap.String("tenant_id", tenantID.String()), zap.String("plan_code", body.PlanCode))
		writeJSON(w, http.StatusOK, map[string]any{
			"subscription":        sub,
			"exempt":              true,
			"classification_only": true,
			"message":             "tenant remains exempt from billing/feature-gating; this plan only classifies its facility_type/presentation",
		})
		return
	}

	writeJSON(w, http.StatusOK, sub)
}

// ConfirmDormancyPurge godoc
// @Summary Confirm deletion of a dormant tenant's data (platform owner)
// @Description Platform-owner-confirmed, irreversible purge of a suspended/dormant tenant. Emits
// @Description tenant.purge for every service to delete that tenant's data. Guarded by the
// @Description /admin platform-owner middleware; only acts on accounts already queued (pending_purge).
// @Tags Platform
// @Produce json
// @Param tenant_id path string true "Tenant UUID"
// @Success 200 {object} map[string]interface{}
// @Router /admin/tenants/{tenant_id}/purge-confirm [post]
func (h *PlatformHandler) ConfirmDormancyPurge(w http.ResponseWriter, r *http.Request) {
	if h.subSvc == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "subscription service unavailable"})
		return
	}
	ctx := r.Context()
	tenantID, err := uuid.Parse(chi.URLParam(r, "tenant_id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid tenant_id"})
		return
	}

	sub, err := h.client.TenantSubscription.Query().
		Where(tenantsubscription.TenantID(tenantID)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "no subscription for tenant"})
			return
		}
		h.log.Error("purge-confirm: query subscription", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	// Safety: only purge accounts the dormancy job actually suspended + queued. Prevents an
	// accidental purge of a live tenant.
	if !sub.PendingPurge {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error":   "not_pending_purge",
			"message": "tenant is not suspended/queued for purge; only dormancy-suspended accounts can be purged",
		})
		return
	}

	tx, err := h.client.Tx(ctx)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	now := time.Now().UTC()
	if _, err := tx.TenantSubscription.UpdateOneID(sub.ID).
		SetStatus(tenantsubscription.StatusCANCELLED).
		SetPendingPurge(false).
		SetCancelledAt(now).
		SetCancelReason("dormancy_purge_confirmed").
		Save(ctx); err != nil {
		_ = tx.Rollback()
		h.log.Error("purge-confirm: update subscription", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	// Emit tenant.purge on the "tenant" aggregate. Every service runs a consumer that deletes
	// that tenant's rows on receipt. Irreversible — gated by the platform-owner /admin middleware
	// and the pending_purge precondition above.
	h.subSvc.WriteOutboxEventPublic(ctx, tx, tenantID, "tenant", tenantID, "purge", map[string]any{
		"tenant_id":    tenantID.String(),
		"reason":       "dormancy",
		"confirmed":    true,
		"confirmed_at": now.Format(time.RFC3339),
		"scope":        "all_services",
	})

	if err := tx.Commit(); err != nil {
		h.log.Error("purge-confirm: commit", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	if h.featureHandler != nil {
		h.featureHandler.InvalidateCache(ctx, tenantID)
	}
	h.log.Warn("dormancy purge confirmed — tenant.purge emitted", zap.String("tenant_id", tenantID.String()))
	writeJSON(w, http.StatusOK, map[string]any{
		"tenant_id": tenantID.String(),
		"status":    "purge_initiated",
		"message":   "tenant.purge emitted to all services; tenant data deletion is in progress",
	})
}

// ListTenantProducts godoc
// @Summary List a tenant's per-product subscription lines (admin)
// @Description Returns the product subscriptions attached to the tenant's main subscription.
// @Tags Platform
// @Produce json
// @Security BearerAuth
// @Param tenant_id path string true "Tenant UUID"
// @Success 200 {object} map[string]interface{}
// @Router /admin/tenants/{tenant_id}/products [get]
func (h *PlatformHandler) ListTenantProducts(w http.ResponseWriter, r *http.Request) {
	if h.subSvc == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "subscription service unavailable"})
		return
	}
	tenantID, err := uuid.Parse(chi.URLParam(r, "tenant_id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid tenant_id"})
		return
	}
	prods, err := h.subSvc.ListProducts(r.Context(), tenantID)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"products": []any{}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"products": prods})
}

// AssignProductToTenant godoc
// @Summary Assign a per-product subscription line to a tenant (admin)
// @Description Adds/activates a product line for a multi-use-case tenant. When planCode is
// supplied the line's features/limits are merged into the tenant's composite entitlements.
// @Tags Platform
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param tenant_id path string true "Tenant UUID"
// @Success 200 {object} map[string]interface{}
// @Router /admin/tenants/{tenant_id}/products [post]
func (h *PlatformHandler) AssignProductToTenant(w http.ResponseWriter, r *http.Request) {
	if h.subSvc == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "subscription service unavailable"})
		return
	}
	ctx := r.Context()
	tenantID, err := uuid.Parse(chi.URLParam(r, "tenant_id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid tenant_id"})
		return
	}
	var body struct {
		ProductCode string `json:"productCode"`
		PlanCode    string `json:"planCode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ProductCode == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "productCode is required"})
		return
	}
	if err := h.subSvc.AssignProductPlan(ctx, tenantID, body.ProductCode, body.PlanCode); err != nil {
		if subscriptions.IsExemptErr(err) {
			writeJSON(w, http.StatusOK, map[string]any{"is_bypass": true, "message": "tenant is exempt from subscriptions"})
			return
		}
		h.log.Error("failed to assign product plan", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if h.featureHandler != nil {
		h.featureHandler.InvalidateCache(ctx, tenantID)
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

// RemoveProductFromTenant godoc
// @Summary Remove a per-product subscription line from a tenant (admin)
// @Tags Platform
// @Produce json
// @Security BearerAuth
// @Param tenant_id path string true "Tenant UUID"
// @Param product_code path string true "Product code"
// @Success 200 {object} map[string]interface{}
// @Router /admin/tenants/{tenant_id}/products/{product_code} [delete]
func (h *PlatformHandler) RemoveProductFromTenant(w http.ResponseWriter, r *http.Request) {
	if h.subSvc == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "subscription service unavailable"})
		return
	}
	ctx := r.Context()
	tenantID, err := uuid.Parse(chi.URLParam(r, "tenant_id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid tenant_id"})
		return
	}
	productCode := chi.URLParam(r, "product_code")
	if productCode == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "product_code is required"})
		return
	}
	if err := h.subSvc.DeactivateProduct(ctx, tenantID, productCode); err != nil {
		h.log.Error("failed to remove product", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if h.featureHandler != nil {
		h.featureHandler.InvalidateCache(ctx, tenantID)
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

// ListTenantFeatureGrants godoc
// @Summary List a tenant's platform-admin add-on feature grants
// @Tags Platform
// @Produce json
// @Security BearerAuth
// @Param tenant_id path string true "Tenant UUID"
// @Success 200 {object} map[string]interface{}
// @Router /admin/tenants/{tenant_id}/feature-grants [get]
func (h *PlatformHandler) ListTenantFeatureGrants(w http.ResponseWriter, r *http.Request) {
	if h.subSvc == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "subscription service unavailable"})
		return
	}
	tenantID, err := uuid.Parse(chi.URLParam(r, "tenant_id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid tenant_id"})
		return
	}
	grants, err := h.subSvc.ListTenantFeatureGrants(r.Context(), tenantID)
	if err != nil {
		h.log.Error("failed to list feature grants", zap.Error(err))
		writeJSON(w, http.StatusOK, map[string]any{"grants": []any{}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"grants": grants})
}

// GrantTenantFeature godoc
// @Summary Grant a single named add-on feature to a tenant (platform admin)
// @Description Unlocks one feature_definitions code for a tenant independent of its
// @Description subscription plan (e.g. multi_branch_pricing, batch_period_pricing, flash_sale).
// @Description The tenant's own settings page still gates the actual on/off switch.
// @Tags Platform
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param tenant_id path string true "Tenant UUID"
// @Success 200 {object} map[string]interface{}
// @Router /admin/tenants/{tenant_id}/feature-grants [post]
func (h *PlatformHandler) GrantTenantFeature(w http.ResponseWriter, r *http.Request) {
	if h.subSvc == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "subscription service unavailable"})
		return
	}
	ctx := r.Context()
	tenantID, err := uuid.Parse(chi.URLParam(r, "tenant_id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid tenant_id"})
		return
	}
	var body struct {
		FeatureCode string `json:"featureCode"`
		Notes       string `json:"notes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.FeatureCode == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "featureCode is required"})
		return
	}
	var grantedBy uuid.UUID
	if claims, ok := authclient.ClaimsFromContext(ctx); ok && claims != nil {
		grantedBy, _ = claims.UserID()
	}
	if err := h.subSvc.GrantTenantFeature(ctx, tenantID, body.FeatureCode, grantedBy, body.Notes); err != nil {
		h.log.Error("failed to grant tenant feature", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if h.featureHandler != nil {
		h.featureHandler.InvalidateCache(ctx, tenantID)
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

// RevokeTenantFeature godoc
// @Summary Revoke a tenant's add-on feature grant (platform admin)
// @Tags Platform
// @Produce json
// @Security BearerAuth
// @Param tenant_id path string true "Tenant UUID"
// @Param feature_code path string true "Feature code"
// @Success 200 {object} map[string]interface{}
// @Router /admin/tenants/{tenant_id}/feature-grants/{feature_code} [delete]
func (h *PlatformHandler) RevokeTenantFeature(w http.ResponseWriter, r *http.Request) {
	if h.subSvc == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "subscription service unavailable"})
		return
	}
	ctx := r.Context()
	tenantID, err := uuid.Parse(chi.URLParam(r, "tenant_id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid tenant_id"})
		return
	}
	featureCode := chi.URLParam(r, "feature_code")
	if featureCode == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "feature_code is required"})
		return
	}
	var revokedBy uuid.UUID
	if claims, ok := authclient.ClaimsFromContext(ctx); ok && claims != nil {
		revokedBy, _ = claims.UserID()
	}
	if err := h.subSvc.RevokeTenantFeature(ctx, tenantID, featureCode, revokedBy); err != nil {
		h.log.Error("failed to revoke tenant feature", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if h.featureHandler != nil {
		h.featureHandler.InvalidateCache(ctx, tenantID)
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

// UpdateSubscriptionStatus godoc
// @Summary Update subscription status (admin)
// @Description Updates the status of a tenant subscription. Requires platform owner.
// @Tags Platform
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "Subscription UUID"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /admin/subscriptions/{id}/status [put]
func (h *PlatformHandler) UpdateSubscriptionStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}

	var body struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if body.Status == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "status is required"})
		return
	}

	sub, err := h.client.TenantSubscription.UpdateOneID(id).
		SetStatus(tenantsubscription.Status(body.Status)).
		Save(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "subscription not found"})
			return
		}
		h.log.Error("failed to update subscription status", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to update subscription"})
		return
	}

	if h.featureHandler != nil {
		h.featureHandler.InvalidateCache(ctx, sub.TenantID)
	}

	writeJSON(w, http.StatusOK, sub)
}

// ListTenants godoc
// @Summary List all tenants (admin)
// @Description Returns paginated tenants with their current subscription status. Supports search and status filter.
// @Tags Platform
// @Produce json
// @Security BearerAuth
// @Param page query int false "Page number (default 1)"
// @Param pageSize query int false "Page size (default 20, max 100)"
// @Param search query string false "Search by tenant name or slug"
// @Param status query string false "Filter by subscription status (ACTIVE, TRIAL, SUSPENDED, etc.)"
// @Success 200 {object} map[string]interface{}
// @Failure 500 {object} map[string]string
// @Router /admin/tenants [get]
func (h *PlatformHandler) ListTenants(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("pageSize"))
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	search := strings.TrimSpace(r.URL.Query().Get("search"))
	statusFilter := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("status")))

	query := h.client.Tenant.Query()
	if search != "" {
		query = query.Where(
			enttenant.Or(
				enttenant.NameContainsFold(search),
				enttenant.SlugContainsFold(search),
			),
		)
	}

	total, err := query.Clone().Count(ctx)
	if err != nil {
		h.log.Error("failed to count tenants", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	offset := (page - 1) * pageSize
	tenants, err := query.
		WithSubscriptions(func(q *ent.TenantSubscriptionQuery) {
			q.WithPlan()
		}).
		Offset(offset).
		Limit(pageSize).
		All(ctx)
	if err != nil {
		h.log.Error("failed to list tenants", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	type tenantRow struct {
		ID                 string `json:"id"`
		Name               string `json:"name"`
		Slug               string `json:"slug"`
		SubscriptionStatus string `json:"subscriptionStatus,omitempty"`
		SubscriptionID     string `json:"subscriptionId,omitempty"`
		PlanName           string `json:"planName,omitempty"`
		PlanCode           string `json:"planCode,omitempty"`
		CurrentPeriodEnd   string `json:"currentPeriodEnd,omitempty"`
		SubscriptionExempt bool   `json:"subscriptionExempt"`
	}

	data := make([]tenantRow, 0, len(tenants))
	for _, t := range tenants {
		exempt := t.SubscriptionExempt
		if !exempt && h.subSvc != nil {
			exempt = h.subSvc.IsExemptTenant(ctx, t.ID)
		}
		row := tenantRow{
			ID:                 t.ID.String(),
			Name:               t.Name,
			Slug:               t.Slug,
			SubscriptionExempt: exempt,
		}
		if len(t.Edges.Subscriptions) > 0 {
			sub := t.Edges.Subscriptions[0]
			subStatus := strings.ToUpper(string(sub.Status))
			// Apply status filter if provided
			if statusFilter != "" && subStatus != statusFilter {
				continue
			}
			row.SubscriptionStatus = subStatus
			row.SubscriptionID = sub.ID.String()
			if !sub.CurrentPeriodEnd.IsZero() {
				row.CurrentPeriodEnd = sub.CurrentPeriodEnd.Format("2006-01-02T15:04:05Z")
			}
			if sub.Edges.Plan != nil {
				row.PlanName = sub.Edges.Plan.Name
				row.PlanCode = sub.Edges.Plan.PlanCode
			}
		} else if statusFilter != "" {
			// Tenant has no subscription — skip if filtering by status
			continue
		}
		data = append(data, row)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"data":     data,
		"total":    total,
		"page":     page,
		"pageSize": pageSize,
	})
}

// SetTenantExemption godoc
// @Summary Grant or revoke a tenant's subscription exemption (platform admin)
// @Description Flips TenantSubscriptionExempt. An exempt tenant bypasses ALL subscription
// @Description gating platform-wide (every feature unlocked, no limits) without needing a real
// @Description subscription record — for platform-owner-approved cases outside the built-in
// @Description demo/platform-owner slugs (e.g. a sponsored/service-charge pilot tenant).
// @Tags Platform
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param tenant_id path string true "Tenant ID"
// @Param body body object{exempt=bool} true "Exemption flag"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /admin/tenants/{tenant_id}/exemption [patch]
func (h *PlatformHandler) SetTenantExemption(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	tenantID, ok := h.parseTenantParam(w, r)
	if !ok {
		return
	}

	var body struct {
		Exempt bool `json:"exempt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	if h.subSvc == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "subscription service unavailable"})
		return
	}
	if err := h.subSvc.SetTenantExemption(ctx, tenantID, body.Exempt); err != nil {
		h.log.Error("failed to set tenant exemption", zap.Error(err), zap.String("tenant_id", tenantID.String()))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"tenantId": tenantID.String(),
		"exempt":   body.Exempt,
	})
}

// ListServiceConfigs returns all platform-level (tenant_id = nil) service configs.
func (h *PlatformHandler) ListServiceConfigs(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	configs, err := h.client.ServiceConfig.Query().
		Where(serviceconfig.TenantIDIsNil()).
		Order(ent.Asc(serviceconfig.FieldConfigKey)).
		All(ctx)
	if err != nil {
		h.log.Error("failed to list service configs", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	type configRow struct {
		ID          string `json:"id"`
		ConfigKey   string `json:"configKey"`
		ConfigValue string `json:"configValue"`
		ConfigType  string `json:"configType"`
		Description string `json:"description"`
		IsSecret    bool   `json:"isSecret"`
		UpdatedAt   string `json:"updatedAt"`
	}

	data := make([]configRow, 0, len(configs))
	for _, c := range configs {
		val := c.ConfigValue
		if c.IsSecret {
			val = "••••••••"
		}
		data = append(data, configRow{
			ID:          c.ID.String(),
			ConfigKey:   c.ConfigKey,
			ConfigValue: val,
			ConfigType:  c.ConfigType,
			Description: c.Description,
			IsSecret:    c.IsSecret,
			UpdatedAt:   c.UpdatedAt.Format("2006-01-02T15:04:05Z"),
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{"data": data, "total": len(data)})
}

// CreateServiceConfig creates a new platform-level service config.
func (h *PlatformHandler) CreateServiceConfig(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body struct {
		ConfigKey   string `json:"configKey"`
		ConfigValue string `json:"configValue"`
		ConfigType  string `json:"configType"`
		Description string `json:"description"`
		IsSecret    bool   `json:"isSecret"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if body.ConfigKey == "" || body.ConfigValue == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "configKey and configValue are required"})
		return
	}
	if body.ConfigType == "" {
		body.ConfigType = "string"
	}

	id := uuid.NewSHA1(uuid.NameSpaceOID, []byte("sc::"+body.ConfigKey))
	cfg, err := h.client.ServiceConfig.Create().
		SetID(id).
		SetConfigKey(body.ConfigKey).
		SetConfigValue(body.ConfigValue).
		SetConfigType(body.ConfigType).
		SetDescription(body.Description).
		SetIsSecret(body.IsSecret).
		Save(ctx)
	if err != nil {
		h.log.Error("failed to create service config", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create config"})
		return
	}

	writeJSON(w, http.StatusCreated, cfg)
}

// UpdateServiceConfig updates an existing service config's value and metadata.
func (h *PlatformHandler) UpdateServiceConfig(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}

	var body struct {
		ConfigValue string `json:"configValue"`
		ConfigType  string `json:"configType"`
		Description string `json:"description"`
		IsSecret    bool   `json:"isSecret"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if body.ConfigValue == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "configValue is required"})
		return
	}

	upd := h.client.ServiceConfig.UpdateOneID(id).
		SetConfigValue(body.ConfigValue).
		SetDescription(body.Description).
		SetIsSecret(body.IsSecret)
	if body.ConfigType != "" {
		upd = upd.SetConfigType(body.ConfigType)
	}

	cfg, err := upd.Save(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "config not found"})
			return
		}
		h.log.Error("failed to update service config", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to update config"})
		return
	}

	writeJSON(w, http.StatusOK, cfg)
}

// UpdateSubscription godoc
// @Summary Update subscription dates / plan / status (admin)
// @Description Allows platform admin to set trial_ends_at, current_period_end, status, or plan_code
// on any tenant subscription regardless of its current state. At least one field must be supplied.
// Invalidates the tenant's feature cache on success.
// @Tags Platform
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "Subscription UUID"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /admin/subscriptions/{id} [put]
func (h *PlatformHandler) UpdateSubscription(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid subscription id"})
		return
	}

	var body struct {
		TrialEndsAt      *string `json:"trial_ends_at"`      // RFC3339 or null to clear
		CurrentPeriodEnd *string `json:"current_period_end"` // RFC3339
		Status           *string `json:"status"`             // ACTIVE | TRIAL | EXPIRED | CANCELLED | SUSPENDED
		PlanCode         *string `json:"plan_code"`          // switch plan
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if body.TrialEndsAt == nil && body.CurrentPeriodEnd == nil && body.Status == nil && body.PlanCode == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "at least one of trial_ends_at, current_period_end, status, plan_code is required"})
		return
	}

	sub, err := h.client.TenantSubscription.Get(ctx, id)
	if err != nil {
		if ent.IsNotFound(err) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "subscription not found"})
			return
		}
		h.log.Error("failed to get subscription", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	upd := h.client.TenantSubscription.UpdateOneID(id)

	if body.TrialEndsAt != nil {
		if *body.TrialEndsAt == "" {
			upd = upd.ClearTrialEndsAt()
		} else {
			t, parseErr := time.Parse(time.RFC3339, *body.TrialEndsAt)
			if parseErr != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "trial_ends_at must be RFC3339 (e.g. 2026-09-01T00:00:00Z) or empty string to clear"})
				return
			}
			upd = upd.SetTrialEndsAt(t)
		}
	}

	if body.CurrentPeriodEnd != nil {
		t, parseErr := time.Parse(time.RFC3339, *body.CurrentPeriodEnd)
		if parseErr != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "current_period_end must be RFC3339 (e.g. 2026-09-01T00:00:00Z)"})
			return
		}
		upd = upd.SetCurrentPeriodEnd(t)
	}

	if body.Status != nil {
		upd = upd.SetStatus(tenantsubscription.Status(*body.Status))
	}

	if body.PlanCode != nil {
		plan, planErr := h.client.SubscriptionPlan.Query().
			Where(subscriptionplan.PlanCode(*body.PlanCode)).
			Only(ctx)
		if planErr != nil {
			if ent.IsNotFound(planErr) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "plan not found: " + *body.PlanCode})
				return
			}
			h.log.Error("failed to find plan", zap.Error(planErr))
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}
		upd = upd.SetPlanID(plan.ID)
	}

	updated, err := upd.Save(ctx)
	if err != nil {
		h.log.Error("failed to update subscription", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to update subscription"})
		return
	}

	if h.featureHandler != nil {
		h.featureHandler.InvalidateCache(ctx, sub.TenantID)
	}

	h.log.Info("subscription updated by admin",
		zap.String("subscription_id", id.String()),
		zap.String("tenant_id", sub.TenantID.String()),
	)
	writeJSON(w, http.StatusOK, updated)
}

// derivePlanTier returns a human-readable tier label from the plan code or tier order.
func derivePlanTier(planCode string, tierOrder int) string {
	code := strings.ToLower(planCode)
	switch {
	case strings.Contains(code, "starter") || tierOrder == 1:
		return "starter"
	case strings.Contains(code, "growth") || tierOrder == 2:
		return "growth"
	case strings.Contains(code, "professional") || strings.Contains(code, "pro") || tierOrder == 3:
		return "professional"
	case strings.Contains(code, "enterprise") || tierOrder >= 4:
		return "enterprise"
	case strings.Contains(code, "custom"):
		return "custom"
	case strings.Contains(code, "free"):
		return "free"
	default:
		if tierOrder > 0 {
			return strconv.Itoa(tierOrder)
		}
		return strings.ToLower(planCode)
	}
}

// DeleteServiceConfig removes a platform-level service config.
func (h *PlatformHandler) DeleteServiceConfig(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}

	if err := h.client.ServiceConfig.DeleteOneID(id).Exec(ctx); err != nil {
		if ent.IsNotFound(err) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "config not found"})
			return
		}
		h.log.Error("failed to delete service config", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to delete config"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// --- Subscription invoices (platform owner) ---

func (h *PlatformHandler) parseTenantParam(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	if h.invoiceSvc == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "invoice service not configured"})
		return uuid.Nil, false
	}
	tenantID, err := uuid.Parse(chi.URLParam(r, "tenant_id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid tenant_id"})
		return uuid.Nil, false
	}
	return tenantID, true
}

// GenerateSubscriptionInvoice godoc
// @Summary Generate a subscription invoice for a tenant (platform owner)
// @Description Creates + emails a subscription invoice with a durable pay link. Use ?force=true to regenerate for the current period.
// @Tags Platform
// @Produce json
// @Security BearerAuth
// @Param tenant_id path string true "Tenant UUID"
// @Param force query bool false "Bypass idempotency and regenerate"
// @Success 201 {object} map[string]interface{}
// @Router /admin/tenants/{tenant_id}/subscription/generate-invoice [post]
func (h *PlatformHandler) GenerateSubscriptionInvoice(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := h.parseTenantParam(w, r)
	if !ok {
		return
	}
	force := r.URL.Query().Get("force") == "true"
	res, err := h.invoiceSvc.GenerateForTenant(r.Context(), tenantID, force)
	if err != nil {
		h.log.Error("manual invoice generation failed", zap.String("tenant_id", tenantID.String()), zap.Error(err))
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, res)
}

// ResendSubscriptionInvoice godoc
// @Summary Resend the latest subscription invoice email (platform owner)
// @Tags Platform
// @Produce json
// @Security BearerAuth
// @Param tenant_id path string true "Tenant UUID"
// @Success 200 {object} map[string]interface{}
// @Router /admin/tenants/{tenant_id}/subscription/invoice/resend [post]
func (h *PlatformHandler) ResendSubscriptionInvoice(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := h.parseTenantParam(w, r)
	if !ok {
		return
	}
	res, err := h.invoiceSvc.ResendLast(r.Context(), tenantID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// GetSubscriptionInvoice godoc
// @Summary Get the latest subscription invoice summary for a tenant (platform owner)
// @Tags Platform
// @Produce json
// @Security BearerAuth
// @Param tenant_id path string true "Tenant UUID"
// @Success 200 {object} map[string]interface{}
// @Router /admin/tenants/{tenant_id}/subscription/invoice [get]
func (h *PlatformHandler) GetSubscriptionInvoice(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := h.parseTenantParam(w, r)
	if !ok {
		return
	}
	res, err := h.invoiceSvc.LastInvoiceFor(r.Context(), tenantID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if res == nil {
		writeJSON(w, http.StatusOK, map[string]any{"invoice": nil})
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// DownloadSubscriptionInvoicePDF godoc
// @Summary Download the latest subscription invoice PDF (platform owner)
// @Description Proxies the treasury invoice PDF so the UI needs no treasury auth.
// @Tags Platform
// @Produce application/pdf
// @Security BearerAuth
// @Param tenant_id path string true "Tenant UUID"
// @Success 200 {file} binary
// @Router /admin/tenants/{tenant_id}/subscription/invoice/pdf [get]
func (h *PlatformHandler) DownloadSubscriptionInvoicePDF(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := h.parseTenantParam(w, r)
	if !ok {
		return
	}
	res, err := h.invoiceSvc.LastInvoiceFor(r.Context(), tenantID)
	if err != nil || res == nil || res.InvoiceID == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no invoice found for tenant"})
		return
	}
	data, err := h.invoiceSvc.FetchInvoicePDF(r.Context(), res.InvoiceID)
	if err != nil {
		h.log.Error("invoice pdf proxy failed", zap.String("tenant_id", tenantID.String()), zap.Error(err))
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "failed to fetch invoice pdf"})
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	disposition := "inline"
	if r.URL.Query().Get("download") == "true" {
		disposition = "attachment"
	}
	w.Header().Set("Content-Disposition", disposition+`; filename="`+res.InvoiceNumber+`.pdf"`)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}
