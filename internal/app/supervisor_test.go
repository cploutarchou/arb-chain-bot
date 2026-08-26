package app

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/config"
	"github.com/cploutarchou/arb-chain-bot/internal/marketdata"
	"github.com/cploutarchou/arb-chain-bot/internal/paper"
	"github.com/cploutarchou/arb-chain-bot/internal/platform"
	"github.com/cploutarchou/arb-chain-bot/internal/strategy"
)

// supervisorTestSettings is a second, distinguishable-from-seed valid
// document (internal/platform's own test fixture is unexported to that
// package).
func supervisorTestSettings() platform.Settings {
	return platform.Settings{
		Venues: map[string]platform.VenueSettings{
			"binance": {
				Enabled: true, PaperEnabled: true,
				Symbols:        []string{"BTCUSDT", "ETHUSDT", "ETHBTC", "BNBUSDT"},
				StartingAssets: []string{"USDT"},
				Fees: platform.FeeSettings{
					MakerBps: decimal.NewFromInt(8),
					TakerBps: decimal.NewFromInt(8),
				},
			},
		},
		Paper: platform.PaperSettings{Balances: map[string]string{"USDT": "10000"}},
	}
}

// fakeEngine is the design's §5 fake EngineRunner: it records
// ApplySettings calls, blocks until its ctx is cancelled, and panics if
// re-entered while a previous run is still active — the sharpest
// possible check that Supervisor never starts a second run before the
// first one returned.
type fakeEngine struct {
	t *testing.T

	mu       sync.Mutex
	running  bool
	applied  []platform.Settings
	versions []int64
	failNext error
	// onStart, when set, is called once Run has marked itself running —
	// tests use it to simulate the real Engine's unconditional
	// paperEng.Resume() at boot, so the paper-pause-across-restart
	// tests can exercise both branches of E9's "if wasRunning" check.
	onStart func()
}

func (f *fakeEngine) ApplySettings(s platform.Settings, version int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.applied = append(f.applied, s)
	f.versions = append(f.versions, version)
}

func (f *fakeEngine) setFailNext(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failNext = err
}

func (f *fakeEngine) isRunning() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.running
}

func (f *fakeEngine) Run(ctx context.Context) error {
	f.mu.Lock()
	if f.running {
		f.mu.Unlock()
		panic("fakeEngine.Run: re-entered while a previous run is still active")
	}
	f.running = true
	fail := f.failNext
	f.failNext = nil
	onStart := f.onStart
	f.mu.Unlock()
	if onStart != nil {
		onStart()
	}
	defer func() {
		f.mu.Lock()
		f.running = false
		f.mu.Unlock()
	}()

	if fail != nil {
		return fail
	}
	<-ctx.Done()
	return ctx.Err()
}

func newFakeSettingsService(t *testing.T) *platform.Service {
	t.Helper()
	svc := platform.NewService(platform.NewMemoryStore(), testLogger(), nil)
	cfg := config.Bootstrap{
		Mode:           config.ModePaper,
		Symbols:        []string{"BTCUSDT", "ETHUSDT", "ETHBTC"},
		StartingAssets: []string{"USDT"},
		PaperBalance:   "10000",
	}
	if _, err := svc.Load(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	return svc
}

func newTestSupervisor(t *testing.T) (*Supervisor, *fakeEngine) {
	t.Helper()
	fe := &fakeEngine{t: t}
	sup := &Supervisor{
		Engine:   fe,
		Settings: newFakeSettingsService(t),
		Grace:    2 * time.Second,
		Log:      testLogger(),
		Ready:    fe.isRunning,
	}
	return sup, fe
}

func runSupervisor(t *testing.T, sup *Supervisor) (context.CancelFunc, chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sup.Run(ctx) }()
	// Wait for the first launch to be observed.
	deadline := time.Now().Add(2 * time.Second)
	for sup.Status().State == "" {
		if time.Now().After(deadline) {
			t.Fatal("supervisor never reached an initial state")
		}
		time.Sleep(5 * time.Millisecond)
	}
	return cancel, done
}

func TestSupervisorFirstRunFailurePropagates(t *testing.T) {
	sup, fe := newTestSupervisor(t)
	fe.setFailNext(errors.New("boom"))

	err := sup.Run(context.Background())
	if err == nil || err.Error() != "boom" {
		t.Fatalf("expected the first-run error to propagate verbatim, got %v", err)
	}
}

