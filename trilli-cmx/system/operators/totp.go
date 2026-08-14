package operators

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

	"trilli-cmx/system/crypto"
	"trilli-cmx/system/logging"
)

const (
	totpIssuer     = "Trilli CMX"
	recoveryCount  = 10
	recoveryGroups = 2
	recoveryBytes  = 4
)

// 2FA sentinel errors.
var (
	ErrAlreadyEnrolled = errors.New("operators: 2FA already enrolled")
	ErrNoPending       = errors.New("operators: no pending TOTP setup")
	ErrInvalidCode     = errors.New("operators: code is incorrect or expired")
	ErrNotEnrolled     = errors.New("operators: 2FA is not enrolled")
)

// TOTPSetup is the QR payload returned during enrollment.
type TOTPSetup struct {
	Secret     string `json:"secret"`
	OtpauthURL string `json:"otpauth_url"`
	QRDataURL  string `json:"qr_data_url"`
}

// HasConfirmedTOTP reports whether the operator has active 2FA.
func (s *Service) HasConfirmedTOTP(ctx context.Context, operatorID int64) (bool, error) {
	var confirmed sql.NullTime
	err := s.db.QueryRowContext(ctx,
		`SELECT confirmed_at FROM cmx_operator_totp WHERE operator_id = $1`, operatorID,
	).Scan(&confirmed)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("operators: totp status: %w", err)
	}
	return confirmed.Valid, nil
}

