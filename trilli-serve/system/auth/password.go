package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// MinPasswordLen is the floor enforced by HashPassword.
const MinPasswordLen = 8

// ErrUnsupportedHash is returned by VerifyPassword for an unrecognized encoded hash.
var ErrUnsupportedHash = errors.New("auth: unsupported password hash format")

type argonParams struct {
	memoryKiB uint32
	iters     uint32
	parallel  uint8
	saltLen   uint32
	keyLen    uint32
}

// OWASP-aligned defaults for interactive logins (2025 guidance).
var defaultArgon = argonParams{
	memoryKiB: 64 * 1024,
	iters:     3,
	parallel:  2,
	saltLen:   16,
	keyLen:    32,
}

// HashPassword returns a PHC-formatted Argon2id encoded hash:
//
//	$argon2id$v=19$m=65536,t=3,p=2$<salt-b64>$<hash-b64>
func HashPassword(plain string) (string, error) {
	if len(plain) < MinPasswordLen {
		return "", ErrPasswordTooShort
	}
	salt := make([]byte, defaultArgon.saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: read salt: %w", err)
	}
	key := argon2.IDKey([]byte(plain), salt,
		defaultArgon.iters, defaultArgon.memoryKiB, defaultArgon.parallel, defaultArgon.keyLen)

	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		defaultArgon.memoryKiB, defaultArgon.iters, defaultArgon.parallel,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

// VerifyPassword constant-time compares plain against an encoded Argon2id hash.
func VerifyPassword(encoded, plain string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return false, ErrUnsupportedHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, ErrUnsupportedHash
	}
	var memory, iters uint32
	var parallel uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iters, &parallel); err != nil {
		return false, ErrUnsupportedHash
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, ErrUnsupportedHash
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false, ErrUnsupportedHash
	}
	got := argon2.IDKey([]byte(plain), salt, iters, memory, parallel, uint32(len(want)))
	return subtle.ConstantTimeCompare(want, got) == 1, nil
}
