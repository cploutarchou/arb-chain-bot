package app

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
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
	// T-059: the fixture predates the platform/ai sections; WithDefaults
	// fills them from a PAPER seed exactly as Service.Load would.
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
	}.WithDefaults(config.Bootstrap{Mode: config.ModePaper, AIModel: "claude-sonnet-5"})
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
	// P3-1: the initial boot no longer counts as a restart — Restarts
	// only reflects THIS one actual restart.
	if st.Restarts != 1 {
		t.Fatalf("Restarts = %d, want 1", st.Restarts)
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
	sup, fe := newTestSupervisor(t)
	pe := &paper.Engine{}
	sup.Paper = func() *paper.Engine { return pe }
	// Mimics the real Engine's unconditional boot-time Resume() (E9) —
	// without this, the fake never resumes anything and the assertion
	// below would pass vacuously regardless of whether the pause
	// decision was actually carried across the restart (it is this
	// unconditional Resume that P2-2's SetPaperPaused/fallback must
	// override for a paused paper engine to stay paused).
	fe.onStart = func() { pe.Resume() }

	cancel, done := runSupervisor(t, sup)
	defer cancel()
	waitForState(t, sup, StateReady)

	// Pause AFTER boot (boot's own onStart just resumed it) — simulates
	// an operator pausing paper trading before requesting the restart.
	pe.Pause()

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

// slowSecondBootEngine delegates to a *fakeEngine, except its SECOND
// Run call (the restart-driven one) blocks BEFORE ever invoking the fake
// (so the fake's own `running` flag — and therefore Supervisor.Ready —
// stays false) until the test closes proceed. Used to force a
// deterministic ready-timeout on a restart without an artificial sleep.
type slowSecondBootEngine struct {
	fe        *fakeEngine
	callIndex *atomic.Int32
	proceed   chan struct{}
}

func (s slowSecondBootEngine) ApplySettings(set platform.Settings, v int64) {
	s.fe.ApplySettings(set, v)
}

func (s slowSecondBootEngine) Run(ctx context.Context) error {
	if s.callIndex.Add(1) == 2 {
		select {
		case <-s.proceed:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return s.fe.Run(ctx)
}

// TestSupervisorReadyTimeoutStaysRestartingThenResolvesPausePreserved is
// the P2-2 regression test, covering both halves of the fix: (1) a
// restart whose bootstrap outlives ReadyTimeout must NOT be marked ready
// early — state stays "restarting" until Ready() genuinely flips true,
// confirmed later by Run's readiness ticker (completeRestart, including
// the Restarts bump); (2) the pause decision is carried into the engine
// via SetPaperPaused BEFORE the launch (not re-applied after the fact,
// which would have nothing to act on while Supervisor.Paper() returns
// nil mid-bootstrap in the real Engine).
func TestSupervisorReadyTimeoutStaysRestartingThenResolvesPausePreserved(t *testing.T) {
	sup, fe := newTestSupervisor(t)
	sup.ReadyTimeout = 50 * time.Millisecond

	pe := &paper.Engine{}
	pe.Pause() // operator had explicitly paused it before the restart
	sup.Paper = func() *paper.Engine { return pe }

	var pausedArg atomic.Bool
	var setPaperPausedCalls atomic.Int32
	sup.SetPaperPaused = func(paused bool) {
		pausedArg.Store(paused)
		setPaperPausedCalls.Add(1)
	}

	proceed := make(chan struct{})
	sup.Engine = slowSecondBootEngine{fe: fe, callIndex: &atomic.Int32{}, proceed: proceed}

	cancel, done := runSupervisor(t, sup)
	defer cancel()
	waitForState(t, sup, StateReady)

	reqDone := make(chan error, 1)
	if err := sup.Request(RestartRequest{Actor: "alice", Done: reqDone}); err != nil {
		t.Fatalf("Request: %v", err)
	}

	// SetPaperPaused must be called BEFORE the launch even attempts
	// readiness — well before the ready-timeout fires — carrying
	// "start paused" (pe was paused, so !wasRunning == true).
	deadline := time.Now().Add(2 * time.Second)
	for setPaperPausedCalls.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("SetPaperPaused was never called")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !pausedArg.Load() {
		t.Fatal("SetPaperPaused(true) expected: the paper engine was paused before the restart")
	}

	// Done fires quickly (accepted, not a failure) even though the new
	// generation has not confirmed readiness yet.
	if err := <-reqDone; err != nil {
		t.Fatalf("a ready-timeout must not be reported as a restart failure: %v", err)
	}

	// The ready-timeout itself: state must NOT be Ready while the fake
	// engine is still blocked before ever setting fe.running — it must
	// also not be Failed (a timeout is not a failure, P2-2).
	time.Sleep(3 * sup.ReadyTimeout)
	if st := sup.Status(); st.State != StateRestarting {
		t.Fatalf("state = %q while still bootstrapping past ReadyTimeout, want %q", st.State, StateRestarting)
	}

	// Release the fake engine: it now becomes ready. Run's readiness
	// ticker must catch this and complete the restart (bumping Restarts
	// — P3-1's counter only counts actual restarts).
	close(proceed)
	waitForState(t, sup, StateReady)
	if got := sup.Status().Restarts; got != 1 {
		t.Fatalf("Restarts = %d, want 1", got)
	}

	// The paused paper engine must still be paused: nothing ever called
	// Resume() on it (the pre-fix shape relied on Supervisor.Paper()
	// after readiness, which is exactly the mechanism replaced here).
	if pe.Running() {
		t.Fatal("P2-2: a paused paper engine must still be paused after a slow restart")
	}

	cancel()
	<-done
}

// pauseRestoreEngine is the P2-3 regression harness: its FIRST Run call
// (the boot generation, which the test then requests a restart against)
// ignores ctx cancellation until release is closed, forcing the
// cancel-and-wait step's grace timeout; every LATER call (a subsequent
// restart) behaves normally — respects ctx immediately, and calls
// onStart synchronously at the top (mimicking the real engine's
// unconditional paperEng.Resume() at boot).
type pauseRestoreEngine struct {
	callIndex *atomic.Int32
	release   chan struct{}
	onStart   func()
}

func (p pauseRestoreEngine) ApplySettings(platform.Settings, int64) {}

func (p pauseRestoreEngine) Run(ctx context.Context) error {
	if p.callIndex.Add(1) == 1 {
		<-ctx.Done()
		<-p.release
		return ctx.Err()
	}
	if p.onStart != nil {
		p.onStart()
	}
	<-ctx.Done()
	return ctx.Err()
}

// TestSupervisorGraceTimeoutRestoresPauseState is the P2-3 regression
// test: a grace timeout leaves the abandoned generation's captured paper
// engine paused (restart()'s own step 3), with nothing else left to ever
// un-pause it. Before the fix that left it "paused forever": a LATER
// restart's OWN wasRunning capture (this same step 3, next time) would
// read that stale, permanently-paused object and conclude "was not
// running", latching the paper engine paused even though the operator
// had it genuinely running. Asserts BOTH halves: (1) the timed-out
// restart itself restores the running state it disturbed, and (2) a
// LATER restart's wasRunning capture sees the correct (running) value —
// the part that makes this an actual regression test for the bug, not
// just for the defer existing.
func TestSupervisorGraceTimeoutRestoresPauseState(t *testing.T) {
	sup, _ := newTestSupervisor(t)
	sup.Grace = 50 * time.Millisecond
	sup.Ready = nil // pauseRestoreEngine has no readiness signal to poll

	pe := &paper.Engine{}
	pe.Resume() // operator has paper trading genuinely running
	sup.Paper = func() *paper.Engine { return pe }

	pe2 := pauseRestoreEngine{
		callIndex: &atomic.Int32{}, release: make(chan struct{}),
		onStart: func() { pe.Resume() }, // mimics real Engine's boot-time Resume()
	}
	sup.Engine = pe2

	cancel, done := runSupervisor(t, sup)
	defer cancel()
	waitForState(t, sup, StateReady)

	// Restart 1: forces the grace timeout (the abandoned generation
	// ignores cancellation until pe2.release is closed).
	reqDone1 := make(chan error, 1)
	if err := sup.Request(RestartRequest{Actor: "alice", Done: reqDone1}); err != nil {
		t.Fatalf("first Request: %v", err)
	}
	if err := <-reqDone1; err == nil {
		t.Fatal("expected the first restart to report a timeout error")
	}
	waitForState(t, sup, StateFailed)

	// (1) The timed-out restart itself must have restored the running
	// state — nothing else exists to do it, since the abandoned
	// generation never reaches its own boot sequence.
	if !pe.Running() {
		t.Fatal("P2-3: the grace-timeout path left the paper engine paused instead of restoring its pre-restart running state")
	}

	// Release the abandoned generation so its exit can be observed and
	// a later restart is no longer refused.
	close(pe2.release)

	// Restart 2: a LATER restart, once accepted, must succeed — and,
	// critically, its OWN wasRunning capture must see the CORRECT
	// (running) state, not a stale paused one.
	deadline := time.Now().Add(3 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		reqDone2 := make(chan error, 1)
		err := sup.Request(RestartRequest{Actor: "bob", Done: reqDone2})
		if err == nil {
			if err := <-reqDone2; err != nil {
				t.Fatalf("second restart failed: %v", err)
			}
			lastErr = nil
			break
		}
		lastErr = err
		time.Sleep(10 * time.Millisecond)
	}
	if lastErr != nil {
		t.Fatalf("second restart never got accepted: %v", lastErr)
	}
	waitForState(t, sup, StateReady)

	// (2) The bug this test guards: a later restart's wasRunning capture
	// must see "running", not a permanently-latched "paused". onStart
	// (pe.Resume()) runs on the launched goroutine, asynchronously with
	// respect to reqDone2 (sup.Ready == nil reports ready immediately,
	// with no synchronization on onStart having actually run yet) — poll
	// briefly rather than asserting on the very next line.
	pollDeadline := time.Now().Add(2 * time.Second)
	for !pe.Running() {
		if time.Now().After(pollDeadline) {
			t.Fatal("P2-3: a later restart ended with paper paused — wasRunning read a stale, permanently-paused object")
		}
		time.Sleep(5 * time.Millisecond)
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

// TestSupervisorRestartRecheckRecordingTOCTOURestoresReadyState is the
// P2-6 regression test for the "re-check under the restart loop" half of
// the fix (the recording guard's TOCTOU): Request() can only see a
// snapshot of recorder state at enqueue time, so a session that starts
// in the window between Request() accepting the restart and the
// single-threaded restart loop actually processing it must still be
// caught — restart() itself calls sup here directly (same package),
// exercising the exact code path a real race would hit without needing
// to win a timing race in the test. It also covers P3-2's "must not get
// stuck": Request() already flipped state to Restarting before this
// runs, and the refusal must unwind that back to Ready, not leave the
// state machine parked in "restarting" forever.
func TestSupervisorRestartRecheckRecordingTOCTOURestoresReadyState(t *testing.T) {
	sup, _ := newTestSupervisor(t)
	cancel, done := runSupervisor(t, sup)
	defer cancel()
	waitForState(t, sup, StateReady)

	rc := &marketdata.RecorderControl{
		Dir: t.TempDir(), Log: testLogger(),
		NewSessionID: func() string { return "rec-1" },
	}
	if _, err := rc.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = rc.Stop() })
	sup.Recorder = func() *marketdata.RecorderControl { return rc }

	// Simulate Request() already having accepted this restart BEFORE the
	// recording session above started (the actual race window) by
	// driving the state transition it performs, then calling restart()
	// directly — restart() must re-check independently of what Request()
	// saw.
	sup.setStatus(func(st *RestartStatus) { st.State = StateRestarting })

	reqDone := make(chan error, 1)
	newCancel, newGen, _, awaiting := sup.restart(context.Background(),
		RestartRequest{Actor: "alice", Done: reqDone}, func() {}, 0, false)
	if newGen != 0 || awaiting {
		t.Fatalf("expected no new generation to launch, got gen=%d awaiting=%v", newGen, awaiting)
	}
	if newCancel == nil {
		t.Fatal("expected the original cancel func back, not nil")
	}
	select {
	case err := <-reqDone:
		if !errors.Is(err, ErrRecordingActive) {
			t.Fatalf("expected ErrRecordingActive, got %v", err)
		}
	default:
		t.Fatal("expected Done to receive the refusal")
	}
	if st := sup.Status(); st.State != StateReady {
		t.Fatalf("P3-2: state = %q after a refused restart, want %q (must not stay stuck in restarting)", st.State, StateReady)
	}

	cancel()
	<-done
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

// overlapEngine is the P1-1 regression harness: it counts how many Run
// calls are concurrently active (peakActive) and fails the test the
// instant more than one is, which is the ultimate failure mode the
// generation-tagged errCh redesign closes (two feeds/scanners/outboxes
// racing on the same *Engine). The FIRST call ignores ctx cancellation
// until release is closed (forcing a deterministic grace timeout, like
// blockingEngine); every later call behaves like fakeEngine (returns as
// soon as ctx is cancelled).
type overlapEngine struct {
	t         *testing.T
	active    *atomic.Int32
	peakSeen  *atomic.Int32
	callIndex *atomic.Int32
	release   chan struct{}
	applied   *atomic.Int64
}

func (o overlapEngine) ApplySettings(s platform.Settings, v int64) { o.applied.Store(v) }

func (o overlapEngine) Run(ctx context.Context) error {
	n := o.active.Add(1)
	for {
		peak := o.peakSeen.Load()
		if n <= peak || o.peakSeen.CompareAndSwap(peak, n) {
			break
		}
	}
	if n > 1 {
		o.t.Errorf("overlapEngine: %d Run calls concurrently active (P1-1 regression: two generations alive at once)", n)
	}
	defer o.active.Add(-1)

	isFirst := o.callIndex.Add(1) == 1
	if isFirst {
		// The abandoned generation: ignore ctx, keep "running" (as if
		// still draining) until the test explicitly releases it.
		<-o.release
		return ctx.Err()
	}
	<-ctx.Done()
	return ctx.Err()
}

// TestSupervisorGracefulTimeoutNeverOverlapsRunsAndGatesRestarts is the
// P1-1 regression test. Sequence: boot succeeds; a restart is requested
// and its cancel-and-wait step times out (Grace elapsed, the abandoned
// generation has NOT actually returned yet) — before the fix, the next
// restart would launch a second Engine.Run while the first was still
// executing. Here: (1) a restart requested WHILE the abandoned
// generation is still alive must be REFUSED (pendingGen gates it); (2)
// once the abandoned generation is released and its exit observed, a
// restart succeeds normally; (3) overlapEngine's own concurrency counter
// proves no two Run calls were EVER active at the same time, throughout.
func TestSupervisorGracefulTimeoutNeverOverlapsRunsAndGatesRestarts(t *testing.T) {
	sup, _ := newTestSupervisor(t)
	sup.Grace = 50 * time.Millisecond
	sup.Ready = nil // overlapEngine has no readiness signal; assume-ready-once-launched

	oe := overlapEngine{
		t: t, active: &atomic.Int32{}, peakSeen: &atomic.Int32{},
		callIndex: &atomic.Int32{}, applied: &atomic.Int64{},
		release: make(chan struct{}),
	}
	sup.Engine = oe

	cancel, done := runSupervisor(t, sup)
	defer cancel()
	waitForState(t, sup, StateReady)

	// Restart 1: forces the grace timeout — the abandoned generation
	// (gen 1) is still executing (blocked on oe.release) when this
	// returns.
	reqDone1 := make(chan error, 1)
	if err := sup.Request(RestartRequest{Actor: "alice", Done: reqDone1}); err != nil {
		t.Fatalf("first Request: %v", err)
	}
	if err := <-reqDone1; err == nil {
		t.Fatal("expected the first restart to report a timeout error")
	}
	waitForState(t, sup, StateFailed)

	// Restart 2, attempted WHILE gen 1 is still alive: must be refused —
	// this is the exact window the pre-fix code let a second Engine.Run
	// launch into.
	if err := sup.Request(RestartRequest{Actor: "bob"}); !errors.Is(err, ErrRestartInProgress) {
		t.Fatalf("expected a restart attempted while the abandoned generation is still alive to be refused, got %v", err)
	}

	// Release gen 1 so it can finally return and be observed.
	close(oe.release)

	// Restart 3, now that gen 1's exit has (or will imminently have)
	// been observed, must eventually succeed.
	deadline := time.Now().Add(3 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		reqDone3 := make(chan error, 1)
		err := sup.Request(RestartRequest{Actor: "carol", Done: reqDone3})
		if err == nil {
			if err := <-reqDone3; err != nil {
				t.Fatalf("restart 3 failed: %v", err)
			}
			lastErr = nil
			break
		}
		lastErr = err
		time.Sleep(10 * time.Millisecond)
	}
	if lastErr != nil {
		t.Fatalf("restart 3 never got accepted: %v", lastErr)
	}
	waitForState(t, sup, StateReady)

	if peak := oe.peakSeen.Load(); peak > 1 {
		t.Fatalf("P1-1: peak concurrently-active Run calls = %d, want at most 1", peak)
	}

	cancel()
	<-done
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
