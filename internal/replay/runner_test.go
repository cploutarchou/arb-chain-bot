package replay

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/exchange"
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
	if _, err := r.Start(Request{Recording: "REC", ConfigVersion: 99}, ""); err != nil {
		t.Fatal(err)
	}
	failed := waitStatus(t, r, "C", StatusFailed)
	if failed.Error == "" {
		t.Fatalf("expected a non-empty error for an unresolvable config_version, got %+v", failed)
	}
}
