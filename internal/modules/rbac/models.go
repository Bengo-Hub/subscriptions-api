package rbac

import (
	"time"

	"github.com/google/uuid"
)

// SubscriptionsUser represents a subscriptions service user reference.
type SubscriptionsUser struct {
	ID                uuid.UUID
	TenantID          uuid.UUID
	AuthServiceUserID uuid.UUID
	Email             string
	Status            string
	SyncStatus        string
	LastSyncAt        *time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// SubscriptionsRole represents a subscriptions service role.
type SubscriptionsRole struct {
	ID           uuid.UUID
	TenantID     *uuid.UUID // nil = global/system role shared platform-wide
	RoleCode     string
	Name         string
	Description  *string
	IsSystemRole bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// SubscriptionsPermission represents a subscriptions service permission.
type SubscriptionsPermission struct {
	ID             uuid.UUID
	PermissionCode string
	Name           string
	Module         string
	Action         string
	Resource       *string
	Description    *string
	CreatedAt      time.Time
}

// UserRoleAssignment represents a user role assignment.
type UserRoleAssignment struct {
	ID         uuid.UUID
	TenantID   uuid.UUID
	UserID     uuid.UUID
	RoleID     uuid.UUID
	AssignedBy uuid.UUID
	AssignedAt time.Time
	ExpiresAt  *time.Time
}
