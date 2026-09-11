package replay

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/exchange"
	"github.com/cploutarchou/arb-chain-bot/internal/jobrun"
)

type memStore struct {
	mu   sync.Mutex
	runs map[string]Run
}

func (m *memStore) UpsertReplayRun(_ context.Context, r Run) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.runs == nil {
		m.runs = map[string]Run{}
	}
	m.runs[r.ID] = r
	return nil
}

func (m *memStore) ListReplayRuns(context.Context, int) ([]Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Run
	for _, r := range m.runs {
		out = append(out, r)
	}
	return out, nil
}

func (m *memStore) GetReplayRun(_ context.Context, id string) (Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.runs[id]
	if !ok {
		return Run{}, ErrNotFound
	}
	return r, nil
}

func waitStatus(t *testing.T, r *Runner, id, want string) Run {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		run, err := r.Get(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if run.Status == want {
			return run
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("run %s never reached %s", id, want)
	return Run{}
}

func TestRunnerLifecycleAndBusy(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "REC1"), 0o750); err != nil {
		t.Fatal(err)
	}
	store := &memStore{}
	var published []Run
	var pubMu sync.Mutex
	release := make(chan struct{})
	n := 0
	r := &Runner{
		Sources: fakeSources{}, Store: store, Dir: dir,
		NewID: func() string { n++; return "J" + string(rune('0'+n)) },
		Publish: func(data any) {
			pubMu.Lock()
			defer pubMu.Unlock()
			published = append(published, data.(map[string]any)["run"].(Run))
		},
		Execute: func(ctx context.Context, _ Sources, _ ParamsSource, segDir string, req Request, progress func(Progress)) (Result, error) {
			if filepath.Base(segDir) != "REC1" {
				t.Errorf("segDir = %s", segDir)
			}
			progress(Progress{Done: 0, Total: 1, Step: "running"})
			<-release
			return Result{Evaluations: 10, Qualified: 3, Cycles: 2, Top: []TopOpportunity{{OpportunityID: "op-1", NetBps: d("5")}}}, nil
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = r.Run(ctx) }()

	if _, err := r.Start(Request{Recording: "MISSING"}, "u1"); err == nil {
		t.Fatal("start on a recording without segments must fail")
	}
	run, err := r.Start(Request{Recording: "REC1"}, "u1")
	if err != nil || run.ID != "J1" || run.Status != StatusQueued || run.Total != 1 {
		t.Fatalf("start: %v %+v", err, run)
	}
	if _, err := r.Start(Request{Recording: "REC1"}, "u1"); !errors.Is(err, ErrBusy) {
		t.Fatalf("second start: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if got, _ := r.Get(ctx, "J1"); got.Status == StatusRunning && got.Step == "running" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got, _ := r.Get(ctx, "J1"); got.Status != StatusRunning || got.Step != "running" {
		t.Fatalf("mid-run = %+v", got)
	}
	close(release)
	done := waitStatus(t, r, "J1", StatusDone)
	if done.Error != "" || done.Evaluations != 10 || done.Qualified != 3 || done.Cycles != 2 || len(done.Top) != 1 {
		t.Fatalf("done run = %+v", done)
	}
	if r.Busy() {
		t.Fatal("runner still busy after completion")
	}
	list, err := r.List(ctx, 10)
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %v %+v", err, list)
	}
	persisted, _ := store.GetReplayRun(ctx, "J1")
	if persisted.Status != StatusDone || persisted.Evaluations != 10 {
		t.Fatalf("persisted = %+v", persisted)
	}
	pubMu.Lock()
	defer pubMu.Unlock()
	if len(published) < 3 || published[len(published)-1].Status != StatusDone {
		t.Fatalf("published = %d last=%+v", len(published), published[len(published)-1])
	}
}

func TestRunnerFailureIsRecorded(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "REC"), 0o750)
	r := &Runner{
		Sources: fakeSources{}, Dir: dir, NewID: func() string { return "F" },
		Execute: func(context.Context, Sources, ParamsSource, string, Request, func(Progress)) (Result, error) {
			return Result{}, errors.New("no segments")
		},
	}
	// review P3(h): Start now refuses until Run has pinned the lifetime
	// context, so every Start call in this package's tests needs a Run
	// goroutine running first.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = r.Run(ctx) }()
	if _, err := r.Start(Request{Recording: "REC"}, ""); err != nil {
		t.Fatal(err)
	}
	failed := waitStatus(t, r, "F", StatusFailed)
	if failed.Error != "no segments" || failed.FinishedAt == nil {
		t.Fatalf("failed run = %+v", failed)
	}
	if _, err := r.Get(context.Background(), "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get unknown: %v", err)
	}
}

