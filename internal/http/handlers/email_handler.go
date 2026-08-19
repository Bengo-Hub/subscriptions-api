package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/emaillicense"
	"github.com/bengobox/subscription-service/internal/ent/emailplan"
	"github.com/bengobox/subscription-service/internal/ent/tenantsubscription"
	"github.com/bengobox/subscription-service/internal/modules/subscriptions"
)

// EmailLicenseHandler serves the email-hosting license lifecycle: public plan
// listing, and tenant-scoped purchase/assign/unassign/upgrade. Publishes
// email.license.* events on every state change — email-provisioner is the
// consumer, per .claude/plans/codevertex-email-hosting-service-plan.md Part 3.
type EmailLicenseHandler struct {
	log    *zap.Logger
	client *ent.Client
	subSvc *subscriptions.Service
}

func NewEmailLicenseHandler(log *zap.Logger, client *ent.Client, subSvc *subscriptions.Service) *EmailLicenseHandler {
	return &EmailLicenseHandler{log: log.Named("email_license.handler"), client: client, subSvc: subSvc}
}

func (h *EmailLicenseHandler) tenantID(r *http.Request) (uuid.UUID, bool) {
	idStr := resolveTenantID(r)
	if idStr == "" {
		return uuid.Nil, false
	}
	id, err := uuid.Parse(idStr)
	return id, err == nil
}

// resolveTenantSubscriptionID maps a tenant's own ID (from the JWT) to its
// TenantSubscription.ID — the value EmailLicense.tenant_subscription_id
// actually stores. These are NOT the same UUID (TenantSubscription.ID is
// independently generated), so every tenant-scoped EmailLicense query must
// go through this resolution rather than filtering on the raw tenant ID
// directly — a real bug found and fixed in this pass: every handler below
// used to compare TenantSubscriptionIDEQ(tenantID), which never matched any
// real row after purchase.
func (h *EmailLicenseHandler) resolveTenantSubscriptionID(ctx context.Context, client *ent.Client, tenantID uuid.UUID) (uuid.UUID, error) {
	return client.TenantSubscription.Query().
		Where(tenantsubscription.TenantIDEQ(tenantID)).
		OnlyID(ctx)
}

// ListEmailPlans handles GET /api/v1/email/plans (public).
func (h *EmailLicenseHandler) ListEmailPlans(w http.ResponseWriter, r *http.Request) {
	plans, err := h.client.EmailPlan.Query().
		Where(emailplan.IsActive(true), emailplan.IsPublic(true)).
		Order(ent.Asc(emailplan.FieldSortOrder)).
		All(r.Context())
	if err != nil {
		h.log.Error("list email plans failed", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, plans)
}

// ListEmailLicenses handles GET /api/v1/email/licenses — the caller's own tenant only.
func (h *EmailLicenseHandler) ListEmailLicenses(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := h.tenantID(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "tenant context required"})
		return
	}

	ctx := r.Context()
	tenantSubID, err := h.resolveTenantSubscriptionID(ctx, h.client, tenantID)
	if err != nil {
		writeJSON(w, http.StatusOK, []any{}) // no subscription yet = no licenses, not an error
		return
	}

	licenses, err := h.client.EmailLicense.Query().
		Where(emaillicense.TenantSubscriptionIDEQ(tenantSubID)).
		WithEmailPlan().
		All(ctx)
	if err != nil {
		h.log.Error("list email licenses failed", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, licenses)
}

type purchaseLicensesInput struct {
	PlanCode  string `json:"plan_code"`
	Quantity  int    `json:"quantity"`
	ReturnURL string `json:"return_url"`
}

