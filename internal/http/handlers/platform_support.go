package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Bengo-Hub/pagination"
	authclient "github.com/Bengo-Hub/shared-auth-client"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	entsql "entgo.io/ent/dialect/sql"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/predicate"
	"github.com/bengobox/subscription-service/internal/ent/supportagreement"
	"github.com/bengobox/subscription-service/internal/ent/supportfeecycle"
	"github.com/bengobox/subscription-service/internal/ent/tenant"
	"github.com/bengobox/subscription-service/internal/ent/tenantsubscription"
	"github.com/bengobox/subscription-service/internal/modules/subscriptions"
)

// Platform-owner management of support agreements (standard hosting and support fee of one-time
// license tenants, plus tenant-specific special support) and their billing cycles. The business
// rules live in subscriptions/support_agreements.go; these handlers only translate HTTP.

// recentCyclesPerAgreement bounds the cycle history embedded in the agreement list. The full
// history is paginated through GET /admin/support-fee-cycles?agreement_id=...
const recentCyclesPerAgreement = 12

type supportCycleDTO struct {
	ID             uuid.UUID  `json:"id"`
	TenantID       uuid.UUID  `json:"tenant_id"`
	TenantName     string     `json:"tenant_name,omitempty"`
	AgreementID    *uuid.UUID `json:"agreement_id,omitempty"`
	AgreementName  string     `json:"agreement_name,omitempty"`
	AgreementKind  string     `json:"agreement_kind,omitempty"`
	CycleNumber    int        `json:"cycle_number"`
	PeriodStart    time.Time  `json:"period_start"`
	PeriodEnd      *time.Time `json:"period_end,omitempty"`
	DueDate        time.Time  `json:"due_date"`
	Status         string     `json:"status"`
	PaidAt         *time.Time `json:"paid_at,omitempty"`
	GraceUntil     *time.Time `json:"grace_until,omitempty"`
	BasePrice      float64    `json:"base_price"`
	CustomPrice    *float64   `json:"custom_price,omitempty"`
	CustomReason   *string    `json:"custom_price_reason,omitempty"`
	EffectivePrice float64    `json:"effective_price"`
	InvoiceNumber  string     `json:"invoice_number,omitempty"`
	InvoiceTotal   any        `json:"invoice_total,omitempty"`
	PayURL         string     `json:"pay_url,omitempty"`
	PDFURL         string     `json:"pdf_url,omitempty"`
	Blocking       bool       `json:"blocking"`
}

func toSupportCycleDTO(c *ent.SupportFeeCycle, now time.Time) supportCycleDTO {
	dto := supportCycleDTO{
		ID:             c.ID,
		TenantID:       c.TenantID,
		AgreementID:    c.AgreementID,
		CycleNumber:    c.CycleNumber,
		PeriodStart:    c.PeriodStart,
		PeriodEnd:      c.PeriodEnd,
		DueDate:        c.DueDate,
		Status:         string(c.Status),
		PaidAt:         c.PaidAt,
		GraceUntil:     c.GraceUntil,
		BasePrice:      c.BasePrice,
		CustomPrice:    c.CustomPrice,
		CustomReason:   c.CustomPriceReason,
		EffectivePrice: subscriptions.EffectiveSupportPrice(c),
		InvoiceNumber:  stringMetaValue(c.Metadata, "last_invoice_number"),
		InvoiceTotal:   c.Metadata["last_invoice_total"],
		PayURL:         stringMetaValue(c.Metadata, "last_invoice_pay_url"),
		PDFURL:         stringMetaValue(c.Metadata, "last_invoice_pdf_url"),
	}
	// Mirrors RequireSupportFeeCurrentForMutations: blocking once due + grace has passed.
	if c.Status == supportfeecycle.StatusOVERDUE || c.Status == supportfeecycle.StatusINVOICED || c.Status == supportfeecycle.StatusPENDING {
		dto.Blocking = now.After(c.DueDate.AddDate(0, 0, subscriptions.SupportFeeGraceDays))
	}
	if a := c.Edges.Agreement; a != nil {
		dto.AgreementName = a.Name
		dto.AgreementKind = string(a.Kind)
	}
	if ts := c.Edges.TenantSubscription; ts != nil && ts.Edges.Tenant != nil {
		dto.TenantName = ts.Edges.Tenant.Name
	}
	return dto
}

