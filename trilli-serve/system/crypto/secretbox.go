// Package crypto provides authenticated symmetric encryption for secrets at
// rest (e.g. TOTP secrets). It uses AES-256-GCM with a key derived from the
// APP_ENCRYPTION_KEY environment variable.
//
// The stored format is: nonce || ciphertext+tag. Rotating APP_ENCRYPTION_KEY
// invalidates previously-encrypted values, so treat it as a long-lived secret.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"

	"trilli/system/logging"
)

const packageName = "crypto"

var (
	keyOnce    sync.Once
	key        [32]byte
	configured bool
	provider   func() ([]byte, error) // optional external source of the master key
)

// SetKeyProvider installs an external source for the 32-byte master key (e.g.
// the DB-backed keystore, which unwraps the key with a binary root key). It
// must be called before any Encrypt/Decrypt use. When set and it returns a
// valid 32-byte key, that key is used in place of deriving one from the env.
func SetKeyProvider(fn func() ([]byte, error)) { provider = fn }

// loadKey resolves the 32-byte master key once. Resolution order:
//  1. the registered provider (DB-backed keystore), if it yields 32 bytes;
//  2. SHA-256 of APP_ENCRYPTION_KEY (legacy / bootstrap-seed path);
//  3. an insecure dev key (logged loudly) when neither is available.
func loadKey() [32]byte {
	keyOnce.Do(func() {
		if provider != nil {
			if k, err := provider(); err != nil {
				logging.Error(packageName, "master-key provider failed (%v) — falling back to APP_ENCRYPTION_KEY", err)
			} else if len(k) == 32 {
				copy(key[:], k)
				configured = true
				return
			} else {
				logging.Error(packageName, "master-key provider returned %d bytes (want 32) — falling back", len(k))
			}
		}
		raw := os.Getenv("APP_ENCRYPTION_KEY")
		if raw == "" {
			logging.Error(packageName, "APP_ENCRYPTION_KEY is not set — using an insecure dev key. "+
				"Set APP_ENCRYPTION_KEY in the environment before storing real secrets.")
			raw = "trilli-insecure-dev-key-please-set-APP_ENCRYPTION_KEY"
		} else {
			configured = true
		}
		key = sha256.Sum256([]byte(raw))
	})
	return key
}

// SeedKeyFromEnv returns the 32-byte key the legacy env path would derive from
// APP_ENCRYPTION_KEY, WITHOUT touching loadKey (so the keystore can call it from
// inside the provider without recursing). ok is false when the env var is
// unset. Used once to seed the DB-stored key while preserving its exact value.
func SeedKeyFromEnv() (key [32]byte, ok bool) {
	raw := os.Getenv("APP_ENCRYPTION_KEY")
	if raw == "" {
		return [32]byte{}, false
	}
	return sha256.Sum256([]byte(raw)), true
}

// KeyConfigured reports whether a real APP_ENCRYPTION_KEY was provided (i.e.
// we're NOT on the insecure dev fallback). Subsystems that must never operate
// on the dev key — notably blob encryption, where a wrong key means permanent
// data loss — gate on this.
func KeyConfigured() bool {
	loadKey()
	return configured
}

// DeriveKey returns a 32-byte subkey bound to label, derived from the master
// key via HMAC-SHA256. This gives each subsystem its own key (domain
// separation) without a separate secret — e.g. the blob key-encryption-key is
// DeriveKey("trilli-file-kek-v1") rather than the raw master key.
func DeriveKey(label string) [32]byte {
	master := loadKey()
	mac := hmac.New(sha256.New, master[:])
	_, _ = mac.Write([]byte(label))
	var out [32]byte
	copy(out[:], mac.Sum(nil))
	return out
}

// Encrypt seals plaintext with AES-256-GCM under the master key.
func Encrypt(plaintext []byte) ([]byte, error) {
	k := loadKey()
	return EncryptWith(k, plaintext)
}

// Decrypt opens a nonce||ciphertext blob produced by Encrypt (master key).
func Decrypt(blob []byte) ([]byte, error) {
	k := loadKey()
	return DecryptWith(k, blob)
}

// EncryptWith seals plaintext with AES-256-GCM under an explicit key, returning
// nonce||ciphertext. Used to wrap subsystem keys (e.g. per-tenant blob DEKs)
// under a derived key-encryption-key rather than the raw master key.
func EncryptWith(k [32]byte, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(k[:])
	if err != nil {
		return nil, fmt.Errorf("crypto: new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("crypto: new gcm: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("crypto: nonce: %w", err)
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// DecryptWith opens a nonce||ciphertext blob produced by EncryptWith.
func DecryptWith(k [32]byte, blob []byte) ([]byte, error) {
	block, err := aes.NewCipher(k[:])
	if err != nil {
		return nil, fmt.Errorf("crypto: new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("crypto: new gcm: %w", err)
	}
	ns := gcm.NonceSize()
	if len(blob) < ns {
		return nil, errors.New("crypto: ciphertext too short")
	}
	nonce, ct := blob[:ns], blob[ns:]
	out, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, fmt.Errorf("crypto: decrypt: %w", err)
	}
	return out, nil
}
