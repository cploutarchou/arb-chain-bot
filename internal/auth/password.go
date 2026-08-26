// Package auth implements authentication and authorization: Argon2id
// password hashing, server-side revocable sessions, CSRF double-submit
// tokens, login throttling, and RBAC enforced in the service layer
// (docs/security.md §4–§5). Frontend hiding is never authorization.
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

// Argon2id parameters. OWASP-recommended baseline for interactive logins;
// raising them later only affects new hashes (the encoded string carries
// its own parameters).
const (
	argonTime    = 3
	argonMemory  = 64 * 1024 // KiB
	argonThreads = 2
	argonKeyLen  = 32
	argonSaltLen = 16
)

var ErrPasswordMismatch = errors.New("auth: password mismatch")

// argonSem bounds concurrent Argon2id derivations process-wide. Each
// derivation pins argonMemory KiB (64 MiB), so unbounded parallel login
// attempts are a trivial memory-exhaustion vector (audit S-006); excess
// callers queue briefly instead.
var argonSem = make(chan struct{}, 4)

func deriveKey(password, salt []byte, iters, mem uint32, par uint8, keyLen uint32) []byte {
	argonSem <- struct{}{}
	defer func() { <-argonSem }()
	return argon2.IDKey(password, salt, iters, mem, par, keyLen)
}

// HashPassword derives an encoded Argon2id hash:
// $argon2id$v=19$m=...,t=...,p=...$salt$hash (standard PHC format).
func HashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: salt: %w", err)
	}
	key := deriveKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

// VerifyPassword checks a password against an encoded hash in constant
// time. Unknown formats fail closed.
func VerifyPassword(password, encoded string) error {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return errors.New("auth: unsupported hash format")
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return errors.New("auth: unsupported argon2 version")
	}
	var mem, iters uint32
	var par uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &mem, &iters, &par); err != nil {
		return errors.New("auth: malformed parameters")
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return errors.New("auth: malformed salt")
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return errors.New("auth: malformed hash")
	}
	if len(want) == 0 || len(want) > 512 {
		return errors.New("auth: malformed hash length")
	}
	got := deriveKey([]byte(password), salt, iters, mem, par, uint32(len(want))) //nolint:gosec // bounded to (0,512] above
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return ErrPasswordMismatch
	}
	return nil
}

// NewToken returns a URL-safe random token with n bytes of entropy
// (sessions: 32; CSRF: 32).
func NewToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("auth: token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