type supportAgreementDTO struct {
	ID                uuid.UUID  `json:"id"`
	TenantID          uuid.UUID  `json:"tenant_id"`
	Kind              string     `json:"kind"`
	Name              string     `json:"name"`
	SupportPlanCode   string     `json:"support_plan_code,omitempty"`
	BillingCycle      string     `json:"billing_cycle"`
	IntervalCount     int        `json:"interval_count"`
	IntervalUnit      string     `json:"interval_unit"`
	Amount            *float64   `json:"amount,omitempty"`
	PeriodAmount      float64    `json:"period_amount"`
	MonthlyEquivalent float64    `json:"monthly_equivalent"`
	AnnualListPrice   float64    `json:"annual_list_price,omitempty"`
	Currency          string     `json:"currency"`
	BillingTiming     string     `json:"billing_timing"`
	StartsAt          time.Time  `json:"starts_at"`
	EndsAt            *time.Time `json:"ends_at,omitempty"`
	Status            string     `json:"status"`
	NextPeriodStart   time.Time  `json:"next_period_start"`
	CycleCount        int        `json:"cycle_count"`
	Notes             *string    `json:"notes,omitempty"`
	BillingEmail      string     `json:"billing_email,omitempty"`
	// Collection is "business" or "personal" (off the company's books).
	Collection string            `json:"collection"`
	Cycles     []supportCycleDTO `json:"cycles"`
}

func toSupportAgreementDTO(a *ent.SupportAgreement, now time.Time) supportAgreementDTO {
	iv := subscriptions.AgreementInterval(a)
	period := subscriptions.AgreementPeriodAmount(a, a.Edges.SupportPlan)
	dto := supportAgreementDTO{
		ID:                a.ID,
		TenantID:          a.TenantID,
		Kind:              string(a.Kind),
		Name:              a.Name,
		BillingCycle:      string(a.BillingCycle),
		IntervalCount:     a.IntervalCount,
		IntervalUnit:      string(a.IntervalUnit),
		Amount:            a.Amount,
		PeriodAmount:      period,
		MonthlyEquivalent: subscriptions.MonthlyEquivalent(period, iv),
		Currency:          a.Currency,
		BillingTiming:     string(a.BillingTiming),
		StartsAt:          a.StartsAt,
		EndsAt:            a.EndsAt,
		Status:            string(a.Status),
		NextPeriodStart:   a.NextPeriodStart,
		CycleCount:        a.CycleCount,
		Notes:             a.Notes,
		BillingEmail:      stringMetaValue(a.Metadata, "billing_email"),
		Collection:        "business",
		Cycles:            []supportCycleDTO{},
	}
	if subscriptions.IsPersonalAgreement(a.Metadata) {
		dto.Collection = subscriptions.CollectionPersonal
	}
	if p := a.Edges.SupportPlan; p != nil {
		dto.SupportPlanCode = p.PlanCode
		dto.AnnualListPrice = p.BasePrice
	}
	for _, c := range a.Edges.Cycles {
		c.Edges.Agreement = a
		dto.Cycles = append(dto.Cycles, toSupportCycleDTO(c, now))
	}
	return dto
}

func stringMetaValue(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	v, _ := m[key].(string)
	return v
}

func actorID(r *http.Request) uuid.UUID {
	if claims, ok := authclient.ClaimsFromContext(r.Context()); ok && claims != nil {
		id, _ := claims.UserID()
		return id
	}
	return uuid.Nil
}

func (h *PlatformHandler) writeSupportError(w http.ResponseWriter, err error, op string) {
	if errors.Is(err, subscriptions.ErrSupportValidation) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": strings.TrimPrefix(err.Error(), subscriptions.ErrSupportValidation.Error()+": ")})
		return
	}
	h.log.Error(op, zap.Error(err))
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
}

