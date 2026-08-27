package app

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/config"
	"github.com/cploutarchou/arb-chain-bot/internal/exchange"
	"github.com/cploutarchou/arb-chain-bot/internal/exchange/binance"
	"github.com/cploutarchou/arb-chain-bot/internal/execution"
	"github.com/cploutarchou/arb-chain-bot/internal/fees"
	"github.com/cploutarchou/arb-chain-bot/internal/graph"
	"github.com/cploutarchou/arb-chain-bot/internal/marketdata"
	"github.com/cploutarchou/arb-chain-bot/internal/metrics"
	"github.com/cploutarchou/arb-chain-bot/internal/notification"
	"github.com/cploutarchou/arb-chain-bot/internal/opportunity"
	"github.com/cploutarchou/arb-chain-bot/internal/orderbook"
	"github.com/cploutarchou/arb-chain-bot/internal/paper"
	"github.com/cploutarchou/arb-chain-bot/internal/platform"
	"github.com/cploutarchou/arb-chain-bot/internal/portfolio"
	"github.com/cploutarchou/arb-chain-bot/internal/pricing"
	"github.com/cploutarchou/arb-chain-bot/internal/realtime"
	"github.com/cploutarchou/arb-chain-bot/internal/reservation"
	"github.com/cploutarchou/arb-chain-bot/internal/risk"
	"github.com/cploutarchou/arb-chain-bot/internal/scanner"
	"github.com/cploutarchou/arb-chain-bot/internal/simulation"
	"github.com/cploutarchou/arb-chain-bot/internal/storage"
	"github.com/cploutarchou/arb-chain-bot/internal/strategy"
)

// bpsDivisor converts platform-settings basis points into a fractional
// rate (fees.Rate): 10 bps -> 0.001.
var bpsDivisor = decimal.NewFromInt(10_000)

// Engine assembles the trading core for the first exchange: metadata →
// topology → feed → books → scanner. It runs until ctx cancels; a failed
// metadata bootstrap keeps retrying rather than pretending to scan.
//
// Engine.Run is re-entered by app.Supervisor across a restart (T-057):
// nothing this file registers on the Hub, the Metrics meter, or the
// Notifier may capture a per-run local — it must resolve through an
// e.mu-guarded accessor, or the console and Prometheus end up reporting
// a dead run after the first restart. That single invariant is the
// review checklist for every change in this file
// (docs/design/platform-settings-and-restart.md §2.1).
type Engine struct {
	cfg config.Bootstrap
	log *slog.Logger

	// RESTHost overrides the metadata/REST endpoint; empty uses
	// binance.MarketDataRESTHost. Test seam only (integration tests
	// point it at an httptest server).
	RESTHost string
	// WSHost overrides the market-data WebSocket endpoint; empty uses
	// binance.MarketDataWSHost. Test seam only — without it, a
	// re-entrancy test would dial the real venue over the network
	// (this sandbox has outbound access, so the test would be slow and
	// non-hermetic rather than merely failing).
	WSHost string

	// Hub, when set by the component wiring, receives scanner/health
	// events for the console.
	Hub *realtime.Hub
	// Store, when set, enables persistence through the outbox.
	Store *storage.Store
	// Strategy, when set, supplies the versioned dynamic config and
	// hot-swaps the scanner on every applied version (T-034).
	Strategy *strategy.Service
	// Metrics, when set, receives the engine's instrument sources; hot
	// paths pay only atomic adds (T-035).
	Metrics *metrics.Metrics
	// Notifier, when set, receives platform alerts (never called on the
	// hot path; keys per SKILL §58 with cooldown/dedup in the service).
	Notifier *notification.Service
	// Center, when set, supplies the live active-alert count (AI input).
	Center *notification.Center

	mu sync.RWMutex

	// settings is the platform-settings document the NEXT Run reads
	// (T-057 E7). ApplySettings is called by the supervisor between
	// runs only, so Run reads it without contention. settingsVersion==0
	// means ApplySettings was never called (direct-constructed engines
	// in tests / ProfileScanner without a platform.Service): Run then
	// falls back to platform.Seed(e.cfg), preserving today's behavior.
	settings        platform.Settings
	settingsVersion int64
	// runMode is the operating mode THIS run snapshotted at entry (T-059
	// §2.3): a mode never changes mid-run; Mode() reports it.
	runMode config.Mode

	scn       *scanner.Scanner
	topo      *graph.Topology
	pap       *paper.Engine
	rctl      *marketdata.RecorderControl
	port      *portfolio.Portfolio
	feed      *binance.Feed
	resv      *reservation.Manager
	brk       *risk.Registry
	books     *orderbook.Set    // retained so metrics accessors survive a restart (E4)
	starts    []exchange.Asset  // this run's starting assets (E4 accessor source)
	catalog   []exchange.Market // full bootstrap metadata slice, retained for Catalog() (E2)
	marker    portfolio.BookMarker
	ready     bool
	sessionID string          // current paper/persistence session id; rotated by ResetPaper (BL-10)
	outbox    *storage.Outbox // this run's outbox, nil without persistence (BL-18 queue depth)
	latency   *latencyWindow  // this run's per-exchange latency samples (BL-18 percentiles)
	// runToken (P1-1) is bumped, under e.mu, at the top of every Run call;
	// each call captures its own value and only its OWN deferred cleanup
	// may act on it, so a stale run's cleanup can never clobber a newer
	// run's state.
	runToken uint64
	// pausePaperOnBoot/pausePaperOnBootSet (P2-2) carry the supervisor's
	// SetPaperPaused decision into the NEXT Run call's PAPER-mode
	// assembly, consumed (read-and-cleared) exactly once so it applies to
	// that one run only.
	pausePaperOnBoot    bool
	pausePaperOnBootSet bool

	metricsOnce   sync.Once // E4: RegisterEngine wired once across restarts
	subscribeOnce sync.Once // E6: Strategy.Subscribe registered once across restarts

	oppMu             sync.Mutex
	recentOpps        []RecentOpportunity
	rejectCounts      map[string]int64     // risk reason code → count (AI input)
	rejectPersistedAt map[string]time.Time // "triangle|reason" → last persisted (BL-31 cooldown)
}

// RecentOpportunity is a compact ring entry for /opportunities and the
// console's recent list.
type RecentOpportunity struct {
	ID         string    `json:"id"`
	TriangleID string    `json:"triangle_id"`
	NetBps     string    `json:"net_bps"`
	Profit     string    `json:"profit"`
	Input      string    `json:"input"`
	At         time.Time `json:"at"`
}

const recentOppCap = 32

