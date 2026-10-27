package handlers

import (
	"encoding/json"
	"errors"
	"net/http"

	httpware "github.com/Bengo-Hub/httpware"
	authclient "github.com/Bengo-Hub/shared-auth-client"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/bengobox/subscription-service/internal/modules/subscriptions"
)

// Upgrade trials and scheduled plan changes. Tenant routes act on the caller's own tenant;
// /admin routes are platform owner only (router) and take the tenant from the path.

// actingUserID is the signed-in user's id, or "" for service calls.
func actingUserID(r *http.Request) string {
	if claims, ok := authclient.ClaimsFromContext(r.Context()); ok && claims != nil {
		if uid, err := claims.UserID(); err == nil {
			return uid.String()
		}
	}
	return ""
}

// callerTenantID resolves the tenant a tenant-scoped route acts on.
func callerTenantID(r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(resolveTenantID(r))
	return id, err == nil && id != uuid.Nil
}

func pathTenantID(r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "tenant_id"))
	return id, err == nil && id != uuid.Nil
}

// respondPlanScheduleError maps upgrade trial and plan change errors to HTTP responses.
func (h *SubscriptionHandler) respondPlanScheduleError(w http.ResponseWriter, tenantID uuid.UUID, err error) {
	if te, ok := subscriptions.AsUpgradeTrialError(err); ok {
		h.respondWithJSON(w, http.StatusConflict, map[string]string{"error": te.Message, "reason": te.Reason})
		return
	}
	if subscriptions.IsExemptErr(err) {
		h.respondWithJSON(w, http.StatusOK, demoBypasResponse(tenantID.String()))
		return
	}
	if errors.Is(err, subscriptions.ErrConcurrentUpdate) {
		h.respondWithError(w, http.StatusConflict, err.Error())
		return
	}
	h.log.Error("upgrade trial request failed", zap.String("tenant_id", tenantID.String()), zap.Error(err))
	h.respondWithError(w, http.StatusBadRequest, err.Error())
}

// tenantPolicyView hides how the policy was configured from tenants; they see the outcome only.
func tenantPolicyView(st *subscriptions.UpgradeTrialStatus) {
	st.Policy.Platform = subscriptions.UpgradeTrialSetting{}
	st.Policy.Tenant = subscriptions.UpgradeTrialSetting{}
	st.Policy.EnabledSource, st.Policy.DaysSource = "", ""
}

// GetUpgradeTrial godoc
// @Summary Upgrade trial status
// @Description Whether upgrade trials are on for the tenant, any running trial or scheduled plan
// @Description change, past trials and, with plan_code, whether a trial of that plan can start.
// @Tags Subscriptions
// @Produce json
// @Security BearerAuth
// @Param plan_code query string false "Plan to check eligibility for"
// @Success 200 {object} subscriptions.UpgradeTrialStatus
// @Router /subscription/upgrade-trial [get]
func (h *SubscriptionHandler) GetUpgradeTrial(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := callerTenantID(r)
	if !ok {
		h.respondWithError(w, http.StatusBadRequest, "tenant_id required")
		return
	}
	isPO := httpware.IsPlatformOwner(r.Context())
	st, err := h.service.UpgradeTrialStatusFor(r.Context(), tenantID, r.URL.Query().Get("plan_code"), isPO)
	if err != nil {
		h.respondPlanScheduleError(w, tenantID, err)
		return
	}
	if !isPO {
		tenantPolicyView(st)
	}
	h.respondWithJSON(w, http.StatusOK, st)
}

