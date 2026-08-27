package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/config"
	"github.com/cploutarchou/arb-chain-bot/internal/marketdata"
	"github.com/cploutarchou/arb-chain-bot/internal/platform"
	"github.com/cploutarchou/arb-chain-bot/internal/strategy"
)

func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// exchangeInfoFixture is a minimal but complete Binance exchangeInfo
// document: three TRADING symbols that close one triangle
// (USDT -> BTC -> ETH -> USDT and its mirror), each with usable
// PRICE_FILTER/LOT_SIZE rules.
const exchangeInfoFixture = `{
  "symbols": [
    {"symbol":"BTCUSDT","status":"TRADING","baseAsset":"BTC","quoteAsset":"USDT",
     "filters":[{"filterType":"PRICE_FILTER","tickSize":"0.01"},
                {"filterType":"LOT_SIZE","minQty":"0.0001","maxQty":"1000","stepSize":"0.0001"}]},
    {"symbol":"ETHUSDT","status":"TRADING","baseAsset":"ETH","quoteAsset":"USDT",
     "filters":[{"filterType":"PRICE_FILTER","tickSize":"0.01"},
                {"filterType":"LOT_SIZE","minQty":"0.001","maxQty":"1000","stepSize":"0.001"}]},
    {"symbol":"ETHBTC","status":"TRADING","baseAsset":"ETH","quoteAsset":"BTC",
     "filters":[{"filterType":"PRICE_FILTER","tickSize":"0.000001"},
                {"filterType":"LOT_SIZE","minQty":"0.001","maxQty":"1000","stepSize":"0.001"}]}
  ]
}`

func testEngineSettings() platform.Settings {
	// T-059: the fixture predates the platform/ai sections; WithDefaults
	// fills them from a PAPER seed exactly as Service.Load would.
	return platform.Settings{
		Venues: map[string]platform.VenueSettings{
			"binance": {
				Enabled: true, PaperEnabled: true,
				Symbols:        []string{"BTCUSDT", "ETHUSDT", "ETHBTC"},
				StartingAssets: []string{"USDT"},
				Fees: platform.FeeSettings{
					MakerBps: decimal.NewFromInt(10),
					TakerBps: decimal.NewFromInt(10),
				},
			},
		},
		Paper: platform.PaperSettings{Balances: map[string]string{"USDT": "10000"}},
	}.WithDefaults(config.Bootstrap{Mode: config.ModePaper, AIModel: "claude-sonnet-5"})
}

// newTestEngine builds an engine wired against a fake exchangeInfo
// server and a deliberately unroutable WS host (the smallest harness
// the design allows for a re-entrancy test — no real network, no
// database, no Docker: internal/app/engine_test.go, design §5).
func newTestEngine(t *testing.T) (*Engine, *strategy.Service) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(exchangeInfoFixture))
	}))
	t.Cleanup(srv.Close)

	cfg := config.Bootstrap{
		Mode:          config.ModePaper,
		ShutdownGrace: 3 * time.Second,
	}
	e := NewEngine(cfg, testLogger())
	e.RESTHost = srv.URL
	// 127.0.0.1:1 refuses connections instantly (no listener), so the
	// feed's dial fails fast and retries with backoff — exercising the
	// re-entrancy path without touching the real network or blocking
	// shutdown on a live session (design §5's "dead address" seam).
	e.WSHost = "ws://127.0.0.1:1"
	e.ApplySettings(testEngineSettings(), 1)

	stratSvc := strategy.NewService(strategy.NewMemoryStore(), testLogger(), nil)
	if _, err := stratSvc.Load(context.Background()); err != nil {
		t.Fatalf("strategy load: %v", err)
	}
	e.Strategy = stratSvc
	return e, stratSvc
}

// runOnce starts Run, waits for readiness, then cancels and waits for
// Run to return — the Supervisor's own restart sequence, minus the
// supervisor (design §2.4 steps 4-5). It waits for a scanner distinct
// from the one that was live before this call started, so a second
// runOnce cannot mistake the PREVIOUS run's still-true Status().Ready
// for its own readiness (a race in this harness, not in Engine.Run:
// E1's reset only happens once Run actually gets scheduled, and
// Status().Ready lingers true from the prior run until then).
func runOnce(t *testing.T, e *Engine) error {
	t.Helper()
	before := e.currentScanner()
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- e.Run(ctx) }()

	deadline := time.Now().Add(5 * time.Second)
	for !e.Status().Ready || e.currentScanner() == before {
		if time.Now().After(deadline) {
			cancel()
			<-errCh
			t.Fatal("engine never became ready")
		}
		time.Sleep(5 * time.Millisecond)
	}

	cancel()
	select {
	case err := <-errCh:
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after cancel + ShutdownGrace")
		return nil
	}
}

