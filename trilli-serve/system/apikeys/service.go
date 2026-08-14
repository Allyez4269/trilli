package apikeys

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"trilli/system/database/postgres"
	"trilli/system/logging"
)

type Service struct {
	client *postgres.Client
}

func NewService(c *postgres.Client) *Service {
	return &Service{client: c}
}

func hashKey(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// Create issues a new key for the tenant. It returns the stored (non-secret)
// metadata plus the plaintext token, which the caller must show to the user
// exactly once — only its hash is persisted.
func (s *Service) Create(ctx context.Context, tenantID, userID int64, name string) (*APIKey, string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 80 {
		return nil, "", ErrInvalidName
	}
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return nil, "", fmt.Errorf("apikeys: rand: %w", err)
	}
	secret := hex.EncodeToString(raw) // 48 hex chars
	token := KeyPrefix + secret
	prefix := KeyPrefix + secret[:6]
	lastFour := secret[len(secret)-4:]

	var k APIKey
	err := s.client.QueryRowContext(ctx, `
		INSERT INTO api_keys (tenant_id, created_by_user_id, name, key_prefix, key_hash, last_four)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, name, key_prefix, last_four, created_by_user_id, created_at, last_used_at`,
		tenantID, userID, name, prefix, hashKey(token), lastFour,
	).Scan(&k.ID, &k.Name, &k.KeyPrefix, &k.LastFour, &k.CreatedByUserID, &k.CreatedAt, &k.LastUsedAt)
	if err != nil {
		return nil, "", fmt.Errorf("apikeys: create: %w", err)
	}
	return &k, token, nil
}

// List returns the tenant's active (non-revoked) keys, newest first.
func (s *Service) List(ctx context.Context, tenantID int64) ([]APIKey, error) {
	rows, err := s.client.QueryContext(ctx, `
		SELECT k.id, k.name, k.key_prefix, k.last_four, k.created_by_user_id,
		       COALESCE(NULLIF(u.full_name, ''), u.email, ''), k.created_at, k.last_used_at
		  FROM api_keys k
		  LEFT JOIN users u ON u.id = k.created_by_user_id
		 WHERE k.tenant_id = $1 AND k.revoked_at IS NULL
		 ORDER BY k.created_at DESC`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("apikeys: list: %w", err)
	}
	defer rows.Close()
	out := []APIKey{}
	for rows.Next() {
		var k APIKey
		if err := rows.Scan(&k.ID, &k.Name, &k.KeyPrefix, &k.LastFour, &k.CreatedByUserID,
			&k.CreatedByName, &k.CreatedAt, &k.LastUsedAt); err != nil {
			return nil, fmt.Errorf("apikeys: list scan: %w", err)
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// Revoke soft-revokes a key; it stops authenticating immediately.
func (s *Service) Revoke(ctx context.Context, tenantID, keyID int64) error {
	res, err := s.client.ExecContext(ctx, `
		UPDATE api_keys SET revoked_at = NOW()
		 WHERE id = $1 AND tenant_id = $2 AND revoked_at IS NULL`, keyID, tenantID)
	if err != nil {
		return fmt.Errorf("apikeys: revoke: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Authenticate resolves a plaintext bearer token to its principal, enforcing
// revocation and the tenant's active status. APIAccess on the returned
// principal reflects the plan entitlement (handlers enforce it). last_used_at
// is stamped best-effort.
func (s *Service) Authenticate(ctx context.Context, token string) (*Principal, error) {
	token = strings.TrimSpace(token)
	if !strings.HasPrefix(token, KeyPrefix) {
		return nil, ErrInvalidKey
	}
	var p Principal
	err := s.client.QueryRowContext(ctx, `
		SELECT k.id, k.tenant_id, k.created_by_user_id,
		       COALESCE(pl.api_access, false), COALESCE(pl.name, '')
		  FROM api_keys k
		  JOIN tenants t ON t.id = k.tenant_id
		  LEFT JOIN plans pl ON pl.id = t.plan_id
		 WHERE k.key_hash = $1 AND k.revoked_at IS NULL AND t.status = 'active'`,
		hashKey(token),
	).Scan(&p.KeyID, &p.TenantID, &p.UserID, &p.APIAccess, &p.PlanName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrInvalidKey
		}
		return nil, fmt.Errorf("apikeys: authenticate: %w", err)
	}
	keyID := p.KeyID
	go func() {
		if _, err := s.client.ExecContext(context.Background(),
			`UPDATE api_keys SET last_used_at = NOW() WHERE id = $1`, keyID); err != nil {
			logging.Error(packageName, "last_used update failed (id=%d): %v", keyID, err)
		}
	}()
	return &p, nil
}

// TenantAPIAccess reports whether the tenant's current plan includes API access.
func (s *Service) TenantAPIAccess(ctx context.Context, tenantID int64) (bool, error) {
	var ok bool
	err := s.client.QueryRowContext(ctx, `
		SELECT COALESCE(pl.api_access, false)
		  FROM tenants t LEFT JOIN plans pl ON pl.id = t.plan_id
		 WHERE t.id = $1`, tenantID).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("apikeys: tenant api access: %w", err)
	}
	return ok, nil
}
