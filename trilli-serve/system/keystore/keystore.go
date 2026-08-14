// Package keystore stores the application master key in the database, wrapped
// (encrypted) by a root key compiled into the binary. This keeps the master
// key out of the host environment and lets every cluster replica load it from
// the shared database — while a database dump, stolen DB credentials, or the
// DB hyperscaler still see only an opaque wrapped key they cannot open without
// the binary.
//
// Resolution: on first use the app reads the wrapped key from app_master_key
// and unwraps it with the root key. If the row doesn't exist yet, it seeds it
// once from APP_ENCRYPTION_KEY (preserving the exact key value so everything
// already encrypted stays valid), after which the env var can be removed.
//
// The root key is supplied via the TRILLI_ROOT_KEY environment variable (64 hex
// characters = 32 bytes). Keep it out of the database and out of version
// control: anyone holding both a database dump and the root key can unwrap the
// master key.
package keystore

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"trilli/system/crypto"
	"trilli/system/database/postgres"
	"trilli/system/logging"
)

const packageName = "keystore"

// rootKey reads the 32-byte root key from TRILLI_ROOT_KEY. It wraps the master
// key at rest in the database; it is never stored in the database. To rotate
// it, rewrap app_master_key.
func rootKey() ([32]byte, error) {
	var rk [32]byte
	b, err := hex.DecodeString(strings.TrimSpace(os.Getenv("TRILLI_ROOT_KEY")))
	if err != nil || len(b) != 32 {
		return rk, fmt.Errorf("keystore: TRILLI_ROOT_KEY must be 64 hex characters (32 bytes)")
	}
	copy(rk[:], b)
	return rk, nil
}

// Install registers the DB-backed master-key provider with the crypto package.
// Safe to call at process start before any crypto use; the provider runs lazily
// on the first Encrypt/Decrypt. newClient lets the provider open its own short
// connection so we don't have to thread a DB handle through crypto.
func Install() {
	crypto.SetKeyProvider(provide)
}

// provide resolves the master key: unwrap the stored one, else seed from env.
func provide() ([]byte, error) {
	rk, err := rootKey()
	if err != nil {
		return nil, err
	}
	pg, err := postgres.NewClient(nil)
	if err != nil {
		return nil, fmt.Errorf("keystore: db connect: %w", err)
	}
	defer pg.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var wrapped []byte
	err = pg.QueryRowContext(ctx, `SELECT wrapped_key FROM app_master_key WHERE id = 1`).Scan(&wrapped)
	switch {
	case err == nil:
		master, derr := crypto.DecryptWith(rk, wrapped)
		if derr != nil {
			return nil, fmt.Errorf("keystore: unwrap master key (wrong root key?): %w", derr)
		}
		logging.Info(packageName, "master key loaded from database")
		return master, nil

	case errors.Is(err, sql.ErrNoRows):
		// First run on this database: seed the row from the env key, preserving
		// the exact value so already-encrypted data stays readable.
		seed, ok := crypto.SeedKeyFromEnv()
		if !ok {
			return nil, errors.New("keystore: no master key in app_master_key and APP_ENCRYPTION_KEY not set to seed it")
		}
		wrapped, werr := crypto.EncryptWith(rk, seed[:])
		if werr != nil {
			return nil, fmt.Errorf("keystore: wrap seed: %w", werr)
		}
		if _, ierr := pg.ExecContext(ctx,
			`INSERT INTO app_master_key (id, wrapped_key) VALUES (1, $1)
			 ON CONFLICT (id) DO NOTHING`, wrapped); ierr != nil {
			return nil, fmt.Errorf("keystore: seed insert: %w", ierr)
		}
		logging.Info(packageName, "master key seeded into the database from APP_ENCRYPTION_KEY — the env var can now be removed")
		out := make([]byte, 32)
		copy(out, seed[:])
		return out, nil

	default:
		// Table missing (pre-migration) or transient error: let crypto fall back
		// to the env so the app still functions during rollout.
		return nil, fmt.Errorf("keystore: read master key: %w", err)
	}
}

// Status reports whether the master key is stored in the database yet.
func Status(ctx context.Context, pg *postgres.Client) (inDB bool, createdAt sql.NullTime, err error) {
	row := pg.QueryRowContext(ctx, `SELECT created_at FROM app_master_key WHERE id = 1`)
	if err := row.Scan(&createdAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, createdAt, nil
		}
		return false, createdAt, err
	}
	return true, createdAt, nil
}