// RecentOpportunities returns the latest qualified opportunities,
// newest first.
func (e *Engine) RecentOpportunities(limit int) []RecentOpportunity {
	e.oppMu.Lock()
	defer e.oppMu.Unlock()
	if limit <= 0 || limit > len(e.recentOpps) {
		limit = len(e.recentOpps)
	}
	out := make([]RecentOpportunity, 0, limit)
	for i := len(e.recentOpps) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, e.recentOpps[i])
	}
	return out
}

func (e *Engine) countReject(code string) {
	if code == "" {
		return
	}
	e.oppMu.Lock()
	defer e.oppMu.Unlock()
	if e.rejectCounts == nil {
		e.rejectCounts = map[string]int64{}
	}
	if len(e.rejectCounts) < 64 || e.rejectCounts[code] > 0 {
		e.rejectCounts[code]++
	}
}

// riskRejectCooldown bounds how often the SAME (triangle, reason code)
// rejection is persisted to risk_events (BL-31). Unlike rejectCounts
// (an in-memory histogram, cheap to update on every rejection), a
// persisted event is a DB write through the outbox's single writer
// (Outbox.write does one INSERT per record, no batching): a busy
// triangle can reject thousands of times a minute for the same reason,
// and persisting every one would make the reject path the dominant
// outbox load, saturate its bounded queue, and trip OnPersistError —
// degrading persistence of opportunities/cycles too. First occurrence
// per (triangle, reason) per window still lands, so the timeline shows
// every DISTINCT thing that happened, not every repetition; the
// unthrottled total stays visible via RejectCounts()/api/v1/risk.
const riskRejectCooldown = 60 * time.Second

// shouldPersistRiskReject reports whether this (triangle, reason)
// rejection is due for a fresh risk_events row.
func (e *Engine) shouldPersistRiskReject(triangleID, reasonCode string, now time.Time) bool {
	key := triangleID + "|" + reasonCode
	e.oppMu.Lock()
	defer e.oppMu.Unlock()
	if e.rejectPersistedAt == nil {
		e.rejectPersistedAt = map[string]time.Time{}
	}
	if last, ok := e.rejectPersistedAt[key]; ok && now.Sub(last) < riskRejectCooldown {
		return false
	}
	// Bounded like rejectCounts: a pathological number of distinct
	// (triangle, reason) pairs must not grow this map unboundedly.
	if len(e.rejectPersistedAt) >= 4096 {
		return false
	}
	e.rejectPersistedAt[key] = now
	return true
}

// RejectCounts snapshots the rejection-reason histogram.
func (e *Engine) RejectCounts() map[string]int64 {
	e.oppMu.Lock()
	defer e.oppMu.Unlock()
	out := make(map[string]int64, len(e.rejectCounts))
	for k, v := range e.rejectCounts {
		out[k] = v
	}
	return out
}

func (e *Engine) rememberOpportunity(op RecentOpportunity) {
	e.oppMu.Lock()
	defer e.oppMu.Unlock()
	e.recentOpps = append(e.recentOpps, op)
	if len(e.recentOpps) > recentOppCap {
		e.recentOpps = e.recentOpps[len(e.recentOpps)-recentOppCap:]
	}
}

// notify is a nil-safe alert emit.
func (e *Engine) notify(sev notification.Severity, key, title, body string) {
	if e.Notifier != nil {
		e.Notifier.Notify(notification.Event{Severity: sev, Key: key, Title: title, Body: body})
	}
}

func NewEngine(cfg config.Bootstrap, log *slog.Logger) *Engine {
	return &Engine{cfg: cfg, log: log}
}

func (e *Engine) Name() string { return "engine" }

// Status is the scanner-facing snapshot for the API layer.
type EngineStatus struct {
	Ready       bool     `json:"ready"`
	Triangles   int      `json:"triangles"`
	Markets     []string `json:"markets"`
	Evaluations int64    `json:"evaluations"`
	Qualified   int64    `json:"qualified"`
	Rejected    int64    `json:"rejected"`
	Skipped     int64    `json:"skipped_unhealthy"`
	Dropped     int64    `json:"dropped_events"`

	Paper *PaperStatus `json:"paper,omitempty"`
}

// PaperStatus reports the paper engine when PAPER mode is active.
type PaperStatus struct {
	Running   bool  `json:"running"`
	Active    int   `json:"active_simulations"`
	Received  int64 `json:"received"`
	Completed int64 `json:"completed"`
	Failed    int64 `json:"failed"`
	Skipped   int64 `json:"skipped"`
}

func (e *Engine) Status() EngineStatus {
	e.mu.RLock()
	defer e.mu.RUnlock()
	st := EngineStatus{Ready: e.ready}
	if e.topo != nil {
		st.Triangles = len(e.topo.Triangles)
		for id := range e.topo.ByMarket {
			st.Markets = append(st.Markets, id.String())
		}
	}
	if e.scn != nil {
		st.Evaluations = e.scn.Stats.Evaluations.Load()
		st.Qualified = e.scn.Stats.Qualified.Load()
		st.Rejected = e.scn.Stats.Rejected.Load()
		st.Skipped = e.scn.Stats.SkippedBooks.Load()
		st.Dropped = e.scn.Stats.DroppedEvts.Load()
	}
	if e.pap != nil {
		ps := e.pap.Snapshot()
		st.Paper = &PaperStatus{
			Running: e.pap.Running(), Active: e.pap.Active(),
			Received: ps.Received, Completed: ps.Completed,
			Failed: ps.Failed, Skipped: ps.Skipped,
		}
	}
	return st
}

// Recorder exposes the in-process recording control (nil before the
// engine has bootstrapped).
func (e *Engine) Recorder() *marketdata.RecorderControl {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.rctl
}

// Paper exposes the paper engine control surface (nil outside PAPER mode).
func (e *Engine) Paper() *paper.Engine {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.pap
}

// Catalog returns the full bootstrap metadata slice retained from the
// last successful metadata fetch (E2), independent of the configured
// symbol scope — the seam ValidateAgainstCatalog's engineCatalog uses.
func (e *Engine) Catalog() []exchange.Market {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]exchange.Market, len(e.catalog))
	copy(out, e.catalog)
	return out
}

// SessionID returns the current paper/persistence session id (E10's
// EndPaperSession input; the Supervisor reads this right before
// cancelling a run so it knows which session to close).
func (e *Engine) SessionID() string {
	return e.currentSessionID()
}

// SetPaperPaused (P2-2) tells the NEXT Run call's PAPER-mode assembly to
// start paused (true) or running (false) instead of the unconditional
// Resume() a direct call always used to do. Consumed exactly once — by
// that Run call — then cleared, so it never leaks into a LATER, unrelated
// run. Supervisor calls this right before every restart-driven launch
// with the pause state the paper engine had immediately before the
// restart, carrying it through even when the new run's own bootstrap
// takes long enough to miss ReadyTimeout (Supervisor.Paper() can return
// nil during that window, so re-pausing after the fact has nothing to
// act on).
func (e *Engine) SetPaperPaused(paused bool) {
	e.mu.Lock()
	e.pausePaperOnBoot, e.pausePaperOnBootSet = paused, true
	e.mu.Unlock()
}

