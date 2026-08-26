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
	ctx     context.Context
	runs    map[string]*Run
	order   []string
	current string
	ready   chan struct{}
	once    sync.Once
}

func (r *Runner) Name() string { return "campaigns" }

func (r *Runner) init() {
	r.once.Do(func() {
		r.runs = map[string]*Run{}
		r.ready = make(chan struct{})
		if r.Log == nil {
			r.Log = slog.New(slog.NewTextHandler(os.Stderr, nil))
		}
	})
}

// Run pins the lifetime context and blocks until it ends.
func (r *Runner) Run(ctx context.Context) error {
	r.init()
	r.mu.Lock()
	r.ctx = ctx
	r.mu.Unlock()
	close(r.ready)
	<-ctx.Done()
	return ctx.Err()
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
		return Run{}, fmt.Errorf("campaign: recording %s has no segment directory under %s", req.Recording, r.Dir)
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
	c, err := exec(ctx, r.Sources, segDir, req, func(p Progress) {
		r.update(id, func(run *Run) {
			run.Done, run.Total = p.Done, p.Total
			run.Step = fmt.Sprintf("%s seed=%d", p.Scenario, p.Seed)
		})
	})
	fin := time.Now().UTC()
	if err != nil {
		r.Log.Error("campaign failed", "run", id, "recording", req.Recording, "error", err)
		r.update(id, func(run *Run) { run.Status = StatusFailed; run.Error = err.Error(); run.FinishedAt = &fin })
		r.finish(id)
		return
	}
	base := filepath.Join(segDir, "campaign-"+id)
	mdPath, jsonPath, werr := WriteFiles(c, req.Assets, base)
	md := Markdown(c, req.Assets)
	flags := Flags(c, req.Assets)
	verdicts := Verdicts(c, req.Assets)
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
	if limit <= 0 || limit > 200 {
		limit = 50
	}
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
	r.init()
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.current != ""
}
