package campaign

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

	"github.com/cploutarchou/arb-chain-bot/internal/backtest"
	"github.com/cploutarchou/arb-chain-bot/internal/jobrun"
)

// Status values of a Run.
const (
	StatusQueued  = "queued"
	StatusRunning = "running"
	StatusDone    = "done"
	StatusFailed  = "failed"
)

// ErrBusy: one campaign at a time — a run saturates a core for minutes
// and the console shows progress; queueing would only hide contention.
var ErrBusy = errors.New("campaign: another run is in progress")

// ErrNotFound is returned by Get for unknown run ids.
var ErrNotFound = errors.New("campaign: run not found")

// Verdict is one §80 verdict line paired with its severity (BL-05b): the
// backend classifies tone so the console never string-matches report
// prose to decide whether a verdict is good, cautionary, or bad.
type Verdict struct {
	Text     string `json:"text"`
	Severity string `json:"severity"`
}

// Run is one campaign job as the console sees it.
type Run struct {
	ID         string              `json:"id"`
	Recording  string              `json:"recording"`
	Request    Request             `json:"request"`
	Status     string              `json:"status"`
	Done       int                 `json:"done"`
	Total      int                 `json:"total"`
	Step       string              `json:"step,omitempty"`
	CreatedAt  time.Time           `json:"created_at"`
	StartedAt  *time.Time          `json:"started_at,omitempty"`
	FinishedAt *time.Time          `json:"finished_at,omitempty"`
	Error      string              `json:"error,omitempty"`
	Flags      map[string][]string `json:"flags,omitempty"`
	// Verdicts mirrors Flags with a severity attached per line (BL-05b);
	// filled alongside Flags whenever a run completes.
	Verdicts   map[string][]Verdict `json:"verdicts,omitempty"`
	ReportMD   string               `json:"report_md,omitempty"`
	ReportPath string               `json:"report_path,omitempty"`
	JSONPath   string               `json:"json_path,omitempty"`
	Actor      string               `json:"actor,omitempty"`
	// OwnerID/HeartbeatAt (review P3(h)) are a persistence-only
	// bookkeeping concern for orphan reconciliation across process
	// restarts — never part of the console's wire contract, so both are
	// json:"-" rather than merely omitempty.
	OwnerID     string    `json:"-"`
	HeartbeatAt time.Time `json:"-"`
}

// Summary strips the report body for list views.
func (r Run) Summary() Run {
	r.ReportMD = ""
	return r
}

// RunStore persists runs across restarts (nil = in-memory only).
type RunStore interface {
	UpsertCampaignRun(ctx context.Context, run Run) error
	ListCampaignRuns(ctx context.Context, limit int) ([]Run, error)
	GetCampaignRun(ctx context.Context, id string) (Run, error)
}

// Runner executes campaigns in the background behind the API. It is a
// Component: its lifetime context bounds every job.
type Runner struct {
	Sources Sources
	Store   RunStore
	// Dir is the recordings root; segments live in Dir/<recording>/ and
	// reports are written next to them.
	Dir   string
	Log   *slog.Logger
	NewID func() string
	// Publish, when set, fans run updates out (topic "campaigns").
	Publish func(data any)
	// Execute is swappable for tests; nil = package Execute.
	Execute func(ctx context.Context, src Sources, dir string, req Request, progress func(Progress)) (*backtest.Campaign, error)

	mu      sync.Mutex
	gate    jobrun.Gate // review P3(h): Start awaits this instead of racing Run to pin the lifetime ctx
	ownerID string      // review P3(h): stamped on every persisted row for orphan reconciliation
	runs    map[string]*Run
	order   []string
	current string
	once    sync.Once
}

func (r *Runner) Name() string { return "campaigns" }

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

