// Package twofactor implements TOTP (authenticator-app) two-factor auth.
//
// Enrollment is two-phase and lockout-safe: BeginSetup stores a PENDING secret
// (encrypted) and hands back a QR + otpauth URL; Confirm verifies a code from
// the user's app before flipping 2FA on and minting one-time recovery codes.
// Nothing is enforced at login until that wiring lands in a later stage.
package twofactor

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"image/png"
	"strings"
	"time"

	"github.com/pquerna/otp/totp"

	"trilli/system/crypto"
	"trilli/system/database/postgres"
	"trilli/system/logging"
)

const (
	packageName    = "twofactor"
	issuerName     = "Trilli"
	recoveryCount  = 10
	recoveryGroups = 2 // codes look like "abcd-efgh"
	recoveryBytes  = 4 // bytes per group → 8 hex chars total
)

// Sentinel errors mapped to HTTP statuses by the handlers.
var (
	ErrAlreadyEnabled = errors.New("twofactor: 2FA is already enabled")
	ErrNoPending      = errors.New("twofactor: no pending TOTP setup to confirm")
	ErrInvalidCode    = errors.New("twofactor: code is incorrect or expired")
	ErrNotEnabled     = errors.New("twofactor: 2FA is not enabled")
)

// Service wires the DB client for 2FA business logic.
type Service struct {
	client *postgres.Client
}

// NewService constructs a twofactor Service.
func NewService(c *postgres.Client) *Service {
	return &Service{client: c}
}

// Status summarizes a user's TOTP state for the Security UI.
type Status struct {
	Enabled           bool `json:"enabled"`            // confirmed + active
	Pending           bool `json:"pending"`            // setup started, not confirmed
	RecoveryRemaining int  `json:"recovery_remaining"` // unused recovery codes
}

// Setup is the payload the enrollment modal needs to show the QR step.
type Setup struct {
	Secret     string `json:"secret"`       // base32, for manual entry
	OtpauthURL string `json:"otpauth_url"`  // the otpauth:// URI encoded in the QR
	QRDataURL  string `json:"qr_data_url"`  // data:image/png;base64,... QR image
}

// GetStatus reports whether the user has TOTP enabled/pending + codes left.
func (s *Service) GetStatus(ctx context.Context, userID int64) (Status, error) {
	var confirmed sql.NullTime
	err := s.client.QueryRowContext(ctx,
		`SELECT confirmed_at FROM user_totp WHERE user_id = $1`, userID,
	).Scan(&confirmed)
	if errors.Is(err, sql.ErrNoRows) {
		return Status{}, nil
	}
	if err != nil {
		return Status{}, fmt.Errorf("twofactor: status: %w", err)
	}
	st := Status{Enabled: confirmed.Valid, Pending: !confirmed.Valid}
	if st.Enabled {
		if cerr := s.client.QueryRowContext(ctx,
			`SELECT count(*) FROM user_recovery_codes WHERE user_id = $1 AND used_at IS NULL`, userID,
		).Scan(&st.RecoveryRemaining); cerr != nil {
			return st, fmt.Errorf("twofactor: recovery count: %w", cerr)
		}
	}
	return st, nil
}

// BeginSetup generates a fresh secret, stores it as PENDING (encrypted), and
// returns the QR + otpauth URL. Refuses if 2FA is already enabled.
func (s *Service) BeginSetup(ctx context.Context, userID int64, accountEmail string) (*Setup, error) {
	var confirmed sql.NullTime
	err := s.client.QueryRowContext(ctx,
		`SELECT confirmed_at FROM user_totp WHERE user_id = $1`, userID,
	).Scan(&confirmed)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("twofactor: setup status: %w", err)
	}
	if err == nil && confirmed.Valid {
		return nil, ErrAlreadyEnabled
	}

	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      issuerName,
		AccountName: accountEmail,
	})
	if err != nil {
		return nil, fmt.Errorf("twofactor: generate key: %w", err)
	}

	enc, err := crypto.Encrypt([]byte(key.Secret()))
	if err != nil {
		return nil, fmt.Errorf("twofactor: encrypt secret: %w", err)
	}
	if _, err := s.client.ExecContext(ctx, `
		INSERT INTO user_totp (user_id, secret_encrypted, confirmed_at, last_used_step)
		VALUES ($1, $2, NULL, 0)
		ON CONFLICT (user_id) DO UPDATE
		   SET secret_encrypted = EXCLUDED.secret_encrypted, confirmed_at = NULL, last_used_step = 0`,
		userID, enc,
	); err != nil {
		return nil, fmt.Errorf("twofactor: store pending: %w", err)
	}

	img, err := key.Image(220, 220)
	if err != nil {
		return nil, fmt.Errorf("twofactor: qr image: %w", err)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("twofactor: qr encode: %w", err)
	}

	logging.Info(packageName, "BeginSetup: user=%d (pending)", userID)
	return &Setup{
		Secret:     key.Secret(),
		OtpauthURL: key.URL(),
		QRDataURL:  "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes()),
	}, nil
}

