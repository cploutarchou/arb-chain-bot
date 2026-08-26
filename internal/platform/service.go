package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/config"
	"github.com/cploutarchou/arb-chain-bot/internal/strategy"
)

// Snapshot is one immutable settings version.
type Snapshot struct {
	Version   int64     `json:"version"`
	Settings  Settings  `json:"settings"`
	CreatedBy string    `json:"created_by,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	ParentVer int64     `json:"parent_version,omitempty"`
}

// VersionInfo is the list-view row (payload omitted; diff kept).
type VersionInfo struct {
	Version   int64           `json:"version"`
	CreatedBy string          `json:"created_by,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
	Active    bool            `json:"active"`
	ParentVer int64           `json:"parent_version,omitempty"`
	Diff      json.RawMessage `json:"diff,omitempty"`
}

// Store persists version rows; mirrors strategy.Store exactly (same
// append-only shape, one active row) — see design §1.4 for why this is
// not a shared generic with strategy.Store.
type Store interface {
	Insert(ctx context.Context, createdBy string, payload, diff json.RawMessage, parent int64) (version int64, createdAt time.Time, err error)
	Active(ctx context.Context) (Snapshot, bool, error)
	Get(ctx context.Context, version int64) (Snapshot, error)
	List(ctx context.Context, limit int) ([]VersionInfo, error)
}

// AuditEvent is the settings-change audit record handed to the sink.
type AuditEvent struct {
	Actor    string
	Source   string // web|telegram|system
	Action   string // settings.apply | settings.rollback | settings.seed
	Entity   string // "platform_settings"
	EntityID string // new version number
	Before   json.RawMessage
	After    json.RawMessage
}

// Authorize inspects the diff a change would produce and refuses it by
// returning an error. It runs INSIDE the writer lock, against the diff
// that is actually written (TOCTOU fix mirrored from strategy.Service).
type Authorize func(diff map[string]strategy.Change) error

// Service owns the current settings snapshot: hot swap on Apply,
// consistent reads via Current, subscriber fan-out.
type Service struct {
	store Store
	log   *slog.Logger
	audit func(context.Context, AuditEvent) // nil = log only

	// Catalog, when set, makes Apply/Rollback dry-run the topology
	// (design D8: "a settings version that cannot build a topology is
	// rejected at apply time"). nil disables the check (tests that do
	// not care about symbol/triangle validity may omit it); the wiring
	// always sets it in production.
	Catalog Catalog
	// Mode, when non-empty, makes Apply/Rollback enforce
	// Settings.ValidatePaperMode (an enabled venue needs paper_enabled
	// set while the process runs in PAPER mode).
	Mode config.Mode

	mu     sync.Mutex // serializes writers (Apply/Rollback/Load)
	cur    atomic.Pointer[Snapshot]
	onSwap []func(Snapshot)
}

func NewService(store Store, log *slog.Logger, audit func(context.Context, AuditEvent)) *Service {
	return &Service{store: store, log: log, audit: audit}
}

// Load installs the active version, seeding the store with Seed(cfg)
// (actor "system") when none exists yet. On every later boot it logs
// that the first-boot env seeds are ignored (D5).
func (s *Service) Load(ctx context.Context, cfg config.Bootstrap) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap, ok, err := s.store.Active(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	if !ok {
		def := Seed(cfg)
		if err := def.Validate(); err != nil {
			return Snapshot{}, err
		}
		payload, err := json.Marshal(def)
		if err != nil {
			return Snapshot{}, err
		}
		version, createdAt, err := s.store.Insert(ctx, "", payload, nil, 0)
		if err != nil {
			return Snapshot{}, err
		}
		snap = Snapshot{Version: version, Settings: def, CreatedBy: "system", CreatedAt: createdAt}
		if s.audit != nil {
			s.audit(ctx, AuditEvent{
				Actor: "system", Source: "system", Action: "settings.seed",
				Entity: "platform_settings", EntityID: fmt.Sprintf("%d", version),
				After: payload,
			})
		}
		s.log.Info("platform settings seeded", "version", version)
	} else {
		s.log.Info(fmt.Sprintf("env symbol/asset/balance/allowlist variables ignored; platform settings v%d is authoritative", snap.Version))
	}
	if err := snap.Settings.Validate(); err != nil {
		return Snapshot{}, fmt.Errorf("platform: stored active version %d invalid: %w", snap.Version, err)
	}
	s.swap(snap)
	return snap, nil
}

// Current returns the active snapshot (zero Version before Load). The
// settings are deep-copied so no caller can mutate the stored version.
func (s *Service) Current() Snapshot {
	if p := s.cur.Load(); p != nil {
		snap := *p
		snap.Settings = snap.Settings.Clone()
		return snap
	}
	return Snapshot{}
}

// Subscribe registers fn to run on every swap (registration order) and,
// when a snapshot is already active, delivers it immediately under the
// writer lock — registration misses no version. Callbacks must be fast
// and non-blocking.
func (s *Service) Subscribe(fn func(Snapshot)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onSwap = append(s.onSwap, fn)
	if p := s.cur.Load(); p != nil {
		fn(*p)
	}
}

// Apply validates, versions, persists, audits, and hot-swaps doc.
func (s *Service) Apply(ctx context.Context, actor, source string, doc Settings) (Snapshot, error) {
	return s.ApplyAuthorized(ctx, actor, source, doc, nil)
}

// ApplyAuthorized is Apply with an in-lock authorization gate.
func (s *Service) ApplyAuthorized(ctx context.Context, actor, source string, doc Settings, authorize Authorize) (Snapshot, error) {
	return s.applyLocked(ctx, actor, source, "settings.apply", doc, authorize)
}

