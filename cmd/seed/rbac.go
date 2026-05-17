package main

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/google/uuid"

	entsql "entgo.io/ent/dialect/sql"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/subscriptionspermission"
)

// ── RBAC Permissions ────────────────────────────────────────────────────────

func seedRBACPermissions(ctx context.Context, tx *ent.Tx) error {
	type permDef struct {
		code   string
		name   string
		module string
		action string
	}

	modules := []string{"plans", "features", "bundles", "pricing", "subscriptions", "usage", "billing", "config", "users"}
	actions := []string{"add", "view", "view_own", "change", "change_own", "delete", "delete_own", "manage", "manage_own"}

	var perms []permDef
	for _, mod := range modules {
		for _, act := range actions {
			code := fmt.Sprintf("subscriptions.%s.%s", mod, act)
			name := fmt.Sprintf("%s %s", capitalise(act), capitalise(mod))
			perms = append(perms, permDef{
				code:   code,
				name:   name,
				module: mod,
				action: act,
			})
		}
	}

	for _, p := range perms {
		permID := uuid.NewSHA1(uuid.NameSpaceOID, []byte(p.code))
		err := tx.SubscriptionsPermission.Create().
			SetID(permID).
			SetPermissionCode(p.code).
			SetName(p.name).
			SetModule(p.module).
			SetAction(p.action).
			SetResource(p.module).
			OnConflictColumns(subscriptionspermission.FieldPermissionCode).
			DoNothing().
			Exec(ctx)
		if err != nil && err.Error() != "sql: no rows in result set" {
			return fmt.Errorf("create permission %s: %w", p.code, err)
		}
	}

	// Seed system roles per existing tenant
	// NOTE: Roles are tenant-scoped. We seed them for ALL tenants in the database.
	tenants, err := tx.Tenant.Query().All(ctx)
	if err != nil {
		return fmt.Errorf("list tenants for role seeding: %w", err)
	}

	type roleDef struct {
		code        string
		name        string
		description string
		permModules []string // modules this role gets ALL actions for
	}

	roles := []roleDef{
		{
			code:        "subscriptions_admin",
			name:        "Subscriptions Admin",
			description: "Full access to all subscriptions management features",
			permModules: modules, // all modules
		},
		{
			code:        "billing_manager",
			name:        "Billing Manager",
			description: "Manage billing, subscriptions, and pricing",
			permModules: []string{"subscriptions", "billing", "pricing", "usage"},
		},
		{
			code:        "viewer",
			name:        "Viewer",
			description: "Read-only access to subscriptions data",
			permModules: nil, // will assign view + view_own only
		},
	}

	for _, t := range tenants {
		for _, rd := range roles {
			roleID := uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf("%s:%s", t.ID, rd.code)))
			err := tx.SubscriptionsRole.Create().
				SetID(roleID).
				SetTenantID(t.ID).
				SetRoleCode(rd.code).
				SetName(rd.name).
				SetDescription(rd.description).
				SetIsSystemRole(true).
				OnConflict(
					entsql.ConflictColumns("tenant_id", "role_code"),
				).
				DoNothing().
				Exec(ctx)
			if err != nil && err.Error() != "sql: no rows in result set" {
				return fmt.Errorf("create role %s for tenant %s: %w", rd.code, t.Slug, err)
			}

			// Assign permissions to role
			var permCodes []string
			if rd.code == "viewer" {
				for _, mod := range modules {
					for _, act := range []string{"view", "view_own"} {
						permCodes = append(permCodes, fmt.Sprintf("subscriptions.%s.%s", mod, act))
					}
				}
			} else {
				for _, mod := range rd.permModules {
					for _, act := range actions {
						permCodes = append(permCodes, fmt.Sprintf("subscriptions.%s.%s", mod, act))
					}
				}
			}
			for _, code := range permCodes {
				permID := uuid.NewSHA1(uuid.NameSpaceOID, []byte(code))
				err := tx.RolePermission.Create().
					SetRoleID(roleID).
					SetPermissionID(permID).
					OnConflict(
						entsql.ConflictColumns("role_id", "permission_id"),
					).
					DoNothing().
					Exec(ctx)
				if err != nil && err.Error() != "sql: no rows in result set" {
					return fmt.Errorf("assign permission %s to role %s: %w", code, rd.code, err)
				}
			}
		}
	}

	log.Println("  ✓ RBAC permissions and roles seeded")
	return nil
}

func capitalise(s string) string {
	if s == "" {
		return s
	}
	// Replace underscores with spaces and capitalise first letter
	s = strings.ReplaceAll(s, "_", " ")
	return strings.ToUpper(s[:1]) + s[1:]
}