// TestEngineRunTwiceIsReentrant is the T-057 required test: Engine.Run
// runs twice sequentially and must not leak goroutines or double-
// register the strategy hot-swap callback (design §2.1, E3, E6).
func TestEngineRunTwiceIsReentrant(t *testing.T) {
	e, stratSvc := newTestEngine(t)

	runtime.GC()
	baseline := runtime.NumGoroutine()

	if err := runOnce(t, e); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("first run: unexpected error: %v", err)
	}
	if got := stratSvc.Subscribers(); got != 1 {
		t.Fatalf("after first run: strategy subscribers = %d, want 1", got)
	}

	if err := runOnce(t, e); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("second run: unexpected error: %v", err)
	}
	if got := stratSvc.Subscribers(); got != 1 {
		t.Fatalf("after second run: strategy subscribers = %d, want 1 (E6: must not grow per restart)", got)
	}

	// Give any straggler goroutines a moment to actually unwind, then
	// assert the count returned close to the pre-run baseline (E3: no
	// goroutine may outlive Run). A little slack allows for GC/runtime
	// housekeeping goroutines unrelated to the engine.
	deadline := time.Now().Add(3 * time.Second)
	var after int
	for {
		runtime.GC()
		after = runtime.NumGoroutine()
		if after <= baseline+3 || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if after > baseline+3 {
		t.Fatalf("goroutine leak after two Run cycles: baseline=%d after=%d", baseline, after)
	}
}

// TestEngineSetPaperPausedConsumedOnBoot is the P2-2 engine-side
// regression test: SetPaperPaused(true) called before a Run makes THAT
// run's paper engine start paused instead of the unconditional Resume()
// a direct call always used to do; a later Run that never calls
// SetPaperPaused again reverts to the default (start running) —
// confirming the flag is consumed (read-and-cleared), not sticky.
func TestEngineSetPaperPausedConsumedOnBoot(t *testing.T) {
	e, _ := newTestEngine(t)

	e.SetPaperPaused(true)
	if err := runOnceUntilReady(t, e, func() {
		pap := e.Paper()
		if pap == nil {
			t.Fatal("expected a paper engine once ready")
		}
		if pap.Running() {
			t.Fatal("SetPaperPaused(true) must make the run start paused")
		}
	}); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("first run: %v", err)
	}

	// No SetPaperPaused call this time: default behavior (start running)
	// must be back, proving the flag does not leak into a later run.
	if err := runOnceUntilReady(t, e, func() {
		pap := e.Paper()
		if pap == nil {
			t.Fatal("expected a paper engine once ready")
		}
		if !pap.Running() {
			t.Fatal("without SetPaperPaused, the default (start running) must apply")
		}
	}); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("second run: %v", err)
	}
}

// runOnceUntilReady is runOnce plus a check callback invoked once ready,
// BEFORE cancelling — the state runOnce alone cannot let a caller
// inspect (runOnce cancels immediately upon detecting readiness).
func runOnceUntilReady(t *testing.T, e *Engine, check func()) error {
	t.Helper()
	before := e.currentScanner()
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- e.Run(ctx) }()

	deadline := time.Now().Add(5 * time.Second)
	for !e.Status().Ready || e.currentScanner() == before {
		if time.Now().After(deadline) {
			cancel()
			<-errCh
			t.Fatal("engine never became ready")
		}
		time.Sleep(5 * time.Millisecond)
	}

	check()

	cancel()
	select {
	case err := <-errCh:
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after cancel + ShutdownGrace")
		return nil
	}
}

// TestEngineRunAppliesLatestStrategyEachRun exercises the advisor's
// "explicit apply at run entry" fix: Subscribe only fires immediately on
// its FIRST registration, so a config applied between two runs must
// still reach the second run's fresh scanner even though no swap
// happens while the engine is stopped.
func TestEngineRunAppliesLatestStrategyEachRun(t *testing.T) {
	e, stratSvc := newTestEngine(t)

	if err := runOnce(t, e); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("first run: %v", err)
	}

	p := stratSvc.Current().Params
	p.Scanner.Depth = 77
	if _, err := stratSvc.Apply(context.Background(), "test", "system", p); err != nil {
		t.Fatalf("apply: %v", err)
	}

	if err := runOnce(t, e); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("second run: %v", err)
	}
	// Run has stopped by now, but the just-finished run's scanner is
	// still reachable (the NEXT Run() call is what resets it) — its
	// captured-at-entry config must reflect the applied version, not
	// the boot literal.
	scn := e.currentScanner()
	if scn == nil {
		t.Fatal("expected the second run's scanner to still be reachable")
	}
	if got := scn.CurrentConfig().Depth; got != 77 {
		t.Fatalf("second run's scanner depth = %d, want 77 (config applied between runs)", got)
	}
}

