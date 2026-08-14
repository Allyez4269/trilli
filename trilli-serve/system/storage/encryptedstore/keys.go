package encryptedstore

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"sync"

	"trilli/system/crypto"
	"trilli/system/database/postgres"
)

// fileKEKLabel domain-separates the blob key-encryption-key from every other
// use of the master key (OAuth state, TOTP, the credentials vault).
const fileKEKLabel = "trilli-file-kek-v1"

// dekBytes is the size of a per-tenant data-encryption key (AES-256).
const dekBytes = 32

// KeyService issues and caches per-tenant DEKs. DEKs are generated on first
// use, stored wrapped (under the KEK) in tenant_file_keys, and cached in memory
// per node (they're immutable for the life of a tenant unless rotated).
type KeyService struct {
	client *postgres.Client
	kek    [32]byte
	cache  sync.Map // tenantID -> []byte (unwrapped DEK)
}

// NewKeyService derives the KEK from the master key and returns the service.
func NewKeyService(c *postgres.Client) *KeyService {
	return &KeyService{client: c, kek: crypto.DeriveKey(fileKEKLabel)}
}

// DEK returns the tenant's data-encryption key, creating + persisting it on
// first use. Cached after the first lookup.
func (k *KeyService) DEK(ctx context.Context, tenantID int64) ([]byte, error) {
	if v, ok := k.cache.Load(tenantID); ok {
		return v.([]byte), nil
	}

	var wrapped []byte
	err := k.client.QueryRowContext(ctx,
		`SELECT wrapped_dek FROM tenant_file_keys WHERE tenant_id = $1`, tenantID).Scan(&wrapped)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return k.create(ctx, tenantID)
	case err != nil:
		return nil, fmt.Errorf("encryptedstore: load dek: %w", err)
	}
	dek, err := crypto.DecryptWith(k.kek, wrapped)
	if err != nil {
		return nil, fmt.Errorf("encryptedstore: unwrap dek (wrong APP_ENCRYPTION_KEY?): %w", err)
	}
	k.cache.Store(tenantID, dek)
	return dek, nil
}

// create generates, wraps, and persists a fresh DEK for the tenant. Safe under
// races: a concurrent insert wins and we re-read the stored key.
func (k *KeyService) create(ctx context.Context, tenantID int64) ([]byte, error) {
	dek := make([]byte, dekBytes)
	if _, err := rand.Read(dek); err != nil {
		return nil, fmt.Errorf("encryptedstore: gen dek: %w", err)
	}
	wrapped, err := crypto.EncryptWith(k.kek, dek)
	if err != nil {
		return nil, fmt.Errorf("encryptedstore: wrap dek: %w", err)
	}
	res, err := k.client.ExecContext(ctx,
		`INSERT INTO tenant_file_keys (tenant_id, wrapped_dek) VALUES ($1, $2)
		 ON CONFLICT (tenant_id) DO NOTHING`, tenantID, wrapped)
	if err != nil {
		return nil, fmt.Errorf("encryptedstore: insert dek: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// Another node created it first — read the canonical one.
		var stored []byte
		if err := k.client.QueryRowContext(ctx,
			`SELECT wrapped_dek FROM tenant_file_keys WHERE tenant_id = $1`, tenantID).Scan(&stored); err != nil {
			return nil, fmt.Errorf("encryptedstore: reload dek: %w", err)
		}
		dek, err = crypto.DecryptWith(k.kek, stored)
		if err != nil {
			return nil, fmt.Errorf("encryptedstore: unwrap dek: %w", err)
		}
	}
	k.cache.Store(tenantID, dek)
	return dek, nil
}
