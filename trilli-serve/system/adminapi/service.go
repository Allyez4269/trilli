// Package adminapi is the app-side operator surface (/api/admin/*) that Trilli
// CMX (cmx.trilli.com) calls to perform tenant MUTATIONS. Per the locked Option
// C integration model, CMX reads the database directly but every write goes
// through these endpoints so it can't bypass the app's invariants (quota,
// lifecycle, status, audit).
//
// Authentication is a SERVICE PRINCIPAL, not an app user session: operators
// live in CMX's own tables, so the app trusts CMX as a privileged caller via a
// shared service token (held in the encrypted credentials vault). Each request
// also carries the acting operator's identity so the app can attribute the
// action in its logs; CMX keeps the authoritative operator-action audit.
package adminapi

import (
	"context"
	"errors"
	"fmt"

	"trilli/system/database/postgres"
	"trilli/system/logging"
)

const packageName = "adminapi"

// ErrNotFound is returned when the target tenant does not exist.
var ErrNotFound = errors.New("adminapi: tenant not found")

// Service performs the guarded tenant mutations.
type Service struct {
	client *postgres.Client
}

// NewService constructs the adminapi Service.
func NewService(c *postgres.Client) *Service { return &Service{client: c} }

// SuspendTenant sets a tenant to 'suspended'. Membership resolution already
// gates on tenants.status='active' (system/auth), so a suspended tenant's users
// lose access on their next request. Reversible via Reactivate. Returns the
// resulting status.
func (s *Service) SuspendTenant(ctx context.Context, tenantID int64) (string, error) {
	return s.setStatus(ctx, tenantID, "suspended")
}

// ReactivateTenant returns a suspended tenant to 'active'.
func (s *Service) ReactivateTenant(ctx context.Context, tenantID int64) (string, error) {
	return s.setStatus(ctx, tenantID, "active")
}

func (s *Service) setStatus(ctx context.Context, tenantID int64, status string) (string, error) {
	res, err := s.client.ExecContext(ctx,
		`UPDATE tenants SET status = $2, updated_at = NOW() WHERE id = $1`, tenantID, status)
	if err != nil {
		return "", fmt.Errorf("adminapi: set status: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return "", ErrNotFound
	}
	logging.Info(packageName, "tenant %d status -> %s", tenantID, status)
	return status, nil
}

// SetQuotaOverride sets (or clears, when bytes is nil) a tenant's storage-cap
// override. The capacity checks in system/files and system/workspaces honor it
// via COALESCE(t.quota_override_bytes, p.max_storage_bytes, ...). Returns the
// effective override (nil = cleared / use plan).
func (s *Service) SetQuotaOverride(ctx context.Context, tenantID int64, bytes *int64) (*int64, error) {
	if bytes != nil && *bytes < 0 {
		return nil, fmt.Errorf("adminapi: quota override must be >= 0")
	}
	var arg any
	if bytes != nil {
		arg = *bytes
	}
	res, err := s.client.ExecContext(ctx,
		`UPDATE tenants SET quota_override_bytes = $2, updated_at = NOW() WHERE id = $1`, tenantID, arg)
	if err != nil {
		return nil, fmt.Errorf("adminapi: set quota override: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrNotFound
	}
	logging.Info(packageName, "tenant %d quota_override_bytes updated", tenantID)
	return bytes, nil
}
