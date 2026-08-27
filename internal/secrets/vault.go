package secrets

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// SecretSource resolves a secret by registry name: (value, source, ok).
// source names which implementation won ("vault" | "env").
type SecretSource interface {
	Get(ctx context.Context, name string) (value, source string, ok bool)
}

// Row is one persisted secret. The plaintext never appears here.
type Row struct {
	Name       string
	Ciphertext []byte
	Nonce      []byte
	KeyID      string
	UpdatedAt  time.Time
	UpdatedBy  string
}

// Store persists rows (pgx in internal/storage; MemoryStore for tests).
type Store interface {
	Put(ctx context.Context, row Row) error
	Get(ctx context.Context, name string) (Row, bool, error)
	Delete(ctx context.Context, name string) (bool, error)
	List(ctx context.Context) ([]Row, error)
}

// Info is the status view of one registry entry — presence, source,
// readability, provenance and when a write applies. Never a value,
// never a prefix, never a last-4.
type Info struct {
	Name      string     `json:"name"`
	Label     string     `json:"label"`
	Present   bool       `json:"present"`
	Source    string     `json:"source,omitempty"` // vault | env
	Readable  bool       `json:"readable"`
	Reason    string     `json:"reason,omitempty"`
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
	UpdatedBy string     `json:"updated_by,omitempty"`
	Applies   string     `json:"applies"`
}

// ErrVaultUnavailable reports a write against a vault that did not
// open (no/invalid ARB_SECRET_KEY, or no persistence) — 503.
var ErrVaultUnavailable = errors.New("secrets: vault unavailable")

// Env is the environment fallback source. Values are handed in by the
// wiring from config.Bootstrap (which already read them) rather than
// re-read from os.Getenv, so tests can inject them.
type Env map[string]string

func (e Env) Get(_ context.Context, name string) (string, string, bool) {
	if v, ok := e[name]; ok && v != "" {
		return v, "env", true
	}
	return "", "", false
}

// Vault is the encrypted store: cipher + rows.
type Vault struct {
	store  Store
	cipher *Cipher
}

// NewVault opens the vault over store with key.
func NewVault(store Store, key []byte) (*Vault, error) {
	c, err := NewCipher(key)
	if err != nil {
		return nil, err
	}
	return &Vault{store: store, cipher: c}, nil
}

// KeyID returns the process key fingerprint.
func (v *Vault) KeyID() string { return v.cipher.KeyID() }

// Get resolves name from the vault; unreadable rows (other key, tamper)
// resolve as absent so the chain falls through to env.
func (v *Vault) Get(ctx context.Context, name string) (string, string, bool) {
	row, ok, err := v.store.Get(ctx, name)
	if err != nil || !ok {
		return "", "", false
	}
	plain, err := v.cipher.Open(row.Name, row.Ciphertext, row.Nonce, row.KeyID)
	if err != nil {
		return "", "", false
	}
	return string(plain), "vault", true
}

// Put validates, encrypts and overwrites name (idempotent rotation).
func (v *Vault) Put(ctx context.Context, name, value, actor string, now time.Time) error {
	clean, err := ValidateValue(name, value)
	if err != nil {
		return err
	}
	ct, nonce, err := v.cipher.Seal(name, []byte(clean))
	if err != nil {
		return err
	}
	return v.store.Put(ctx, Row{
		Name: name, Ciphertext: ct, Nonce: nonce, KeyID: v.cipher.KeyID(),
		UpdatedAt: now.UTC(), UpdatedBy: actor,
	})
}

// Delete removes name; false when there was no row.
func (v *Vault) Delete(ctx context.Context, name string) (bool, error) {
	if _, ok := Known[name]; !ok {
		return false, fmt.Errorf("%w: %q", ErrUnknownSecret, name)
	}
	return v.store.Delete(ctx, name)
}

// Chain resolves vault first, env fallback (design §3.3).
type Chain []SecretSource

func (c Chain) Get(ctx context.Context, name string) (string, string, bool) {
	for _, s := range c {
		if s == nil {
			continue
		}
		if v, src, ok := s.Get(ctx, name); ok {
			return v, src, true
		}
	}
	return "", "", false
}

// Manager is what the API and the wiring talk to: a possibly-closed
// vault plus the env fallback, with the status view merged from both.
type Manager struct {
	vault  *Vault // nil = vault not configured
	reason string // why the vault is closed
	env    Env
	now    func() time.Time

	// OnChange, when set, is called after every successful Put/Delete
	// with the secret name (never the value) so a consumer that resolves
	// at (re)start can re-resolve right away — this is what makes the
	// Anthropic key's applies:"immediately" literally true.
	OnChange func(name string)
}