// TestRecordingSessionStopsWhenRunReturns is a P2-6 regression test
// against the real Engine: an API-started recording session (bound via
// RecorderControl.Bind at Run entry) must not survive the Run call that
// bound it. Exercises the ordinary shutdown path (runOnce's cancel +
// wait) end to end.
func TestRecordingSessionStopsWhenRunReturns(t *testing.T) {
	e, _ := newTestEngine(t)
	e.cfg.RecordingDir = t.TempDir()

	before := e.currentScanner()
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- e.Run(ctx) }()

	deadline := time.Now().Add(5 * time.Second)
	for !e.Status().Ready || e.currentScanner() == before {
		if time.Now().After(deadline) {
			cancel()
			<-errCh
			t.Fatal("engine never became ready")
		}
		time.Sleep(5 * time.Millisecond)
	}

	rc := e.Recorder()
	if rc == nil {
		t.Fatal("expected a recorder control once ready")
	}
	if _, err := rc.StartSession(); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	if !rc.Status().Running {
		t.Fatal("expected the session to be running")
	}

	cancel()
	select {
	case <-errCh:
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after cancel")
	}

	// The recorder session's own internal goroutine (independent of
	// Engine.Run's WaitGroup-tracked children) needs a moment to notice
	// runCtx cancelled and finish draining — Run returning does not wait
	// on it synchronously. The invariant this test guards is "stops on
	// its own, without an explicit Stop() call from anyone", not
	// "already stopped the instant Run() returns" — poll briefly rather
	// than asserting on the very next line.
	deadline = time.Now().Add(2 * time.Second)
	for rc.Status().Running {
		if time.Now().After(deadline) {
			t.Fatal("P2-6: recording session outlived the Run call that bound it (orphaned)")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestRecorderControlBoundToRunCtxStopsOnInternalFatalWhileOuterCtxAlive
// is the P2-6 mechanism test: it reconstructs the exact context
// relationship engine.go's Run now uses (runCtx := context.WithCancel
// (ctx), with rctl.Bind(runCtx) rather than rctl.Bind(ctx)) and confirms
// a session started against the bound lifetime stops when RUN'S OWN
// cleanup cancels runCtx — even though the caller's outer ctx is still
// alive. Before the fix, RecorderControl was bound to the wider `ctx`
// parameter directly: Engine.Run returning on an internal fatal error
// (a child goroutine's error, not the caller cancelling ctx) left an
// API-started session running, orphaned, until the SUPERVISOR eventually
// noticed the exit and cancelled its outer context — an unbounded window
// in the presence of the generation-tracking bugs P1-1 fixes.
func TestRecorderControlBoundToRunCtxStopsOnInternalFatalWhileOuterCtxAlive(t *testing.T) {
	outerCtx, outerCancel := context.WithCancel(context.Background())
	t.Cleanup(outerCancel)

	runCtx, cancelRun := context.WithCancel(outerCtx)
	rc := &marketdata.RecorderControl{
		Dir: t.TempDir(), Log: testLogger(),
		NewSessionID: func() string { return "sess-1" },
	}
	rc.Bind(runCtx) // engine.go's post-fix wiring
	if _, err := rc.StartSession(); err != nil {
		t.Fatal(err)
	}
	if !rc.Status().Running {
		t.Fatal("expected the session to be running")
	}

	// Simulate Run's own deferred cleanup firing on an internal fatal
	// error — the OUTER ctx (the caller's, e.g. Supervisor's) is
	// deliberately left alive.
	cancelRun()

	deadline := time.Now().Add(2 * time.Second)
	for rc.Status().Running {
		if time.Now().After(deadline) {
			t.Fatal("P2-6: session bound to runCtx did not stop when runCtx was cancelled")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if outerCtx.Err() != nil {
		t.Fatal("test setup bug: outer ctx must still be alive")
	}
}

// TestStartAssetsFallsBackWithoutARun exercises the D5 fallback: a
// profile that constructs an Engine purely for the read model (e.g.
// ProfileAPI) never calls Run, so e.starts (populated only mid-run,
// see E1/E4) stays nil forever. startAssets must still report the
// live settings document's starting assets in that window rather than
// an empty list — dropping every portfolio/report/Telegram/AI row that
// reads it.
func TestStartAssetsFallsBackWithoutARun(t *testing.T) {
	e := NewEngine(config.Bootstrap{StartingAssets: []string{"usdt"}}, testLogger())

	// Before ApplySettings: falls all the way back to platform.Seed(cfg).
	if got := e.startAssets(); len(got) != 1 || string(got[0]) != "USDT" {
		t.Fatalf("startAssets before ApplySettings = %v, want [USDT] (seeded from cfg)", got)
	}

	// After ApplySettings but still before any Run: the applied
	// document's starting assets, not the (now stale) boot cfg.
	e.ApplySettings(testEngineSettings(), 1)
	got := e.startAssets()
	if len(got) != 1 || string(got[0]) != "USDT" {
		t.Fatalf("startAssets after ApplySettings, before Run = %v, want [USDT]", got)
	}
}
