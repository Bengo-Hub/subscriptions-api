package handlers

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/servicechargeplan"
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

// ListServiceChargePlans returns all active service charge plans.
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

// GetServiceChargePlan returns a single service charge plan by code.
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

// writeJSON is defined in features.go
