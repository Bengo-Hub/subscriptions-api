package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/productsubscription"
	"github.com/bengobox/subscription-service/internal/ent/servicechargeplan"
	"github.com/bengobox/subscription-service/internal/ent/tenantsubscription"
)

// ServiceChargeHandler handles service charge plan endpoints.
type ServiceChargeHandler struct {
	log *zap.Logger
	db  *ent.Client
}

// NewServiceChargeHandler creates a new service charge handler.
func NewServiceChargeHandler(log *zap.Logger, db *ent.Client) *ServiceChargeHandler {
	return &ServiceChargeHandler{
		log: log.Named("servicecharge.handler"),
		db:  db,
	}
}

// ListServiceChargePlans godoc
// @Summary List service charge plans
// @Description Returns all active service charge plans, optionally filtered by service
// @Tags ServiceCharges
// @Produce json
// @Security BearerAuth
// @Param service query string false "Filter by service name"
// @Success 200 {object} map[string]interface{}
// @Router /service-charges/plans [get]
func (h *ServiceChargeHandler) ListServiceChargePlans(w http.ResponseWriter, r *http.Request) {
	query := h.db.ServiceChargePlan.Query().
		Where(servicechargeplan.IsActive(true)).
		Order(ent.Asc(servicechargeplan.FieldCode))

	if svc := r.URL.Query().Get("service"); svc != "" {
		query = query.Where(servicechargeplan.Or(
			servicechargeplan.ApplicableServicesIsNil(),
			servicechargeplan.ApplicableServicesNotNil(), // filter in Go below
		))
	}

	plans, err := query.All(r.Context())
	if err != nil {
		h.log.Error("failed to list service charge plans", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list plans"})
		return
	}

	// Filter by service if specified
	if svc := r.URL.Query().Get("service"); svc != "" {
		filtered := make([]*ent.ServiceChargePlan, 0, len(plans))
		for _, p := range plans {
			if len(p.ApplicableServices) == 0 {
				filtered = append(filtered, p) // universal plan
			} else {
				for _, s := range p.ApplicableServices {
					if s == svc {
						filtered = append(filtered, p)
						break
					}
				}
			}
		}
		plans = filtered
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"data":  plans,
		"total": len(plans),
	})
}

// GetServiceChargePlan godoc
// @Summary Get service charge plan
// @Description Returns a specific service charge plan by code
// @Tags ServiceCharges
// @Produce json
// @Security BearerAuth
// @Param code path string true "Service charge plan code"
// @Success 200 {object} map[string]interface{}
// @Failure 404 {object} map[string]string
// @Router /service-charges/plans/{code} [get]
func (h *ServiceChargeHandler) GetServiceChargePlan(w http.ResponseWriter, r *http.Request) {
	code := chi.URLParam(r, "code")
	if code == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "code is required"})
		return
	}

	plan, err := h.db.ServiceChargePlan.Query().
		Where(servicechargeplan.Code(code)).
		Only(r.Context())
	if err != nil {
		if ent.IsNotFound(err) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "service charge plan not found"})
			return
		}
		h.log.Error("failed to get service charge plan", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	writeJSON(w, http.StatusOK, plan)
}