// ErrInterrupted is recorded on runs that were in flight when the
// process stopped: a campaign is in-memory work, so a restart can only
// lose it, and an honest "failed" beats a row that says "running" forever.
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
	rows, err := r.Store.ListCampaignRuns(lctx, jobrun.RingCap)
	if err != nil {
		r.Log.Warn("campaign orphan reconciliation failed", "error", err)
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
		full, err := r.Store.GetCampaignRun(lctx, run.ID)
		if err != nil {
			full = run
		}
		full.Status = StatusFailed
		full.Error = errInterrupted
		full.FinishedAt = &now
		full.Step = ""
		r.Log.Warn("campaign run orphaned by restart; marked failed", "run", full.ID, "recording", full.Recording, "done", full.Done, "total", full.Total)
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
		return Run{}, fmt.Errorf("campaign: recording %s has no segment directory under %s", req.Recording, r.Dir)
	}
	// review P3(h): Start is reachable before Run has pinned the
	// lifetime context (the app.Component contract only guarantees Run
	// is CALLED, not that it wins any race against a concurrent Start).
	// Executing under the fallback context.Background() would let this
	// run outlive a graceful shutdown of the very process that launched
	// it, since nothing ever cancels context.Background(). Refuse with a
	// clear error instead of silently taking that shape.
	if _, err := r.gate.Await(startGateTimeout); err != nil {
		return Run{}, fmt.Errorf("campaign: %w", err)
	}
	r.mu.Lock()
	if r.current != "" {
		r.mu.Unlock()
		return Run{}, ErrBusy
	}
	id := r.NewID()
	run := &Run{
		ID: id, Recording: req.Recording, Request: req, Status: StatusQueued,
		Total: req.Total(), CreatedAt: time.Now().UTC(), Actor: actor,
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
	c, err := exec(ctx, r.Sources, segDir, req, func(p Progress) {
		r.update(id, func(run *Run) {
			run.Done, run.Total = p.Done, p.Total
			run.Step = fmt.Sprintf("%s seed=%d", p.Scenario, p.Seed)
		})
	})
	fin := time.Now().UTC()
	if err != nil {
		r.Log.Error("campaign failed", "run", id, "recording", req.Recording, "error", err)
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
	base := filepath.Join(segDir, "campaign-"+id)
	mdPath, jsonPath, werr := WriteFiles(c, req.Assets, base)
	md := Markdown(c, req.Assets)
	flags := Flags(c, req.Assets)
	verdicts := Verdicts(c, req.Assets)
	r.finish(id)
	r.update(id, func(run *Run) {
		run.Status = StatusDone
		run.FinishedAt = &fin
		run.Done = run.Total
		run.Step = ""
		run.Flags = flags
		run.Verdicts = verdicts
		run.ReportMD = md
		run.ReportPath, run.JSONPath = mdPath, jsonPath
		if werr != nil {
			run.Error = "report files not written: " + werr.Error()
		}
	})
	for asset, fl := range flags {
		for _, f := range fl {
			r.Log.Info("campaign verdict", "run", id, "asset", asset, "verdict", f)
		}
	}
}

// heartbeat re-stamps id's persisted row with this process's owner id
// and the current time every jobrun.HeartbeatInterval, independent of
// whatever progress callbacks fire (review P3(h)): a long campaign can
// spend minutes between (scenario, seed) progress updates, so relying
// on progress alone would let its row go heartbeat-stale well before it
// finishes — any OTHER process restarting meanwhile would then reclaim
// a run that is very much still alive. Stops the moment either the
// lifetime context ends or execute() returns (done), so it never
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
		if err := r.Store.UpsertCampaignRun(ctx, run); err != nil {
			r.Log.Warn("campaign run persistence failed", "run", run.ID, "error", err)
		}
		cancel()
	}
	if r.Publish != nil {
		r.Publish(map[string]any{"kind": "campaign_run", "run": run.Summary()})
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
		run := r.runs[r.order[i]].Summary()
		seen[run.ID] = true
		out = append(out, run)
	}
	r.mu.Unlock()
	if r.Store != nil {
		hist, err := r.Store.ListCampaignRuns(ctx, limit)
		if err != nil {
			return nil, err
		}
		for _, h := range hist {
			if !seen[h.ID] {
				out = append(out, h.Summary())
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// Get returns one run including its report.
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
	out, err := r.Store.GetCampaignRun(ctx, id)
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
// supervisor's restart guard rail (T-057 design §2.5) names the run in
// its refusal message.
func (r *Runner) BusyRun() (string, bool) {
	r.init()
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.current, r.current != ""
}