// Rollback re-activates version's payload as a NEW version (parent set
// to the rolled-back-to version) so history stays append-only.
func (s *Service) Rollback(ctx context.Context, actor, source string, version int64) (Snapshot, error) {
	return s.RollbackAuthorized(ctx, actor, source, version, nil)
}

// RollbackAuthorized is Rollback with an in-lock authorization gate.
func (s *Service) RollbackAuthorized(ctx context.Context, actor, source string, version int64, authorize Authorize) (Snapshot, error) {
	old, err := s.store.Get(ctx, version)
	if err != nil {
		return Snapshot{}, err
	}
	return s.applyLocked(ctx, actor, source, "settings.rollback", old.Settings, authorize)
}

// Get returns one stored version.
func (s *Service) Get(ctx context.Context, version int64) (Snapshot, error) {
	return s.store.Get(ctx, version)
}

// List returns recent versions, newest first.
func (s *Service) List(ctx context.Context, limit int) ([]VersionInfo, error) {
	return s.store.List(ctx, limit)
}

// PlanDiff computes the diff doc would produce against the current
// version (API permission mapping + change previews).
func (s *Service) PlanDiff(doc Settings) (map[string]strategy.Change, error) {
	return strategy.DiffAny(s.Current().Settings, doc)
}

func (s *Service) applyLocked(ctx context.Context, actor, source, action string, doc Settings, authorize Authorize) (Snapshot, error) {
	if err := doc.Validate(); err != nil {
		return Snapshot{}, fmt.Errorf("%w: %s", ErrInvalid, err)
	}
	if s.Mode != "" {
		if err := doc.ValidatePaperMode(s.Mode); err != nil {
			return Snapshot{}, fmt.Errorf("%w: %s", ErrInvalid, err)
		}
	}
	if s.Catalog != nil {
		// D8: a document that cannot build a topology is rejected HERE,
		// at apply time (and identically at rollback time — a version
		// whose symbols were since delisted must fail the same way),
		// never discovered for the first time when the engine restarts.
		if _, err := ValidateAgainstCatalog(ctx, doc, s.Catalog); err != nil {
			return Snapshot{}, err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cur := s.Current()
	diff, err := strategy.DiffAny(cur.Settings, doc)
	if err != nil {
		return Snapshot{}, err
	}
	if len(diff) == 0 && cur.Version != 0 {
		return Snapshot{}, ErrNoChange
	}
	if authorize != nil {
		if err := authorize(diff); err != nil {
			return Snapshot{}, err
		}
	}
	payload, err := json.Marshal(doc)
	if err != nil {
		return Snapshot{}, err
	}
	diffJSON, err := json.Marshal(diff)
	if err != nil {
		return Snapshot{}, err
	}
	version, createdAt, err := s.store.Insert(ctx, actor, payload, diffJSON, cur.Version)
	if err != nil {
		return Snapshot{}, err
	}
	snap := Snapshot{
		Version: version, Settings: doc,
		CreatedBy: actor, CreatedAt: createdAt, ParentVer: cur.Version,
	}
	s.swap(snap)

	before, _ := json.Marshal(cur.Settings)
	ev := AuditEvent{
		Actor: actor, Source: source, Action: action,
		Entity: "platform_settings", EntityID: fmt.Sprintf("%d", version),
		Before: before, After: payload,
	}
	if s.audit != nil {
		s.audit(ctx, ev)
	}
	s.log.Info("platform settings activated",
		"version", version, "parent", cur.Version,
		"actor", actor, "source", source, "action", action, "changes", len(diff))
	return snap, nil
}

// swap requires s.mu held.
func (s *Service) swap(snap Snapshot) {
	s.cur.Store(&snap)
	for _, fn := range s.onSwap {
		fn(snap)
	}
}

// MemoryStore keeps versions in memory: tests and DB-less dev profiles.
type MemoryStore struct {
	mu     sync.Mutex
	rows   []memRow
	active int64
	now    func() time.Time
}

type memRow struct {
	snap Snapshot
	diff json.RawMessage
}

func NewMemoryStore() *MemoryStore { return &MemoryStore{now: time.Now} }

func (m *MemoryStore) Insert(_ context.Context, createdBy string, payload, diff json.RawMessage, parent int64) (int64, time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var doc Settings
	if err := json.Unmarshal(payload, &doc); err != nil {
		return 0, time.Time{}, err
	}
	version := int64(len(m.rows) + 1)
	createdAt := m.now().UTC()
	m.rows = append(m.rows, memRow{snap: Snapshot{
		Version: version, Settings: doc,
		CreatedBy: createdBy, CreatedAt: createdAt, ParentVer: parent,
	}, diff: diff})
	m.active = version
	return version, createdAt, nil
}

func (m *MemoryStore) Active(_ context.Context) (Snapshot, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active == 0 {
		return Snapshot{}, false, nil
	}
	return m.rows[m.active-1].snap, true, nil
}

func (m *MemoryStore) Get(_ context.Context, version int64) (Snapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if version < 1 || version > int64(len(m.rows)) {
		return Snapshot{}, ErrNotFound
	}
	return m.rows[version-1].snap, nil
}

func (m *MemoryStore) List(_ context.Context, limit int) ([]VersionInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit <= 0 || limit > len(m.rows) {
		limit = len(m.rows)
	}
	out := make([]VersionInfo, 0, limit)
	for i := len(m.rows) - 1; i >= 0 && len(out) < limit; i-- {
		r := m.rows[i]
		out = append(out, VersionInfo{
			Version: r.snap.Version, CreatedBy: r.snap.CreatedBy,
			CreatedAt: r.snap.CreatedAt, Active: r.snap.Version == m.active,
			ParentVer: r.snap.ParentVer, Diff: r.diff,
		})
	}
	return out, nil
}