func TestSupervisorRestartAppliesNewSettingsAndDoesNotDoubleRun(t *testing.T) {
	sup, fe := newTestSupervisor(t)
	cancel, done := runSupervisor(t, sup)
	defer cancel()

	waitForState(t, sup, StateReady)

	next := supervisorTestSettings()
	if _, err := sup.Settings.Apply(context.Background(), "alice", "web", next); err != nil {
		t.Fatal(err)
	}

	reqDone := make(chan error, 1)
	if err := sup.Request(RestartRequest{Actor: "alice", Reason: "apply", Done: reqDone}); err != nil {
		t.Fatalf("Request: %v", err)
	}
	if err := <-reqDone; err != nil {
		t.Fatalf("restart failed: %v", err)
	}
	waitForState(t, sup, StateReady)

	st := sup.Status()
	if st.Restarts != 2 { // initial launch (1) + this restart (2)
		t.Fatalf("Restarts = %d, want 2", st.Restarts)
	}
	if st.SettingsVersion != 2 {
		t.Fatalf("SettingsVersion = %d, want 2", st.SettingsVersion)
	}
	fe.mu.Lock()
	gotVersions := append([]int64(nil), fe.versions...)
	fe.mu.Unlock()
	if len(gotVersions) != 2 || gotVersions[0] != 1 || gotVersions[1] != 2 {
		t.Fatalf("ApplySettings versions = %v, want [1 2]", gotVersions)
	}

	cancel()
	if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("Run returned unexpected error: %v", err)
	}
}

func TestSupervisorStopRecordingBeforeCancel(t *testing.T) {
	sup, _ := newTestSupervisor(t)

	rc := &marketdata.RecorderControl{
		Dir:          t.TempDir(),
		Log:          testLogger(),
		NewSessionID: func() string { return "rec-1" },
	}
	sessions := 0
	rc.NewSessionID = func() string { sessions++; return "rec-" + string(rune('0'+sessions)) }
	if _, err := rc.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	sup.Recorder = func() *marketdata.RecorderControl { return rc }

	cancel, done := runSupervisor(t, sup)
	defer cancel()
	waitForState(t, sup, StateReady)

	reqDone := make(chan error, 1)
	if err := sup.Request(RestartRequest{Actor: "alice", StopRecording: true, Done: reqDone}); err != nil {
		t.Fatalf("Request: %v", err)
	}
	if err := <-reqDone; err != nil {
		t.Fatalf("restart failed: %v", err)
	}
	if rc.Status().Running {
		t.Fatal("recorder must be stopped before the restart completes")
	}

	cancel()
	<-done
}

func TestSupervisorPausedPaperStaysPausedAcrossRestart(t *testing.T) {
	sup, _ := newTestSupervisor(t)
	pe := &paper.Engine{}
	pe.Pause() // simulate an operator-paused paper engine
	sup.Paper = func() *paper.Engine { return pe }

	cancel, done := runSupervisor(t, sup)
	defer cancel()
	waitForState(t, sup, StateReady)

	reqDone := make(chan error, 1)
	if err := sup.Request(RestartRequest{Actor: "alice", Done: reqDone}); err != nil {
		t.Fatalf("Request: %v", err)
	}
	if err := <-reqDone; err != nil {
		t.Fatalf("restart failed: %v", err)
	}
	if pe.Running() {
		t.Fatal("E9: a paused paper engine must still be paused after a restart")
	}

	cancel()
	<-done
}

func TestSupervisorRunningPaperStaysRunningAcrossRestart(t *testing.T) {
	sup, fe := newTestSupervisor(t)
	pe := &paper.Engine{}
	pe.Resume()
	fe.onStart = func() { pe.Resume() } // mimics the real Engine's unconditional boot-time Resume()
	sup.Paper = func() *paper.Engine { return pe }

	cancel, done := runSupervisor(t, sup)
	defer cancel()
	waitForState(t, sup, StateReady)

	reqDone := make(chan error, 1)
	if err := sup.Request(RestartRequest{Actor: "alice", Done: reqDone}); err != nil {
		t.Fatalf("Request: %v", err)
	}
	if err := <-reqDone; err != nil {
		t.Fatalf("restart failed: %v", err)
	}
	if !pe.Running() {
		t.Fatal("a running paper engine must still be running after a restart")
	}

	cancel()
	<-done
}