// Confirm verifies a code against the pending secret. On success it activates
// 2FA, mints fresh one-time recovery codes (returned once, stored hashed), and
// returns them. Returns ErrInvalidCode if the code doesn't match.
func (s *Service) Confirm(ctx context.Context, userID int64, code string) ([]string, error) {
	var enc []byte
	var confirmed sql.NullTime
	err := s.client.QueryRowContext(ctx,
		`SELECT secret_encrypted, confirmed_at FROM user_totp WHERE user_id = $1`, userID,
	).Scan(&enc, &confirmed)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoPending
	}
	if err != nil {
		return nil, fmt.Errorf("twofactor: load pending: %w", err)
	}
	if confirmed.Valid {
		return nil, ErrAlreadyEnabled
	}
	secret, err := crypto.Decrypt(enc)
	if err != nil {
		return nil, fmt.Errorf("twofactor: decrypt: %w", err)
	}
	if !totp.Validate(strings.TrimSpace(code), string(secret)) {
		return nil, ErrInvalidCode
	}

	codes, hashes, err := newRecoveryCodes()
	if err != nil {
		return nil, err
	}

	tx, err := s.client.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("twofactor: begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if _, err := tx.ExecContext(ctx,
		`UPDATE user_totp SET confirmed_at = NOW() WHERE user_id = $1`, userID,
	); err != nil {
		return nil, fmt.Errorf("twofactor: confirm: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM user_recovery_codes WHERE user_id = $1`, userID,
	); err != nil {
		return nil, fmt.Errorf("twofactor: clear codes: %w", err)
	}
	for _, h := range hashes {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO user_recovery_codes (user_id, code_hash) VALUES ($1, $2)`, userID, h,
		); err != nil {
			return nil, fmt.Errorf("twofactor: store code: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("twofactor: commit: %w", err)
	}
	committed = true

	logging.Info(packageName, "Confirm: user=%d 2FA enabled", userID)
	return codes, nil
}

// VerifyCode checks a login-time challenge for an enabled user: a 6-digit TOTP
// code (with a one-window replay guard via last_used_step) or a one-time
// recovery code. Returns ErrInvalidCode on any mismatch, ErrNotEnabled if the
// user has no active TOTP.
func (s *Service) VerifyCode(ctx context.Context, userID int64, code string) error {
	code = strings.TrimSpace(code)
	if !isSixDigits(code) {
		return s.verifyRecoveryCode(ctx, userID, code)
	}

	var enc []byte
	var confirmed sql.NullTime
	var lastStep int64
	err := s.client.QueryRowContext(ctx,
		`SELECT secret_encrypted, confirmed_at, last_used_step FROM user_totp WHERE user_id = $1`, userID,
	).Scan(&enc, &confirmed, &lastStep)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !confirmed.Valid) {
		return ErrNotEnabled
	}
	if err != nil {
		return fmt.Errorf("twofactor: verify load: %w", err)
	}
	secret, err := crypto.Decrypt(enc)
	if err != nil {
		return fmt.Errorf("twofactor: decrypt: %w", err)
	}
	step := time.Now().Unix() / 30
	if step <= lastStep {
		// A code from this window was already used — block replay.
		return ErrInvalidCode
	}
	if !totp.Validate(code, string(secret)) {
		return ErrInvalidCode
	}
	if _, err := s.client.ExecContext(ctx,
		`UPDATE user_totp SET last_used_step = $1 WHERE user_id = $2`, step, userID,
	); err != nil {
		return fmt.Errorf("twofactor: record step: %w", err)
	}
	return nil
}

// verifyRecoveryCode consumes a one-time recovery code (case/dash-insensitive).
func (s *Service) verifyRecoveryCode(ctx context.Context, userID int64, code string) error {
	res, err := s.client.ExecContext(ctx, `
		UPDATE user_recovery_codes SET used_at = NOW()
		 WHERE user_id = $1 AND code_hash = $2 AND used_at IS NULL`,
		userID, hashCode(code),
	)
	if err != nil {
		return fmt.Errorf("twofactor: recovery verify: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrInvalidCode
	}
	return nil
}

func isSixDigits(s string) bool {
	if len(s) != 6 {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// Disable turns off 2FA and removes the secret + recovery codes.
func (s *Service) Disable(ctx context.Context, userID int64) error {
	res, err := s.client.ExecContext(ctx, `DELETE FROM user_totp WHERE user_id = $1`, userID)
	if err != nil {
		return fmt.Errorf("twofactor: disable: %w", err)
	}
	if _, err := s.client.ExecContext(ctx,
		`DELETE FROM user_recovery_codes WHERE user_id = $1`, userID,
	); err != nil {
		return fmt.Errorf("twofactor: disable codes: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotEnabled
	}
	logging.Info(packageName, "Disable: user=%d", userID)
	return nil
}

// Replay-window enforcement (last_used_step) is added in the login-enforcement
// stage; enrollment confirm above is a one-time action so it isn't needed yet.

// newRecoveryCodes returns N human-friendly codes plus their SHA-256 hex hashes.
func newRecoveryCodes() (plain []string, hashes []string, err error) {
	for i := 0; i < recoveryCount; i++ {
		groups := make([]string, recoveryGroups)
		for g := range groups {
			b := make([]byte, recoveryBytes)
			if _, err = rand.Read(b); err != nil {
				return nil, nil, fmt.Errorf("twofactor: recovery rand: %w", err)
			}
			groups[g] = hex.EncodeToString(b)
		}
		code := strings.Join(groups, "-")
		plain = append(plain, code)
		hashes = append(hashes, hashCode(code))
	}
	return plain, hashes, nil
}

// hashCode normalizes (lowercase, no dashes/spaces) then SHA-256-hex hashes a
// recovery code, so verification is dash/case-insensitive.
func hashCode(code string) string {
	norm := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(code, "-", ""), " ", ""))
	sum := sha256.Sum256([]byte(norm))
	return hex.EncodeToString(sum[:])
}