// PurchaseEmailLicenses handles POST /api/v1/email/licenses/purchase.
// Buying N seats is a discrete, one-off paid action — creates a Treasury
// payment intent (mirroring AddonHandler.PurchaseAddon's pattern) and
// returns payment_required + the intent; licenses are only actually
// provisioned once payment completes, via
// Service.FulfillEmailLicensePurchase, called from the Treasury webhook/NATS
// consumer (webhook.go / payment_consumer.go), never from this handler.
func (h *EmailLicenseHandler) PurchaseEmailLicenses(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := h.tenantID(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "tenant context required"})
		return
	}

	var in purchaseLicensesInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if in.Quantity <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "quantity must be positive"})
		return
	}

	ctx := r.Context()

	// Plan-level gate: does this tenant's subscription entitle them to the
	// email-hosting product at all. Fails open (Entitled=true) if no
	// FeatureDefinition is tagged service_tag="email-hosting" — a seed-data
	// decision, not a code gap; see product_activation.go.
	entitlement, err := h.subSvc.IsEntitledToProduct(ctx, tenantID, "email-hosting")
	if err != nil {
		h.log.Error("email-hosting entitlement check failed", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	if !entitlement.Entitled {
		writeJSON(w, http.StatusForbidden, entitlement)
		return
	}

	treasuryResp, err := h.subSvc.CreateEmailLicensePurchaseIntent(ctx, subscriptions.EmailLicensePurchaseInput{
		TenantID:  tenantID,
		PlanCode:  in.PlanCode,
		Quantity:  in.Quantity,
		ReturnURL: in.ReturnURL,
	})
	if err != nil {
		h.log.Error("create email license purchase intent failed", zap.String("tenant_id", tenantID.String()), zap.Error(err))
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":    "payment_required",
		"plan_code": in.PlanCode,
		"quantity":  in.Quantity,
		"intent":    treasuryResp,
	})
}

type assignLicenseInput struct {
	Email string `json:"email"`
	// NotifyEmail is the tenant admin's own already-reachable contact
	// address — NOT the new mailbox address, which its owner can't read
	// yet. Stored in EmailLicense.metadata (existing JSON field, no
	// migration needed) and forwarded to email-provisioner via the
	// license.assigned event so it can deliver a "choose your password"
	// link there instead of to the mailbox it's meant to unlock.
	NotifyEmail string `json:"notify_email"`
}

// AssignEmailLicense handles PUT /api/v1/email/licenses/{id}/assign.
func (h *EmailLicenseHandler) AssignEmailLicense(w http.ResponseWriter, r *http.Request) {
	h.transition(w, r, "ASSIGNED", func(ctx *ent.EmailLicenseUpdateOne, in assignLicenseInput) *ent.EmailLicenseUpdateOne {
		return ctx.SetAssignedToEmail(in.Email).SetStatus("ASSIGNED")
	})
}