// StartUpgradeTrial godoc
// @Summary Start an upgrade trial
// @Description Gives the tenant a higher tier of its current product for the configured number of
// @Description days and schedules the upgrade for the first renewal date after the trial ends.
// @Tags Subscriptions
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body object true "{plan_code}"
// @Success 200 {object} subscriptions.SubscriptionResult
// @Failure 409 {object} map[string]string
// @Router /subscription/upgrade-trial [post]
func (h *SubscriptionHandler) StartUpgradeTrial(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := callerTenantID(r)
	if !ok {
		h.respondWithError(w, http.StatusBadRequest, "tenant_id required")
		return
	}
	var body struct {
		PlanCode string `json:"plan_code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.PlanCode == "" {
		h.respondWithError(w, http.StatusBadRequest, "plan_code is required")
		return
	}
	h.startTrial(w, r, tenantID, body.PlanCode, 0, false)
}

func (h *SubscriptionHandler) startTrial(w http.ResponseWriter, r *http.Request, tenantID uuid.UUID, planCode string, days int, byPlatform bool) {
	res, err := h.service.StartUpgradeTrial(r.Context(), subscriptions.StartUpgradeTrialInput{
		TenantID: tenantID, PlanCode: planCode, Days: days, ByPlatform: byPlatform, StartedBy: actingUserID(r),
	})
	if err != nil {
		h.respondPlanScheduleError(w, tenantID, err)
		return
	}
	h.invalidate(r, tenantID)
	h.respondWithJSON(w, http.StatusOK, res)
}

// EndUpgradeTrial godoc
// @Summary End the running upgrade trial
// @Description Ends the trial now. The tenant stays on the plan it pays for and the upgrade the
// @Description trial scheduled is cancelled.
// @Tags Subscriptions
// @Produce json
// @Security BearerAuth
// @Success 200 {object} subscriptions.SubscriptionResult
// @Failure 409 {object} map[string]string
// @Router /subscription/upgrade-trial [delete]
func (h *SubscriptionHandler) EndUpgradeTrial(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := callerTenantID(r)
	if !ok {
		h.respondWithError(w, http.StatusBadRequest, "tenant_id required")
		return
	}
	h.endTrial(w, r, tenantID, false)
}

func (h *SubscriptionHandler) endTrial(w http.ResponseWriter, r *http.Request, tenantID uuid.UUID, byPlatform bool) {
	res, err := h.service.EndUpgradeTrial(r.Context(), tenantID, byPlatform)
	if err != nil {
		h.respondPlanScheduleError(w, tenantID, err)
		return
	}
	h.invalidate(r, tenantID)
	h.respondWithJSON(w, http.StatusOK, res)
}

// CancelPlanChange godoc
// @Summary Cancel the scheduled plan change
// @Description Drops the plan change waiting for the next renewal date.
// @Tags Subscriptions
// @Produce json
// @Security BearerAuth
// @Success 200 {object} subscriptions.SubscriptionResult
// @Failure 409 {object} map[string]string
// @Router /subscription/plan-change [delete]
func (h *SubscriptionHandler) CancelPlanChange(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := callerTenantID(r)
	if !ok {
		h.respondWithError(w, http.StatusBadRequest, "tenant_id required")
		return
	}
	h.cancelPlanChange(w, r, tenantID)
}

func (h *SubscriptionHandler) cancelPlanChange(w http.ResponseWriter, r *http.Request, tenantID uuid.UUID) {
	res, err := h.service.CancelScheduledPlanChange(r.Context(), tenantID)
	if err != nil {
		h.respondPlanScheduleError(w, tenantID, err)
		return
	}
	h.invalidate(r, tenantID)
	h.respondWithJSON(w, http.StatusOK, res)
}

func (h *SubscriptionHandler) invalidate(r *http.Request, tenantID uuid.UUID) {
	if h.featureHandler != nil {
		h.featureHandler.InvalidateCache(r.Context(), tenantID)
	}
}

// GetUpgradeTrialSettings godoc
// @Summary Platform upgrade trial settings
// @Description The platform-wide upgrade trial switch and length (platform owner).
// @Tags Platform
// @Produce json
// @Security BearerAuth
// @Success 200 {object} subscriptions.UpgradeTrialPolicy
// @Router /admin/upgrade-trial/settings [get]
func (h *SubscriptionHandler) GetUpgradeTrialSettings(w http.ResponseWriter, r *http.Request) {
	p, err := h.service.UpgradeTrialPolicyFor(r.Context(), uuid.Nil)
	if err != nil {
		h.respondWithError(w, http.StatusInternalServerError, err.Error())
		return
	}
	h.respondWithJSON(w, http.StatusOK, p)
}

// PutUpgradeTrialSettings godoc
// @Summary Set platform upgrade trial settings
// @Description Turns upgrade trials on or off for every tenant without its own setting, and sets
// @Description the default trial length in days (1 to 90). Platform owner.
// @Tags Platform
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body object true "{enabled, days}"
// @Success 200 {object} subscriptions.UpgradeTrialPolicy
// @Failure 400 {object} map[string]string
// @Router /admin/upgrade-trial/settings [put]
func (h *SubscriptionHandler) PutUpgradeTrialSettings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled *bool `json:"enabled"`
		Days    *int  `json:"days"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Enabled == nil || body.Days == nil {
		h.respondWithError(w, http.StatusBadRequest, "enabled and days are required")
		return
	}
	p, err := h.service.SetPlatformUpgradeTrialPolicy(r.Context(), *body.Enabled, *body.Days)
	if err != nil {
		h.respondWithError(w, http.StatusBadRequest, err.Error())
		return
	}
	h.log.Info("platform upgrade trial settings changed", zap.Bool("enabled", *body.Enabled), zap.Int("days", *body.Days), zap.String("by", actingUserID(r)))
	h.respondWithJSON(w, http.StatusOK, p)
}

// AdminGetTenantUpgradeTrial godoc
// @Summary Tenant upgrade trial status (admin)
// @Description The tenant's policy with both levels, running trial, scheduled change, history and,
// @Description with plan_code, eligibility as the platform owner would grant it.
// @Tags Platform
// @Produce json
// @Security BearerAuth
// @Param tenant_id path string true "Tenant UUID"
// @Param plan_code query string false "Plan to check"
// @Success 200 {object} subscriptions.UpgradeTrialStatus
// @Router /admin/tenants/{tenant_id}/upgrade-trial [get]
func (h *SubscriptionHandler) AdminGetTenantUpgradeTrial(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := pathTenantID(r)
	if !ok {
		h.respondWithError(w, http.StatusBadRequest, "invalid tenant_id")
		return
	}
	st, err := h.service.UpgradeTrialStatusFor(r.Context(), tenantID, r.URL.Query().Get("plan_code"), true)
	if err != nil {
		h.respondPlanScheduleError(w, tenantID, err)
		return
	}
	h.respondWithJSON(w, http.StatusOK, st)
}

// AdminPutTenantUpgradeTrialPolicy godoc
// @Summary Set a tenant's upgrade trial override (admin)
// @Description enabled and days override the platform setting for this tenant; null (or absent)
// @Description removes that override so the tenant follows the platform setting.
// @Tags Platform
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param tenant_id path string true "Tenant UUID"
// @Param body body object true "{enabled: bool|null, days: int|null}"
// @Success 200 {object} subscriptions.UpgradeTrialPolicy
// @Failure 400 {object} map[string]string
// @Router /admin/tenants/{tenant_id}/upgrade-trial/policy [put]
func (h *SubscriptionHandler) AdminPutTenantUpgradeTrialPolicy(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := pathTenantID(r)
	if !ok {
		h.respondWithError(w, http.StatusBadRequest, "invalid tenant_id")
		return
	}
	var body struct {
		Enabled *bool `json:"enabled"`
		Days    *int  `json:"days"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		h.respondWithError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	p, err := h.service.SetTenantUpgradeTrialPolicy(r.Context(), tenantID, subscriptions.UpgradeTrialSetting{Enabled: body.Enabled, Days: body.Days})
	if err != nil {
		h.respondWithError(w, http.StatusBadRequest, err.Error())
		return
	}
	h.log.Info("tenant upgrade trial override changed", zap.String("tenant_id", tenantID.String()), zap.String("by", actingUserID(r)))
	h.respondWithJSON(w, http.StatusOK, p)
}

// AdminStartUpgradeTrial godoc
// @Summary Grant an upgrade trial (admin)
// @Description Starts a trial of plan_code for the tenant, regardless of the policy switch or an
// @Description earlier trial of the same plan. days (1 to 90) overrides the configured length.
// @Tags Platform
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param tenant_id path string true "Tenant UUID"
// @Param body body object true "{plan_code, days}"
// @Success 200 {object} subscriptions.SubscriptionResult
// @Failure 409 {object} map[string]string
// @Router /admin/tenants/{tenant_id}/upgrade-trial [post]
func (h *SubscriptionHandler) AdminStartUpgradeTrial(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := pathTenantID(r)
	if !ok {
		h.respondWithError(w, http.StatusBadRequest, "invalid tenant_id")
		return
	}
	var body struct {
		PlanCode string `json:"plan_code"`
		Days     int    `json:"days"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.PlanCode == "" {
		h.respondWithError(w, http.StatusBadRequest, "plan_code is required")
		return
	}
	h.startTrial(w, r, tenantID, body.PlanCode, body.Days, true)
}

// AdminEndUpgradeTrial godoc
// @Summary End a tenant's upgrade trial (admin)
// @Description Ends the running trial now; the tenant stays on its paid plan.
// @Tags Platform
// @Produce json
// @Security BearerAuth
// @Param tenant_id path string true "Tenant UUID"
// @Success 200 {object} subscriptions.SubscriptionResult
// @Failure 409 {object} map[string]string
// @Router /admin/tenants/{tenant_id}/upgrade-trial [delete]
func (h *SubscriptionHandler) AdminEndUpgradeTrial(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := pathTenantID(r)
	if !ok {
		h.respondWithError(w, http.StatusBadRequest, "invalid tenant_id")
		return
	}
	h.endTrial(w, r, tenantID, true)
}

// AdminApplyPlanChange godoc
// @Summary Apply a scheduled plan change now (admin)
// @Description Moves the tenant to the scheduled plan today instead of on the renewal date.
// @Tags Platform
// @Produce json
// @Security BearerAuth
// @Param tenant_id path string true "Tenant UUID"
// @Success 200 {object} subscriptions.SubscriptionResult
// @Failure 409 {object} map[string]string
// @Router /admin/tenants/{tenant_id}/plan-change/apply [post]
func (h *SubscriptionHandler) AdminApplyPlanChange(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := pathTenantID(r)
	if !ok {
		h.respondWithError(w, http.StatusBadRequest, "invalid tenant_id")
		return
	}
	res, err := h.service.ApplyScheduledPlanChangeNow(r.Context(), tenantID)
	if err != nil {
		h.respondPlanScheduleError(w, tenantID, err)
		return
	}
	h.log.Info("scheduled plan change applied early by admin", zap.String("tenant_id", tenantID.String()), zap.String("by", actingUserID(r)))
	h.invalidate(r, tenantID)
	h.respondWithJSON(w, http.StatusOK, res)
}

// AdminCancelPlanChange godoc
// @Summary Cancel a tenant's scheduled plan change (admin)
// @Tags Platform
// @Produce json
// @Security BearerAuth
// @Param tenant_id path string true "Tenant UUID"
// @Success 200 {object} subscriptions.SubscriptionResult
// @Failure 409 {object} map[string]string
// @Router /admin/tenants/{tenant_id}/plan-change [delete]
func (h *SubscriptionHandler) AdminCancelPlanChange(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := pathTenantID(r)
	if !ok {
		h.respondWithError(w, http.StatusBadRequest, "invalid tenant_id")
		return
	}
	h.cancelPlanChange(w, r, tenantID)
}
