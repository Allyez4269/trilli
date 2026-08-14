// Package admin powers CMX's Administration section (SPEC §6.7, Global-only):
// the operator-action audit viewer (served from the operators service), a
// read-only credentials/vault inventory, and (later) operator management. The
// vault view never exposes secret material — only provider/key/env/last4 and
// status, read directly from service_credentials (Option C).
package admin

import (
	"context"
	"fmt"

	"trilli-cmx/system/database/postgres"
)

const packageName = "admin"

// Service runs Administration read queries.
type Service struct {
	db *postgres.Client
}

// NewService constructs the admin Service.
func NewService(db *postgres.Client) *Service { return &Service{db: db} }

// VaultEntry is a non-secret view of one stored credential.
type VaultEntry struct {
	ID          int64  `json:"id"`
	Provider    string `json:"provider"`
	KeyName     string `json:"key_name"`
	Environment string `json:"environment"` // test | live
	Last4       string `json:"last4"`
	IsActive    bool   `json:"is_active"`
	UpdatedAt   string `json:"updated_at"`
}

// ListVault returns the credentials inventory — metadata only, never the
// encrypted value. Ordered by provider/key/env for a stable display.
func (s *Service) ListVault(ctx context.Context) ([]VaultEntry, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, provider, key_name, environment, COALESCE(last4,''), is_active,
		       to_char(updated_at, 'YYYY-MM-DD"T"HH24:MI:SSOF')
		  FROM service_credentials
		 ORDER BY provider ASC, key_name ASC, environment ASC`)
	if err != nil {
		return nil, fmt.Errorf("admin: list vault: %w", err)
	}
	defer rows.Close()
	out := []VaultEntry{}
	for rows.Next() {
		var e VaultEntry
		if err := rows.Scan(&e.ID, &e.Provider, &e.KeyName, &e.Environment, &e.Last4, &e.IsActive, &e.UpdatedAt); err != nil {
			return nil, fmt.Errorf("admin: scan vault: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
