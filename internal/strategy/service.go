package strategy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// Snapshot is one immutable config version. Components read a snapshot
// per evaluation and cite its Version in every decision.
type Snapshot struct {
	Version       int64     `json:"version"`
	Params        Params    `json:"params"`
	CreatedBy     string    `json:"created_by,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	ParentVersion int64     `json:"parent_version,omitempty"`
}

// VersionInfo is the list-view row (payload omitted; diff kept).
type VersionInfo struct {
	Version       int64           `json:"version"`
	CreatedBy     string          `json:"created_by,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
	Active        bool            `json:"active"`
	ParentVersion int64           `json:"parent_version,omitempty"`
	Diff          json.RawMessage `json:"diff,omitempty"`
}

// Store persists version rows. Insert must atomically deactivate the
// previous active row and activate the new one.
type Store interface {
	Insert(ctx context.Context, createdBy string, payload, diff json.RawMessage, parent int64) (version int64, createdAt time.Time, err error)
	Active(ctx context.Context) (Snapshot, bool, error)
	Get(ctx context.Context, version int64) (Snapshot, error)
	List(ctx context.Context, limit int) ([]VersionInfo, error)
}

// AuditEvent is the config-change audit record handed to the sink.
type AuditEvent struct {
	Actor    string
	Source   string // web|telegram|system|ai
	Action   string // config.apply | config.rollback
	Entity   string // "strategy_config"
	EntityID string // new version number
	Before   json.RawMessage
	After    json.RawMessage
}

// ErrNoChange rejects an Apply whose payload equals the current version.
var ErrNoChange = errors.New("strategy: no changes against current version")

// ErrInvalid wraps validation failures (callers map it to 400).
var ErrInvalid = errors.New("strategy: invalid parameters")

// ErrNotFound reports an unknown version (Get/Rollback).
var ErrNotFound = errors.New("strategy: version not found")

// Service owns the current snapshot: hot swap on Apply, consistent reads
// via Current, subscriber fan-out for components that cache derived
// forms (scanner config, risk resolver).
type Service struct {
	store Store
	log   *slog.Logger
	audit func(context.Context, AuditEvent) // nil = log only

	mu     sync.Mutex // serializes writers (Apply/Rollback/Load)
	cur    atomic.Pointer[Snapshot]
	onSwap []func(Snapshot)
}

func NewService(store Store, log *slog.Logger, audit func(context.Context, AuditEvent)) *Service {
	return &Service{store: store, log: log, audit: audit}
}

// Load installs the active version, seeding the store with defaults
// (actor "system") when none exists yet.
func (s *Service) Load(ctx context.Context) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap, ok, err := s.store.Active(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	if !ok {
		def := DefaultParams()
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
		snap = Snapshot{Version: version, Params: def, CreatedBy: "system", CreatedAt: createdAt}
		s.log.Info("strategy config seeded", "version", version)
	}
	if err := snap.Params.Validate(); err != nil {
		return Snapshot{}, fmt.Errorf("strategy: stored active version %d invalid: %w", snap.Version, err)
	}
	s.swap(snap)
	return snap, nil
}

// Current returns the active snapshot (zero Version before Load).
func (s *Service) Current() Snapshot {
	if p := s.cur.Load(); p != nil {
		return *p
	}
	return Snapshot{}
}

// Subscribe registers fn to run on every swap (registration order) and,
// when a snapshot is already active, delivers it immediately under the
// writer lock — so registration misses no version. Callbacks must be
// fast and non-blocking.
func (s *Service) Subscribe(fn func(Snapshot)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onSwap = append(s.onSwap, fn)
	if p := s.cur.Load(); p != nil {
		fn(*p)
	}
}

// Apply validates, versions, persists, audits, and hot-swaps p.
func (s *Service) Apply(ctx context.Context, actor, source string, p Params) (Snapshot, error) {
	return s.applyLocked(ctx, actor, source, "config.apply", p)
}

// Rollback re-activates version's payload as a NEW version (parent set
// to the rolled-back-to version) so history stays append-only.
func (s *Service) Rollback(ctx context.Context, actor, source string, version int64) (Snapshot, error) {
	old, err := s.store.Get(ctx, version)
	if err != nil {
		return Snapshot{}, err
	}
	snap, err := s.applyLocked(ctx, actor, source, "config.rollback", old.Params)
	if err != nil {
		return Snapshot{}, err
	}
	return snap, nil
}

// Get returns one stored version.
func (s *Service) Get(ctx context.Context, version int64) (Snapshot, error) {
	return s.store.Get(ctx, version)
}

// List returns recent versions, newest first.
func (s *Service) List(ctx context.Context, limit int) ([]VersionInfo, error) {
	return s.store.List(ctx, limit)
}

// PlanDiff computes the diff p would produce against the current
// version (API permission mapping + change previews).
func (s *Service) PlanDiff(p Params) (map[string]Change, error) {
	return Diff(s.Current().Params, p)
}

func (s *Service) applyLocked(ctx context.Context, actor, source, action string, p Params) (Snapshot, error) {
	if err := p.Validate(); err != nil {
		return Snapshot{}, fmt.Errorf("%w: %s", ErrInvalid, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cur := s.Current()
	diff, err := Diff(cur.Params, p)
	if err != nil {
		return Snapshot{}, err
	}
	if len(diff) == 0 && cur.Version != 0 {
		return Snapshot{}, ErrNoChange
	}
	payload, err := json.Marshal(p)
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
		Version: version, Params: p,
		CreatedBy: actor, CreatedAt: createdAt, ParentVersion: cur.Version,
	}
	s.swap(snap)

	before, _ := json.Marshal(cur.Params)
	ev := AuditEvent{
		Actor: actor, Source: source, Action: action,
		Entity: "strategy_config", EntityID: fmt.Sprintf("%d", version),
		Before: before, After: payload,
	}
	if s.audit != nil {
		s.audit(ctx, ev)
	}
	s.log.Info("strategy config activated",
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
	var p Params
	if err := json.Unmarshal(payload, &p); err != nil {
		return 0, time.Time{}, err
	}
	version := int64(len(m.rows) + 1)
	createdAt := m.now().UTC()
	m.rows = append(m.rows, memRow{snap: Snapshot{
		Version: version, Params: p,
		CreatedBy: createdBy, CreatedAt: createdAt, ParentVersion: parent,
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
			ParentVersion: r.snap.ParentVersion, Diff: r.diff,
		})
	}
	return out, nil
}