// consumePausePaperOnBoot reads and clears the pending SetPaperPaused
// decision. set is false when SetPaperPaused was never called before
// this Run (direct-constructed engines, tests, or the initial boot,
// which is not a restart) — the caller then keeps today's unconditional
// Resume() behavior.
func (e *Engine) consumePausePaperOnBoot() (paused, set bool) {
	e.mu.Lock()
	paused, set = e.pausePaperOnBoot, e.pausePaperOnBootSet
	e.pausePaperOnBoot, e.pausePaperOnBootSet = false, false
	e.mu.Unlock()
	return paused, set
}

// RunningWorkers returns the live scanner's Workers count (0 when no
// scanner is running). Supervisor's "strategy vN (scanner.workers)"
// pending-reason source compares this against a newly-applied strategy
// snapshot (design §2.3 "one restart banner, two documents").
func (e *Engine) RunningWorkers() int {
	if scn := e.currentScanner(); scn != nil {
		return scn.CurrentConfig().Workers
	}
	return 0
}

// ApplySettings stores the platform-settings document the NEXT Run
// reads (T-057 E7). The supervisor calls this only between runs.
func (e *Engine) ApplySettings(s platform.Settings, version int64) {
	e.mu.Lock()
	e.settings = s.Clone()
	e.settingsVersion = version
	e.mu.Unlock()
}

// currentSettings resolves the document Run should use: the last
// applied one, or platform.Seed(e.cfg) when ApplySettings was never
// called (direct-constructed engines in tests keep working unchanged).
func (e *Engine) currentSettings() platform.Settings {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.settingsLocked()
}

func (e *Engine) settingsLocked() platform.Settings {
	if e.settingsVersion == 0 {
		return platform.Seed(e.cfg)
	}
	return e.settings.Clone()
}

// Mode returns the operating mode the engine is RUNNING (the value Run
// snapshotted at entry), falling back to the configured document's mode
// before the first Run (T-059 §2.3). It never reads cfg.Mode directly:
// ARB_MODE is a first-boot seed only.
func (e *Engine) Mode() config.Mode {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.runMode != "" {
		return e.runMode
	}
	return e.settingsLocked().Platform.Mode
}

// --- e.mu-guarded accessors -------------------------------------------
//
// Every callback registered on a process-lifetime object (Hub topics,
// Metrics pull sources) must resolve the CURRENT run's objects through
// one of these instead of closing over a per-run local (design §2.1).

func (e *Engine) currentScanner() *scanner.Scanner {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.scn
}

func (e *Engine) currentTopology() *graph.Topology {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.topo
}

func (e *Engine) currentFeed() *binance.Feed {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.feed
}

func (e *Engine) currentBooks() *orderbook.Set {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.books
}

func (e *Engine) currentReservation() *reservation.Manager {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.resv
}

func (e *Engine) currentBreakers() *risk.Registry {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.brk
}

func (e *Engine) currentPortfolio() *portfolio.Portfolio {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.port
}

// currentOutbox exposes this run's persistence queue (BL-18 queue
// depth); nil without persistence or before Run reaches it.
func (e *Engine) currentOutbox() *storage.Outbox {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.outbox
}

// currentLatency exposes this run's latency sample window (BL-18
// percentiles); nil only before Run's reset block runs.
func (e *Engine) currentLatency() *latencyWindow {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.latency
}

func (e *Engine) currentStarts() []exchange.Asset {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]exchange.Asset, len(e.starts))
	copy(out, e.starts)
	return out
}

// currentSessionID / setSessionID let ResetPaper rotate the session a
// settled cycle is tagged with (new cycles get the new session; already
// -persisted history keeps its original tag — SKILL §37).
func (e *Engine) currentSessionID() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.sessionID
}

func (e *Engine) setSessionID(id string) {
	e.mu.Lock()
	e.sessionID = id
	e.mu.Unlock()
}

// errPaperNotBootstrapped: ResetPaper was called before the engine
// finished its PAPER-mode bootstrap (paper.Engine does not exist yet).
var errPaperNotBootstrapped = errors.New("engine: paper engine not bootstrapped yet")

// paperInitialBalances recomputes the configured starting balances the
// same way Run does, so ResetPaper rebuilds the ledger to exactly what a
// fresh boot (or the last applied platform settings) would start with.
// This reads the applied platform settings, NOT ARB_PAPER_BALANCE — env
// vars are first-boot seeds only (design D5); a paper reset must not
// silently revert balances to the environment default.
func (e *Engine) paperInitialBalances() (map[exchange.Asset]decimal.Decimal, error) {
	settings := e.currentSettings()
	initial := make(map[exchange.Asset]decimal.Decimal, len(settings.Paper.Balances))
	for asset, raw := range settings.Paper.Balances {
		v, err := decimal.NewFromString(raw)
		if err != nil {
			return nil, fmt.Errorf("engine: invalid platform paper balance for %s: %w", asset, err)
		}
		initial[exchange.Asset(asset)] = v
	}
	return initial, nil
}

// ResetPaper rebuilds the paper engine's reservation ledger and
// portfolio to the configured initial balances and registers a new
// paper session row, preserving every historical cycle/order under its
// original session id (BL-10, SKILL §37: "do not delete historical
// metrics when a new session begins"). It refuses (paper.ErrActive) if
// the engine is running or a simulation is in flight — the caller is
// expected to have paused first.
//
// Ordering matters here: paper_cycles.session_id is a NOT NULL foreign
// key into paper_sessions, so the new session row must exist in the
// database BEFORE any cycle can be tagged with its id. This method
// registers the session first and only rotates the live session id (so
// the next settled cycle picks it up) after that write succeeds — if
// EnsurePaperSession fails, ResetPaper aborts without touching the
// ledger/portfolio or the live session id at all, rather than leaving
// every subsequent cycle insert rejected by the foreign key.
func (e *Engine) ResetPaper(ctx context.Context) error {
	e.mu.RLock()
	pap := e.pap
	e.mu.RUnlock()
	if pap == nil {
		return errPaperNotBootstrapped
	}
	// Cheap, non-mutating idle check up front: the common rejection path
	// (still running / a simulation in flight) then never touches the
	// database or creates a session row nobody will use.
	if pap.Running() || pap.Active() > 0 {
		return paper.ErrActive
	}
	initial, err := e.paperInitialBalances()
	if err != nil {
		return err
	}
	newSession := newULID()
	if e.Store != nil {
		balances := make(map[string]string, len(initial))
		for a, v := range initial {
			balances[string(a)] = v.String()
		}
		if err := e.Store.EnsurePaperSession(ctx, newSession, string(e.Mode()), balances, 1, e.cfg.Seed); err != nil {
			return fmt.Errorf("engine: register new paper session: %w", err)
		}
	}
	// The session row (if any) is durable now; only after that do we
	// rebuild the in-memory ledger/portfolio and start tagging cycles
	// with the new session id.
	if err := pap.Reset(initial); err != nil {
		return err
	}
	e.setSessionID(newSession)
	return nil
}