func TestSupervisorRefusalsCampaignBusy(t *testing.T) {
	sup, _ := newTestSupervisor(t)
	sup.Campaigns = busyCampaigns{id: "camp-1"}
	err := sup.Request(RestartRequest{Actor: "alice"})
	if !errors.Is(err, ErrCampaignRunning) {
		t.Fatalf("expected ErrCampaignRunning, got %v", err)
	}
}

type busyCampaigns struct{ id string }

func (b busyCampaigns) BusyRun() (string, bool) { return b.id, true }

// TestSupervisorRefusalsReplayBusy is BusyCampaign's counterpart for
// BL-17: a replay run doesn't touch the live *Engine, but the restart
// guard still refuses so an operator gets an honest reason instead of
// the restart and the replay silently fighting for the same core.
func TestSupervisorRefusalsReplayBusy(t *testing.T) {
	sup, _ := newTestSupervisor(t)
	sup.Replays = busyReplays{id: "replay-1"}
	err := sup.Request(RestartRequest{Actor: "alice"})
	if !errors.Is(err, ErrReplayRunning) {
		t.Fatalf("expected ErrReplayRunning, got %v", err)
	}
}

type busyReplays struct{ id string }

func (b busyReplays) BusyRun() (string, bool) { return b.id, true }

func TestSupervisorRefusalsRecordingActive(t *testing.T) {
	sup, _ := newTestSupervisor(t)
	rc := &marketdata.RecorderControl{
		Dir: t.TempDir(), Log: testLogger(),
		NewSessionID: func() string { return "rec-1" },
	}
	if _, err := rc.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = rc.Stop() })
	sup.Recorder = func() *marketdata.RecorderControl { return rc }

	err := sup.Request(RestartRequest{Actor: "alice", StopRecording: false})
	if !errors.Is(err, ErrRecordingActive) {
		t.Fatalf("expected ErrRecordingActive, got %v", err)
	}
	// stop_recording:true bypasses the refusal.
	if err := sup.Request(RestartRequest{Actor: "alice", StopRecording: true}); err != nil {
		t.Fatalf("expected stop_recording:true to bypass the refusal, got %v", err)
	}
}

func TestSupervisorRefusalsDoubleRequest(t *testing.T) {
	sup, _ := newTestSupervisor(t)
	if err := sup.Request(RestartRequest{Actor: "alice"}); err != nil {
		t.Fatalf("first request: %v", err)
	}
	err := sup.Request(RestartRequest{Actor: "bob"})
	if !errors.Is(err, ErrRestartInProgress) {
		t.Fatalf("expected ErrRestartInProgress, got %v", err)
	}
}

func TestSupervisorSecondRunFailureStaysAliveAndAcceptsLaterRequest(t *testing.T) {
	sup, fe := newTestSupervisor(t)
	cancel, done := runSupervisor(t, sup)
	defer cancel()
	waitForState(t, sup, StateReady)

	fe.setFailNext(errors.New("second run boom"))
	reqDone := make(chan error, 1)
	if err := sup.Request(RestartRequest{Actor: "alice", Done: reqDone}); err != nil {
		t.Fatalf("Request: %v", err)
	}
	<-reqDone // the restart itself reports failure through Done, not through Run's return

	waitForState(t, sup, StateFailed)

	// Supervisor.Run must still be alive (not returned) and a later
	// Request must be accepted.
	select {
	case err := <-done:
		t.Fatalf("Supervisor.Run returned after a post-boot failure (should stay alive): %v", err)
	case <-time.After(200 * time.Millisecond):
	}

	reqDone2 := make(chan error, 1)
	if err := sup.Request(RestartRequest{Actor: "alice", Done: reqDone2}); err != nil {
		t.Fatalf("later Request must be accepted: %v", err)
	}
	if err := <-reqDone2; err != nil {
		t.Fatalf("later restart failed: %v", err)
	}
	waitForState(t, sup, StateReady)

	cancel()
	<-done
}

