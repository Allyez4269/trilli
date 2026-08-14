package operators

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrNotFound is returned when an operator or session does not exist.
var ErrNotFound = errors.New("operators: not found")

// operatorColumns is the canonical SELECT list for an Operator row.
const operatorColumns = `id, email, name, role, status, failed_login_count,
	locked_at, geofence_enabled, last_login_at, created_by, created_at, updated_at`

func scanOperator(row interface{ Scan(...any) error }) (*Operator, error) {
	var o Operator
	var role, status string
	var locked, lastLogin sql.NullTime
	var createdBy sql.NullInt64
	if err := row.Scan(
		&o.ID, &o.Email, &o.Name, &role, &status, &o.FailedLoginCount,
		&locked, &o.GeofenceEnabled, &lastLogin, &createdBy, &o.CreatedAt, &o.UpdatedAt,
	); err != nil {
		return nil, err
	}
	o.Role = Role(role)
	o.Status = Status(status)
	if locked.Valid {
		o.LockedAt = &locked.Time
	}
	if lastLogin.Valid {
		o.LastLoginAt = &lastLogin.Time
	}
	if createdBy.Valid {
		o.CreatedBy = &createdBy.Int64
	}
	return &o, nil
}

// GetByEmail loads an operator by (case-insensitive) email.
func (s *Service) GetByEmail(ctx context.Context, email string) (*Operator, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+operatorColumns+` FROM cmx_operators WHERE lower(email) = lower($1)`,
		strings.TrimSpace(email),
	)
	o, err := scanOperator(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("operators: get by email: %w", err)
	}
	return o, nil
}

// GetByID loads an operator by id.
func (s *Service) GetByID(ctx context.Context, id int64) (*Operator, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+operatorColumns+` FROM cmx_operators WHERE id = $1`, id)
	o, err := scanOperator(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("operators: get by id: %w", err)
	}
	return o, nil
}

// CreateInput captures the fields needed to provision an operator.
type CreateInput struct {
	Email     string
	Name      string
	Password  string
	Role      Role
	CreatedBy *int64
}

// Create provisions a new operator with an Argon2id password hash. The caller
// is responsible for authorization (Global-only) and for the operator then
// enrolling 2FA on first login (mandatory, SPEC §6.9).
func (s *Service) Create(ctx context.Context, in CreateInput) (*Operator, error) {
	in.Email = strings.TrimSpace(strings.ToLower(in.Email))
	if in.Email == "" {
		return nil, fmt.Errorf("operators: email required")
	}
	if !in.Role.Valid() {
		return nil, fmt.Errorf("operators: invalid role %q", in.Role)
	}
	hash, err := s.hashPassword(in.Password)
	if err != nil {
		return nil, err
	}
	var id int64
	err = s.db.QueryRowContext(ctx, `
		INSERT INTO cmx_operators (email, name, password_hash, role, created_by)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id`,
		in.Email, in.Name, hash, string(in.Role), in.CreatedBy,
	).Scan(&id)
	if err != nil {
		return nil, fmt.Errorf("operators: create: %w", err)
	}
	return s.GetByID(ctx, id)
}

// passwordHash fetches an operator's stored Argon2id hash for the login path
// only. Kept off the Operator struct so it never serializes to a client.
func (s *Service) passwordHash(ctx context.Context, operatorID int64) (string, error) {
	var hash string
	err := s.db.QueryRowContext(ctx,
		`SELECT password_hash FROM cmx_operators WHERE id = $1`, operatorID,
	).Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("operators: password hash: %w", err)
	}
	return hash, nil
}

// CountActiveGlobals returns how many active Global admins exist. Used to
// enforce the ≥2-Global invariant (SPEC §6.9) before demote/suspend/delete.
func (s *Service) CountActiveGlobals(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT count(*) FROM cmx_operators WHERE role = 'global' AND status = 'active'`,
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("operators: count globals: %w", err)
	}
	return n, nil
}

// recordFailedLogin increments the strike counter and locks at the threshold.
// Returns the post-update operator so callers can see whether it locked.
func (s *Service) recordFailedLogin(ctx context.Context, operatorID int64) (*Operator, error) {
	_, err := s.db.ExecContext(ctx, `
		UPDATE cmx_operators
		   SET failed_login_count = failed_login_count + 1,
		       status   = CASE WHEN failed_login_count + 1 >= $2 THEN 'locked' ELSE status END,
		       locked_at = CASE WHEN failed_login_count + 1 >= $2 THEN NOW() ELSE locked_at END,
		       updated_at = NOW()
		 WHERE id = $1 AND status <> 'locked'`,
		operatorID, MaxFailedLogins,
	)
	if err != nil {
		return nil, fmt.Errorf("operators: record failed login: %w", err)
	}
	return s.GetByID(ctx, operatorID)
}

// clearFailedLogins resets the strike counter and stamps last_login_at.
func (s *Service) clearFailedLogins(ctx context.Context, operatorID int64) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE cmx_operators
		   SET failed_login_count = 0, last_login_at = NOW(), updated_at = NOW()
		 WHERE id = $1`, operatorID)
	if err != nil {
		return fmt.Errorf("operators: clear failed logins: %w", err)
	}
	return nil
}