func (e *Engine) Run(ctx context.Context) error {
	// E1: reset every per-run field BEFORE bootstrapMetadata, which
	// retries with backoff — otherwise Status() advertises the PREVIOUS
	// run's dead topology as ready for minutes into a restart.
	e.mu.Lock()
	e.scn, e.topo, e.pap, e.rctl, e.port = nil, nil, nil, nil, nil
	e.feed, e.resv, e.brk, e.books, e.starts, e.catalog = nil, nil, nil, nil, nil, nil
	e.marker = portfolio.BookMarker{}
	e.ready = false
	e.outbox = nil
	e.latency = &latencyWindow{}
	// P1-1 (engine.go half): a per-run token so THIS call's deferred
	// "ready = false" (below) can never clear a LATER run's readiness.
	// Without it, a run that returns late (e.g. after a slow shutdown
	// drain) would run its defer after a NEXT run has already set
	// e.ready = true, wiping out the live run's readiness out from under
	// it — exactly the failure mode a supervisor bug that briefly allows
	// two overlapping runs would trigger.
	e.runToken++
	myToken := e.runToken
	// T-059: the mode is a per-run snapshot of the applied document.
	mode := e.settingsLocked().Platform.Mode
	e.runMode = mode
	e.mu.Unlock()
	e.oppMu.Lock()
	e.recentOpps, e.rejectCounts, e.rejectPersistedAt = nil, nil, nil
	e.oppMu.Unlock()

	// P2-6: every resource THIS Run call creates (metadata retries,
	// market upsert, the recorder control's session lifetime, spawned
	// children) is scoped to runCtx, not the bare `ctx` parameter —
	// runCtx is cancelled by Run's OWN deferred cleanup no later than
	// when Run returns, for ANY reason (a fatal child error included,
	// not only the caller cancelling `ctx`). Binding the recorder control
	// to the wider `ctx` instead (the pre-fix shape) let an API-started
	// recording session outlive a run that exited on an internal error:
	// `ctx` itself is only cancelled by the SUPERVISOR, once it notices,
	// so the recorder kept writing frames for an engine that was already
	// gone. Created here (not at its previous, later position) so both
	// paths that construct run-scoped resources before the child-spawn
	// section (recorder control, PAPER-mode session registration) get it
	// too.
	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()

	host := e.RESTHost
	if host == "" {
		host = binance.MarketDataRESTHost
	}
	rest := binance.NewRESTClient(host)

	markets, err := e.bootstrapMetadata(runCtx, rest)
	if err != nil {
		return err
	}
	e.mu.Lock()
	e.catalog = markets // E2: full slice retained regardless of configured scope
	e.mu.Unlock()

	settings := e.currentSettings()
	venue, ok := settings.Venues[string(binance.ID)]
	if !ok {
		return fmt.Errorf("engine: no platform settings configured for venue %s", binance.ID)
	}
	symbols := make(map[string]bool, len(venue.Symbols))
	for _, s := range venue.Symbols {
		symbols[s] = true
	}
	var scoped []exchange.Market
	rules := make(map[exchange.MarketID]exchange.InstrumentRules)
	var feedSymbols []exchange.Symbol
	for _, m := range markets {
		if !symbols[string(m.ID.Symbol)] {
			continue
		}
		scoped = append(scoped, m)
		rules[m.ID] = m.Rules
		feedSymbols = append(feedSymbols, m.ID.Symbol)
	}
	if len(scoped) == 0 {
		return fmt.Errorf("engine: none of the configured symbols exist on %s", binance.ID)
	}

	starts := make([]exchange.Asset, 0, len(venue.StartingAssets))
	for _, a := range venue.StartingAssets {
		starts = append(starts, exchange.Asset(a))
	}
	topo := graph.Build(binance.ID, scoped, starts)
	e.log.Info("triangle topology built",
		"markets", len(scoped), "triangles", len(topo.Triangles),
		"rejected_untradeable", topo.Rejected.Untradeable)

	var outbox *storage.Outbox
	sessionID := newULID()
	e.setSessionID(sessionID)
	if e.Store != nil {
		// P2-1: persist the FULL bootstrap catalog (markets), not just the
		// currently configured scope. storage.Catalog (the platform.Catalog
		// the API profile falls back to when no live engine is attached)
		// reads this same table; if only `scoped` ever landed here, adding
		// a symbol through the console could never validate — it would
		// look like an unknown_symbol even though the venue genuinely
		// lists it, because the row for it was never written.
		if err := e.Store.UpsertMarkets(runCtx, markets); err != nil {
			e.log.Warn("market metadata sync failed", "error", err)
		}
		outbox = &storage.Outbox{Store: e.Store, Log: e.log, SessionID: sessionID}
	}
	e.mu.Lock()
	e.outbox = outbox
	e.mu.Unlock()

	// Recording control: raw frames + splice snapshots for replay. The
	// control is always wired so the console can start/stop sessions
	// in-process; RECORD mode auto-starts one at boot.
	streamTable := make(map[exchange.Symbol]uint16, len(feedSymbols))
	for i, sym := range feedSymbols {
		streamTable[sym] = uint16(i + 1) //nolint:gosec // bounded by symbol count
	}
	rctl := &marketdata.RecorderControl{
		Dir:            e.cfg.RecordingDir,
		Log:            e.log,
		StreamOfSymbol: streamTable,
		NewSessionID:   newULID,
	}
	rctl.Bind(runCtx)
	if e.Store != nil {
		streams := (&marketdata.Recorder{StreamOfSymbol: streamTable}).Streams()
		rctl.OnSegment = func(recID string, startedAt time.Time, meta marketdata.SegmentMeta) {
			regCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := e.Store.UpsertRecordingSegment(regCtx, recID, binance.ID, streams, startedAt, meta); err != nil {
				e.log.Warn("recording metadata registration failed", "error", err)
			}
		}
	}
	if e.Hub != nil {
		rctl.OnChange = func(st marketdata.RecorderStatus) {
			_ = e.Hub.Publish("recordings", map[string]any{"kind": "recorder", "recorder": st})
		}
	}
	e.mu.Lock()
	e.rctl = rctl
	e.mu.Unlock()

	// Venue fee schedule from the applied platform settings (T-057 §1.5):
	// bps -> fractional rate, per-symbol overrides, then the compiled-in
	// token-discount profile gated on the operator's toggle.
	sched, err := fees.NewSchedule(binance.ID, binance.Capabilities.FeeConvention,
		fees.Rate{Maker: venue.Fees.MakerBps.Div(bpsDivisor), Taker: venue.Fees.TakerBps.Div(bpsDivisor)})
	if err != nil {
		return err
	}
	for sym, o := range venue.Fees.Overrides {
		mid := exchange.MarketID{Exchange: binance.ID, Symbol: exchange.Symbol(sym)}
		if err := sched.SetOverride(mid, fees.Rate{Maker: o.MakerBps.Div(bpsDivisor), Taker: o.TakerBps.Div(bpsDivisor)}); err != nil {
			return fmt.Errorf("engine: fee override %s: %w", sym, err)
		}
	}
	// P1-2: platform.FeeSettings.validate now refuses token_discount:true
	// for every venue (no pay-asset debit ledger exists), so this branch
	// is unreachable through any document that has passed Apply/Rollback
	// or boot's Load since that fix landed. It stays here, defensively,
	// only for a settings row persisted BEFORE the fix — never a path a
	// freshly-validated document can take.
	if venue.Fees.TokenDiscount {
		if d, ok := fees.VenueDiscount(binance.ID); ok {
			d.Enabled = true
			sched.Discount = d
		}
	}

	initial := make(map[exchange.Asset]decimal.Decimal, len(starts))
	for asset, raw := range settings.Paper.Balances {
		v, err := decimal.NewFromString(raw)
		if err != nil {
			return fmt.Errorf("engine: invalid platform paper balance for %s: %w", asset, err)
		}
		initial[exchange.Asset(asset)] = v
	}
	resv := reservation.New(initial, newULID, time.Now)

	wsHost := e.WSHost
	if wsHost == "" {
		wsHost = binance.MarketDataWSHost
	}
	books := orderbook.NewSet()
	feed := &binance.Feed{
		WSHost:  wsHost,
		REST:    rest,
		Books:   books,
		Symbols: feedSymbols,
		Log:     e.log,
	}
	feed.RawTap = rctl.TapWS
	feed.SnapTap = rctl.TapSnapshot

	breakers := risk.NewRegistry(func(tr risk.Transition) {
		e.log.Warn("circuit breaker transition",
			"breaker", tr.Name, "scope", tr.Scope,
			"from", tr.From.String(), "to", tr.To.String(), "reason", tr.Reason)
		sev := notification.SeverityInfo
		if tr.To.String() == "OPEN" {
			sev = notification.SeverityCritical
		}
		e.notify(sev, "breaker:"+tr.Scope, "Circuit breaker "+tr.To.String(),
			fmt.Sprintf("%s (%s): %s → %s (%s)", tr.Name, tr.Scope, tr.From.String(), tr.To.String(), tr.Reason))
		// BL-31: persist every transition so the Risk Center's timeline
		// survives a restart (breaker transitions are state changes, not
		// per-frame hot-path traffic — enqueue is non-blocking regardless).
		if outbox != nil {
			outbox.Enqueue(storage.Record{Kind: "risk_event", RiskEvent: &storage.RiskEvent{
				ID: newULID(), TS: tr.At, Kind: "breaker_transition",
				Subject: tr.Scope, LimitName: tr.Name,
				Observed: tr.From.String(), Threshold: tr.To.String(),
				Action: tr.Reason, BreakerState: tr.To.String(),
			}})
		}
	})

	scn := &scanner.Scanner{
		Topo:     topo,
		Books:    books,
		Rules:    rules,
		Fees:     sched,
		Resolver: defaultRiskLimits(),
		Breakers: breakers,
		Capital:  resv,
		Clock:    time.Now,
		IDGen:    newULID,
		Cfg: scanner.Config{
			ConfigVersion: 1,
			Buffers:       opportunity.Buffers{LatencyBps: decimal.NewFromInt(5), RiskBps: decimal.NewFromInt(5)},
			TTL:           400 * time.Millisecond,
			MinInput:      decimal.NewFromInt(50),
			Depth:         50,
			Workers:       2,
			Search:        pricing.DefaultSizeSearch,
			MaxBookAge:    2 * time.Second,
		},
		Out: make(chan scanner.Event, 256),
	}
	scn.ClockHealthy.Store(true)
	e.attachRunObservers(scn, feed)
	if e.Strategy != nil {
		// E6: register the hot-swap callback ONCE across the engine's
		// lifetime — Strategy.Service.Subscribe otherwise accumulates one
		// callback per restart, each calling SetStrategy on an
		// increasingly stale (or dead) scanner. The callback resolves
		// the CURRENT run's scanner through the accessor and no-ops
		// once Run has moved on (or not started a new one yet).
		e.subscribeOnce.Do(func() {
			e.Strategy.Subscribe(func(snap strategy.Snapshot) {
				if live := e.currentScanner(); live != nil {
					live.SetStrategy(scanner.Strategy{
						Cfg:      snap.Params.ScannerConfig(snap.Version),
						Resolver: snap.Params.RiskResolver(),
					})
				}
			})
		})
		// Subscribe only fires immediately on its FIRST registration, so
		// every later Run must apply the current snapshot itself.
		if snap := e.Strategy.Current(); snap.Version != 0 {
			scn.SetStrategy(scanner.Strategy{
				Cfg:      snap.Params.ScannerConfig(snap.Version),
				Resolver: snap.Params.RiskResolver(),
			})
			// Workers is start-time-only, so copy it into the base config
			// the Run loop reads.
			scn.Cfg.Workers = snap.Params.Scanner.Workers
		}
	}

	// PAPER mode: assemble the full simulation loop behind the scanner.
	var paperEng *paper.Engine
	var paperIn chan scanner.Event
	port := portfolio.New(resv, initial)
	if mode == config.ModePaper {
		if e.Store != nil {
			balances := map[string]string{}
			for a, v := range initial {
				balances[string(a)] = v.String()
			}
			if err := e.Store.EnsurePaperSession(runCtx, sessionID, string(mode), balances, 1, e.cfg.Seed); err != nil {
				e.log.Warn("paper session registration failed", "error", err)
			}
		}
		marker := portfolio.BookMarker{Books: books, Markets: scoped}
		executor := simulation.NewPaper(
			books, rulesLookup(rules), sched,
			simulation.WallClock{}, simulation.RealWaiter{}, marker,
			simulation.Config{
				Latency: simulation.LatencyModel{
					SubmitBase: 20 * time.Millisecond, SubmitJitter: 30 * time.Millisecond,
					FillBase: 30 * time.Millisecond, FillJitter: 50 * time.Millisecond,
				},
				LimitToleranceBps: decimal.NewFromInt(20),
				Depth:             50,
				Seed:              e.cfg.Seed,
			},
			newULID,
		)
		byID := make(map[string]graph.Triangle, len(topo.Triangles))
		for _, tri := range topo.Triangles {
			byID[tri.ID] = tri
		}
		paperIn = make(chan scanner.Event, 128)
		paperEng = &paper.Engine{
			Executor:  executor,
			Resv:      resv,
			Portfolio: port,
			Triangles: byID,
			In:        paperIn,
			Clock:     time.Now,
			IDGen:     newULID,
			OnResult: func(res execution.CycleResult) {
				e.log.Info("paper cycle settled",
					"cycle_id", res.CycleID, "outcome", string(res.Outcome),
					"pnl", res.TotalPnL.String(), "consumed", res.InputConsumed.String())
				if e.Metrics != nil && res.Outcome == execution.OutcomeAllFilled {
					e.Metrics.ObserveSlippage(string(binance.ID), res.SlippageBps.InexactFloat64())
				}
				if res.Outcome != execution.OutcomeAllFilled {
					e.notify(notification.SeverityWarning, "paper:cycle_failed",
						"Paper cycle "+string(res.Outcome),
						fmt.Sprintf("cycle %s: %s (pnl %s)", res.CycleID, res.Reason, res.TotalPnL.String()))
				}
				if e.Strategy != nil {
					limit := e.Strategy.Current().Params.Risk.MaxDrawdown
					dd := port.CurrentDrawdown(res.StartAsset)
					if limit.IsPositive() && dd.GreaterThan(limit.Mul(decimal.RequireFromString("0.8"))) {
						e.notify(notification.SeverityWarning, "portfolio:drawdown",
							"Drawdown threshold approached",
							fmt.Sprintf("%s drawdown %s (limit %s)", res.StartAsset, dd.StringFixed(4), limit.String()))
					}
				}
				if outbox != nil {
					r := res
					// Read the live session id (not the closure-captured
					// local): ResetPaper (BL-10) rotates it so cycles
					// settling after a reset are tagged to the new
					// session while everything already persisted keeps
					// its original tag.
					outbox.Enqueue(storage.Record{Kind: "cycle", Cycle: &r, SessionID: e.currentSessionID()})
				}
				if e.Hub != nil {
					_ = e.Hub.Publish("cycles", map[string]any{
						"cycle_id": res.CycleID, "outcome": string(res.Outcome),
						"realized_pnl": res.RealizedPnL.String(),
						"total_pnl":    res.TotalPnL.String(),
						"settled_at":   res.SettledAt,
					})
				}
			},
		}
		// P2-2: honor a pause decision carried in from the supervisor
		// (SetPaperPaused, consumed exactly once here) instead of
		// unconditionally resuming. A caller that never sets it (a
		// direct-constructed engine in tests, or ProfileScanner without
		// a Supervisor) keeps today's behavior exactly: start running.
		if paused, set := e.consumePausePaperOnBoot(); set && paused {
			paperEng.Pause()
		} else {
			paperEng.Resume()
		}
		scn.Sims = paperEng.Active
	}

	e.mu.Lock()
	e.scn, e.topo, e.pap, e.port, e.ready = scn, topo, paperEng, port, true
	e.feed, e.resv, e.brk, e.books, e.starts = feed, resv, breakers, books, starts
	e.marker = portfolio.BookMarker{Books: books, Markets: scoped}
	e.mu.Unlock()
	// Honest readiness across the inter-run gap: once this Run call
	// returns (ctx cancelled, restart, or fatal error), Ready must go
	// back to false immediately, not linger true until the NEXT Run
	// happens to reach this point. A supervisor restart polls Ready to
	// decide when the new run has actually finished bootstrapping; a
	// stale true here would report "ready" for a still-connecting
	// engine (or a permanently dead one after a fatal exit).
	defer func() {
		e.mu.Lock()
		// P1-1 (engine.go half): only clear readiness if THIS run is
		// still the live one (no later Run call has bumped e.runToken
		// since). Guards against a stale run's deferred cleanup running
		// after a newer run has already started and reported ready —
		// which would otherwise make the newer run look dead to anything
		// polling Status().Ready (e.g. Supervisor.waitReadyOrFail).
		if e.runToken == myToken {
			e.ready = false
		}
		e.mu.Unlock()
	}()
	e.notify(notification.SeverityInfo, "engine:ready", "Engine ready",
		fmt.Sprintf("%d triangles over %d markets in %s mode", len(topo.Triangles), len(scoped), mode))
	defer e.notify(notification.SeverityInfo, "engine:stopped", "Engine stopped", "shutdown or fatal component exit")

	// E4: register the pull-metrics sources once, with accessor-based
	// closures; re-registering per restart would double-count OTel
	// instruments and callbacks.
	e.registerMetricsOnce()

	if e.Hub != nil {
		e.Hub.RegisterTopic("scanner", func() (json.RawMessage, error) {
			return json.Marshal(e.Status())
		})
		e.Hub.RegisterTopic("recordings", func() (json.RawMessage, error) {
			// E5: resolve the CURRENT run's recorder control through the
			// accessor rather than the "rctl" local — RegisterTopic
			// overwrites (safe to call every run), but a captured local
			// would keep reporting the previous (dead) run's recorder.
			var status marketdata.RecorderStatus
			if c := e.Recorder(); c != nil {
				status = c.Status()
			}
			return json.Marshal(map[string]any{"recorder": status})
		})
		// "health" is registered by the wiring (components.go), not
		// here: it needs to fold in supervisor restart state, which the
		// engine cannot see (design §2.6).
	}
	if mode == config.ModeRecord {
		if _, err := rctl.Start(runCtx); err != nil {
			return fmt.Errorf("engine: start recording: %w", err)
		}
	}

	// E3: child goroutines must not outlive Run. runCtx (created at the
	// top of this call, P2-6) is cancelled either when the parent ctx is
	// (normal shutdown/restart) or when a child returns a fatal error
	// (this Run's own decision, via cancelRun() below); either way Run
	// waits (bounded by ShutdownGrace) for every goroutine it spawned to
	// actually return before it returns itself — otherwise a restart
	// would produce two feeds, two scanners and two outboxes racing on
	// e.scn, and the outbox's cancel-path drain (3s deadline) would never
	// get to run before the next Run starts overwriting persisted state.
	var wg sync.WaitGroup
	errCh := make(chan error, 8)
	spawn := func(fn func(context.Context) error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errCh <- fn(runCtx)
		}()
	}
	spawn(feed.Run)
	spawn(scn.Run)
	spawn(func(c context.Context) error { return e.consumeEvents(c, scn, paperIn, outbox) })
	if paperEng != nil {
		spawn(paperEng.Run)
	}
	if outbox != nil {
		spawn(outbox.Run)
	}

	// Staleness sweep: books that stop ticking degrade to STALE.
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	var runErr error
loop:
	for {
		select {
		case <-ctx.Done():
			runErr = ctx.Err()
			break loop
		case cerr := <-errCh:
			if cerr != nil && !errors.Is(cerr, context.Canceled) {
				runErr = cerr
				break loop
			}
		case now := <-ticker.C:
			// Live config, not the boot literal: a hot-swapped
			// max_book_age_ms applies to the sweep too (audit CR-P2-7).
			maxAge := scn.CurrentConfig().MaxBookAge
			for _, id := range books.All() {
				if b, ok := books.Get(id); ok {
					b.EvaluateStaleness(now, maxAge)
				}
			}
		}
	}
	cancelRun()

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(e.cfg.ShutdownGrace):
		e.log.Error("engine: shutdown grace exceeded; child goroutines still running", "grace", e.cfg.ShutdownGrace.String())
	}
	return runErr
}

