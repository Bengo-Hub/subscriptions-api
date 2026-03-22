package handlers

import (
	"encoding/json"
	"net/http"

	authclient "github.com/Bengo-Hub/shared-auth-client"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/bengobox/subscription-service/internal/modules/rbac"
)

// RBACHandler handles RBAC-related operations.
type RBACHandler struct {
	logger      *zap.Logger
	rbacService *rbac.Service
	rbacRepo    rbac.Repository
}

// NewRBACHandler creates a new RBAC handler.
func NewRBACHandler(logger *zap.Logger, rbacService *rbac.Service, rbacRepo rbac.Repository) *RBACHandler {
	return &RBACHandler{
		logger:      logger.Named("rbac.handler"),
		rbacService: rbacService,
		rbacRepo:    rbacRepo,
	}
}

// AssignRoleRequest represents a request to assign a role.
type AssignRoleRequest struct {
	UserID uuid.UUID `json:"user_id"`
	RoleID uuid.UUID `json:"role_id"`
}

// AssignRole assigns a role to a user.
func (h *RBACHandler) AssignRole(w http.ResponseWriter, r *http.Request) {
	tenantIDStr := chi.URLParam(r, "tenant")
	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		h.respondWithError(w, http.StatusBadRequest, "invalid tenant ID")
		return
	}

	var req AssignRoleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.respondWithError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	claims, ok := authclient.ClaimsFromContext(r.Context())
	if !ok {
		h.respondWithError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	assignedBy, err := claims.UserID()
	if err != nil || assignedBy == uuid.Nil {
		h.respondWithError(w, http.StatusUnauthorized, "invalid user ID")
		return
	}

	if err := h.rbacService.AssignRole(r.Context(), tenantID, req.UserID, req.RoleID, assignedBy); err != nil {
		h.logger.Error("failed to assign role", zap.Error(err))
		h.respondWithError(w, http.StatusInternalServerError, "failed to assign role")
		return
	}

	h.respondWithJSON(w, http.StatusCreated, map[string]string{"message": "role assigned successfully"})
}

// RevokeRole revokes a role from a user.
func (h *RBACHandler) RevokeRole(w http.ResponseWriter, r *http.Request) {
	tenantIDStr := chi.URLParam(r, "tenant")
	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		h.respondWithError(w, http.StatusBadRequest, "invalid tenant ID")
		return
	}

	assignmentIDStr := chi.URLParam(r, "id")
	assignmentID, err := uuid.Parse(assignmentIDStr)
	if err != nil {
		h.respondWithError(w, http.StatusBadRequest, "invalid assignment ID")
		return
	}

	// Get assignment to extract user ID and role ID
	assignments, err := h.rbacRepo.ListUserAssignments(r.Context(), tenantID, rbac.AssignmentFilters{})
	if err != nil {
		h.respondWithError(w, http.StatusInternalServerError, "failed to get assignment")
		return
	}

	var assignment *rbac.UserRoleAssignment
	for _, a := range assignments {
		if a.ID == assignmentID {
			assignment = a
			break
		}
	}

	if assignment == nil {
		h.respondWithError(w, http.StatusNotFound, "assignment not found")
		return
	}

	if err := h.rbacService.RevokeRole(r.Context(), tenantID, assignment.UserID, assignment.RoleID); err != nil {
		h.logger.Error("failed to revoke role", zap.Error(err))
		h.respondWithError(w, http.StatusInternalServerError, "failed to revoke role")
		return
	}

	h.respondWithJSON(w, http.StatusOK, map[string]string{"message": "role revoked successfully"})
}

// ListAssignments lists all role assignments.
func (h *RBACHandler) ListAssignments(w http.ResponseWriter, r *http.Request) {
	tenantIDStr := chi.URLParam(r, "tenant")
	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		h.respondWithError(w, http.StatusBadRequest, "invalid tenant ID")
		return
	}

	assignments, err := h.rbacRepo.ListUserAssignments(r.Context(), tenantID, rbac.AssignmentFilters{})
	if err != nil {
		h.logger.Error("failed to list assignments", zap.Error(err))
		h.respondWithError(w, http.StatusInternalServerError, "failed to list assignments")
		return
	}

	h.respondWithJSON(w, http.StatusOK, map[string]interface{}{"assignments": assignments})
}

