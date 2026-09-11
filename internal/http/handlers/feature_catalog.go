package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/featuredefinition"
	"github.com/bengobox/subscription-service/internal/ent/subscriptionplan"
)

// planFamily mirrors shared-ui-lib's feature-gate.tsx planFamily() exactly: a plan code's
// family, upper-cased. PowerSuite plans return their first TWO segments (POWERSUITE_DUKA_GOLD
// -> "POWERSUITE_DUKA") because the PowerSuite line encodes a further disjoint use-case
// sub-family (Hospitality/Retail/Pharmacy) as its second segment; every other product line
// (ERP_*, LIBRARY_*, TRULOAD_*, ...) has no such split, so the first segment is correct there.
// Used to prefer an in-family upgrade suggestion (e.g. "upgrade your own PowerSuite Duka tier")
// over a globally-cheaper but practically-irrelevant cross-product one (e.g. suggesting a bare
// ERP_STARTER purchase to a PowerSuite tenant just because it has a lower tier_order).
func planFamily(code string) string {
	upper := strings.ToUpper(code)
	if strings.HasPrefix(upper, "POWERSUITE_") {
		parts := strings.SplitN(upper, "_", 3)
		if len(parts) >= 2 {
			return parts[0] + "_" + parts[1]
		}
		return upper
	}
	if i := strings.Index(upper, "_"); i != -1 {
		return upper[:i]
	}
	return upper
}

// FeatureCatalogHandler serves the platform-wide feature/limit catalog that
// powers the admin plan-builder UI (service-grouped, categorized feature picker)
// and gives the platform owner full CRUD over feature definitions.
type FeatureCatalogHandler struct {
	log *zap.Logger
	orm *ent.Client
}

// NewFeatureCatalogHandler constructs a FeatureCatalogHandler.
func NewFeatureCatalogHandler(log *zap.Logger, orm *ent.Client) *FeatureCatalogHandler {
	return &FeatureCatalogHandler{log: log.Named("feature_catalog.handler"), orm: orm}
}

type featureDefinitionDTO struct {
	ID            uuid.UUID `json:"id"`
	FeatureCode   string    `json:"featureCode"`
	ServiceTag    string    `json:"serviceTag"`
	Category      string    `json:"category"`
	Label         string    `json:"label"`
	Description   string    `json:"description,omitempty"`
	Kind          string    `json:"kind"`
	ValueType     string    `json:"valueType"`
	DefaultLimit  *int      `json:"defaultLimit,omitempty"`
	IsRateLimited bool      `json:"isRateLimited"`
	Unit          string    `json:"unit,omitempty"`
	NatsEvent     string    `json:"natsEvent,omitempty"`
	SortOrder     int       `json:"sortOrder"`
	IsActive      bool      `json:"isActive"`
	// MinPlanCode / MinTierLabel / MinTierOrder = the cheapest active plan that unlocks this
	// feature, computed from the plans' feature sets. Lets any UI render "Upgrade to <tier>" +
	// deep-link to the pricing page without a hardcoded per-app feature→tier map, and lets the
	// gating wrapper compare the tenant's tier rank against MinTierOrder (family-scoped) so
	// at/below-tier features never show an upgrade banner. Empty/0 when no plan grants it.
	MinPlanCode  string `json:"minPlanCode,omitempty"`
	MinTierLabel string `json:"minTierLabel,omitempty"`
	MinTierOrder int    `json:"minTierOrder,omitempty"`
}

// minPlan is the cheapest plan that unlocks a given feature code or service tag. Exported
// fields so it can also be marshalled directly as the serviceUnlockPlans DTO (minUnlockingPlans
// keeps mapping it into featureDefinitionDTO's own Min* fields, so both resolvers share the
// same candidate-comparison shape).
type minPlan struct {
	Code  string  `json:"planCode"`
	Name  string  `json:"planName"`
	Tier  int     `json:"tierOrder"`
	Price float64 `json:"price"`
}