// consumeEvents fans scanner events out: qualified opportunities go to
// the paper engine (PAPER mode) and the hub; persistence attaches here
// when the storage layer (T-022) lands.
func (e *Engine) consumeEvents(ctx context.Context, scn *scanner.Scanner, paperIn chan<- scanner.Event, outbox *storage.Outbox) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev := <-scn.Out:
			if outbox != nil && ev.Opportunity.Status == opportunity.StatusQualified {
				op, dec := ev.Opportunity, ev.Decision
				outbox.Enqueue(storage.Record{Kind: "opportunity", Opportunity: &op, Decision: &dec})
			}
			if ev.Opportunity.Status == opportunity.StatusRejected {
				e.countReject(ev.Decision.ReasonCode)
				// BL-31: persist the rejection so the Risk Center's
				// timeline survives a restart (today only the in-memory
				// reject_reason_counts histogram did) — but only the
				// first occurrence per (triangle, reason) per cooldown
				// window (shouldPersistRiskReject): unthrottled, a busy
				// triangle rejecting thousands of times a minute for the
				// same reason would make risk_events the dominant outbox
				// write and risk saturating the queue that opportunities
				// and cycles also depend on. The unthrottled total stays
				// visible via RejectCounts()/GET /api/v1/risk.
				if outbox != nil && e.shouldPersistRiskReject(ev.Opportunity.TriangleID, ev.Decision.ReasonCode, ev.Opportunity.DetectedAt) {
					var observed, threshold string
					for _, c := range ev.Decision.Checks {
						if c.Name == ev.Decision.ReasonCode {
							observed, threshold = c.Observed, c.Threshold
							break
						}
					}
					outbox.Enqueue(storage.Record{Kind: "risk_event", RiskEvent: &storage.RiskEvent{
						ID: newULID(), TS: ev.Opportunity.DetectedAt, Kind: "risk_reject",
						Subject: "triangle:" + ev.Opportunity.TriangleID, LimitName: ev.Decision.ReasonCode,
						Observed: observed, Threshold: threshold,
						Action: ev.Opportunity.Reason,
					}})
				}
			}
			if ev.Opportunity.Status == opportunity.StatusQualified {
				if e.Metrics != nil {
					e.Metrics.ObserveQualifiedEdge(string(ev.Opportunity.Exchange),
						ev.Opportunity.NetReturnBps.InexactFloat64())
				}
				e.rememberOpportunity(RecentOpportunity{
					ID: ev.Opportunity.ID, TriangleID: ev.Opportunity.TriangleID,
					NetBps: ev.Opportunity.NetReturnBps.StringFixed(2),
					Profit: ev.Opportunity.NetProfit.String(),
					Input:  ev.Opportunity.Quote.InputConsumed.String(),
					At:     ev.Opportunity.DetectedAt,
				})
				if ev.Opportunity.NetReturnBps.GreaterThanOrEqual(decimal.NewFromInt(50)) {
					e.notify(notification.SeverityInfo, "opportunity:large",
						"Large qualified opportunity",
						fmt.Sprintf("%s: %s bps, profit %s", ev.Opportunity.TriangleID,
							ev.Opportunity.NetReturnBps.StringFixed(2), ev.Opportunity.NetProfit.String()))
				}
				if paperIn != nil {
					select {
					case paperIn <- ev:
					default:
						e.log.Warn("paper queue full; opportunity dropped",
							"opportunity_id", ev.Opportunity.ID)
					}
				}
				e.log.Info("opportunity qualified",
					"opportunity_id", ev.Opportunity.ID,
					"triangle_id", ev.Opportunity.TriangleID,
					"input", ev.Opportunity.Quote.InputConsumed.String(),
					"net_bps", ev.Opportunity.NetReturnBps.StringFixed(2),
					"net_profit", ev.Opportunity.NetProfit.String(),
				)
				if e.Hub != nil {
					_ = e.Hub.Publish("scanner", map[string]any{
						"opportunity_id": ev.Opportunity.ID,
						"triangle_id":    ev.Opportunity.TriangleID,
						"status":         string(ev.Opportunity.Status),
						"input":          ev.Opportunity.Quote.InputConsumed.String(),
						"net_bps":        ev.Opportunity.NetReturnBps.StringFixed(4),
						"net_profit":     ev.Opportunity.NetProfit.String(),
						"detected_at":    ev.Opportunity.DetectedAt,
					})
				}
			} else {
				e.log.Debug("opportunity rejected",
					"triangle_id", ev.Opportunity.TriangleID,
					"reason", ev.Decision.ReasonCode,
					"net_bps", ev.Opportunity.NetReturnBps.StringFixed(2),
				)
			}
		}
	}
}

