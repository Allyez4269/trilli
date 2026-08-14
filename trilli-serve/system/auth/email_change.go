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

// emailChangeTTL is how long a confirmation link stays valid. Deliberately
// short — the user is expected to click the link right after requesting it.
const emailChangeTTL = 30 * time.Minute

// Email-change sentinels. Handlers map these to HTTP statuses; the SPA shows
// the matching message.
var (
	ErrSameEmail          = errors.New("auth: that's already your current email")
	ErrEmailChangeToken   = errors.New("auth: this confirmation link is invalid or has already been used")
	ErrEmailChangeExpired = errors.New("auth: this confirmation link has expired")
)

// EmailChangeRequest is a pending email-change record — enough for the handler
// to build + send the confirmation email and to confirm it later.
type EmailChangeRequest struct {
	UserID    int64
	NewEmail  string
	Token     string
	ExpiresAt time.Time
}

// RequestEmailChange verifies the caller's password, checks the new address is
// valid and not already in use, then records a single-use pending change with a
// 30-minute TTL — superseding any earlier pending request for the user. It does
// NOT change the email; that happens only when the emailed link is confirmed.
func (s *Service) RequestEmailChange(ctx context.Context, userID int64, newEmail, password string) (*EmailChangeRequest, error) {
	newEmail = normalizeEmail(newEmail)
	if !emailRE.MatchString(newEmail) {
		return nil, ErrEmailInvalid
	}

	var currentEmail, hash string
	err := s.client.QueryRowContext(ctx,
		`SELECT email, password_hash FROM users WHERE id = $1`, userID,
	).Scan(&currentEmail, &hash)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrUnauthorized
		}
		return nil, fmt.Errorf("auth: load user for email change: %w", err)
	}
	if strings.EqualFold(strings.TrimSpace(currentEmail), newEmail) {
		return nil, ErrSameEmail
	}

	ok, err := VerifyPassword(hash, password)
	if err != nil {
		return nil, fmt.Errorf("auth: verify password: %w", err)
	}
	if !ok {
		return nil, ErrInvalidCredentials
	}

	// The new address must not already belong to another account.
	var taken bool
	if err := s.client.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM users WHERE lower(email) = $1 AND id <> $2)`,
		newEmail, userID,
	).Scan(&taken); err != nil {
		return nil, fmt.Errorf("auth: email-taken check: %w", err)
	}
	if taken {
		return nil, ErrEmailTaken
	}

	token, err := newEmailChangeToken()
	if err != nil {
		return nil, err
	}
	expiresAt := time.Now().UTC().Add(emailChangeTTL)

	tx, err := s.client.GetDB().BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("auth: begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if _, err := tx.ExecContext(ctx,
		`UPDATE email_change_requests SET status = 'cancelled' WHERE user_id = $1 AND status = 'pending'`,
		userID,
	); err != nil {
		return nil, fmt.Errorf("auth: supersede pending email change: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO email_change_requests (user_id, new_email, token, expires_at) VALUES ($1, $2, $3, $4)`,
		userID, newEmail, token, expiresAt,
	); err != nil {
		return nil, fmt.Errorf("auth: insert email change: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("auth: commit email change: %w", err)
	}
	committed = true

	logging.Info(packageName, "RequestEmailChange: user=%d -> %s", userID, newEmail)
	return &EmailChangeRequest{UserID: userID, NewEmail: newEmail, Token: token, ExpiresAt: expiresAt}, nil
}

// LookupEmailChange resolves a pending request by token so the confirm page can
// show which address is being confirmed. Opportunistically marks an expired
// pending row as expired. Returns ErrEmailChangeToken / ErrEmailChangeExpired
// for anything not currently confirmable.
func (s *Service) LookupEmailChange(ctx context.Context, token string) (*EmailChangeRequest, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, ErrEmailChangeToken
	}
	var (
		req    EmailChangeRequest
		status string
	)
	err := s.client.QueryRowContext(ctx,
		`SELECT user_id, new_email, token, status, expires_at FROM email_change_requests WHERE token = $1`, token,
	).Scan(&req.UserID, &req.NewEmail, &req.Token, &status, &req.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrEmailChangeToken
	}
	if err != nil {
		return nil, fmt.Errorf("auth: lookup email change: %w", err)
	}
	if status != "pending" {
		return nil, ErrEmailChangeToken
	}
	if time.Now().After(req.ExpiresAt) {
		_, _ = s.client.ExecContext(ctx,
			`UPDATE email_change_requests SET status = 'expired' WHERE token = $1 AND status = 'pending'`, token)
		return nil, ErrEmailChangeExpired
	}
	return &req, nil
}

// ConfirmEmailChange consumes a pending token and applies the new email
// permanently, in one transaction. Single-use: the row flips to 'confirmed'.
// Re-checks uniqueness at apply time (someone could have taken the address in
// the meantime). Returns the new email on success.
func (s *Service) ConfirmEmailChange(ctx context.Context, token string) (string, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return "", ErrEmailChangeToken
	}

	tx, err := s.client.GetDB().BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("auth: begin tx: %w", err)
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
		newEmail  string
		status    string
		expiresAt time.Time
	)
	err = tx.QueryRowContext(ctx,
		`SELECT id, user_id, new_email, status, expires_at FROM email_change_requests WHERE token = $1 FOR UPDATE`, token,
	).Scan(&reqID, &userID, &newEmail, &status, &expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrEmailChangeToken
	}
	if err != nil {
		return "", fmt.Errorf("auth: load email change for confirm: %w", err)
	}
	if status == "confirmed" {
		// Already applied — treat a repeat click as success (idempotent).
		return newEmail, nil
	}
	if status != "pending" {
		return "", ErrEmailChangeToken
	}
	if time.Now().After(expiresAt) {
		if _, err := tx.ExecContext(ctx,
			`UPDATE email_change_requests SET status = 'expired' WHERE id = $1`, reqID); err == nil {
			_ = tx.Commit()
			committed = true
		}
		return "", ErrEmailChangeExpired
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE users SET email = $1 WHERE id = $2`, newEmail, userID); err != nil {
		if pgUniqueViolation(err) {
			return "", ErrEmailTaken
		}
		return "", fmt.Errorf("auth: apply email change: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE email_change_requests SET status = 'confirmed', confirmed_at = NOW() WHERE id = $1`, reqID); err != nil {
		return "", fmt.Errorf("auth: mark email change confirmed: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("auth: commit email change confirm: %w", err)
	}
	committed = true

	logging.Info(packageName, "ConfirmEmailChange: user=%d email=%s", userID, newEmail)
	return newEmail, nil
}

func newEmailChangeToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("auth: email change token: %w", err)
	}
	return hex.EncodeToString(buf), nil
}