// BeginTOTPSetup generates a fresh secret, stores it as PENDING (encrypted), and
// returns the QR + otpauth URL. Refuses if 2FA is already confirmed.
func (s *Service) BeginTOTPSetup(ctx context.Context, operatorID int64, email string) (*TOTPSetup, error) {
	var confirmed sql.NullTime
	err := s.db.QueryRowContext(ctx,
		`SELECT confirmed_at FROM cmx_operator_totp WHERE operator_id = $1`, operatorID,
	).Scan(&confirmed)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("operators: setup status: %w", err)
	}
	if err == nil && confirmed.Valid {
		return nil, ErrAlreadyEnrolled
	}

	key, err := totp.Generate(totp.GenerateOpts{Issuer: totpIssuer, AccountName: email})
	if err != nil {
		return nil, fmt.Errorf("operators: generate totp: %w", err)
	}
	enc, err := crypto.Encrypt([]byte(key.Secret()))
	if err != nil {
		return nil, fmt.Errorf("operators: encrypt secret: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO cmx_operator_totp (operator_id, secret_encrypted, confirmed_at, last_used_step)
		VALUES ($1, $2, NULL, 0)
		ON CONFLICT (operator_id) DO UPDATE
		   SET secret_encrypted = EXCLUDED.secret_encrypted, confirmed_at = NULL, last_used_step = 0`,
		operatorID, enc,
	); err != nil {
		return nil, fmt.Errorf("operators: store pending totp: %w", err)
	}

	img, err := key.Image(220, 220)
	if err != nil {
		return nil, fmt.Errorf("operators: qr image: %w", err)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("operators: qr encode: %w", err)
	}
	logging.Info(packageName, "TOTP setup begun for operator=%d", operatorID)
	return &TOTPSetup{
		Secret:     key.Secret(),
		OtpauthURL: key.URL(),
		QRDataURL:  "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes()),
	}, nil
}

// ConfirmTOTP verifies a code against the pending secret, activates 2FA, and
// mints fresh one-time recovery codes (returned once, stored hashed).
func (s *Service) ConfirmTOTP(ctx context.Context, operatorID int64, code string) ([]string, error) {
	var enc []byte
	var confirmed sql.NullTime
	err := s.db.QueryRowContext(ctx,
		`SELECT secret_encrypted, confirmed_at FROM cmx_operator_totp WHERE operator_id = $1`, operatorID,
	).Scan(&enc, &confirmed)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoPending
	}
	if err != nil {
		return nil, fmt.Errorf("operators: load pending: %w", err)
	}
	if confirmed.Valid {
		return nil, ErrAlreadyEnrolled
	}
	secret, err := crypto.Decrypt(enc)
	if err != nil {
		return nil, fmt.Errorf("operators: decrypt: %w", err)
	}
	if !totp.Validate(strings.TrimSpace(code), string(secret)) {
		return nil, ErrInvalidCode
	}

	codes, hashes, err := newRecoveryCodes()
	if err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("operators: begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if _, err := tx.ExecContext(ctx,
		`UPDATE cmx_operator_totp SET confirmed_at = NOW() WHERE operator_id = $1`, operatorID,
	); err != nil {
		return nil, fmt.Errorf("operators: confirm: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM cmx_operator_recovery_codes WHERE operator_id = $1`, operatorID,
	); err != nil {
		return nil, fmt.Errorf("operators: clear codes: %w", err)
	}
	for _, h := range hashes {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO cmx_operator_recovery_codes (operator_id, code_hash) VALUES ($1, $2)`, operatorID, h,
		); err != nil {
			return nil, fmt.Errorf("operators: store code: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("operators: commit: %w", err)
	}
	committed = true
	logging.Info(packageName, "TOTP confirmed for operator=%d", operatorID)
	return codes, nil
}

// verifyTOTP checks a login/step-up challenge: a 6-digit TOTP code (with a
// one-window replay guard) or a one-time recovery code.
func (s *Service) verifyTOTP(ctx context.Context, operatorID int64, code string) error {
	code = strings.TrimSpace(code)
	if !isSixDigits(code) {
		return s.verifyRecoveryCode(ctx, operatorID, code)
	}
	var enc []byte
	var confirmed sql.NullTime
	var lastStep int64
	err := s.db.QueryRowContext(ctx,
		`SELECT secret_encrypted, confirmed_at, last_used_step FROM cmx_operator_totp WHERE operator_id = $1`,
		operatorID,
	).Scan(&enc, &confirmed, &lastStep)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !confirmed.Valid) {
		return ErrNotEnrolled
	}
	if err != nil {
		return fmt.Errorf("operators: verify load: %w", err)
	}
	secret, err := crypto.Decrypt(enc)
	if err != nil {
		return fmt.Errorf("operators: decrypt: %w", err)
	}
	step := time.Now().Unix() / 30
	if step <= lastStep {
		return ErrInvalidCode // replay of an already-used window
	}
	if !totp.Validate(code, string(secret)) {
		return ErrInvalidCode
	}
	if _, err := s.db.ExecContext(ctx,
		`UPDATE cmx_operator_totp SET last_used_step = $1 WHERE operator_id = $2`, step, operatorID,
	); err != nil {
		return fmt.Errorf("operators: record step: %w", err)
	}
	return nil
}

func (s *Service) verifyRecoveryCode(ctx context.Context, operatorID int64, code string) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE cmx_operator_recovery_codes SET used_at = NOW()
		 WHERE operator_id = $1 AND code_hash = $2 AND used_at IS NULL`,
		operatorID, hashCode(code),
	)
	if err != nil {
		return fmt.Errorf("operators: recovery verify: %w", err)
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

func newRecoveryCodes() (plain []string, hashes []string, err error) {
	for i := 0; i < recoveryCount; i++ {
		groups := make([]string, recoveryGroups)
		for g := range groups {
			b := make([]byte, recoveryBytes)
			if _, err = rand.Read(b); err != nil {
				return nil, nil, fmt.Errorf("operators: recovery rand: %w", err)
			}
			groups[g] = hex.EncodeToString(b)
		}
		code := strings.Join(groups, "-")
		plain = append(plain, code)
		hashes = append(hashes, hashCode(code))
	}
	return plain, hashes, nil
}

func hashCode(code string) string {
	norm := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(code, "-", ""), " ", ""))
	sum := sha256.Sum256([]byte(norm))
	return hex.EncodeToString(sum[:])
}