func (e *Engine) bootstrapMetadata(ctx context.Context, rest *binance.RESTClient) ([]exchange.Market, error) {
	backoff := 2 * time.Second
	for {
		markets, err := rest.ExchangeInfo(ctx)
		if err == nil {
			e.log.Info("exchange metadata loaded", "markets", len(markets))
			return markets, nil
		}
		e.log.Warn("metadata bootstrap failed; retrying", "error", err, "backoff", backoff.String())
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(backoff):
		}
		if backoff < time.Minute {
			backoff *= 2
		}
	}
}

// attachRunObservers wires the sync observers (EvalObserver,
// LatencyObserver) onto THIS run's scanner/feed. Unlike
// registerMetricsOnce, this runs every Run: the objects are per-run and
// the observers must be re-attached to each fresh one. scn may be nil on
// the first call (feed exists before the scanner does); the second call
// with scn set fills it in.
func (e *Engine) attachRunObservers(scn *scanner.Scanner, feed *binance.Feed) {
	// The in-memory latency window (BL-18: system health's per-exchange
	// percentiles) is independent of Metrics — it must keep sampling
	// even when OTel registration failed or is unconfigured, since it is
	// the only in-process-readable source of this data.
	window := e.currentLatency()
	if window != nil {
		feed.LatencyObserver = func(d time.Duration) {
			window.Record(float64(d.Nanoseconds()) / 1e6)
		}
	}
	if e.Metrics == nil {
		return
	}
	m := e.Metrics
	scn.EvalObserver = func(d time.Duration) {
		m.ObserveEval(float64(d.Nanoseconds()) / 1e6)
	}
	recordLatency := m.MessageLatencyRecorder(string(binance.ID))
	prior := feed.LatencyObserver
	feed.LatencyObserver = func(d time.Duration) {
		if prior != nil {
			prior(d)
		}
		recordLatency(float64(d.Nanoseconds()) / 1e6)
	}
}