// minUnlockingPlans builds featureCode → cheapest unlocking plan, by lowest (tier_order, base_price)
// among active plans that include the feature. Powers the shared upgrade dialog's "Upgrade to X".
func (h *FeatureCatalogHandler) minUnlockingPlans(ctx context.Context) map[string]minPlan {
	res := map[string]minPlan{}
	plans, err := h.orm.SubscriptionPlan.Query().
		Where(subscriptionplan.IsActive(true)).
		WithFeatures().
		All(ctx)
	if err != nil {
		h.log.Warn("min-unlocking-plans: load plans failed (feature→tier omitted)", zap.Error(err))
		return res
	}
	for _, p := range plans {
		for _, pf := range p.Edges.Features {
			if !pf.IsIncluded {
				continue
			}
			cand := minPlan{Code: p.PlanCode, Name: p.Name, Tier: p.TierOrder, Price: p.BasePrice}
			cur, ok := res[pf.FeatureCode]
			if !ok || cand.Tier < cur.Tier || (cand.Tier == cur.Tier && cand.Price < cur.Price) {
				res[pf.FeatureCode] = cand
			}
		}
	}
	return res
}

// minUnlockingPlansByServiceTag builds serviceTag → cheapest plan whose UNION of (its own
// service_tag + every included feature's service_tag) contains that tag — the same union logic
// entitlements.go's resolveActiveServiceTags applies per-tenant, applied per-plan instead. This
// is the "which plan unlocks this WHOLE MODULE" resolver that minUnlockingPlans (feature-code
// -keyed) can't answer on its own; it powers the "service_not_subscribed" gate's "Upgrade to
// <specific plan>" UX rather than a generic toast.
//
// currentPlanCode, when non-empty, biases the pick toward the CALLER'S OWN product family
// (via planFamily) — e.g. a PowerSuite Duka tenant missing "erp" gets "upgrade to PowerSuite
// Duka Professional", not the globally-cheapest-by-tier_order candidate, which could be an
// unrelated standalone product like ERP_STARTER (tier_order 1) that nobody actually wants to
// bolt on beside their existing PowerSuite subscription. Falls back to the global cheapest when
// no same-family plan grants the tag (e.g. a Library tenant has no in-family path to "erp") or
// when currentPlanCode is empty (unauthenticated/tenant-agnostic callers).
func (h *FeatureCatalogHandler) minUnlockingPlansByServiceTag(ctx context.Context, currentPlanCode string) map[string]minPlan {
	overall := map[string]minPlan{}
	sameFamily := map[string]minPlan{}
	family := planFamily(currentPlanCode)

	plans, err := h.orm.SubscriptionPlan.Query().
		Where(subscriptionplan.IsActive(true)).
		WithFeatures().
		All(ctx)
	if err != nil {
		h.log.Warn("min-unlocking-plans-by-tag: load plans failed (service upgrade targets omitted)", zap.Error(err))
		return overall
	}

	featureCodeSet := map[string]bool{}
	for _, p := range plans {
		for _, pf := range p.Edges.Features {
			if pf.IsIncluded {
				featureCodeSet[pf.FeatureCode] = true
			}
		}
	}
	featureCodes := make([]string, 0, len(featureCodeSet))
	for c := range featureCodeSet {
		featureCodes = append(featureCodes, c)
	}
	tagByFeature := map[string]string{}
	if len(featureCodes) > 0 {
		rows, err := h.orm.FeatureDefinition.Query().
			Where(featuredefinition.FeatureCodeIn(featureCodes...)).
			All(ctx)
		if err != nil {
			h.log.Warn("min-unlocking-plans-by-tag: load feature definitions failed (service upgrade targets omitted)", zap.Error(err))
			return overall
		}
		for _, r := range rows {
			tagByFeature[r.FeatureCode] = r.ServiceTag
		}
	}

	for _, p := range plans {
		tags := map[string]bool{}
		if p.ServiceTag != nil && *p.ServiceTag != "" {
			tags[*p.ServiceTag] = true
		}
		for _, pf := range p.Edges.Features {
			if !pf.IsIncluded {
				continue
			}
			if t := tagByFeature[pf.FeatureCode]; t != "" {
				tags[t] = true
			}
		}
		cand := minPlan{Code: p.PlanCode, Name: p.Name, Tier: p.TierOrder, Price: p.BasePrice}
		inFamily := family != "" && planFamily(p.PlanCode) == family
		for tag := range tags {
			if cur, ok := overall[tag]; !ok || cand.Tier < cur.Tier || (cand.Tier == cur.Tier && cand.Price < cur.Price) {
				overall[tag] = cand
			}
			if inFamily {
				if cur, ok := sameFamily[tag]; !ok || cand.Tier < cur.Tier || (cand.Tier == cur.Tier && cand.Price < cur.Price) {
					sameFamily[tag] = cand
				}
			}
		}
	}

	// Prefer the in-family candidate per tag; fall back to the global cheapest where the
	// tenant's own family grants nothing (or currentPlanCode was empty).
	for tag, cand := range sameFamily {
		overall[tag] = cand
	}
	return overall
}