// ListTenantSupportAgreements godoc
// @Summary List a tenant's support agreements (admin)
// @Description Standard hosting and support plus any special support agreements, each with its
// @Description latest cycles, and the tenant's outstanding support balance.
// @Tags Platform
// @Produce json
// @Security BearerAuth
// @Param tenant_id path string true "Tenant UUID"
// @Success 200 {object} map[string]interface{}
// @Router /admin/tenants/{tenant_id}/support-agreements [get]
func (h *PlatformHandler) ListTenantSupportAgreements(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tenantID, err := uuid.Parse(chi.URLParam(r, "tenant_id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid tenant id"})
		return
	}
	agreements, err := h.client.SupportAgreement.Query().
		Where(supportagreement.TenantIDEQ(tenantID)).
		Order(ent.Asc(supportagreement.FieldKind), ent.Asc(supportagreement.FieldCreatedAt)).
		WithSupportPlan().
		All(ctx)
	if err != nil {
		h.writeSupportError(w, err, "list support agreements")
		return
	}
	// Recent cycles per agreement: one bounded query each (a tenant has a handful of agreements).
	for _, a := range agreements {
		cycles, err := h.client.SupportFeeCycle.Query().
			Where(supportfeecycle.AgreementIDEQ(a.ID)).
			Order(ent.Desc(supportfeecycle.FieldCycleNumber)).
			Limit(recentCyclesPerAgreement).
			All(ctx)
		if err != nil {
			h.writeSupportError(w, err, "list support cycles")
			return
		}
		a.Edges.Cycles = cycles
	}
	outstanding, err := h.supportOutstanding(r, supportfeecycle.TenantIDEQ(tenantID))
	if err != nil {
		h.writeSupportError(w, err, "support outstanding")
		return
	}
	now := time.Now().UTC()
	out := make([]supportAgreementDTO, 0, len(agreements))
	for _, a := range agreements {
		out = append(out, toSupportAgreementDTO(a, now))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"agreements":  out,
		"outstanding": outstanding,
	})
}

// CreateTenantSupportAgreement godoc
// @Summary Create a support agreement for a tenant (admin)
// @Description kind SPECIAL (default) needs name and amount; billing_cycle MONTHLY, QUARTERLY,
// @Description SEMI_ANNUAL, ANNUAL or CUSTOM (with interval_count and interval_unit MONTH or DAY);
// @Description billing_timing ADVANCE (default for SPECIAL) or ARREARS. Unpaid cycles block data
// @Description mutations fleet-wide once due date plus grace has passed.
// @Tags Platform
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param tenant_id path string true "Tenant UUID"
// @Param body body subscriptions.SupportAgreementInput true "Agreement"
// @Success 201 {object} supportAgreementDTO
// @Failure 400 {object} map[string]string
// @Router /admin/tenants/{tenant_id}/support-agreements [post]
func (h *PlatformHandler) CreateTenantSupportAgreement(w http.ResponseWriter, r *http.Request) {
	tenantID, err := uuid.Parse(chi.URLParam(r, "tenant_id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid tenant id"})
		return
	}
	var in subscriptions.SupportAgreementInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	a, err := h.subSvc.CreateSupportAgreement(r.Context(), tenantID, in, actorID(r))
	if err != nil {
		h.writeSupportError(w, err, "create support agreement")
		return
	}
	h.log.Info("support agreement created", zap.String("tenant_id", tenantID.String()), zap.String("agreement_id", a.ID.String()))
	h.writeAgreement(w, r, a.ID, http.StatusCreated)
}

// UpdateSupportAgreement godoc
// @Summary Update a support agreement (admin)
// @Description Partial update. A cycle or timing change applies from the next period
// @Description (reschedule_from=next_period) or restarts today (reschedule_from=now, cancelling
// @Description the current not-yet-invoiced period). status PAUSED stops new periods; ENDED also
// @Description cancels future not-yet-invoiced periods. Invoiced charges stay payable.
// @Tags Platform
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "Agreement UUID"
// @Param body body subscriptions.SupportAgreementInput true "Changes"
// @Success 200 {object} supportAgreementDTO
// @Failure 400 {object} map[string]string
// @Router /admin/support-agreements/{id} [put]
func (h *PlatformHandler) UpdateSupportAgreement(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid agreement id"})
		return
	}
	var in subscriptions.SupportAgreementInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	a, err := h.subSvc.UpdateSupportAgreement(r.Context(), id, in, actorID(r))
	if err != nil {
		h.writeSupportError(w, err, "update support agreement")
		return
	}
	// A personal agreement's charges already invoiced on the company's books move off them too,
	// so switching collection never leaves an open personal charge in revenue and AR. Checked on
	// every save (charges already moved are skipped), so saving the agreement also repairs one
	// raised before this check existed.
	if subscriptions.IsPersonalAgreement(a.Metadata) && h.invoiceSvc != nil {
		if _, merr := h.invoiceSvc.MoveOpenSupportChargesOffBooks(r.Context(), id); merr != nil {
			h.log.Error("support agreement: open charges not moved off the books", zap.String("agreement_id", id.String()), zap.Error(merr))
			writeJSON(w, http.StatusBadGateway, map[string]string{
				"error": "Collection is now personal, but some open invoices could not be moved off the books: " + merr.Error(),
			})
			return
		}
	}
	h.writeAgreement(w, r, id, http.StatusOK)
}