// Unlock clears a 3-strike lockout (Global-admin action, SPEC §6.9). No
// time-based auto-unlock exists by design.
func (s *Service) Unlock(ctx context.Context, operatorID int64) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE cmx_operators
		   SET status = 'active', failed_login_count = 0, locked_at = NULL, updated_at = NOW()
		 WHERE id = $1 AND status = 'locked'`, operatorID)
	if err != nil {
		return fmt.Errorf("operators: unlock: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- Geofence rules -------------------------------------------------------

// geofenceRules loads an operator's allowed-region rules.
func (s *Service) geofenceRules(ctx context.Context, operatorID int64) ([]GeofenceRule, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT region_type, region_code FROM cmx_operator_geofences WHERE operator_id = $1`,
		operatorID)
	if err != nil {
		return nil, fmt.Errorf("operators: load geofences: %w", err)
	}
	defer rows.Close()
	var out []GeofenceRule
	for rows.Next() {
		var r GeofenceRule
		if err := rows.Scan(&r.RegionType, &r.RegionCode); err != nil {
			return nil, fmt.Errorf("operators: scan geofence: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ---- Sessions -------------------------------------------------------------

func newSessionToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// createSession mints a session with a 12-hour hard cap and a fresh step-up
// stamp (login itself counts as a step-up).
func (s *Service) createSession(ctx context.Context, operatorID int64, ip string, geo Geo, ua string) (*Session, error) {
	id, err := newSessionToken()
	if err != nil {
		return nil, fmt.Errorf("operators: session token: %w", err)
	}
	now := time.Now().UTC()
	expires := now.Add(MaxSessionLifetime)
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO cmx_operator_sessions
			(id, operator_id, ip, continent_code, country_code, region, user_agent,
			 step_up_at, created_at, last_seen_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8, $8, $9)`,
		id, operatorID, ip, geo.ContinentCode, geo.CountryCode, geo.Region, ua, now, expires,
	)
	if err != nil {
		return nil, fmt.Errorf("operators: create session: %w", err)
	}
	return &Session{
		ID: id, OperatorID: operatorID, IP: ip,
		ContinentCode: geo.ContinentCode, CountryCode: geo.CountryCode, Region: geo.Region,
		UserAgent: ua, StepUpAt: &now, CreatedAt: now, LastSeenAt: now, ExpiresAt: expires,
	}, nil
}

// GetSession loads a session by token.
func (s *Service) GetSession(ctx context.Context, id string) (*Session, error) {
	if id == "" {
		return nil, ErrNotFound
	}
	var sess Session
	var stepUp, revoked sql.NullTime
	err := s.db.QueryRowContext(ctx, `
		SELECT id, operator_id, ip, continent_code, country_code, region, user_agent,
		       step_up_at, created_at, last_seen_at, expires_at, revoked_at
		  FROM cmx_operator_sessions WHERE id = $1`, id,
	).Scan(&sess.ID, &sess.OperatorID, &sess.IP, &sess.ContinentCode, &sess.CountryCode,
		&sess.Region, &sess.UserAgent, &stepUp, &sess.CreatedAt, &sess.LastSeenAt,
		&sess.ExpiresAt, &revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("operators: get session: %w", err)
	}
	if stepUp.Valid {
		sess.StepUpAt = &stepUp.Time
	}
	if revoked.Valid {
		sess.RevokedAt = &revoked.Time
	}
	return &sess, nil
}

// touchSession bumps last_seen_at (the idle anchor). It never extends
// expires_at — the 12-hour hard cap is fixed at creation.
func (s *Service) touchSession(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE cmx_operator_sessions SET last_seen_at = NOW() WHERE id = $1 AND revoked_at IS NULL`, id)
	if err != nil {
		return fmt.Errorf("operators: touch session: %w", err)
	}
	return nil
}

// markStepUp stamps a fresh step-up confirmation on a session.
func (s *Service) markStepUp(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE cmx_operator_sessions SET step_up_at = NOW() WHERE id = $1 AND revoked_at IS NULL`, id)
	if err != nil {
		return fmt.Errorf("operators: mark step-up: %w", err)
	}
	return nil
}

// RevokeSession revokes a single session (logout).
func (s *Service) RevokeSession(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE cmx_operator_sessions SET revoked_at = NOW() WHERE id = $1 AND revoked_at IS NULL`, id)
	if err != nil {
		return fmt.Errorf("operators: revoke session: %w", err)
	}
	return nil
}

// RevokeAllSessions force-logs-out an operator everywhere.
func (s *Service) RevokeAllSessions(ctx context.Context, operatorID int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE cmx_operator_sessions SET revoked_at = NOW() WHERE operator_id = $1 AND revoked_at IS NULL`,
		operatorID)
	if err != nil {
		return fmt.Errorf("operators: revoke all sessions: %w", err)
	}
	return nil
}

// ---- Login events (append-only audit) -------------------------------------

// LoginContext carries the request metadata recorded on every login attempt.
type LoginContext struct {
	IP        string
	Geo       Geo
	UserAgent string
}

// recordLoginEvent appends to cmx_login_events. Best-effort: a logging failure
// never blocks the auth decision (but is surfaced in the service log).
func (s *Service) recordLoginEvent(ctx context.Context, operatorID *int64, email string, outcome LoginOutcome, lc LoginContext) {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO cmx_login_events
			(operator_id, email_attempted, outcome, ip, continent_code, country_code, region, user_agent)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		operatorID, email, string(outcome), lc.IP,
		lc.Geo.ContinentCode, lc.Geo.CountryCode, lc.Geo.Region, lc.UserAgent,
	)
	if err != nil {
		// Don't fail the request on an audit write error; just log it.
		logfailLoginEvent(email, outcome, err)
	}
}