func toFeatureDefinitionDTO(e *ent.FeatureDefinition) featureDefinitionDTO {
	dto := featureDefinitionDTO{
		ID:            e.ID,
		FeatureCode:   e.FeatureCode,
		ServiceTag:    e.ServiceTag,
		Category:      e.Category,
		Label:         e.Label,
		Description:   e.Description,
		Kind:          string(e.Kind),
		ValueType:     string(e.ValueType),
		IsRateLimited: e.IsRateLimited,
		Unit:          e.Unit,
		NatsEvent:     e.NatsEvent,
		SortOrder:     e.SortOrder,
		IsActive:      e.IsActive,
	}
	if e.DefaultLimit != nil {
		dl := *e.DefaultLimit
		dto.DefaultLimit = &dl
	}
	return dto
}

// ListCatalog godoc
// @Summary List feature catalog
// @Description Returns the platform-wide feature & limit catalog, optionally filtered by service or kind. Powers the admin plan builder.
// @Tags Catalog
// @Produce json
// @Param service query string false "Filter by service_tag (ordering, pos, inventory, treasury, ...)"
// @Param kind query string false "Filter by kind (FEATURE | LIMIT)"
// @Param plan query string false "Caller's current plan_code — biases serviceUnlockPlans toward an in-family upgrade (e.g. POWERSUITE_DUKA_BASIC -> POWERSUITE_DUKA_PRO) instead of the globally cheapest cross-product plan"
// @Success 200 {object} map[string]interface{}
// @Router /features/catalog [get]
func (h *FeatureCatalogHandler) ListCatalog(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := h.orm.FeatureDefinition.Query().Where(featuredefinition.IsActive(true))
	currentPlanCode := r.URL.Query().Get("plan")

	if svc := r.URL.Query().Get("service"); svc != "" {
		q = q.Where(featuredefinition.ServiceTagEQ(svc))
	}
	if kind := r.URL.Query().Get("kind"); kind == "FEATURE" || kind == "LIMIT" {
		q = q.Where(featuredefinition.KindEQ(featuredefinition.Kind(kind)))
	}

	defs, err := q.
		Order(
			ent.Asc(featuredefinition.FieldServiceTag),
			ent.Asc(featuredefinition.FieldSortOrder),
			ent.Asc(featuredefinition.FieldLabel),
		).
		All(ctx)
	if err != nil {
		h.log.Error("list feature catalog", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list feature catalog"})
		return
	}

	minPlans := h.minUnlockingPlans(ctx)
	items := make([]featureDefinitionDTO, len(defs))
	services := map[string]bool{}
	for i, d := range defs {
		items[i] = toFeatureDefinitionDTO(d)
		if mp, ok := minPlans[d.FeatureCode]; ok {
			items[i].MinPlanCode = mp.Code
			items[i].MinTierLabel = mp.Name
			items[i].MinTierOrder = mp.Tier
		}
		services[d.ServiceTag] = true
	}
	serviceTags := make([]string, 0, len(services))
	for s := range services {
		serviceTags = append(serviceTags, s)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"features": items,
		"services": serviceTags,
		"count":    len(items),
		// serviceUnlockPlans: serviceTag -> cheapest plan that grants whole-module access to it
		// (e.g. {"erp": {"planCode":"POWERSUITE_DUKA_PRO", "tierOrder":2, ...}}). Powers the
		// RequireServiceAccess "service_not_subscribed" gate's named-plan upgrade UX.
		"serviceUnlockPlans": h.minUnlockingPlansByServiceTag(ctx, currentPlanCode),
	})
}

