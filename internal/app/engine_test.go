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
	}
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