// NewManager builds the manager. vault may be nil with reason set.
func NewManager(vault *Vault, reason string, env Env) *Manager {
	if env == nil {
		env = Env{}
	}
	return &Manager{vault: vault, reason: reason, env: env, now: time.Now}
}

// Configured reports whether the vault opened.
func (m *Manager) Configured() bool { return m.vault != nil }

// Reason explains a closed vault ("" when configured).
func (m *Manager) Reason() string { return m.reason }

// KeyID returns the process key fingerprint ("" when closed).
func (m *Manager) KeyID() string {
	if m.vault == nil {
		return ""
	}
	return m.vault.KeyID()
}

// Get resolves vault first, then env.
func (m *Manager) Get(ctx context.Context, name string) (string, string, bool) {
	if m.vault != nil {
		return Chain{m.vault, m.env}.Get(ctx, name)
	}
	return m.env.Get(ctx, name)
}

// Put writes name to the vault (503 when closed).
func (m *Manager) Put(ctx context.Context, name, value, actor string) (Info, error) {
	if _, ok := Known[name]; !ok {
		return Info{}, fmt.Errorf("%w: %q", ErrUnknownSecret, name)
	}
	if m.vault == nil {
		return Info{}, fmt.Errorf("%w: %s", ErrVaultUnavailable, m.reason)
	}
	if err := m.vault.Put(ctx, name, value, actor, m.now()); err != nil {
		return Info{}, err
	}
	if m.OnChange != nil {
		m.OnChange(name)
	}
	return m.info(ctx, name)
}

// Delete removes name from the vault so resolution falls back to env.
func (m *Manager) Delete(ctx context.Context, name string) (Info, error) {
	if _, ok := Known[name]; !ok {
		return Info{}, fmt.Errorf("%w: %q", ErrUnknownSecret, name)
	}
	if m.vault == nil {
		return Info{}, fmt.Errorf("%w: %s", ErrVaultUnavailable, m.reason)
	}
	if _, err := m.vault.Delete(ctx, name); err != nil {
		return Info{}, err
	}
	if m.OnChange != nil {
		m.OnChange(name)
	}
	return m.info(ctx, name)
}

// List returns one Info per registry entry, sorted by name.
func (m *Manager) List(ctx context.Context) ([]Info, error) {
	out := make([]Info, 0, len(Known))
	for _, name := range Names() {
		in, err := m.info(ctx, name)
		if err != nil {
			return nil, err
		}
		out = append(out, in)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (m *Manager) info(ctx context.Context, name string) (Info, error) {
	spec := Known[name]
	in := Info{Name: name, Label: spec.Label, Applies: spec.Applies}
	if m.vault != nil {
		row, ok, err := m.vault.store.Get(ctx, name)
		if err != nil {
			return Info{}, err
		}
		if ok {
			in.Present, in.Source = true, "vault"
			at := row.UpdatedAt
			in.UpdatedAt, in.UpdatedBy = &at, row.UpdatedBy
			if _, err := m.vault.cipher.Open(row.Name, row.Ciphertext, row.Nonce, row.KeyID); err != nil {
				in.Readable = false
				var km *KeyMismatchError
				if errors.As(err, &km) {
					in.Reason = km.Error()
				} else {
					in.Reason = "stored ciphertext failed authentication"
				}
			} else {
				in.Readable = true
			}
			return in, nil
		}
	}
	if _, _, ok := m.env.Get(ctx, name); ok {
		in.Present, in.Source, in.Readable = true, "env", true
	}
	return in, nil
}

// MemoryStore is the in-memory Store for tests.
type MemoryStore struct {
	mu   sync.Mutex
	rows map[string]Row
}

func NewMemoryStore() *MemoryStore { return &MemoryStore{rows: map[string]Row{}} }

func (m *MemoryStore) Put(_ context.Context, row Row) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rows[row.Name] = row
	return nil
}

func (m *MemoryStore) Get(_ context.Context, name string) (Row, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rows[name]
	return r, ok, nil
}

func (m *MemoryStore) Delete(_ context.Context, name string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.rows[name]
	delete(m.rows, name)
	return ok, nil
}

func (m *MemoryStore) List(_ context.Context) ([]Row, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Row, 0, len(m.rows))
	for _, r := range m.rows {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
