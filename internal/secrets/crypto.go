// Package secrets is the write-only secrets vault (T-060,
// docs/design/settings-expansion.md §3): AES-256-GCM under ARB_SECRET_KEY,
// the secret name as additional authenticated data, a key fingerprint
// per row, and a CLOSED two-entry registry — adding a name is a code
// change, which is what keeps this from ever becoming a store for
// exchange trading keys. Values are never returned by any API, never
// logged, never partially displayed.
//
// ARB_SECRET_KEY is read HERE (KeyFromEnv) and deliberately never enters
// config.Bootstrap: keeping the master key in the one package that uses
// it means it cannot be logged by a future Redacted() omission at all.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
)

// EnvKey is the environment variable holding the base64 master key.
const EnvKey = "ARB_SECRET_KEY"

const (
	keyLen   = 32 // AES-256
	nonceLen = 12 // GCM standard nonce
)

// ErrNoKey reports an unset ARB_SECRET_KEY: the vault does not open and
// the platform keeps running on env-provided secrets.
var ErrNoKey = errors.New("secrets: " + EnvKey + " unset")

// ErrBadKey wraps a present-but-invalid ARB_SECRET_KEY (bad base64 or
// wrong length); the message names the actual length.
var ErrBadKey = errors.New("secrets: invalid " + EnvKey)

// KeyMismatchError reports a row encrypted under a different master key
// than the process holds — surfaced as present:true, readable:false
// with an actionable reason, never as an opaque auth failure.
type KeyMismatchError struct{ Row, Process string }

func (e *KeyMismatchError) Error() string {
	return fmt.Sprintf("encrypted under key %s; this process holds %s", e.Row, e.Process)
}

// KeyFromEnv reads and validates ARB_SECRET_KEY: standard base64
// decoding to exactly 32 bytes.
func KeyFromEnv() ([]byte, error) {
	raw, ok := os.LookupEnv(EnvKey)
	if !ok || raw == "" {
		return nil, ErrNoKey
	}
	return ParseKey(raw)
}

// ParseKey decodes a base64 master key and checks its length.
func ParseKey(raw string) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: not standard base64", ErrBadKey)
	}
	if len(key) != keyLen {
		return nil, fmt.Errorf("%w: decodes to %d bytes, need exactly %d", ErrBadKey, len(key), keyLen)
	}
	return key, nil
}

// KeyID is the fingerprint stored with every row: hex(sha256(key))[:8].
func KeyID(key []byte) string {
	sum := sha256.Sum256(key)
	return hex.EncodeToString(sum[:])[:8]
}

// Cipher seals and opens values under one master key.
type Cipher struct {
	aead cipher.AEAD
	id   string
}

// NewCipher builds an AES-256-GCM cipher over key.
func NewCipher(key []byte) (*Cipher, error) {
	if len(key) != keyLen {
		return nil, fmt.Errorf("%w: %d bytes, need %d", ErrBadKey, len(key), keyLen)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Cipher{aead: aead, id: KeyID(key)}, nil
}

// KeyID returns the fingerprint of the key this cipher holds.
func (c *Cipher) KeyID() string { return c.id }

// Seal encrypts value with a fresh random 12-byte nonce, binding the
// ciphertext to name through the AAD so a row swapped between names
// fails authentication instead of decrypting as a different secret.
func (c *Cipher) Seal(name string, value []byte) (ciphertext, nonce []byte, err error) {
	nonce = make([]byte, nonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, err
	}
	ciphertext = c.aead.Seal(nil, nonce, value, []byte(name))
	return ciphertext, nonce, nil
}

// Open authenticates and decrypts. A keyID that does not match this
// cipher's key is reported as *KeyMismatchError before any crypto runs.
func (c *Cipher) Open(name string, ciphertext, nonce []byte, keyID string) ([]byte, error) {
	if keyID != c.id {
		return nil, &KeyMismatchError{Row: keyID, Process: c.id}
	}
	if len(nonce) != nonceLen {
		return nil, fmt.Errorf("secrets: nonce is %d bytes, need %d", len(nonce), nonceLen)
	}
	plain, err := c.aead.Open(nil, nonce, ciphertext, []byte(name))
	if err != nil {
		return nil, fmt.Errorf("secrets: %s: authentication failed", name)
	}
	return plain, nil
}
