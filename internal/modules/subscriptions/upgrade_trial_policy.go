package subscriptions

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/serviceconfig"
)

// Upgrade trial policy. The platform owner switches upgrade trials on for every tenant with a
// platform-level ServiceConfig row (tenant_id NULL) or for one tenant with a row carrying that
// tenant's id, and sets the trial length the same way. A tenant row wins over the platform row;
// with neither, trials are off and last DefaultUpgradeTrialDays.
const (
	UpgradeTrialEnabledConfigKey = "subscriptions.upgrade_trial_enabled"
	UpgradeTrialDaysConfigKey    = "subscriptions.upgrade_trial_days"

	DefaultUpgradeTrialDays = 14
	MaxUpgradeTrialDays     = 90
)

// Policy sources reported to the admin UI.
const (
	PolicySourceTenant   = "tenant"
	PolicySourcePlatform = "platform"
	PolicySourceDefault  = "default"
)

// UpgradeTrialSetting is one level of the policy. A nil field is not set at that level.
type UpgradeTrialSetting struct {
	Enabled   *bool      `json:"enabled"`
	Days      *int       `json:"days"`
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
}

// UpgradeTrialPolicy is the policy in force for a tenant, with both levels it was built from.
type UpgradeTrialPolicy struct {
	Enabled       bool                `json:"enabled"`
	Days          int                 `json:"days"`
	EnabledSource string              `json:"enabled_source"`
	DaysSource    string              `json:"days_source"`
	Platform      UpgradeTrialSetting `json:"platform"`
	Tenant        UpgradeTrialSetting `json:"tenant"`
	MaxDays       int                 `json:"max_days"`
}

// ValidateUpgradeTrialDays rejects a trial length outside 1 to MaxUpgradeTrialDays.
func ValidateUpgradeTrialDays(days int) error {
	if days < 1 || days > MaxUpgradeTrialDays {
		return fmt.Errorf("upgrade trial days must be between 1 and %d", MaxUpgradeTrialDays)
	}
	return nil
}

// UpgradeTrialPolicyFor resolves the policy for tenantID. uuid.Nil returns the platform level only.
// Both keys at both levels come back in one query; when duplicate rows exist the most recently
// updated one wins.
func (s *Service) UpgradeTrialPolicyFor(ctx context.Context, tenantID uuid.UUID) (UpgradeTrialPolicy, error) {
	scope := serviceconfig.TenantIDIsNil()
	if tenantID != uuid.Nil {
		scope = serviceconfig.Or(serviceconfig.TenantIDIsNil(), serviceconfig.TenantIDEQ(tenantID))
	}
	rows, err := s.client.ServiceConfig.Query().
		Where(
			serviceconfig.ConfigKeyIn(UpgradeTrialEnabledConfigKey, UpgradeTrialDaysConfigKey),
			scope,
		).
		Order(ent.Asc(serviceconfig.FieldUpdatedAt)).
		All(ctx)
	if err != nil {
		return UpgradeTrialPolicy{}, fmt.Errorf("read upgrade trial policy: %w", err)
	}
	return resolveUpgradeTrialPolicy(rows), nil
}

// resolveUpgradeTrialPolicy folds config rows (oldest first) into a policy. Values that do not
// parse are ignored, so a bad hand edit through the generic config screen falls back cleanly.
func resolveUpgradeTrialPolicy(rows []*ent.ServiceConfig) UpgradeTrialPolicy {
	p := UpgradeTrialPolicy{MaxDays: MaxUpgradeTrialDays}
	for _, row := range rows {
		level := &p.Platform
		if row.TenantID != nil {
			level = &p.Tenant
		}
		updated := row.UpdatedAt
		switch row.ConfigKey {
		case UpgradeTrialEnabledConfigKey:
			if v, ok := parseConfigBool(row.ConfigValue); ok {
				level.Enabled = &v
				level.UpdatedAt = &updated
			}
		case UpgradeTrialDaysConfigKey:
			if v, ok := parseConfigDays(row.ConfigValue); ok {
				level.Days = &v
				level.UpdatedAt = &updated
			}
		}
	}

	p.Enabled, p.EnabledSource = false, PolicySourceDefault
	switch {
	case p.Tenant.Enabled != nil:
		p.Enabled, p.EnabledSource = *p.Tenant.Enabled, PolicySourceTenant
	case p.Platform.Enabled != nil:
		p.Enabled, p.EnabledSource = *p.Platform.Enabled, PolicySourcePlatform
	}
	p.Days, p.DaysSource = DefaultUpgradeTrialDays, PolicySourceDefault
	switch {
	case p.Tenant.Days != nil:
		p.Days, p.DaysSource = *p.Tenant.Days, PolicySourceTenant
	case p.Platform.Days != nil:
		p.Days, p.DaysSource = *p.Platform.Days, PolicySourcePlatform
	}
	return p
}

