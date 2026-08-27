package replay

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/jobrun"
)

// Status values of a Run.
const (
	StatusQueued  = "queued"
	StatusRunning = "running"
	StatusDone    = "done"
	StatusFailed  = "failed"
)

// ErrBusy: one replay at a time (task requirement) — a run drives the
// full scanner pipeline over an entire recording and the console shows
// progress; queueing silently would only hide contention. This is a
// SEPARATE single-flight gate from internal/campaign.Runner's: a
// campaign and a replay may run concurrently today (both are read-only
// against the live engine — see the Runner doc comment on why the
// restart guard, not this gate, is what protects engine state).
var ErrBusy = errors.New("replay: another run is in progress")

// ErrNotFound is returned by Get for unknown run ids.
var ErrNotFound = errors.New("replay: run not found")

// Run is one replay job as the console sees it.
type Run struct {
	ID         string     `json:"id"`
	Recording  string     `json:"recording"`
	Request    Request    `json:"request"`
	Status     string     `json:"status"`
	Done       int        `json:"done"`
	Total      int        `json:"total"`
	Step       string     `json:"step,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	Error      string     `json:"error,omitempty"`
	// Evaluations is every opportunity the scanner evaluated during the
	// run — JSON tag "opportunities" to match the task's literal result
	// shape ("opportunities count") and the replay_runs.opportunities
	// column; the Go field keeps backtest.Result's own name
	// (Evaluations) since that is what it actually counts. No
	// "omitempty": a run that qualifies nothing is a real zero, not an
	// absent field — the console must be able to tell "0 opportunities"
	// from "not reported" (same reasoning as BL-19's "every aggregate
	// returns n").
	Evaluations int64 `json:"opportunities"`
	Qualified   int64 `json:"qualified"`
	Cycles      int64 `json:"cycles"`
	// Top is executed cycles ranked by net bps, NOT raw qualified
	// opportunities — see TopOpportunity's doc comment.
	Top   []TopOpportunity `json:"top,omitempty"`
	Actor string           `json:"actor,omitempty"`
	// OwnerID/HeartbeatAt (review P3(h)) are a persistence-only
	// bookkeeping concern for orphan reconciliation across process
	// restarts — never part of the console's wire contract, so both are
	// json:"-" rather than merely omitempty.
	OwnerID     string    `json:"-"`
	HeartbeatAt time.Time `json:"-"`
}

// RunStore persists runs across restarts (nil = in-memory only).
type RunStore interface {
	UpsertReplayRun(ctx context.Context, run Run) error
	ListReplayRuns(ctx context.Context, limit int) ([]Run, error)
	GetReplayRun(ctx context.Context, id string) (Run, error)
}

// Runner executes replays in the background behind the API. It is an
// app.Component: its lifetime context bounds every job.
//
// Unlike campaign.Runner, Runner does NOT register itself against the
// supervisor's restart guard through this package (it cannot: that
// would be an import cycle back to internal/app). The wiring in
// internal/app/components.go instead exposes BusyRun to the supervisor
// exactly like campaign.Runner does, under its own refusal code
// ("replay_running") — a replay run doesn't touch the live *Engine
// (backtest.Run builds its own books/scanner/portfolio in-process), so
// a concurrent restart cannot corrupt it; the guard exists purely so an
// operator restarting the engine gets an honest "a replay is using a
// core" instead of the restart and the replay silently fighting for CPU.
type Runner struct {
	Sources Sources
	// Strategy resolves a persisted config_version; nil disables that
	// field (every request with ConfigVersion != 0 fails honestly rather
	// than silently running strategy.DefaultParams()).
	Strategy ParamsSource
	Store    RunStore
	// Dir is the recordings root; segments live in Dir/<recording>/.
	Dir   string
	Log   *slog.Logger
	NewID func() string
	// Publish, when set, fans run updates out (topic "replays").
	Publish func(data any)
	// Execute is swappable for tests; nil = package Execute.
	Execute func(ctx context.Context, src Sources, params ParamsSource, dir string, req Request, progress func(Progress)) (Result, error)

	mu      sync.Mutex
	gate    jobrun.Gate // review P3(h): Start awaits this instead of racing Run to pin the lifetime ctx
	ownerID string      // review P3(h): stamped on every persisted row for orphan reconciliation
	runs    map[string]*Run
	order   []string
	current string
	once    sync.Once
}

func (r *Runner) Name() string { return "replays" }

func (r *Runner) init() {
	r.once.Do(func() {
		r.runs = map[string]*Run{}
		r.ownerID = jobrun.NewOwnerID()
		if r.Log == nil {
			r.Log = slog.New(slog.NewTextHandler(os.Stderr, nil))
		}
	})
}

// Run pins the lifetime context, reconciles runs orphaned by a previous
// process, and blocks until the context ends.
func (r *Runner) Run(ctx context.Context) error {
	r.init()
	r.gate.Pin(ctx)
	r.reconcileOrphans(ctx)
	<-ctx.Done()
	return ctx.Err()
}

// errInterrupted mirrors campaign.Runner's: a replay is in-memory work,
// so a restart can only lose it, and an honest "failed" beats a row
// that says "running" forever.
const errInterrupted = "interrupted: the process restarted before the run finished"

// reconcileOrphans marks "queued"/"running" rows left behind by a dead
// process as failed. review P3(h): a row is only reclaimed when its
// owner is verifiably dead (jobrun.Reclaimable — not tracked in THIS
// process's in-memory map AND its heartbeat has gone stale), not merely
// "absent from this fresh process's empty-at-boot map" — the old check
// would have one live instance reclaim another live instance's
// in-progress run the moment they shared a database.
func (r *Runner) reconcileOrphans(ctx context.Context) {
	if r.Store == nil {
		return
	}
	lctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	rows, err := r.Store.ListReplayRuns(lctx, jobrun.RingCap)
	if err != nil {
		r.Log.Warn("replay orphan reconciliation failed", "error", err)
		return
	}
	r.mu.Lock()
	mine := make(map[string]bool, len(r.runs))
	for id := range r.runs {
		mine[id] = true
	}
	r.mu.Unlock()
	now := time.Now().UTC()
	for _, run := range rows {
		if run.Status != StatusQueued && run.Status != StatusRunning {
			continue
		}
		if !jobrun.Reclaimable(mine[run.ID], r.ownerID, run.OwnerID, run.HeartbeatAt, now) {
			continue
		}
		full, err := r.Store.GetReplayRun(lctx, run.ID)
		if err != nil {
			full = run
		}
		full.Status = StatusFailed
		full.Error = errInterrupted
		full.FinishedAt = &now
		full.Step = ""
		r.Log.Warn("replay run orphaned by restart; marked failed", "run", full.ID, "recording", full.Recording, "done", full.Done, "total", full.Total)
		r.persist(full)
	}
}

// startGateTimeout bounds how long Start blocks waiting for Run to pin
// the lifetime context (review P3(h)) — short enough not to hang an
// HTTP request handler, comfortably longer than Run's own init() +
// gate.Pin path ever takes in practice.
const startGateTimeout = 2 * time.Second

// Start validates the request and launches it in the background.
func (r *Runner) Start(req Request, actor string) (Run, error) {
	r.init()
	req, err := req.Normalize()
	if err != nil {
		return Run{}, err
	}
	segDir := filepath.Join(r.Dir, req.Recording)
	if st, err := os.Stat(segDir); err != nil || !st.IsDir() {
		return Run{}, fmt.Errorf("replay: recording %s has no segment directory under %s", req.Recording, r.Dir)
	}
	// review P3(h): Start is reachable before Run has pinned the
	// lifetime context (the app.Component contract only guarantees Run
	// is CALLED, not that it wins any race against a concurrent Start).
	// Executing under the fallback context.Background() would let this
	// run outlive a graceful shutdown of the very process that launched
	// it, since nothing ever cancels context.Background(). Refuse with a
	// clear error instead of silently taking that shape.
	if _, err := r.gate.Await(startGateTimeout); err != nil {
		return Run{}, fmt.Errorf("replay: %w", err)
	}
	r.mu.Lock()
	if r.current != "" {
		r.mu.Unlock()
		return Run{}, ErrBusy
	}
	id := r.NewID()
	run := &Run{
		ID: id, Recording: req.Recording, Request: req, Status: StatusQueued,
		Total: 1, CreatedAt: time.Now().UTC(), Actor: actor,
	}
	r.runs[id] = run
	r.order = append(r.order, id)
	// review P3(h): bound the in-memory mirror to a ring of RingCap —
	// the just-appended (current, in-flight) run is always the NEWEST
	// entry, so it can never be the one Bound evicts.
	r.order = jobrun.Bound(r.order, r.runs, jobrun.RingCap)
	r.current = id
	snapshot := *run
	r.mu.Unlock()
	r.persist(snapshot)
	go r.execute(id, segDir)
	return snapshot, nil
}

func (r *Runner) execute(id, segDir string) {
	ctx := r.gate.Current()
	hbDone := make(chan struct{})
	go r.heartbeat(ctx, id, hbDone)
	defer close(hbDone)

	now := time.Now().UTC()
	r.update(id, func(run *Run) { run.Status = StatusRunning; run.StartedAt = &now })

	exec := r.Execute
	if exec == nil {
		exec = Execute
	}
	req := r.snapshot(id).Request
	res, err := exec(ctx, r.Sources, r.Strategy, segDir, req, func(p Progress) {
		r.update(id, func(run *Run) {
			run.Done, run.Total = p.Done, p.Total
			run.Step = p.Step
		})
	})
	fin := time.Now().UTC()
	if err != nil {
		r.Log.Error("replay failed", "run", id, "recording", req.Recording, "error", err)
		// finish() BEFORE the terminal update (incidental fix found while
		// testing P3(h)'s heartbeat goroutine): the old order let a
		// caller observe Status==failed via Get()/List() while Busy()
		// still reported true, a real (if narrow) race between this
		// goroutine and a concurrent poller — clearing r.current first
		// means "terminal status observable" implies "not busy" always.
		r.finish(id)
		r.update(id, func(run *Run) { run.Status = StatusFailed; run.Error = err.Error(); run.FinishedAt = &fin })
		return
	}
	r.finish(id)
	r.update(id, func(run *Run) {
		run.Status = StatusDone
		run.FinishedAt = &fin
		run.Done = run.Total
		run.Step = ""
		run.Evaluations = res.Evaluations
		run.Qualified = res.Qualified
		run.Cycles = res.Cycles
		run.Top = res.Top
	})
	r.Log.Info("replay done", "run", id, "recording", req.Recording, "evaluations", res.Evaluations, "qualified", res.Qualified, "cycles", res.Cycles)
}

// heartbeat re-stamps id's persisted row with this process's owner id
// and the current time every jobrun.HeartbeatInterval, independent of
// whatever progress callbacks fire (review P3(h)): a replay's own
// Progress only reports Done=0/Total=1 at the start and Done=1/Total=1
// at the end (see Progress's doc comment) — relying on progress alone
// would let a long-running replay's row go heartbeat-stale for its
// entire duration, so any OTHER process restarting meanwhile would
// reclaim a run that is very much still alive. Stops the moment either
// the lifetime context ends or execute() returns (done), so it never
// outlives the run it is heartbeating for.
func (r *Runner) heartbeat(ctx context.Context, id string, done <-chan struct{}) {
	ticker := time.NewTicker(jobrun.HeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case <-ticker.C:
			if snap := r.snapshot(id); snap.ID != "" {
				r.persist(snap)
			}
		}
	}
}

func (r *Runner) finish(id string) {
	r.mu.Lock()
	if r.current == id {
		r.current = ""
	}
	r.mu.Unlock()
}

// snapshot is nil-safe (review P3(h) advisor note): id can in principle
// be absent from r.runs (evicted by the ring bound) even though, under
// today's single-flight invariant, the run this is called for is always
// the current in-flight one and can never itself have been evicted.
// Returning a zero Run rather than dereferencing a nil pointer keeps
// that a documented invariant instead of a latent crash if it is ever
// relaxed.
func (r *Runner) snapshot(id string) Run {
	r.mu.Lock()
	defer r.mu.Unlock()
	run := r.runs[id]
	if run == nil {
		return Run{}
	}
	return *run
}

// update is the nil-safe write-side counterpart to snapshot.
func (r *Runner) update(id string, fn func(*Run)) {
	r.mu.Lock()
	run := r.runs[id]
	if run == nil {
		r.mu.Unlock()
		return
	}
	fn(run)
	snapshot := *run
	r.mu.Unlock()
	r.persist(snapshot)
}

// persist stamps the owner/heartbeat (review P3(h)) and writes through
// to the store and any WS subscribers.
func (r *Runner) persist(run Run) {
	run.OwnerID = r.ownerID
	run.HeartbeatAt = time.Now().UTC()
	if r.Store != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := r.Store.UpsertReplayRun(ctx, run); err != nil {
			r.Log.Warn("replay run persistence failed", "run", run.ID, "error", err)
		}
		cancel()
	}
	if r.Publish != nil {
		r.Publish(map[string]any{"kind": "replay_run", "run": run})
	}
}

// List returns runs newest-first: in-memory runs from this process
// merged over persisted history.
func (r *Runner) List(ctx context.Context, limit int) ([]Run, error) {
	r.init()
	// review P3(h): clamp INTO [1, RingCap] rather than the old
	// "anything out of [1,200] collapses to the default" shape, which
	// silently ignored a caller asking for up to the documented cap.
	limit = jobrun.ClampLimit(limit, 50, jobrun.RingCap)
	seen := map[string]bool{}
	var out []Run
	r.mu.Lock()
	for i := len(r.order) - 1; i >= 0; i-- {
		run := *r.runs[r.order[i]]
		seen[run.ID] = true
		out = append(out, run)
	}
	r.mu.Unlock()
	if r.Store != nil {
		hist, err := r.Store.ListReplayRuns(ctx, limit)
		if err != nil {
			return nil, err
		}
		for _, h := range hist {
			if !seen[h.ID] {
				out = append(out, h)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// Get returns one run.
func (r *Runner) Get(ctx context.Context, id string) (Run, error) {
	r.init()
	r.mu.Lock()
	run, ok := r.runs[id]
	if ok {
		out := *run
		r.mu.Unlock()
		return out, nil
	}
	r.mu.Unlock()
	if r.Store == nil {
		return Run{}, ErrNotFound
	}
	out, err := r.Store.GetReplayRun(ctx, id)
	if err != nil {
		return Run{}, err
	}
	return out, nil
}

// Busy reports whether a run is in progress.
func (r *Runner) Busy() bool {
	_, busy := r.BusyRun()
	return busy
}

// BusyRun reports whether a run is in progress and, if so, its id — the
// supervisor's restart guard rail names the run in its refusal message,
// mirroring campaign.Runner.BusyRun exactly.
func (r *Runner) BusyRun() (string, bool) {
	r.init()
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.current, r.current != ""
}
