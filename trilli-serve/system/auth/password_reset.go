package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"trilli/system/logging"
)

// passwordResetTTL is how long a reset link stays valid. Short on purpose — the
// user requests it and clicks straight through.
const passwordResetTTL = 30 * time.Minute

// Password-reset sentinels. Handlers map these to HTTP statuses.
var (
	ErrPasswordResetToken   = errors.New("auth: this reset link is invalid or has already been used")
	ErrPasswordResetExpired = errors.New("auth: this reset link has expired")
)

// PasswordResetRequest is a pending reset — enough to build + send the email.
type PasswordResetRequest struct {
	UserID    int64
	Email     string // the account's current email (where the link is sent)
	Token     string
	ExpiresAt time.Time
}

// CreatePasswordReset records a single-use reset token for a user (superseding
// any earlier pending one) and returns it plus the address to mail. Step-up
// verification (2FA / passkey) is the caller's responsibility — by the time we
// get here, identity is already proven.
func (s *Service) CreatePasswordReset(ctx context.Context, userID int64) (*PasswordResetRequest, error) {
	var email string
	if err := s.client.QueryRowContext(ctx,
		`SELECT email FROM users WHERE id = $1`, userID,
	).Scan(&email); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrUnauthorized
		}
		return nil, fmt.Errorf("auth: load user for reset: %w", err)
	}

	token, err := newPasswordResetToken()
	if err != nil {
		return nil, err
	}
	expiresAt := time.Now().UTC().Add(passwordResetTTL)

	// Don't cancel earlier pending links — several can be outstanding at once so
	// clicking ANY unexpired email works. The leftovers are invalidated when one
	// is used (see ResetPassword) or simply expire after their TTL.
	if _, err := s.client.ExecContext(ctx,
		`INSERT INTO password_reset_requests (user_id, token, expires_at) VALUES ($1, $2, $3)`,
		userID, token, expiresAt,
	); err != nil {
		return nil, fmt.Errorf("auth: insert reset: %w", err)
	}

	logging.Info(packageName, "CreatePasswordReset: user=%d", userID)
	return &PasswordResetRequest{UserID: userID, Email: email, Token: token, ExpiresAt: expiresAt}, nil
}

// LookupPasswordReset resolves a pending token so the set-password page can
// confirm the link is good (and show whose account it's for). Marks an expired
// pending row as expired. Returns the sentinels for anything not resettable.
func (s *Service) LookupPasswordReset(ctx context.Context, token string) (*PasswordResetRequest, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, ErrPasswordResetToken
	}
	var (
		req       PasswordResetRequest
		status    string
		expiresAt time.Time
	)
	err := s.client.QueryRowContext(ctx, `
		SELECT pr.user_id, u.email, pr.status, pr.expires_at
		  FROM password_reset_requests pr
		  JOIN users u ON u.id = pr.user_id
		 WHERE pr.token = $1`, token,
	).Scan(&req.UserID, &req.Email, &status, &expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrPasswordResetToken
	}
	if err != nil {
		return nil, fmt.Errorf("auth: lookup reset: %w", err)
	}
	if status != "pending" {
		return nil, ErrPasswordResetToken
	}
	if time.Now().After(expiresAt) {
		_, _ = s.client.ExecContext(ctx,
			`UPDATE password_reset_requests SET status = 'expired' WHERE token = $1 AND status = 'pending'`, token)
		return nil, ErrPasswordResetExpired
	}
	req.Token = token
	req.ExpiresAt = expiresAt
	return &req, nil
}

// ResetPassword consumes a pending token and sets a new password in one
// transaction (single-use: token -> 'used'). On success it revokes every
// session for the user — a password change signs them out everywhere.
func (s *Service) ResetPassword(ctx context.Context, token, newPassword string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return ErrPasswordResetToken
	}
	if len(newPassword) < MinPasswordLen {
		return ErrPasswordTooShort
	}
	hash, err := HashPassword(newPassword)
	if err != nil {
		return err
	}

	tx, err := s.client.GetDB().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("auth: begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	var (
		reqID     int64
		userID    int64
		status    string
		expiresAt time.Time
	)
	err = tx.QueryRowContext(ctx,
		`SELECT id, user_id, status, expires_at FROM password_reset_requests WHERE token = $1 FOR UPDATE`, token,
	).Scan(&reqID, &userID, &status, &expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrPasswordResetToken
	}
	if err != nil {
		return fmt.Errorf("auth: load reset for apply: %w", err)
	}
	if status != "pending" {
		return ErrPasswordResetToken
	}
	if time.Now().After(expiresAt) {
		if _, err := tx.ExecContext(ctx,
			`UPDATE password_reset_requests SET status = 'expired' WHERE id = $1`, reqID); err == nil {
			_ = tx.Commit()
			committed = true
		}
		return ErrPasswordResetExpired
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE users SET password_hash = $1 WHERE id = $2`, hash, userID); err != nil {
		return fmt.Errorf("auth: apply new password: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE password_reset_requests SET status = 'used', used_at = NOW() WHERE id = $1`, reqID); err != nil {
		return fmt.Errorf("auth: mark reset used: %w", err)
	}
	// Burn any other still-pending links for this user — once the password is
	// changed, the other emailed links must stop working.
	if _, err := tx.ExecContext(ctx,
		`UPDATE password_reset_requests SET status = 'cancelled' WHERE user_id = $1 AND status = 'pending' AND id <> $2`,
		userID, reqID); err != nil {
		return fmt.Errorf("auth: cancel sibling resets: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("auth: commit reset: %w", err)
	}
	committed = true

	// Sign out everywhere — a password change invalidates all live sessions.
	if err := s.sessionStore.RevokeAllForUser(ctx, userID); err != nil {
		logging.Error(packageName, "ResetPassword: revoke sessions failed for user %d: %v", userID, err)
	}
	logging.Info(packageName, "ResetPassword: user=%d password changed", userID)
	return nil
}

// CreatePasswordResetForEmail is the logged-OUT entry point ("Forgot your
// password?" at login). It resolves an active user by email and issues a reset
// token. Callers MUST NOT leak whether the email existed — on a miss this
// returns ErrUnauthorized, which the handler swallows into a uniform success
// response (anti-enumeration). No step-up here: possession of the email inbox
// is the factor, and 2FA still gates the eventual login.
func (s *Service) CreatePasswordResetForEmail(ctx context.Context, email string) (*PasswordResetRequest, error) {
	email = normalizeEmail(email)
	if !emailRE.MatchString(email) {
		return nil, ErrUnauthorized
	}
	var userID int64
	err := s.client.QueryRowContext(ctx,
		`SELECT id FROM users WHERE email = $1 AND status = 'active'`, email,
	).Scan(&userID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrUnauthorized
	}
	if err != nil {
		return nil, fmt.Errorf("auth: lookup user for forgot-password: %w", err)
	}
	return s.CreatePasswordReset(ctx, userID)
}

func newPasswordResetToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("auth: reset token: %w", err)
	}
	return hex.EncodeToString(buf), nil
}