func parseConfigBool(raw string) (bool, bool) {
	v, err := strconv.ParseBool(strings.Trim(strings.TrimSpace(raw), `"`))
	return v, err == nil
}

func parseConfigDays(raw string) (int, bool) {
	v, err := strconv.Atoi(strings.Trim(strings.TrimSpace(raw), `"`))
	if err != nil || ValidateUpgradeTrialDays(v) != nil {
		return 0, false
	}
	return v, true
}

// SetPlatformUpgradeTrialPolicy stores the platform-wide switch and trial length.
func (s *Service) SetPlatformUpgradeTrialPolicy(ctx context.Context, enabled bool, days int) (UpgradeTrialPolicy, error) {
	if err := ValidateUpgradeTrialDays(days); err != nil {
		return UpgradeTrialPolicy{}, err
	}
	if err := s.putConfig(ctx, nil, UpgradeTrialEnabledConfigKey, strconv.FormatBool(enabled), "bool",
		"Whether tenants can start a free trial of a higher plan tier before paying for it"); err != nil {
		return UpgradeTrialPolicy{}, err
	}
	if err := s.putConfig(ctx, nil, UpgradeTrialDaysConfigKey, strconv.Itoa(days), "int",
		"Length in days of a free trial of a higher plan tier"); err != nil {
		return UpgradeTrialPolicy{}, err
	}
	return s.UpgradeTrialPolicyFor(ctx, uuid.Nil)
}

// SetTenantUpgradeTrialPolicy stores a tenant's own switch and trial length. A nil field removes
// that override, so the tenant follows the platform setting again.
func (s *Service) SetTenantUpgradeTrialPolicy(ctx context.Context, tenantID uuid.UUID, in UpgradeTrialSetting) (UpgradeTrialPolicy, error) {
	if tenantID == uuid.Nil {
		return UpgradeTrialPolicy{}, fmt.Errorf("tenant_id is required")
	}
	if in.Days != nil {
		if err := ValidateUpgradeTrialDays(*in.Days); err != nil {
			return UpgradeTrialPolicy{}, err
		}
	}
	tid := tenantID
	if in.Enabled != nil {
		if err := s.putConfig(ctx, &tid, UpgradeTrialEnabledConfigKey, strconv.FormatBool(*in.Enabled), "bool",
			"Tenant override: whether this tenant can start upgrade trials"); err != nil {
			return UpgradeTrialPolicy{}, err
		}
	} else if err := s.deleteConfig(ctx, tid, UpgradeTrialEnabledConfigKey); err != nil {
		return UpgradeTrialPolicy{}, err
	}
	if in.Days != nil {
		if err := s.putConfig(ctx, &tid, UpgradeTrialDaysConfigKey, strconv.Itoa(*in.Days), "int",
			"Tenant override: upgrade trial length in days"); err != nil {
			return UpgradeTrialPolicy{}, err
		}
	} else if err := s.deleteConfig(ctx, tid, UpgradeTrialDaysConfigKey); err != nil {
		return UpgradeTrialPolicy{}, err
	}
	return s.UpgradeTrialPolicyFor(ctx, tenantID)
}

// putConfig updates every row for (tenant, key), or creates one when none exists. Updating all
// matches keeps accidental duplicate platform rows (NULL tenant_id is not unique in Postgres) in
// agreement.
func (s *Service) putConfig(ctx context.Context, tenantID *uuid.UUID, key, value, typ, desc string) error {
	scope := serviceconfig.TenantIDIsNil()
	if tenantID != nil {
		scope = serviceconfig.TenantIDEQ(*tenantID)
	}
	n, err := s.client.ServiceConfig.Update().
		Where(serviceconfig.ConfigKeyEQ(key), scope).
		SetConfigValue(value).
		SetConfigType(typ).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("store %s: %w", key, err)
	}
	if n > 0 {
		return nil
	}
	create := s.client.ServiceConfig.Create().
		SetConfigKey(key).
		SetConfigValue(value).
		SetConfigType(typ).
		SetDescription(desc)
	if tenantID != nil {
		create.SetTenantID(*tenantID)
	}
	if err := create.Exec(ctx); err != nil {
		return fmt.Errorf("store %s: %w", key, err)
	}
	return nil
}

func (s *Service) deleteConfig(ctx context.Context, tenantID uuid.UUID, key string) error {
	if _, err := s.client.ServiceConfig.Delete().
		Where(serviceconfig.ConfigKeyEQ(key), serviceconfig.TenantIDEQ(tenantID)).
		Exec(ctx); err != nil {
		return fmt.Errorf("clear %s: %w", key, err)
	}
	return nil
}
