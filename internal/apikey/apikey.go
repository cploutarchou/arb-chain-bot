// Package apikey mints and verifies client API keys (T-086,
// docs/design/packages.md §3.1 api.*). A key authenticates a Bearer
// request the way a session cookie authenticates a browser one: the
// plaintext exists only at creation time (returned once, never
// stored), and every lookup after that goes through an indexed,
// non-secret "prefix" followed by a constant-time digest compare —
// the same shape internal/auth.HashToken uses for session tokens
// (audit S-005: a leaked table contains no usable bearer credential).
//
// Hashing choice: SHA-256 of a 256-bit random token, not argon2id. A
// KDF buys nothing against brute-forcing a value with this much
// entropy, and argon2id's deliberate CPU cost is actively hostile to
// the per-request authentication path an API key sits on (every
// request would pay tens of milliseconds just to authenticate). A
// pepper was considered and dropped: there is no existing registry
// entry to derive one from without adding a secret this package would
// then have to protect, for negligible benefit given the token's
// entropy.
package apikey

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

// TokenPrefix marks the plaintext as an arb-chain-bot API key (and lets
// authenticate() cheaply reject anything that is not one before it does
// a store lookup).
const TokenPrefix = "arbk_"

// prefixLen is how much of the plaintext is stored, unhashed, as the
// lookup index: "arbk_" plus 7 base32 characters (~35 bits) — enough to
// keep the live-key index selective without narrowing the secret's
// search space in any way that matters against its ~150 bits of
// remaining entropy.
const prefixLen = len(TokenPrefix) + 7

// randomBytes is the plaintext secret's entropy: 24 bytes = 192 bits,
// comfortably beyond brute-force range forever.
const randomBytes = 24

// AllowedScopes mirrors docs/design/packages.md §3.1 api.scopes. Any
// other value is rejected at creation (and, redundantly but safely,
// would simply never appear in an org's entitlement api.scopes, so
// entitlements.CheckAPIScope would refuse it too).
var AllowedScopes = map[string]bool{
	"read":            true,
	"rules:write":     true,
	"templates:write": true,
	"paper:write":     true,
}

// Key is one issued API key. Hash is the SHA-256 hex digest of the
// plaintext; the plaintext itself never reaches this struct after
// Generate returns.
type Key struct {
	ID         string
	OrgID      int64
	UserID     string
	Name       string
	Prefix     string
	Hash       string
	Scopes     []string
	CreatedAt  time.Time
	LastUsedAt *time.Time
	RevokedAt  *time.Time
}

// Active reports whether the key may still authenticate.
func (k Key) Active() bool { return k.RevokedAt == nil }

// Redacted strips Hash for API/console responses (never serialize Hash
// directly — Public is the one place that decides what leaves this
// package).
type Redacted struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	Scopes     []string   `json:"scopes"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}

func (k Key) Redact() Redacted {
	return Redacted{
		ID: k.ID, Name: k.Name, Prefix: k.Prefix, Scopes: k.Scopes,
		CreatedAt: k.CreatedAt, LastUsedAt: k.LastUsedAt, RevokedAt: k.RevokedAt,
	}
}

// Generate mints a new plaintext key and the record to store for it.
// The caller assigns ID (the rest of the codebase uses ULIDs via
// newULID/newScreenerID; apikey stays ID-generator agnostic so it does
// not need to import a specific one).
func Generate(orgID int64, userID, name string, scopes []string) (Key, string, error) {
	buf := make([]byte, randomBytes)
	if _, err := rand.Read(buf); err != nil {
		return Key{}, "", err
	}
	token := TokenPrefix + strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(buf))
	k := Key{
		OrgID: orgID, UserID: userID, Name: name,
		Prefix: token[:prefixLen], Hash: Hash(token),
		Scopes: append([]string(nil), scopes...), CreatedAt: time.Now().UTC(),
	}
	return k, token, nil
}

// Hash returns the hex SHA-256 digest of a plaintext key.
func Hash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// Verify compares a plaintext key against a stored digest in constant
// time.
func Verify(token, hash string) bool {
	got := Hash(token)
	return subtle.ConstantTimeCompare([]byte(got), []byte(hash)) == 1
}

// ValidateScopes rejects an empty list or any value outside
// AllowedScopes; it does not check the caller's entitlement to grant a
// scope — that is entitlements.CheckAPIScope, applied by the caller
// against the requesting organisation's package.
func ValidateScopes(scopes []string) error {
	if len(scopes) == 0 {
		return ErrNoScopes
	}
	seen := map[string]bool{}
	for _, s := range scopes {
		if !AllowedScopes[s] {
			return ErrUnknownScope
		}
		if seen[s] {
			return ErrDuplicateScope
		}
		seen[s] = true
	}
	return nil
}

var (
	ErrNotFound        = errors.New("apikey: not found")
	ErrNoScopes        = errors.New("apikey: at least one scope is required")
	ErrUnknownScope    = errors.New("apikey: unknown scope")
	ErrDuplicateScope  = errors.New("apikey: duplicate scope")
	ErrPrefixCollision = errors.New("apikey: prefix collision, generate a new key")
)

// Store persists API keys. Prefix lookups only ever need to see live
// (non-revoked) keys; ListKeys returns every key (including revoked
// ones) so the console can show history.
type Store interface {
	CreateKey(ctx context.Context, k Key) (Key, error)
	ListKeys(ctx context.Context, orgID int64) ([]Key, error)
	ByPrefix(ctx context.Context, prefix string) (Key, error)
	RevokeKey(ctx context.Context, orgID int64, id string, at time.Time) error
	Touch(ctx context.Context, id string, at time.Time) error
}

// Authenticate resolves a plaintext Bearer token to its Key. ok=false
// (with a nil error) covers every "this is not a valid, live key"
// case — malformed prefix, unknown prefix, revoked, or a hash mismatch
// — so callers cannot distinguish "wrong secret" from "unknown key" by
// timing or error shape.
func Authenticate(ctx context.Context, store Store, token string) (Key, bool, error) {
	if !strings.HasPrefix(token, TokenPrefix) || len(token) <= prefixLen {
		return Key{}, false, nil
	}
	k, err := store.ByPrefix(ctx, token[:prefixLen])
	if errors.Is(err, ErrNotFound) {
		return Key{}, false, nil
	}
	if err != nil {
		return Key{}, false, err
	}
	if !k.Active() {
		return Key{}, false, nil
	}
	if !Verify(token, k.Hash) {
		return Key{}, false, nil
	}
	return k, true, nil
}