func TestRunnerMarksOrphanedRunsFailedOnStart(t *testing.T) {
	store := &memStore{}
	orphan := Run{ID: "OLD", Recording: "REC", Status: StatusRunning, Done: 0, Total: 1, CreatedAt: time.Now().Add(-time.Hour)}
	done := Run{ID: "DONE", Recording: "REC", Status: StatusDone, CreatedAt: time.Now().Add(-2 * time.Hour)}
	_ = store.UpsertReplayRun(context.Background(), orphan)
	_ = store.UpsertReplayRun(context.Background(), done)
	var published []Run
	r := &Runner{Store: store, Dir: t.TempDir(), NewID: func() string { return "X" },
		Publish: func(data any) { published = append(published, data.(map[string]any)["run"].(Run)) }}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = r.Run(ctx) }()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if got, _ := store.GetReplayRun(ctx, "OLD"); got.Status == StatusFailed {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	got, _ := store.GetReplayRun(context.Background(), "OLD")
	if got.Status != StatusFailed || got.Error != errInterrupted || got.FinishedAt == nil {
		t.Fatalf("orphan = %+v", got)
	}
	if d, _ := store.GetReplayRun(context.Background(), "DONE"); d.Status != StatusDone {
		t.Fatalf("finished run touched: %+v", d)
	}
	if len(published) != 1 || published[0].ID != "OLD" {
		t.Fatalf("published = %+v", published)
	}
}

// TestRunnerStartRefusesBeforeRun is the review P3(h) regression: Start
// is reachable before Run has ever pinned the lifetime context (Run is
// deliberately never called here) — it must refuse with a clear,
// wrapped jobrun.ErrNotStarted rather than silently execute the job
// under context.Background(), which a supervised shutdown would never
// cancel.
func TestRunnerStartRefusesBeforeRun(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "REC"), 0o750); err != nil {
		t.Fatal(err)
	}
	r := &Runner{Sources: fakeSources{}, Dir: dir, NewID: func() string { return "should-not-be-used" }}
	if _, err := r.Start(Request{Recording: "REC"}, "u1"); !errors.Is(err, jobrun.ErrNotStarted) {
		t.Fatalf("Start before Run = %v, want jobrun.ErrNotStarted", err)
	}
}

// TestRunnerBoundsInMemoryRunsAndHonoursListLimit is the review P3(h)
// regression: the in-memory run map/order must never grow past
// jobrun.RingCap regardless of how many runs this process has ever
// started, and List must clamp an over-cap limit TO the cap rather than
// silently falling back to the default (the old
// `if limit <= 0 || limit > 200 { limit = 50 }` shape did the latter).
func TestRunnerBoundsInMemoryRunsAndHonoursListLimit(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "REC"), 0o750); err != nil {
		t.Fatal(err)
	}
	n := 0
	r := &Runner{
		Sources: fakeSources{}, Dir: dir,
		NewID: func() string { n++; return fmt.Sprintf("run-%d", n) },
		Execute: func(context.Context, Sources, ParamsSource, string, Request, func(Progress)) (Result, error) {
			return Result{}, nil // completes instantly: no <-release gate
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = r.Run(ctx) }()

	total := jobrun.RingCap + 20
	for i := 0; i < total; i++ {
		run, err := r.Start(Request{Recording: "REC"}, "u1")
		if err != nil {
			t.Fatalf("start %d: %v", i, err)
		}
		waitStatus(t, r, run.ID, StatusDone)
	}

	r.mu.Lock()
	gotRuns, gotOrder := len(r.runs), len(r.order)
	r.mu.Unlock()
	if gotRuns != jobrun.RingCap || gotOrder != jobrun.RingCap {
		t.Fatalf("in-memory runs = %d order = %d, want both bounded to RingCap (%d)", gotRuns, gotOrder, jobrun.RingCap)
	}
	// The oldest 20 (run-1..run-20) were evicted; the newest survives.
	if _, ok := r.runs["run-1"]; ok {
		t.Fatal("run-1 should have been evicted by the ring bound")
	}
	if _, ok := r.runs[fmt.Sprintf("run-%d", total)]; !ok {
		t.Fatalf("run-%d (the most recent) should still be present", total)
	}

	list, err := r.List(ctx, 10_000) // far over the cap
	if err != nil {
		t.Fatal(err)
	}
	if len(list) > jobrun.RingCap {
		t.Fatalf("List(10000) returned %d rows, want <= RingCap (%d) — a limit over the cap must clamp TO the cap, not fall back to the default", len(list), jobrun.RingCap)
	}
}