// UnassignEmailLicense handles PUT /api/v1/email/licenses/{id}/unassign.
func (h *EmailLicenseHandler) UnassignEmailLicense(w http.ResponseWriter, r *http.Request) {
	licenseID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid license id"})
		return
	}
	tenantID, ok := h.tenantID(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "tenant context required"})
		return
	}

	ctx := r.Context()
	tx, err := h.client.Tx(ctx)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	tenantSubID, err := h.resolveTenantSubscriptionID(ctx, tx.Client(), tenantID)
	if err != nil {
		_ = tx.Rollback()
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "license not found"})
		return
	}

	lic, err := tx.EmailLicense.Query().
		Where(emaillicense.ID(licenseID), emaillicense.TenantSubscriptionIDEQ(tenantSubID)).
		Only(ctx)
	if err != nil {
		_ = tx.Rollback()
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "license not found"})
		return
	}

	previousEmail := lic.AssignedToEmail
	updated, err := tx.EmailLicense.UpdateOneID(licenseID).
		ClearAssignedToEmail().
		SetStatus("AVAILABLE").
		Save(ctx)
	if err != nil {
		_ = tx.Rollback()
		h.log.Error("unassign email license failed", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	h.subSvc.WriteOutboxEventPublic(ctx, tx, tenantID, "email", licenseID, "license.unassigned", map[string]any{
		"license_id":        licenseID.String(),
		"assigned_to_email": previousEmail,
	})

	if err := tx.Commit(); err != nil {
		h.log.Error("commit unassign email license failed", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

type upgradeLicenseInput struct {
	PlanCode string `json:"plan_code"`
}

// UpgradeEmailLicense handles PUT /api/v1/email/licenses/{id}/upgrade.
// Moves the license to a different EmailPlan tier — re-denormalizes
// storage_quota_gb/features_json from the new plan (mirrors PurchaseEmailLicenses'
// denormalization at creation time), leaves status/assignment untouched.
func (h *EmailLicenseHandler) UpgradeEmailLicense(w http.ResponseWriter, r *http.Request) {
	licenseID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid license id"})
		return
	}
	tenantID, ok := h.tenantID(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "tenant context required"})
		return
	}

	var in upgradeLicenseInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.PlanCode == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "plan_code is required"})
		return
	}

	ctx := r.Context()
	tx, err := h.client.Tx(ctx)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	tenantSubID, err := h.resolveTenantSubscriptionID(ctx, tx.Client(), tenantID)
	if err != nil {
		_ = tx.Rollback()
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "license not found"})
		return
	}

	lic, err := tx.EmailLicense.Query().
		Where(emaillicense.ID(licenseID), emaillicense.TenantSubscriptionIDEQ(tenantSubID)).
		Only(ctx)
	if err != nil {
		_ = tx.Rollback()
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "license not found"})
		return
	}

	newPlan, err := tx.EmailPlan.Query().
		Where(emailplan.CodeEQ(in.PlanCode), emailplan.IsActive(true)).
		Only(ctx)
	if err != nil {
		_ = tx.Rollback()
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("unknown or inactive plan_code %q", in.PlanCode)})
		return
	}

	updated, err := tx.EmailLicense.UpdateOneID(licenseID).
		SetEmailPlanID(newPlan.ID).
		SetStorageQuotaGB(newPlan.StoragePerUserGB).
		SetFeaturesJSON(newPlan.FeaturesJSON).
		Save(ctx)
	if err != nil {
		_ = tx.Rollback()
		h.log.Error("upgrade email license failed", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	h.subSvc.WriteOutboxEventPublic(ctx, tx, tenantID, "email", licenseID, "license.upgraded", map[string]any{
		"license_id":        licenseID.String(),
		"assigned_to_email": lic.AssignedToEmail,
		"domain":            "codevertexafrica.com",
		"storage_quota_gb":  updated.StorageQuotaGB,
		"plan_code":         newPlan.Code,
	})

	if err := tx.Commit(); err != nil {
		h.log.Error("commit upgrade email license failed", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

type suspendLicenseInput struct {
	Reason string `json:"reason"`
}

// SuspendEmailLicense handles PUT /api/v1/email/licenses/{id}/suspend.
// Triggered today by direct API call only — plan Part 6's abuse-response
// ladder (bounce/complaint-threshold automation in email-provisioner) and
// billing non-payment are the two intended real callers, neither of which
// exists yet (see plan §13.2); this closes the subscriptions-api half of
// that gap so email-provisioner's already-built license.suspended consumer
// stops being unreachable. Never touches inbound delivery/mailbox read —
// that's enforced downstream in email-provisioner's Stalwart client, not here.
func (h *EmailLicenseHandler) SuspendEmailLicense(w http.ResponseWriter, r *http.Request) {
	licenseID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid license id"})
		return
	}
	tenantID, ok := h.tenantID(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "tenant context required"})
		return
	}

	var in suspendLicenseInput
	_ = json.NewDecoder(r.Body).Decode(&in)
	if in.Reason == "" {
		in.Reason = "manual"
	}

	ctx := r.Context()
	tx, err := h.client.Tx(ctx)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	tenantSubID, err := h.resolveTenantSubscriptionID(ctx, tx.Client(), tenantID)
	if err != nil {
		_ = tx.Rollback()
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "license not found"})
		return
	}

	lic, err := tx.EmailLicense.Query().
		Where(emaillicense.ID(licenseID), emaillicense.TenantSubscriptionIDEQ(tenantSubID)).
		Only(ctx)
	if err != nil {
		_ = tx.Rollback()
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "license not found"})
		return
	}

	updated, err := tx.EmailLicense.UpdateOneID(licenseID).
		SetStatus("SUSPENDED").
		SetSuspendReason(in.Reason).
		Save(ctx)
	if err != nil {
		_ = tx.Rollback()
		h.log.Error("suspend email license failed", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	h.subSvc.WriteOutboxEventPublic(ctx, tx, tenantID, "email", licenseID, "license.suspended", map[string]any{
		"license_id":        licenseID.String(),
		"assigned_to_email": lic.AssignedToEmail,
		"domain":            "codevertexafrica.com",
		"suspend_reason":    in.Reason,
	})

	if err := tx.Commit(); err != nil {
		h.log.Error("commit suspend email license failed", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// ExpireEmailLicense handles PUT /api/v1/email/licenses/{id}/expire.
// Manual/explicit trigger only — this closes the "can this license be moved
// to EXPIRED and does email-provisioner react correctly" gap, but automatic
// detection (a periodic scan for expires_at < now()) is separate, unbuilt
// work, not implied by wiring this transition (plan §13.2).
func (h *EmailLicenseHandler) ExpireEmailLicense(w http.ResponseWriter, r *http.Request) {
	licenseID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid license id"})
		return
	}
	tenantID, ok := h.tenantID(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "tenant context required"})
		return
	}

	ctx := r.Context()
	tx, err := h.client.Tx(ctx)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	tenantSubID, err := h.resolveTenantSubscriptionID(ctx, tx.Client(), tenantID)
	if err != nil {
		_ = tx.Rollback()
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "license not found"})
		return
	}

	lic, err := tx.EmailLicense.Query().
		Where(emaillicense.ID(licenseID), emaillicense.TenantSubscriptionIDEQ(tenantSubID)).
		Only(ctx)
	if err != nil {
		_ = tx.Rollback()
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "license not found"})
		return
	}

	updated, err := tx.EmailLicense.UpdateOneID(licenseID).
		SetStatus("EXPIRED").
		Save(ctx)
	if err != nil {
		_ = tx.Rollback()
		h.log.Error("expire email license failed", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	h.subSvc.WriteOutboxEventPublic(ctx, tx, tenantID, "email", licenseID, "license.expired", map[string]any{
		"license_id":        licenseID.String(),
		"assigned_to_email": lic.AssignedToEmail,
		"domain":            "codevertexafrica.com",
	})

	if err := tx.Commit(); err != nil {
		h.log.Error("commit expire email license failed", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// transition is the shared assign/upgrade path: loads the license (tenant-scoped),
// applies the given mutation, publishes the matching email.license.* event, commits.
func (h *EmailLicenseHandler) transition(w http.ResponseWriter, r *http.Request, eventSuffix string, mutate func(*ent.EmailLicenseUpdateOne, assignLicenseInput) *ent.EmailLicenseUpdateOne) {
	licenseID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid license id"})
		return
	}
	tenantID, ok := h.tenantID(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "tenant context required"})
		return
	}

	var in assignLicenseInput
	_ = json.NewDecoder(r.Body).Decode(&in)

	ctx := r.Context()
	tx, err := h.client.Tx(ctx)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	tenantSubID, err := h.resolveTenantSubscriptionID(ctx, tx.Client(), tenantID)
	if err != nil {
		_ = tx.Rollback()
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "license not found"})
		return
	}

	lic, err := tx.EmailLicense.Query().
		Where(emaillicense.ID(licenseID), emaillicense.TenantSubscriptionIDEQ(tenantSubID)).
		WithEmailPlan().
		Only(ctx)
	if err != nil {
		_ = tx.Rollback()
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "license not found"})
		return
	}

	updated, err := mutate(tx.EmailLicense.UpdateOneID(licenseID), in).Save(ctx)
	if err != nil {
		_ = tx.Rollback()
		h.log.Error("email license transition failed", zap.String("event", eventSuffix), zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	if in.NotifyEmail != "" {
		meta := map[string]any{}
		for k, v := range updated.Metadata {
			meta[k] = v
		}
		meta["notify_email"] = in.NotifyEmail
		updated, err = tx.EmailLicense.UpdateOneID(licenseID).SetMetadata(meta).Save(ctx)
		if err != nil {
			_ = tx.Rollback()
			h.log.Error("store notify_email on email license failed", zap.Error(err))
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}
	}

	domain := "codevertexafrica.com"
	if in.Email == "" && updated.AssignedToEmail != nil {
		in.Email = *updated.AssignedToEmail
	}
	h.subSvc.WriteOutboxEventPublic(ctx, tx, tenantID, "email", licenseID, "license."+eventSuffix, map[string]any{
		"license_id":        licenseID.String(),
		"assigned_to_email": in.Email,
		"domain":            domain,
		"storage_quota_gb":  updated.StorageQuotaGB,
		"suspend_reason":    updated.SuspendReason,
		"notify_email":      in.NotifyEmail,
	})

	if err := tx.Commit(); err != nil {
		h.log.Error("commit email license transition failed", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	_ = lic // used above only for the tenant-scoped existence check
	writeJSON(w, http.StatusOK, updated)
}

// RegisterEmailRoutes registers the email-hosting license routes.
func (h *EmailLicenseHandler) RegisterEmailRoutes(r chi.Router) {
	r.Get("/email/plans", h.ListEmailPlans)
	r.Get("/email/licenses", h.ListEmailLicenses)
	r.Post("/email/licenses/purchase", h.PurchaseEmailLicenses)
	r.Put("/email/licenses/{id}/assign", h.AssignEmailLicense)
	r.Put("/email/licenses/{id}/unassign", h.UnassignEmailLicense)
	r.Put("/email/licenses/{id}/upgrade", h.UpgradeEmailLicense)
	r.Put("/email/licenses/{id}/suspend", h.SuspendEmailLicense)
	r.Put("/email/licenses/{id}/expire", h.ExpireEmailLicense)
}
