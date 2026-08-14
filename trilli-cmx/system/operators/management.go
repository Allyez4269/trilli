package operators

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// management.go holds the Administration operator-management mutations (SPEC
// §6.7): list/role/status/unlock/password. The ≥2-active-Global invariant (SPEC
// §6.9) is enforced before any demote/suspend so the platform can never lock
// itself out of its super-admin tier — a locked-out or 2FA-lost Global admin
// must always have another Global to recover them.

// ErrLastGlobal is returned when an action would drop the active-Global count
// below the required minimum of two (SPEC §6.9).
var ErrLastGlobal = fmt.Errorf("operators: at least two active Global admins must remain")

// List returns every operator, Globals first then by email.
func (s *Service) List(ctx context.Context) ([]*Operator, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+operatorColumns+` FROM cmx_operators ORDER BY (role = 'global') DESC, lower(email) ASC`)
	if err != nil {
		return nil, fmt.Errorf("operators: list: %w", err)
	}
	defer rows.Close()
	var out []*Operator
	for rows.Next() {
		o, err := scanOperator(rows)
		if err != nil {
			return nil, fmt.Errorf("operators: scan: %w", err)
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// wouldStrandGlobals reports whether changing operator `id` away from an active
// Global (demote or suspend) would drop the active-Global count below
// MinActiveGlobals (SPEC §6.9: at least two Globals must always remain so one
// can always recover another).
func (s *Service) wouldStrandGlobals(ctx context.Context, id int64) (bool, error) {
	cur, err := s.GetByID(ctx, id)
	if err != nil {
		return false, err
	}
	if cur.Role != RoleGlobal || cur.Status != StatusActive {
		return false, nil // not an active Global → removing it strands nothing
	}
	n, err := s.CountActiveGlobals(ctx)
	if err != nil {
		return false, err
	}
	// Block when removing this one would leave fewer than MinActiveGlobals.
	return n-1 < MinActiveGlobals, nil
}

// SetRole changes an operator's role, guarding the MinActiveGlobals invariant.
func (s *Service) SetRole(ctx context.Context, id int64, role Role) error {
	if !role.Valid() {
		return fmt.Errorf("operators: invalid role %q", role)
	}
	if role != RoleGlobal {
		stranded, err := s.wouldStrandGlobals(ctx, id)
		if err != nil {
			return err
		}
		if stranded {
			return ErrLastGlobal
		}
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE cmx_operators SET role = $2, updated_at = NOW() WHERE id = $1`, id, string(role))
	if err != nil {
		return fmt.Errorf("operators: set role: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetStatus activates or suspends an operator, guarding the MinActiveGlobals
// invariant. Use Unlock to clear a 3-strike lockout. Only 'active'/'suspended'
// are settable here ('locked' is reached via failed logins).
func (s *Service) SetStatus(ctx context.Context, id int64, status Status) error {
	if status != StatusActive && status != StatusSuspended {
		return fmt.Errorf("operators: status must be active or suspended")
	}
	if status != StatusActive {
		stranded, err := s.wouldStrandGlobals(ctx, id)
		if err != nil {
			return err
		}
		if stranded {
			return ErrLastGlobal
		}
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE cmx_operators SET status = $2, locked_at = NULL, failed_login_count = 0, updated_at = NOW() WHERE id = $1`,
		id, string(status))
	if err != nil {
		return fmt.Errorf("operators: set status: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetProfile updates an operator's display name and email (self-service). Email
// is normalized and must be unique across operators.
func (s *Service) SetProfile(ctx context.Context, id int64, name, email string) error {
	email = strings.TrimSpace(strings.ToLower(email))
	name = strings.TrimSpace(name)
	if email == "" || !strings.Contains(email, "@") {
		return fmt.Errorf("a valid email is required")
	}
	var other int64
	err := s.db.QueryRowContext(ctx,
		`SELECT id FROM cmx_operators WHERE lower(email) = $1 AND id <> $2`, email, id).Scan(&other)
	if err == nil {
		return fmt.Errorf("that email is already in use")
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("operators: profile email check: %w", err)
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE cmx_operators SET name = $2, email = $3, updated_at = NOW() WHERE id = $1`, id, name, email)
	if err != nil {
		return fmt.Errorf("operators: set profile: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetPassword re-hashes and stores a new password for an operator and revokes
// all of that operator's sessions (so a reset forces re-login). Enforces the
// minimum length.
func (s *Service) SetPassword(ctx context.Context, id int64, newPassword string) error {
	// hashPassword (auth.HashPassword) enforces the MinPasswordLen floor.
	hash, err := s.hashPassword(newPassword)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE cmx_operators SET password_hash = $2, updated_at = NOW() WHERE id = $1`, id, hash)
	if err != nil {
		return fmt.Errorf("operators: set password: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	if err := s.RevokeAllSessions(ctx, id); err != nil {
		return fmt.Errorf("operators: revoke sessions after password change: %w", err)
	}
	return nil
}
