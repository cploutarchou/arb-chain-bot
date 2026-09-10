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

// Parse-time ceilings for parameters read back from a stored hash (audit
// S12). Hashes are produced only by HashPassword, but any write primitive
// on users.password_hash would otherwise turn verification into an
// allocation bomb (m=4 GiB, t=2^32). Deriving with clamped parameters
// still fails the constant-time compare, so a tampered row fails closed
// the same way a wrong password does.
const (
	argonMaxMem    = 256 * 1024 // KiB
	argonMaxTime   = 10
	argonMaxPar    = 8
	argonMaxSalt   = 64 // bytes
	argonMinSalt   = 8  // bytes
	argonMaxKeyLen = 512
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

// argonParams is the decoded parameter set of a PHC-format hash.
type argonParams struct {
	mem, iters uint32
	par        uint8
	salt, key  []byte
}

// parseHash decodes an encoded Argon2id hash, validating the fields a
// tampered row could abuse and clamping the derivation parameters to
// the S12 ceilings. Zero parameters are malformed (no hash this package
// ever wrote has them), oversized ones are clamped — the derivation then
// simply no longer matches the stored key.
func parseHash(encoded string) (argonParams, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return argonParams{}, errors.New("auth: unsupported hash format")
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return argonParams{}, errors.New("auth: unsupported argon2 version")
	}
	var p argonParams
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.mem, &p.iters, &p.par); err != nil {
		return argonParams{}, errors.New("auth: malformed parameters")
	}
	if p.iters == 0 || p.mem == 0 || p.par == 0 {
		return argonParams{}, errors.New("auth: malformed parameters")
	}
	if p.mem > argonMaxMem {
		p.mem = argonMaxMem
	}
	if p.iters > argonMaxTime {
		p.iters = argonMaxTime
	}
	if p.par > argonMaxPar {
		p.par = argonMaxPar
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return argonParams{}, errors.New("auth: malformed salt")
	}
	if len(salt) < argonMinSalt || len(salt) > argonMaxSalt {
		return argonParams{}, errors.New("auth: malformed salt")
	}
	key, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return argonParams{}, errors.New("auth: malformed hash")
	}
	if len(key) == 0 || len(key) > argonMaxKeyLen {
		return argonParams{}, errors.New("auth: malformed hash length")
	}
	p.salt, p.key = salt, key
	return p, nil
}

// VerifyPassword checks a password against an encoded hash in constant
// time. Unknown formats fail closed.
func VerifyPassword(password, encoded string) error {
	p, err := parseHash(encoded)
	if err != nil {
		return err
	}
	got := deriveKey([]byte(password), p.salt, p.iters, p.mem, p.par, uint32(len(p.key))) //nolint:gosec // bounded to (0,512] above
	if subtle.ConstantTimeCompare(got, p.key) != 1 {
		return ErrPasswordMismatch
	}
	return nil
}

// NeedsRehash reports whether an encoded hash was written with weaker
// parameters than HashPassword writes today, so a successful login can
// transparently upgrade the stored row (S12) instead of waiting for the
// user's next password change.
func NeedsRehash(encoded string) bool {
	p, err := parseHash(encoded)
	if err != nil {
		return false // unparseable rows are VerifyPassword's problem
	}
	return p.mem < argonMemory || p.iters < argonTime || uint32(len(p.key)) < argonKeyLen //nolint:gosec // bounded to (0,512] at parse
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