func TestSupervisorRestartTimeoutFailsWithoutReentry(t *testing.T) {
	sup, fe := newTestSupervisor(t)
	sup.Grace = 50 * time.Millisecond
	// blockingEngine doesn't drive fe.running, so readiness can't be
	// observed through it for this test; assume-ready-once-launched
	// (Ready==nil) is exactly what the fake-engine unit tests in the
	// design use.
	sup.Ready = nil
	// blockedRun never respects ctx cancellation, forcing a Grace timeout.
	blocked := make(chan struct{})
	t.Cleanup(func() { close(blocked) })
	sup.Engine = blockingEngine{fe: fe, blocked: blocked}

	cancel, done := runSupervisor(t, sup)
	defer cancel()
	waitForState(t, sup, StateReady)

	reqDone := make(chan error, 1)
	if err := sup.Request(RestartRequest{Actor: "alice", Done: reqDone}); err != nil {
		t.Fatalf("Request: %v", err)
	}
	if err := <-reqDone; err == nil {
		t.Fatal("expected the restart to report a timeout error")
	}
	waitForState(t, sup, StateFailed)

	select {
	case err := <-done:
		t.Fatalf("Supervisor.Run returned after a restart timeout (should stay alive): %v", err)
	case <-time.After(100 * time.Millisecond):
	}
}

// blockingEngine never returns from Run until the ctx AND blocked are
// both done, forcing Supervisor's Grace timeout on the FIRST restart
// attempt while still eventually cleaning up so the test can finish.
type blockingEngine struct {
	fe      *fakeEngine
	blocked chan struct{}
}

func (b blockingEngine) ApplySettings(s platform.Settings, v int64) { b.fe.ApplySettings(s, v) }

func (b blockingEngine) Run(ctx context.Context) error {
	select {
	case <-ctx.Done():
		<-b.blocked // ignore cancellation until the test releases us
		return ctx.Err()
	case <-b.blocked:
		return nil
	}
}

func TestSupervisorPendingReasonsSettingsChange(t *testing.T) {
	sup, _ := newTestSupervisor(t)
	cancel, done := runSupervisor(t, sup)
	defer cancel()
	waitForState(t, sup, StateReady)

	next := supervisorTestSettings() // symbols differ from the seed: restart-scoped
	if _, err := sup.Settings.Apply(context.Background(), "alice", "web", next); err != nil {
		t.Fatal(err)
	}
	waitForPendingReason(t, sup, "platform settings v2")
	if got := sup.Status().State; got != StatePending {
		t.Fatalf("state = %q, want pending", got)
	}

	cancel()
	<-done
}

func TestSupervisorPendingReasonsAllowlistOnlyStaysReady(t *testing.T) {
	sup, _ := newTestSupervisor(t)
	cancel, done := runSupervisor(t, sup)
	defer cancel()
	waitForState(t, sup, StateReady)

	cur := sup.Settings.Current().Settings.Clone()
	cur.Telegram.Allowlist = append(cur.Telegram.Allowlist, 555)
	if _, err := sup.Settings.Apply(context.Background(), "alice", "web", cur); err != nil {
		t.Fatal(err)
	}
	// Give the Settings.Subscribe callback a moment to run; there is
	// nothing to poll FOR here (the assertion is an absence), so a
	// short fixed wait is the least-flaky option.
	time.Sleep(100 * time.Millisecond)
	if st := sup.Status(); st.State != StateReady || len(st.PendingReasons) != 0 {
		t.Fatalf("allowlist-only change must not raise the restart banner: state=%q reasons=%v", st.State, st.PendingReasons)
	}

	cancel()
	<-done
}

func TestSupervisorPendingReasonsScannerWorkers(t *testing.T) {
	sup, _ := newTestSupervisor(t)
	stratSvc := strategy.NewService(strategy.NewMemoryStore(), testLogger(), nil)
	if _, err := stratSvc.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	sup.Strategy = stratSvc
	var (
		mu      sync.Mutex
		workers = stratSvc.Current().Params.Scanner.Workers
	)
	sup.RunningWorkers = func() int {
		mu.Lock()
		defer mu.Unlock()
		return workers
	}

	cancel, done := runSupervisor(t, sup)
	defer cancel()
	waitForState(t, sup, StateReady)

	p := stratSvc.Current().Params
	p.Scanner.Workers++
	if _, err := stratSvc.Apply(context.Background(), "alice", "web", p); err != nil {
		t.Fatal(err)
	}
	waitForPendingReason(t, sup, "strategy v2 (scanner.workers)")
	if got := sup.Status().State; got != StatePending {
		t.Fatalf("state = %q, want pending", got)
	}

	cancel()
	<-done
}

func waitForPendingReason(t *testing.T, sup *Supervisor, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		for _, r := range sup.Status().PendingReasons {
			if r == want {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("pending reason %q never appeared; got %v", want, sup.Status().PendingReasons)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func waitForState(t *testing.T, sup *Supervisor, want RestartState) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if got := sup.Status().State; got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("supervisor state = %q, want %q (timed out)", sup.Status().State, want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
