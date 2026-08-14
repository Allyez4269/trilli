// Package clouddrive implements Cloud Import: connecting a user's external cloud
// storage (Google Drive first) over OAuth and copying selected files into their
// Trilli file space.
//
// The provider client id/secret come from the credentials vault
// (provider='google_drive'); per-user OAuth refresh tokens live in the
// cloud_connections table, AES-GCM-encrypted at rest via system/crypto. Access
// tokens are short-lived and minted on demand from the refresh token — never
// stored.
package clouddrive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"trilli/system/crypto"
	"trilli/system/database/postgres"
)

const providerGoogle = "google_drive"

var errNotConnected = errors.New("clouddrive: not connected")

type connection struct {
	AccountEmail string
	RefreshToken string // decrypted
	Scope        string
}

type store struct{ db *postgres.Client }

// save upserts the per-user connection. A blank refreshToken keeps the existing
// one — Google omits the refresh token on re-consent when access was already
// granted, so we must not overwrite the stored one with an empty value.
func (s *store) save(ctx context.Context, tenantID, userID int64, email, refreshToken, scope string) error {
	if refreshToken == "" {
		_, err := s.db.ExecContext(ctx, `
			UPDATE cloud_connections SET account_email = $3, scope = $4, updated_at = now()
			 WHERE user_id = $1 AND provider = $2`,
			userID, providerGoogle, email, scope)
		return err
	}
	enc, err := crypto.Encrypt([]byte(refreshToken))
	if err != nil {
		return fmt.Errorf("clouddrive: encrypt: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO cloud_connections (tenant_id, user_id, provider, account_email, refresh_token_enc, scope, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, now())
		ON CONFLICT (user_id, provider) DO UPDATE
		  SET tenant_id = EXCLUDED.tenant_id, account_email = EXCLUDED.account_email,
		      refresh_token_enc = EXCLUDED.refresh_token_enc, scope = EXCLUDED.scope, updated_at = now()`,
		tenantID, userID, providerGoogle, email, enc, scope)
	return err
}

func (s *store) get(ctx context.Context, userID int64) (*connection, error) {
	var (
		email, scope string
		enc          []byte
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT account_email, refresh_token_enc, scope FROM cloud_connections
		 WHERE user_id = $1 AND provider = $2`,
		userID, providerGoogle).Scan(&email, &enc, &scope)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errNotConnected
	}
	if err != nil {
		return nil, err
	}
	plain, err := crypto.Decrypt(enc)
	if err != nil {
		return nil, fmt.Errorf("clouddrive: decrypt: %w", err)
	}
	return &connection{AccountEmail: email, RefreshToken: string(plain), Scope: scope}, nil
}

func (s *store) delete(ctx context.Context, userID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM cloud_connections WHERE user_id = $1 AND provider = $2`,
		userID, providerGoogle)
	return err
}
