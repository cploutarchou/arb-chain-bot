package screener

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/strategy"
)

// Snapshot is one immutable settings version (mirrors platform.Snapshot).
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

// SettingsStore persists screener_settings version rows: append-only,
// exactly one active row, same shape as platform.Store.
type SettingsStore interface {
	Insert(ctx context.Context, createdBy string, payload, diff json.RawMessage, parent int64) (version int64, createdAt time.Time, err error)
	Active(ctx context.Context) (Snapshot, bool, error)
	Get(ctx context.Context, version int64) (Snapshot, error)
	List(ctx context.Context, limit int) ([]VersionInfo, error)
}

// Template is one saved filter template (per user, design §7).
type Template struct {
	ID        string          `json:"id"`
	UserID    string          `json:"user_id"`
	Name      string          `json:"name"`
	Filters   json.RawMessage `json:"filters"`
	CreatedAt time.Time       `json:"created_at"`
}

// TemplateStore persists screener_templates rows.
type TemplateStore interface {
	ListTemplates(ctx context.Context, userID string) ([]Template, error)
	InsertTemplate(ctx context.Context, t Template) (Template, error)
	DeleteTemplate(ctx context.Context, userID, id string) error
}

// RuleStore persists screener_rules rows. actor populates the
// screener_rules.updated_by column (migration 000010) so "who last
// touched this rule" survives independently of the audit log.
type RuleStore interface {
	ListRules(ctx context.Context) ([]Rule, error)
	GetRule(ctx context.Context, id string) (Rule, error)
	InsertRule(ctx context.Context, r Rule, actor string) (Rule, error)
	UpdateRule(ctx context.Context, r Rule, actor string) (Rule, error)
	DeleteRule(ctx context.Context, id string) error
}

// Event is one alert open/close record (design §7).
type Event struct {
	ID               string     `json:"id"`
	RuleID           string     `json:"rule_id"`
	Kind             RuleKind   `json:"kind"`
	Base             string     `json:"base"`
	Quote            string     `json:"quote"`
	BuyVenue         Venue      `json:"buy_venue,omitempty"`
	SellVenue        Venue      `json:"sell_venue,omitempty"`
	OpenedAt         time.Time  `json:"opened_at"`
	ClosedAt         *time.Time `json:"closed_at,omitempty"`
	LifetimeS        int64      `json:"lifetime_s"`
	PeakNetBps       string     `json:"peak_net_bps"`
	TelegramSent     bool       `json:"telegram_sent"`
	PaperExecutionID *string    `json:"paper_execution_id,omitempty"`
	// Delivered records the delivery outcome per alert channel this
	// event's rule pushed to (docs/design/packages.md §3.1
	// alerts.channels, T-086): "telegram" is set synchronously by the
	// evaluator before the row is inserted; "email"/"webhook" start
	// "pending" (dispatched to a bounded worker so a slow/retrying
	// webhook never blocks the poll loop) and are patched to their final
	// status by SetEventDelivered once the worker finishes.
	Delivered map[string]DeliveryOutcome `json:"delivered,omitempty"`
}

// DeliveryOutcome is one channel's delivery result for an Event.
type DeliveryOutcome struct {
	Status string    `json:"status"` // sent | failed | skipped | pending
	Reason string    `json:"reason,omitempty"`
	At     time.Time `json:"at"`
}

// EventStore persists screener_events rows.
type EventStore interface {
	ListEvents(ctx context.Context, ruleID string, limit int) ([]Event, error)
	InsertEvent(ctx context.Context, e Event) error
}

// FundingPoint is one settled funding observation.
type FundingPoint struct {
	At   time.Time `json:"at"`
	Rate string    `json:"rate"`
}

// FundingSeries is one venue/base's funding history (design §7).
type FundingSeries struct {
	Venue  Venue          `json:"venue"`
	Base   string         `json:"base"`
	Points []FundingPoint `json:"points"`
}

// FundingStore persists funding_history rows.
type FundingStore interface {
	ListFunding(ctx context.Context, base string, venues []Venue, since time.Time) ([]FundingSeries, error)
	UpsertFunding(ctx context.Context, venue Venue, base string, at time.Time, rate string) error
}

// AuditEvent is the settings-change audit record handed to the sink
// (mirrors platform.AuditEvent).
type AuditEvent struct {
	Actor    string
	Source   string // web|telegram|system
	Action   string // screener.settings.apply | screener.settings.seed
	Entity   string // "screener_settings"
	EntityID string
	Before   json.RawMessage
	After    json.RawMessage
}