// TestRunnerReconcileOrphansRespectsLiveOwnerHeartbeat is the review
// P3(h) owner/heartbeat regression: a "running" row stamped by a
// DIFFERENT, still-live process (fresh heartbeat) must NOT be reclaimed
// — only a row whose heartbeat has actually gone stale is a genuine
// orphan.
func TestRunnerReconcileOrphansRespectsLiveOwnerHeartbeat(t *testing.T) {
	store := &memStore{}
	now := time.Now().UTC()
	liveElsewhere := Run{
		ID: "LIVE", Recording: "REC", Status: StatusRunning, CreatedAt: now.Add(-time.Hour),
		OwnerID: "some-other-process", HeartbeatAt: now.Add(-time.Second),
	}
	deadElsewhere := Run{
		ID: "DEAD", Recording: "REC", Status: StatusRunning, CreatedAt: now.Add(-time.Hour),
		OwnerID: "some-other-process", HeartbeatAt: now.Add(-time.Hour),
	}
	_ = store.UpsertReplayRun(context.Background(), liveElsewhere)
	_ = store.UpsertReplayRun(context.Background(), deadElsewhere)

	r := &Runner{Store: store, Dir: t.TempDir(), NewID: func() string { return "X" }}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = r.Run(ctx) }()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if got, _ := store.GetReplayRun(ctx, "DEAD"); got.Status == StatusFailed {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()

	live, err := store.GetReplayRun(context.Background(), "LIVE")
	if err != nil || live.Status != StatusRunning {
		t.Fatalf("a row with a fresh heartbeat from a DIFFERENT live owner must not be reclaimed: %+v err=%v", live, err)
	}
	dead, err := store.GetReplayRun(context.Background(), "DEAD")
	if err != nil || dead.Status != StatusFailed || dead.Error != errInterrupted {
		t.Fatalf("a row with a stale heartbeat must be reclaimed: %+v err=%v", dead, err)
	}
}

// TestRunnerConfigVersionResolutionFailureFailsTheRun exercises the
// Runner-level plumbing of ParamsSource through Execute (the real
// backtest-driving path is covered directly in execute_test.go).
func TestRunnerConfigVersionResolutionFailureFailsTheRun(t *testing.T) {
	dir := t.TempDir()
	recDir := filepath.Join(dir, "REC")
	_ = os.MkdirAll(recDir, 0o750)
	_, streams := writeRecording(t, recDir)
	src := fakeSources{
		streams: streams,
		markets: []exchange.Market{market("BTCUSDT", "BTC", "USDT"), market("ETHBTC", "ETH", "BTC"), market("ETHUSDT", "ETH", "USDT")},
	}
	// No Strategy wired: a request naming a config_version must fail the
	// run, not silently run strategy.DefaultParams().
	r := &Runner{Sources: src, Dir: dir, NewID: func() string { return "C" }}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = r.Run(ctx) }()
	if _, err := r.Start(Request{Recording: "REC", ConfigVersion: 99}, ""); err != nil {
		t.Fatal(err)
	}
	failed := waitStatus(t, r, "C", StatusFailed)
	if failed.Error == "" {
		t.Fatalf("expected a non-empty error for an unresolvable config_version, got %+v", failed)
	}
}

// TestRunCountsTracksTerminalStatuses (audit O15): the runner counts
// done/failed runs for replay_runs_total{status}, matching the campaign
// runner's counters — orphan reconciliation is not a run and does not
// count.
func TestRunCountsTracksTerminalStatuses(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "REC"), 0o750)
	fail := true
	r := &Runner{
		Sources: fakeSources{}, Dir: dir, NewID: func() string { return "C" },
		Execute: func(context.Context, Sources, ParamsSource, string, Request, func(Progress)) (Result, error) {
			if fail {
				return Result{}, errors.New("no segments")
			}
			return Result{Evaluations: 4}, nil
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = r.Run(ctx) }()

	if counts := r.RunCounts(); counts[StatusDone] != 0 || counts[StatusFailed] != 0 {
		t.Fatalf("cold runner counts = %v", counts)
	}
	if _, err := r.Start(Request{Recording: "REC"}, ""); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, r, "C", StatusFailed)
	if c := r.RunCounts(); c[StatusFailed] != 1 || c[StatusDone] != 0 {
		t.Fatalf("after failure = %v", c)
	}

	fail = false
	if _, err := r.Start(Request{Recording: "REC"}, ""); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, r, "C", StatusDone)
	if c := r.RunCounts(); c[StatusFailed] != 1 || c[StatusDone] != 1 {
		t.Fatalf("after success = %v", c)
	}
}