type upsertFeatureDefinitionRequest struct {
	FeatureCode   string `json:"featureCode"`
	ServiceTag    string `json:"serviceTag"`
	Category      string `json:"category"`
	Label         string `json:"label"`
	Description   string `json:"description"`
	Kind          string `json:"kind"`
	ValueType     string `json:"valueType"`
	DefaultLimit  *int   `json:"defaultLimit"`
	IsRateLimited bool   `json:"isRateLimited"`
	Unit          string `json:"unit"`
	NatsEvent     string `json:"natsEvent"`
	SortOrder     int    `json:"sortOrder"`
	IsActive      *bool  `json:"isActive"`
}

// UpsertCatalogEntry creates or updates a feature definition by feature_code. Platform owner only.
// @Summary Upsert feature catalog entry (admin)
// @Tags Catalog
// @Accept json
// @Produce json
// @Security BearerAuth
// @Router /admin/feature-catalog [post]
func (h *FeatureCatalogHandler) UpsertCatalogEntry(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req upsertFeatureDefinitionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if req.FeatureCode == "" || req.ServiceTag == "" || req.Label == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "featureCode, serviceTag and label are required"})
		return
	}

	kind := featuredefinition.KindFEATURE
	if req.Kind == "LIMIT" {
		kind = featuredefinition.KindLIMIT
	}
	valueType := featuredefinition.ValueType(req.ValueType)
	if err := featuredefinition.ValueTypeValidator(valueType); err != nil {
		valueType = featuredefinition.ValueTypeBool
	}
	category := req.Category
	if category == "" {
		category = "General"
	}
	isActive := true
	if req.IsActive != nil {
		isActive = *req.IsActive
	}

	existing, err := h.orm.FeatureDefinition.Query().
		Where(featuredefinition.FeatureCodeEQ(req.FeatureCode)).
		Only(ctx)

	switch {
	case err == nil:
		upd := existing.Update().
			SetServiceTag(req.ServiceTag).
			SetCategory(category).
			SetLabel(req.Label).
			SetDescription(req.Description).
			SetKind(kind).
			SetValueType(valueType).
			SetIsRateLimited(req.IsRateLimited).
			SetUnit(req.Unit).
			SetNatsEvent(req.NatsEvent).
			SetSortOrder(req.SortOrder).
			SetIsActive(isActive)
		if req.DefaultLimit != nil {
			upd = upd.SetDefaultLimit(*req.DefaultLimit)
		} else {
			upd = upd.ClearDefaultLimit()
		}
		saved, serr := upd.Save(ctx)
		if serr != nil {
			h.log.Error("update feature definition", zap.Error(serr))
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to update feature definition"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"feature": toFeatureDefinitionDTO(saved)})
	case ent.IsNotFound(err):
		cr := h.orm.FeatureDefinition.Create().
			SetFeatureCode(req.FeatureCode).
			SetServiceTag(req.ServiceTag).
			SetCategory(category).
			SetLabel(req.Label).
			SetDescription(req.Description).
			SetKind(kind).
			SetValueType(valueType).
			SetIsRateLimited(req.IsRateLimited).
			SetUnit(req.Unit).
			SetNatsEvent(req.NatsEvent).
			SetSortOrder(req.SortOrder).
			SetIsActive(isActive)
		if req.DefaultLimit != nil {
			cr = cr.SetDefaultLimit(*req.DefaultLimit)
		}
		saved, serr := cr.Save(ctx)
		if serr != nil {
			h.log.Error("create feature definition", zap.Error(serr))
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create feature definition"})
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"feature": toFeatureDefinitionDTO(saved)})
	default:
		h.log.Error("lookup feature definition", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to lookup feature definition"})
	}
}

// DeleteCatalogEntry removes a feature definition by ID. Platform owner only.
// @Summary Delete feature catalog entry (admin)
// @Tags Catalog
// @Security BearerAuth
// @Router /admin/feature-catalog/{id} [delete]
func (h *FeatureCatalogHandler) DeleteCatalogEntry(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}
	if err := h.orm.FeatureDefinition.DeleteOneID(id).Exec(ctx); err != nil {
		h.log.Error("delete feature definition", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to delete feature definition"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