// Service owns the current settings snapshot, the in-memory quote/perp
// book, and the rule/event/template/funding stores. The T-066 venue
// poller (Collectors, see service_collectors.go) fills the book when
// wired; without it the book stays empty and GET /screener/status says
// so (collectors: "not_started").
type Service struct {
	Book      *Book
	Rules     RuleStore
	Events    EventStore
	Templates TemplateStore
	Funding   FundingStore
	// Collectors is the T-066 venue poller (venue.Poller) when wired;
	// nil in profiles/tests that do not run it (status: not_started).
	Collectors CollectorRunner

	// OnSettingsApplied, when set, is called after a new settings version
	// becomes active. The paper executor uses it to re-seed wallets for
	// (venue, asset) pairs the operator has just added: without it the
	// executor latches its wallets at first load and an edited
	// paper.balances silently does nothing (T-096).
	OnSettingsApplied func(Snapshot)

	// SpreadLifetime is the ONE process-wide LifetimeTracker every GET
	// /screener/spreads request shares (spreads.go's doc comment: the
	// tracker's threshold must not be request-scoped, or one viewer's
	// filter would reset "first seen" for every other viewer watching
	// the same lane). Fixed at zero net bps: lifetime is measured from
	// when a lane first turns net-positive.
	SpreadLifetime *LifetimeTracker

	store SettingsStore
	log   *slog.Logger
	audit func(context.Context, AuditEvent)

	mu  sync.Mutex // serializes writers (Apply/Load)
	cur atomic.Pointer[Snapshot]

	autoPaper AutoPaperSource // service_automation.go; nil when no executor runs
}

// NewService wires a Service over the given stores. Book must be
// non-nil; the stores may be nil in a profile that does not run this
// subsystem's persistence (callers check before dereferencing, same
// convention as api.Server's optional fields).
func NewService(book *Book, store SettingsStore, log *slog.Logger, audit func(context.Context, AuditEvent)) *Service {
	return &Service{Book: book, store: store, log: log, audit: audit, SpreadLifetime: NewLifetimeTracker(decimal.Zero)}
}

// Load installs the active settings version, seeding the store with
// Defaults() (actor "system") when none exists yet — logged either way
// (mirrors platform.Service.Load).
func (s *Service) Load(ctx context.Context) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap, ok, err := s.store.Active(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	if !ok {
		def := Defaults()
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
				Actor: "system", Source: "system", Action: "screener.settings.seed",
				Entity: "screener_settings", EntityID: fmt.Sprintf("%d", version), After: payload,
			})
		}
		s.log.Info("screener settings seeded", "version", version)
	} else {
		s.log.Info(fmt.Sprintf("screener settings v%d is authoritative", snap.Version))
	}
	if err := snap.Settings.Validate(); err != nil {
		return Snapshot{}, fmt.Errorf("screener: stored active version %d invalid: %w", snap.Version, err)
	}
	// A document stored before max_plausible_spread_bps existed carries
	// a zero there; the active snapshot shows the effective default so
	// the console edits (and re-posts) the real value.
	snap.Settings = snap.Settings.Normalised()
	s.cur.Store(&snap)
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

// Get returns one stored version.
func (s *Service) Get(ctx context.Context, version int64) (Snapshot, error) {
	return s.store.Get(ctx, version)
}

// List returns recent versions, newest first.
func (s *Service) List(ctx context.Context, limit int) ([]VersionInfo, error) {
	return s.store.List(ctx, limit)
}

// PlanDiff computes the diff doc would produce against the current
// version.
func (s *Service) PlanDiff(doc Settings) (map[string]strategy.Change, error) {
	return Diff(s.Current().Settings, doc)
}

// ApplyExpect validates, versions, persists, audits, and hot-swaps doc,
// refusing the write with ErrStaleVersion when expectedParent (if
// non-zero) no longer matches the active version — checked inside the
// writer lock (mirrors platform.Service.ApplyAuthorizedExpect).
func (s *Service) ApplyExpect(ctx context.Context, actor, source string, doc Settings, expectedParent int64) (Snapshot, error) {
	if err := doc.Validate(); err != nil {
		return Snapshot{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cur := s.Current()
	if expectedParent != 0 && cur.Version != expectedParent {
		return Snapshot{}, &StaleVersionError{Current: cur.Version}
	}
	diff, err := Diff(cur.Settings, doc)
	if err != nil {
		return Snapshot{}, err
	}
	if len(diff) == 0 && cur.Version != 0 {
		return Snapshot{}, ErrNoChange
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
	snap := Snapshot{Version: version, Settings: doc, CreatedBy: actor, CreatedAt: createdAt, ParentVer: cur.Version}
	s.cur.Store(&snap)

	before, _ := json.Marshal(cur.Settings)
	if s.audit != nil {
		s.audit(ctx, AuditEvent{
			Actor: actor, Source: source, Action: "screener.settings.apply",
			Entity: "screener_settings", EntityID: fmt.Sprintf("%d", version),
			Before: before, After: payload,
		})
	}
	s.log.Info("screener settings activated", "version", version, "parent", cur.Version, "actor", actor, "source", source, "changes", len(diff))
	if s.OnSettingsApplied != nil {
		s.OnSettingsApplied(snap)
	}
	return snap, nil
}

// MemoryStore keeps settings versions in memory: tests and DB-less dev
// profiles (mirrors platform.MemoryStore).
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

// NewMemoryStore returns an empty in-memory SettingsStore.
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
		Version: version, Settings: doc, CreatedBy: createdBy, CreatedAt: createdAt, ParentVer: parent,
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
	snap := m.rows[m.active-1].snap
	snap.Settings = snap.Settings.Clone()
	return snap, true, nil
}

func (m *MemoryStore) Get(_ context.Context, version int64) (Snapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if version < 1 || version > int64(len(m.rows)) {
		return Snapshot{}, ErrNotFound
	}
	snap := m.rows[version-1].snap
	snap.Settings = snap.Settings.Clone()
	return snap, nil
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
