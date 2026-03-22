package rbac

import (
	"context"

	"github.com/google/uuid"
)

// Repository abstracts persistence for RBAC entities.
type Repository interface {
	// User operations
	CreateUser(ctx context.Context, tenantID uuid.UUID, user *SubscriptionsUser) error
	GetUser(ctx context.Context, tenantID uuid.UUID, userID uuid.UUID) (*SubscriptionsUser, error)
	GetUserByAuthServiceID(ctx context.Context, tenantID uuid.UUID, authServiceUserID uuid.UUID) (*SubscriptionsUser, error)
	UpdateUser(ctx context.Context, tenantID uuid.UUID, userID uuid.UUID, updates *UserUpdates) error

	// Role operations
	CreateRole(ctx context.Context, tenantID uuid.UUID, role *SubscriptionsRole) error
	GetRole(ctx context.Context, tenantID uuid.UUID, roleID uuid.UUID) (*SubscriptionsRole, error)
	GetRoleByCode(ctx context.Context, tenantID uuid.UUID, roleCode string) (*SubscriptionsRole, error)
	ListRoles(ctx context.Context, tenantID uuid.UUID) ([]*SubscriptionsRole, error)

	// Permission operations
	CreatePermission(ctx context.Context, permission *SubscriptionsPermission) error
	GetPermission(ctx context.Context, permissionID uuid.UUID) (*SubscriptionsPermission, error)
	GetPermissionByCode(ctx context.Context, permissionCode string) (*SubscriptionsPermission, error)
	ListPermissions(ctx context.Context, filters PermissionFilters) ([]*SubscriptionsPermission, error)

	// Role-Permission operations
	AssignPermissionToRole(ctx context.Context, roleID uuid.UUID, permissionID uuid.UUID) error
	RemovePermissionFromRole(ctx context.Context, roleID uuid.UUID, permissionID uuid.UUID) error
	GetRolePermissions(ctx context.Context, roleID uuid.UUID) ([]*SubscriptionsPermission, error)

	// User-Role assignment operations
	AssignRoleToUser(ctx context.Context, tenantID uuid.UUID, assignment *UserRoleAssignment) error
	RevokeRoleFromUser(ctx context.Context, tenantID uuid.UUID, userID uuid.UUID, roleID uuid.UUID) error
	GetUserRoles(ctx context.Context, tenantID uuid.UUID, userID uuid.UUID) ([]*SubscriptionsRole, error)
	GetUserPermissions(ctx context.Context, tenantID uuid.UUID, userID uuid.UUID) ([]*SubscriptionsPermission, error)
	ListUserAssignments(ctx context.Context, tenantID uuid.UUID, filters AssignmentFilters) ([]*UserRoleAssignment, error)
}

// UserUpdates for partial user updates.
type UserUpdates struct {
	Status     *string
	SyncStatus *string
}

// PermissionFilters for listing permissions.
type PermissionFilters struct {
	Module *string
	Action *string
}

// AssignmentFilters for listing role assignments.
type AssignmentFilters struct {
	UserID *uuid.UUID
	RoleID *uuid.UUID
}