// GetTenantServiceCharges returns the service charge configuration for a tenant's active product subscriptions.
func (h *ServiceChargeHandler) GetTenantServiceCharges(w http.ResponseWriter, r *http.Request) {
	tenantID := chi.URLParam(r, "tenantID")
	if tenantID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "tenant ID required"})
		return
	}

	// Find the tenant's active subscription and its product subscriptions with service charge plans
	subs, err := h.db.ProductSubscription.Query().
		WithServiceChargePlan().
		WithTenantSubscription().
		WithProduct().
		All(r.Context())
	if err != nil {
		h.log.Error("failed to get tenant service charges", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	// Filter to only those belonging to the tenant and with service charge plans
	type serviceCharge struct {
		ProductCode string                `json:"productCode"`
		ProductName string                `json:"productName"`
		ChargeType  string                `json:"chargeType"`
		ChargeValue float64               `json:"chargeValue"`
		MinCharge   *float64              `json:"minCharge,omitempty"`
		MaxCharge   *float64              `json:"maxCharge,omitempty"`
		PlanCode    string                `json:"planCode"`
		PlanName    string                `json:"planName"`
	}

	var charges []serviceCharge
	for _, ps := range subs {
		if ps.Edges.ServiceChargePlan == nil {
			continue
		}
		sc := ps.Edges.ServiceChargePlan
		productName := ps.ProductCode
		if ps.Edges.Product != nil {
			productName = ps.Edges.Product.Name
		}
		charges = append(charges, serviceCharge{
			ProductCode: ps.ProductCode,
			ProductName: productName,
			ChargeType:  string(sc.ChargeType),
			ChargeValue: sc.ChargeValue,
			MinCharge:   sc.MinCharge,
			MaxCharge:   sc.MaxCharge,
			PlanCode:    sc.Code,
			PlanName:    sc.Name,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"data":  charges,
		"total": len(charges),
	})
}

// ComputeTenantServiceCharge resolves the platform's per-transaction commission for a single
// payment, so treasury-api can store service_charge_amount/net_amount WITHOUT each calling service
// re-deriving the rate. S2S, tenant-scoped.
//
//	GET /api/v1/internal/tenants/{tenantID}/service-charge/compute?service={src}&amount={gross}
//
// Only PER-TRANSACTION service-charge plans apply (PERCENTAGE clamped to [min,max], or
// FIXED_PER_TRANSACTION). Tenants billed by a monthly subscription instead (e.g. ISP: KES500 +
// 3% of monthly sales + KES35/PPPoE) carry NO product-level service_charge_plan, so this correctly
// returns applies=false for them — their commission is billed monthly, never per payment.
// Fails CLOSED: any ambiguity returns applies=false / zero so a tenant is never over-charged.
func (h *ServiceChargeHandler) ComputeTenantServiceCharge(w http.ResponseWriter, r *http.Request) {
	tenantID, err := uuid.Parse(chi.URLParam(r, "tenantID"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid tenant ID"})
		return
	}
	service := r.URL.Query().Get("service")
	gross, err := strconv.ParseFloat(r.URL.Query().Get("amount"), 64)
	if err != nil || gross <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid amount"})
		return
	}

	// Tenant-scoped, active product subscriptions that reference a service-charge plan.
	subs, err := h.db.ProductSubscription.Query().
		Where(
			productsubscription.HasTenantSubscriptionWith(tenantsubscription.TenantID(tenantID)),
			productsubscription.StatusEQ(productsubscription.StatusActive),
		).
		WithServiceChargePlan().
		All(r.Context())
	if err != nil {
		h.log.Error("compute service charge: query failed", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	for _, ps := range subs {
		sc := ps.Edges.ServiceChargePlan
		if sc == nil || !sc.IsActive {
			continue
		}
		if !serviceApplies(sc.ApplicableServices, service) {
			continue
		}
		charge, pct := computeCharge(sc, gross)
		if charge < 0 {
			charge = 0
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"applies":            true,
			"charge_amount":      strconv.FormatFloat(charge, 'f', 2, 64),
			"charge_percentage":  pct,
			"plan_code":          sc.Code,
			"charge_type":        string(sc.ChargeType),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"applies": false, "charge_amount": "0.00", "plan_code": ""})
}

// serviceApplies reports whether a plan whose applicable_services list is `applicable` covers
// `service`. An empty/nil list means "all services".
func serviceApplies(applicable []string, service string) bool {
	if len(applicable) == 0 {
		return true
	}
	for _, s := range applicable {
		if strings.EqualFold(strings.TrimSpace(s), strings.TrimSpace(service)) {
			return true
		}
	}
	return false
}

// computeCharge returns (charge, percentageApplied) for a per-transaction plan. PERCENTAGE is
// clamped to [min_charge, max_charge] when set; FIXED_PER_TRANSACTION is the flat value.
func computeCharge(sc *ent.ServiceChargePlan, gross float64) (float64, float64) {
	switch sc.ChargeType {
	case servicechargeplan.ChargeTypeFIXED_PER_TRANSACTION:
		return sc.ChargeValue, 0
	case servicechargeplan.ChargeTypePERCENTAGE:
		charge := gross * sc.ChargeValue / 100.0
		if sc.MinCharge != nil && charge < *sc.MinCharge {
			charge = *sc.MinCharge
		}
		if sc.MaxCharge != nil && *sc.MaxCharge > 0 && charge > *sc.MaxCharge {
			charge = *sc.MaxCharge
		}
		return charge, sc.ChargeValue
	default:
		return 0, 0
	}
}

// serviceChargePlanInput is the request body for create/update service charge plan.
type serviceChargePlanInput struct {
	Code               string   `json:"code"`
	Name               string   `json:"name"`
	Description        string   `json:"description"`
	ChargeType         string   `json:"chargeType"`
	ChargeValue        float64  `json:"chargeValue"`
	Currency           string   `json:"currency"`
	MinCharge          *float64 `json:"minCharge"`
	MaxCharge          *float64 `json:"maxCharge"`
	ApplicableServices []string `json:"applicableServices"`
	IsDefault          bool     `json:"isDefault"`
	IsActive           bool     `json:"isActive"`
}

// CreateServiceChargePlan godoc
// @Summary Create service charge plan (admin)
// @Description Creates a new service charge plan. Requires platform owner.
// @Tags ServiceCharges
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 201 {object} map[string]interface{}
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /admin/service-charges [post]
func (h *ServiceChargeHandler) CreateServiceChargePlan(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var body serviceChargePlanInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	if body.Code == "" || body.Name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "code and name are required"})
		return
	}

	currency := body.Currency
	if currency == "" {
		currency = "KES"
	}

	chargeType := servicechargeplan.ChargeType(body.ChargeType)
	if chargeType == "" {
		chargeType = servicechargeplan.ChargeTypePERCENTAGE
	}

	create := h.db.ServiceChargePlan.Create().
		SetCode(body.Code).
		SetName(body.Name).
		SetDescription(body.Description).
		SetChargeType(chargeType).
		SetChargeValue(body.ChargeValue).
		SetCurrency(currency).
		SetIsDefault(body.IsDefault).
		SetIsActive(body.IsActive)

	if body.MinCharge != nil {
		create = create.SetMinCharge(*body.MinCharge)
	}
	if body.MaxCharge != nil {
		create = create.SetMaxCharge(*body.MaxCharge)
	}
	if len(body.ApplicableServices) > 0 {
		create = create.SetApplicableServices(body.ApplicableServices)
	}

	plan, err := create.Save(ctx)
	if err != nil {
		h.log.Error("failed to create service charge plan", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create plan"})
		return
	}

	writeJSON(w, http.StatusCreated, plan)
}

// UpdateServiceChargePlan godoc
// @Summary Update service charge plan (admin)
// @Description Updates an existing service charge plan. Requires platform owner.
// @Tags ServiceCharges
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path string true "Service charge plan UUID"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /admin/service-charges/{id} [put]
func (h *ServiceChargeHandler) UpdateServiceChargePlan(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}

	var body serviceChargePlanInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	update := h.db.ServiceChargePlan.UpdateOneID(id).
		SetName(body.Name).
		SetDescription(body.Description).
		SetChargeType(servicechargeplan.ChargeType(body.ChargeType)).
		SetChargeValue(body.ChargeValue).
		SetIsDefault(body.IsDefault).
		SetIsActive(body.IsActive)

	if body.Currency != "" {
		update = update.SetCurrency(body.Currency)
	}
	if body.MinCharge != nil {
		update = update.SetMinCharge(*body.MinCharge)
	} else {
		update = update.ClearMinCharge()
	}
	if body.MaxCharge != nil {
		update = update.SetMaxCharge(*body.MaxCharge)
	} else {
		update = update.ClearMaxCharge()
	}
	if body.ApplicableServices != nil {
		update = update.SetApplicableServices(body.ApplicableServices)
	}

	plan, err := update.Save(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "service charge plan not found"})
			return
		}
		h.log.Error("failed to update service charge plan", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to update plan"})
		return
	}

	writeJSON(w, http.StatusOK, plan)
}

// DeleteServiceChargePlan godoc
// @Summary Delete service charge plan (admin)
// @Description Deletes a service charge plan by ID. Requires platform owner.
// @Tags ServiceCharges
// @Produce json
// @Security BearerAuth
// @Param id path string true "Service charge plan UUID"
// @Success 204
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /admin/service-charges/{id} [delete]
func (h *ServiceChargeHandler) DeleteServiceChargePlan(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}

	if err := h.db.ServiceChargePlan.DeleteOneID(id).Exec(ctx); err != nil {
		if ent.IsNotFound(err) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "service charge plan not found"})
			return
		}
		h.log.Error("failed to delete service charge plan", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to delete plan"})
		return
	}

	w.WriteHeader(http.StatusNoContent)
}