// registerMetricsOnce attaches the engine's pull-metrics sources to the
// meter exactly once across the engine's lifetime (E4): every source is
// an accessor-based closure (e.currentX()), so the SAME registered
// callback keeps reporting the live run's numbers across a restart
// instead of the run it was registered against.
func (e *Engine) registerMetricsOnce() {
	if e.Metrics == nil {
		return
	}
	e.metricsOnce.Do(func() {
		m := e.Metrics
		src := metrics.EngineSources{
			Scanner: func() metrics.ScannerStats {
				scn := e.currentScanner()
				if scn == nil {
					return metrics.ScannerStats{}
				}
				return metrics.ScannerStats{
					Evaluations:   scn.Stats.Evaluations.Load(),
					Qualified:     scn.Stats.Qualified.Load(),
					Rejected:      scn.Stats.Rejected.Load(),
					SkippedBooks:  scn.Stats.SkippedBooks.Load(),
					DroppedEvents: scn.Stats.DroppedEvts.Load(),
				}
			},
			Triangles: func() int64 {
				topo := e.currentTopology()
				if topo == nil {
					return 0
				}
				return int64(len(topo.Triangles))
			},
			Feed: func() metrics.FeedStats {
				feed := e.currentFeed()
				if feed == nil {
					return metrics.FeedStats{Exchange: string(binance.ID)}
				}
				return metrics.FeedStats{
					Exchange:   string(binance.ID),
					Frames:     feed.Stats.Frames.Load(),
					Reconnects: feed.Stats.Reconnects.Load(),
					APIErrors:  feed.Stats.APIErrors.Load(),
					Resyncs:    feed.Stats.Resyncs.Load(),
					SeqErrors:  feed.Stats.SeqGaps.Load(),
				}
			},
			Books: func() []metrics.BookStat {
				books := e.currentBooks()
				if books == nil {
					return nil
				}
				now := time.Now()
				ids := books.All()
				out := make([]metrics.BookStat, 0, len(ids))
				for _, id := range ids {
					view, ok := books.View(id, 1)
					if !ok {
						continue
					}
					out = append(out, metrics.BookStat{
						Exchange: string(id.Exchange), Market: string(id.Symbol),
						AgeMS: float64(view.Age(now).Nanoseconds()) / 1e6,
						State: view.State.String(),
					})
				}
				return out
			},
			Capital: func() []metrics.CapitalStat {
				resv := e.currentReservation()
				starts := e.currentStarts()
				if resv == nil {
					return nil
				}
				out := make([]metrics.CapitalStat, 0, len(starts))
				for _, a := range starts {
					avail, reserved := resv.Balance(a)
					out = append(out, metrics.CapitalStat{
						Asset:     string(a),
						Available: avail.InexactFloat64(), Reserved: reserved.InexactFloat64(),
					})
				}
				return out
			},
			Breakers: func() []metrics.BreakerStat {
				breakers := e.currentBreakers()
				if breakers == nil {
					return nil
				}
				states := breakers.States()
				out := make([]metrics.BreakerStat, 0, len(states))
				for _, tr := range states {
					var v int64
					switch tr.To.String() {
					case "HALF_OPEN":
						v = 1
					case "OPEN":
						v = 2
					}
					out = append(out, metrics.BreakerStat{Name: tr.Name, Scope: tr.Scope, State: v})
				}
				return out
			},
			Paper: func() *metrics.PaperStats {
				pap := e.Paper()
				if pap == nil {
					return nil
				}
				st := pap.Snapshot()
				return &metrics.PaperStats{
					Received: st.Received, Started: st.Started,
					Completed: st.Completed, Failed: st.Failed, Skipped: st.Skipped,
					Active: int64(pap.Active()),
				}
			},
			PnL: func() []metrics.AssetPnL {
				port := e.currentPortfolio()
				starts := e.currentStarts()
				if port == nil {
					return nil
				}
				out := make([]metrics.AssetPnL, 0, len(starts))
				for _, a := range starts {
					out = append(out, metrics.AssetPnL{
						Asset:    string(a),
						Realized: port.Realized(a).InexactFloat64(),
						Fees:     port.FeesPaid(a).InexactFloat64(),
					})
				}
				return out
			},
			Recorder: func() (int64, int64) {
				c := e.Recorder()
				if c == nil {
					return 0, 0
				}
				st := c.Status()
				return st.Written, st.Dropped
			},
		}
		if err := m.RegisterEngine(src); err != nil {
			e.log.Error("metrics registration failed", "error", err)
		}
	})
}

