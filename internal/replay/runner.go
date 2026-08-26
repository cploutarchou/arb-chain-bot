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
	ctx     context.Context
	runs    map[string]*Run
	order   []string
	current string
	ready   chan struct{}
	once    sync.Once
}

func (r *Runner) Name() string { return "replays" }

func (r *Runner) init() {
	r.once.Do(func() {
		r.runs = map[string]*Run{}
		r.ready = make(chan struct{})
		if r.Log == nil {
			r.Log = slog.New(slog.NewTextHandler(os.Stderr, nil))
		}
	})
}

// Run pins the lifetime context, reconciles runs orphaned by a previous
// process, and blocks until the context ends.
func (r *Runner) Run(ctx context.Context) error {
	r.init()
	r.mu.Lock()
	r.ctx = ctx
	r.mu.Unlock()
	close(r.ready)
	r.reconcileOrphans(ctx)
	<-ctx.Done()
	return ctx.Err()
}

// errInterrupted mirrors campaign.Runner's: a replay is in-memory work,
// so a restart can only lose it, and an honest "failed" beats a row
// that says "running" forever.
const errInterrupted = "interrupted: the process restarted before the run finished"

func (r *Runner) reconcileOrphans(ctx context.Context) {
	if r.Store == nil {
		return
	}
	lctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	rows, err := r.Store.ListReplayRuns(lctx, 200)
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
		if mine[run.ID] || (run.Status != StatusQueued && run.Status != StatusRunning) {
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

func (r *Runner) lifetime() context.Context {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ctx == nil {
		return context.Background()
	}
	return r.ctx
}

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
	r.current = id
	snapshot := *run
	r.mu.Unlock()
	r.persist(snapshot)
	go r.execute(id, segDir)
	return snapshot, nil
}

func (r *Runner) execute(id, segDir string) {
	ctx := r.lifetime()
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
		r.update(id, func(run *Run) { run.Status = StatusFailed; run.Error = err.Error(); run.FinishedAt = &fin })
		r.finish(id)
		return
	}
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
	r.finish(id)
}

func (r *Runner) finish(id string) {
	r.mu.Lock()
	if r.current == id {
		r.current = ""
	}
	r.mu.Unlock()
}

func (r *Runner) snapshot(id string) Run {
	r.mu.Lock()
	defer r.mu.Unlock()
	return *r.runs[id]
}

func (r *Runner) update(id string, fn func(*Run)) {
	r.mu.Lock()
	run := r.runs[id]
	fn(run)
	snapshot := *run
	r.mu.Unlock()
	r.persist(snapshot)
}

func (r *Runner) persist(run Run) {
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
	if limit <= 0 || limit > 200 {
		limit = 50
	}
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
