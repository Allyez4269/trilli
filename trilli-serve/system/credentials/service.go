// Package credentials is an encrypted vault for third-party service keys
// (Stripe, future APIs). Values are AES-GCM encrypted at rest via system/crypto
// (keyed by APP_ENCRYPTION_KEY); only a last-4 tail is stored in plaintext for
// masked display. The DB connection stays baked in code and APP_ENCRYPTION_KEY
// lives in the systemd drop-in — this vault holds everything else.
package credentials

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"trilli/system/crypto"
	"trilli/system/database/postgres"
)

var ErrNotFound = errors.New("credentials: not found")

type Service struct {
	client *postgres.Client
}

func NewService(c *postgres.Client) *Service {
	return &Service{client: c}
}

// Credential is the non-sensitive view used for listing — never the plaintext.
type Credential struct {
	Provider    string `json:"provider"`
	KeyName     string `json:"key_name"`
	Environment string `json:"environment"`
	Last4       string `json:"last4"`
	IsActive    bool   `json:"is_active"`
	UpdatedAt   string `json:"updated_at"`
}

func last4(s string) string {
	if len(s) <= 4 {
		return s
	}
	return s[len(s)-4:]
}

// Set encrypts and upserts a credential value for (provider, key_name, env).
func (s *Service) Set(ctx context.Context, provider, keyName, env, value string) error {
	enc, err := crypto.Encrypt([]byte(value))
	if err != nil {
		return fmt.Errorf("credentials: encrypt: %w", err)
	}
	if _, err := s.client.ExecContext(ctx, `
		INSERT INTO service_credentials (provider, key_name, environment, value_enc, last4, updated_at)
		VALUES ($1, $2, $3, $4, $5, NOW())
		ON CONFLICT (provider, key_name, environment)
		DO UPDATE SET value_enc = EXCLUDED.value_enc, last4 = EXCLUDED.last4, updated_at = NOW()`,
		provider, keyName, env, enc, last4(value),
	); err != nil {
		return fmt.Errorf("credentials: set: %w", err)
	}
	return nil
}

// Get returns the decrypted value for a specific environment.
func (s *Service) Get(ctx context.Context, provider, keyName, env string) (string, error) {
	var enc []byte
	err := s.client.QueryRowContext(ctx,
		`SELECT value_enc FROM service_credentials
		  WHERE provider = $1 AND key_name = $2 AND environment = $3`,
		provider, keyName, env,
	).Scan(&enc)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("credentials: get: %w", err)
	}
	plain, err := crypto.Decrypt(enc)
	if err != nil {
		return "", fmt.Errorf("credentials: decrypt: %w", err)
	}
	return string(plain), nil
}

// GetActive returns the decrypted value for the active environment of a key —
// what runtime code (e.g. the Stripe client) should call. Returns the value and
// the environment it came from ('test'/'live').
func (s *Service) GetActive(ctx context.Context, provider, keyName string) (value, env string, err error) {
	var enc []byte
	qerr := s.client.QueryRowContext(ctx,
		`SELECT value_enc, environment FROM service_credentials
		  WHERE provider = $1 AND key_name = $2 AND is_active
		  ORDER BY updated_at DESC LIMIT 1`,
		provider, keyName,
	).Scan(&enc, &env)
	if errors.Is(qerr, sql.ErrNoRows) {
		return "", "", ErrNotFound
	}
	if qerr != nil {
		return "", "", fmt.Errorf("credentials: get active: %w", qerr)
	}
	plain, derr := crypto.Decrypt(enc)
	if derr != nil {
		return "", "", fmt.Errorf("credentials: decrypt: %w", derr)
	}
	return string(plain), env, nil
}

// List returns masked metadata for every stored credential (no plaintext).
func (s *Service) List(ctx context.Context) ([]Credential, error) {
	rows, err := s.client.QueryContext(ctx, `
		SELECT provider, key_name, environment, last4, is_active, updated_at::text
		  FROM service_credentials
		 ORDER BY provider, key_name, environment`)
	if err != nil {
		return nil, fmt.Errorf("credentials: list: %w", err)
	}
	defer rows.Close()
	var out []Credential
	for rows.Next() {
		var c Credential
		if err := rows.Scan(&c.Provider, &c.KeyName, &c.Environment, &c.Last4, &c.IsActive, &c.UpdatedAt); err != nil {
			return nil, fmt.Errorf("credentials: scan: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