// defaultRiskLimits are the conservative fallback limits used only when
// no strategy service is attached (direct-constructed engines in tests);
// normal wiring hot-swaps versioned values over these at startup.
func defaultRiskLimits() risk.Resolver {
	return risk.Resolver{Global: risk.Limits{
		MinNetEdgeBps:            decimal.NewFromInt(5),
		MinExpectedProfit:        decimal.NewFromInt(1),
		MaxTradeSize:             decimal.NewFromInt(1000),
		MaxCapitalPerTriangle:    decimal.NewFromInt(2000),
		MaxCapitalUtilization:    decimal.RequireFromString("0.5"),
		MaxConcurrentSimulations: 3,
		MaxBookAge:               1500 * time.Millisecond,
		MaxBookAgeSpread:         750 * time.Millisecond,
		MaxPriceImpactBps:        decimal.NewFromInt(30),
		MaxDailyLoss:             decimal.NewFromInt(200),
		MaxDrawdown:              decimal.RequireFromString("0.05"),
		MinDataQuality:           decimal.RequireFromString("0.5"),
	}}
}

func newULID() string {
	return ulid.MustNew(ulid.Timestamp(time.Now()), rand.Reader).String()
}

// rulesLookup adapts the instrument-rules map to simulation.RulesSource.
type rulesLookup map[exchange.MarketID]exchange.InstrumentRules

func (r rulesLookup) Rules(id exchange.MarketID) (exchange.InstrumentRules, bool) {
	v, ok := r[id]
	return v, ok
}
