package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/featuredefinition"
)

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
// @Success 200 {object} map[string]interface{}
// @Router /features/catalog [get]
func (h *FeatureCatalogHandler) ListCatalog(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := h.orm.FeatureDefinition.Query().Where(featuredefinition.IsActive(true))

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

	items := make([]featureDefinitionDTO, len(defs))
	services := map[string]bool{}
	for i, d := range defs {
		items[i] = toFeatureDefinitionDTO(d)
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