func (h *PlatformHandler) writeAgreement(w http.ResponseWriter, r *http.Request, id uuid.UUID, status int) {
	ctx := r.Context()
	a, err := h.client.SupportAgreement.Query().
		Where(supportagreement.IDEQ(id)).
		WithSupportPlan().
		WithCycles(func(q *ent.SupportFeeCycleQuery) {
			q.Order(ent.Desc(supportfeecycle.FieldCycleNumber)).Limit(recentCyclesPerAgreement)
		}).
		Only(ctx)
	if err != nil {
		h.writeSupportError(w, err, "load support agreement")
		return
	}
	writeJSON(w, status, toSupportAgreementDTO(a, time.Now().UTC()))
}

// ListSupportFeeCycles godoc
// @Summary List support charges across tenants (admin receivables)
// @Description Paginated, newest due first. Filters: status (comma list of PENDING, INVOICED,
// @Description OVERDUE, PAID, WAIVED), tenant_id, agreement_id, kind (STANDARD or SPECIAL),
// @Description blocking=true (overdue past grace). Includes the outstanding totals for the filter.
// @Tags Platform
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{}
// @Router /admin/support-fee-cycles [get]
func (h *PlatformHandler) ListSupportFeeCycles(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()
	now := time.Now().UTC()
	var preds []predicate.SupportFeeCycle
	if v := strings.TrimSpace(q.Get("status")); v != "" {
		var sts []supportfeecycle.Status
		for _, part := range strings.Split(v, ",") {
			st := supportfeecycle.Status(strings.ToUpper(strings.TrimSpace(part)))
			if supportfeecycle.StatusValidator(st) != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid status " + part})
				return
			}
			sts = append(sts, st)
		}
		preds = append(preds, supportfeecycle.StatusIn(sts...))
	}
	if v := q.Get("tenant_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid tenant_id"})
			return
		}
		preds = append(preds, supportfeecycle.TenantIDEQ(id))
	}
	if v := q.Get("agreement_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid agreement_id"})
			return
		}
		preds = append(preds, supportfeecycle.AgreementIDEQ(id))
	}
	if v := strings.ToUpper(q.Get("kind")); v != "" {
		if supportagreement.KindValidator(supportagreement.Kind(v)) != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid kind"})
			return
		}
		preds = append(preds, supportfeecycle.HasAgreementWith(supportagreement.KindEQ(supportagreement.Kind(v))))
	}
	if q.Get("blocking") == "true" {
		preds = append(preds,
			supportfeecycle.StatusIn(supportfeecycle.StatusPENDING, supportfeecycle.StatusINVOICED, supportfeecycle.StatusOVERDUE),
			supportfeecycle.DueDateLT(now.AddDate(0, 0, -subscriptions.SupportFeeGraceDays)),
		)
	}
	if v := strings.TrimSpace(q.Get("search")); v != "" {
		preds = append(preds, supportfeecycle.HasTenantSubscriptionWith(
			tenantsubscription.HasTenantWith(tenantSearch(v)),
		))
	}
	query := h.client.SupportFeeCycle.Query().Where(preds...)
	p := pagination.Parse(r)
	total, err := query.Clone().Count(ctx)
	if err != nil {
		h.writeSupportError(w, err, "count support cycles")
		return
	}
	cycles, err := query.
		Order(ent.Desc(supportfeecycle.FieldDueDate), ent.Desc(supportfeecycle.FieldID)).
		Offset(p.Offset).
		Limit(p.Limit).
		WithAgreement().
		WithTenantSubscription(func(q *ent.TenantSubscriptionQuery) {
			q.WithTenant(func(tq *ent.TenantQuery) { tq.Select(tenant.FieldName) })
		}).
		All(ctx)
	if err != nil {
		h.writeSupportError(w, err, "list support cycles")
		return
	}
	rows := make([]supportCycleDTO, 0, len(cycles))
	for _, c := range cycles {
		rows = append(rows, toSupportCycleDTO(c, now))
	}
	totals, err := h.supportOutstanding(r, preds...)
	if err != nil {
		h.writeSupportError(w, err, "support outstanding")
		return
	}
	writeJSON(w, http.StatusOK, struct {
		pagination.Response[supportCycleDTO]
		Outstanding supportTotals `json:"outstanding"`
	}{pagination.NewResponse(rows, total, p), totals})
}