// ListRoles lists all roles.
func (h *RBACHandler) ListRoles(w http.ResponseWriter, r *http.Request) {
	tenantIDStr := chi.URLParam(r, "tenant")
	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		h.respondWithError(w, http.StatusBadRequest, "invalid tenant ID")
		return
	}

	roles, err := h.rbacRepo.ListRoles(r.Context(), tenantID)
	if err != nil {
		h.logger.Error("failed to list roles", zap.Error(err))
		h.respondWithError(w, http.StatusInternalServerError, "failed to list roles")
		return
	}

	h.respondWithJSON(w, http.StatusOK, map[string]interface{}{"roles": roles})
}

// ListPermissions lists all permissions.
func (h *RBACHandler) ListPermissions(w http.ResponseWriter, r *http.Request) {
	permissions, err := h.rbacRepo.ListPermissions(r.Context(), rbac.PermissionFilters{})
	if err != nil {
		h.logger.Error("failed to list permissions", zap.Error(err))
		h.respondWithError(w, http.StatusInternalServerError, "failed to list permissions")
		return
	}

	h.respondWithJSON(w, http.StatusOK, map[string]interface{}{"permissions": permissions})
}

// GetMyRoles returns the current user's roles.
func (h *RBACHandler) GetMyRoles(w http.ResponseWriter, r *http.Request) {
	claims, ok := authclient.ClaimsFromContext(r.Context())
	if !ok {
		h.respondWithError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	userID, err := claims.UserID()
	if err != nil || userID == uuid.Nil {
		h.respondWithError(w, http.StatusUnauthorized, "invalid user ID")
		return
	}

	tenantID, err := uuid.Parse(claims.TenantID)
	if err != nil {
		h.respondWithError(w, http.StatusBadRequest, "invalid tenant ID in claims")
		return
	}

	roles, err := h.rbacRepo.GetUserRoles(r.Context(), tenantID, userID)
	if err != nil {
		h.logger.Error("failed to get user roles", zap.Error(err))
		h.respondWithError(w, http.StatusInternalServerError, "failed to get user roles")
		return
	}

	h.respondWithJSON(w, http.StatusOK, map[string]interface{}{"roles": roles})
}

// GetMyPermissions returns the current user's permissions.
func (h *RBACHandler) GetMyPermissions(w http.ResponseWriter, r *http.Request) {
	claims, ok := authclient.ClaimsFromContext(r.Context())
	if !ok {
		h.respondWithError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	userID, err := claims.UserID()
	if err != nil || userID == uuid.Nil {
		h.respondWithError(w, http.StatusUnauthorized, "invalid user ID")
		return
	}

	tenantID, err := uuid.Parse(claims.TenantID)
	if err != nil {
		h.respondWithError(w, http.StatusBadRequest, "invalid tenant ID in claims")
		return
	}

	permissions, err := h.rbacRepo.GetUserPermissions(r.Context(), tenantID, userID)
	if err != nil {
		h.logger.Error("failed to get user permissions", zap.Error(err))
		h.respondWithError(w, http.StatusInternalServerError, "failed to get user permissions")
		return
	}

	h.respondWithJSON(w, http.StatusOK, map[string]interface{}{"permissions": permissions})
}

// RegisterRoutes registers RBAC routes on the given router.
func (h *RBACHandler) RegisterRoutes(r chi.Router) {
	// Self-service: current user's roles and permissions
	r.Get("/rbac/me/roles", h.GetMyRoles)
	r.Get("/rbac/me/permissions", h.GetMyPermissions)
	r.Get("/permissions", h.ListPermissions)

	// Tenant-scoped admin operations
	r.Route("/tenants/{tenant}/rbac", func(r chi.Router) {
		r.Post("/assignments", h.AssignRole)
		r.Get("/assignments", h.ListAssignments)
		r.Delete("/assignments/{id}", h.RevokeRole)
		r.Get("/roles", h.ListRoles)
	})
}

func (h *RBACHandler) respondWithError(w http.ResponseWriter, code int, message string) {
	h.respondWithJSON(w, code, rbacErrorResponse{Error: message})
}

func (h *RBACHandler) respondWithJSON(w http.ResponseWriter, code int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(payload)
}

type rbacErrorResponse struct {
	Error string `json:"error"`
}