// UpdateSupportFeeCycle godoc
// @Summary Reprice, waive or record an offline payment for one support charge (admin)
// @Description custom_price / clear_custom_price override this period's amount (a sales-agreed
// @Description price). status PAID records an offline payment; WAIVED excuses the charge. Either
// @Description lifts the mutation block on the tenant's next token refresh.
// @Tags Platform
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "SupportFeeCycle UUID"
// @Success 200 {object} supportCycleDTO
// @Failure 400 {object} map[string]string
// @Router /admin/support-fee-cycles/{id} [put]
func (h *PlatformHandler) UpdateSupportFeeCycle(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid support fee cycle id"})
		return
	}
	var body struct {
		CustomPrice       *float64 `json:"custom_price"`
		CustomPriceReason *string  `json:"custom_price_reason"`
		ClearCustomPrice  bool     `json:"clear_custom_price"`
		Status            string   `json:"status"`
		Reason            string   `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	priceChange := body.CustomPrice != nil || body.ClearCustomPrice
	if !priceChange && body.Status == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "one of custom_price, clear_custom_price or status is required"})
		return
	}
	if body.CustomPrice != nil && body.ClearCustomPrice {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "custom_price and clear_custom_price are mutually exclusive"})
		return
	}
	if body.CustomPrice != nil && *body.CustomPrice < 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "custom_price must not be negative"})
		return
	}

	cycle, err := h.client.SupportFeeCycle.Get(ctx, id)
	if err != nil {
		if ent.IsNotFound(err) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "support fee cycle not found"})
			return
		}
		h.writeSupportError(w, err, "get support cycle")
		return
	}
	actor := actorID(r)
	if priceChange {
		if cycle.Status == supportfeecycle.StatusPAID || cycle.Status == supportfeecycle.StatusWAIVED {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "a settled charge cannot be repriced"})
			return
		}
		upd := h.client.SupportFeeCycle.UpdateOneID(id).SetCustomPriceSetAt(time.Now())
		if actor != uuid.Nil {
			upd = upd.SetCustomPriceSetBy(actor)
		}
		if body.ClearCustomPrice {
			upd = upd.ClearCustomPrice().ClearCustomPriceReason()
		} else {
			upd = upd.SetCustomPrice(*body.CustomPrice).SetNillableCustomPriceReason(body.CustomPriceReason)
		}
		if err := upd.Exec(ctx); err != nil {
			h.writeSupportError(w, err, "reprice support cycle")
			return
		}
	}
	if body.Status != "" {
		st := supportfeecycle.Status(strings.ToUpper(strings.TrimSpace(body.Status)))
		if _, err := h.subSvc.SettleSupportCycleManually(ctx, id, st, body.Reason, actor); err != nil {
			h.writeSupportError(w, err, "settle support cycle")
			return
		}
	}
	h.writeCycle(w, r, id)
}

// GenerateSupportFeeCycleInvoice godoc
// @Summary Issue (or with force=true re-issue) the invoice for one support charge (admin)
// @Tags Platform
// @Produce json
// @Security BearerAuth
// @Param id path string true "SupportFeeCycle UUID"
// @Param force query bool false "Re-issue even if already invoiced"
// @Success 200 {object} supportCycleDTO
// @Router /admin/support-fee-cycles/{id}/invoice [post]
func (h *PlatformHandler) GenerateSupportFeeCycleInvoice(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if h.invoiceSvc == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "invoicing not configured"})
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid support fee cycle id"})
		return
	}
	cycle, err := h.client.SupportFeeCycle.Query().
		Where(supportfeecycle.IDEQ(id)).
		WithTenantSubscription(func(q *ent.TenantSubscriptionQuery) { q.WithTenant() }).
		WithSupportPlan().
		WithAgreement().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "support fee cycle not found"})
			return
		}
		h.writeSupportError(w, err, "get support cycle")
		return
	}
	if _, err := h.invoiceSvc.GenerateAndSendSupportFeeInvoice(ctx, cycle, r.URL.Query().Get("force") == "true"); err != nil {
		h.log.Error("support fee invoice generation failed", zap.Error(err))
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	h.writeCycle(w, r, id)
}

func (h *PlatformHandler) writeCycle(w http.ResponseWriter, r *http.Request, id uuid.UUID) {
	c, err := h.client.SupportFeeCycle.Query().Where(supportfeecycle.IDEQ(id)).WithAgreement().Only(r.Context())
	if err != nil {
		h.writeSupportError(w, err, "load support cycle")
		return
	}
	writeJSON(w, http.StatusOK, toSupportCycleDTO(c, time.Now().UTC()))
}

// tenantSearch matches a tenant by name or slug, case-insensitively, in SQL.
func tenantSearch(term string) predicate.Tenant {
	return tenant.Or(tenant.NameContainsFold(term), tenant.SlugContainsFold(term))
}

// supportTotals is the outstanding support balance for a filter.
type supportTotals struct {
	OpenCount      int     `json:"open_count"`
	OpenAmount     float64 `json:"open_amount"`
	OverdueCount   int     `json:"overdue_count"`
	OverdueAmount  float64 `json:"overdue_amount"`
	BlockingCount  int     `json:"blocking_count"`
	BlockingAmount float64 `json:"blocking_amount"`
}

// supportOutstanding sums unpaid support charges in SQL without loading rows: a GROUP BY status
// aggregate plus one aggregate for the charges already past grace (blocking). Each charge counts
// at its effective price COALESCE(custom_price, base_price).
func (h *PlatformHandler) supportOutstanding(r *http.Request, preds ...predicate.SupportFeeCycle) (supportTotals, error) {
	ctx := r.Context()
	open := append([]predicate.SupportFeeCycle{supportfeecycle.StatusIn(
		supportfeecycle.StatusPENDING, supportfeecycle.StatusINVOICED, supportfeecycle.StatusOVERDUE)}, preds...)
	var byStatus []struct {
		Status string  `json:"status"`
		N      int     `json:"n"`
		Amount float64 `json:"amount"`
	}
	if err := h.client.SupportFeeCycle.Query().Where(open...).
		GroupBy(supportfeecycle.FieldStatus).
		Aggregate(countAs("n"), effectiveSupportSumAs("amount")).
		Scan(ctx, &byStatus); err != nil {
		return supportTotals{}, err
	}
	var t supportTotals
	for _, row := range byStatus {
		t.OpenCount += row.N
		t.OpenAmount += row.Amount
		if row.Status == string(supportfeecycle.StatusOVERDUE) {
			t.OverdueCount += row.N
			t.OverdueAmount += row.Amount
		}
	}
	var blocking []struct {
		N      int     `json:"n"`
		Amount float64 `json:"amount"`
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -subscriptions.SupportFeeGraceDays)
	if err := h.client.SupportFeeCycle.Query().
		Where(append(open, supportfeecycle.DueDateLT(cutoff))...).
		Aggregate(countAs("n"), effectiveSupportSumAs("amount")).
		Scan(ctx, &blocking); err != nil {
		return supportTotals{}, err
	}
	if len(blocking) > 0 {
		t.BlockingCount, t.BlockingAmount = blocking[0].N, blocking[0].Amount
	}
	return t, nil
}

// countAs and effectiveSupportSumAs alias their columns: ent's bare Count/Sum emit unaliased
// expressions that collide when a query has more than one aggregate.
func countAs(alias string) ent.AggregateFunc {
	return func(s *entsql.Selector) string { return entsql.As(entsql.Count("*"), alias) }
}

func effectiveSupportSumAs(alias string) ent.AggregateFunc {
	return func(s *entsql.Selector) string {
		return entsql.As(fmt.Sprintf("COALESCE(SUM(COALESCE(%s, %s)), 0)",
			s.C(supportfeecycle.FieldCustomPrice), s.C(supportfeecycle.FieldBasePrice)), alias)
	}
}
