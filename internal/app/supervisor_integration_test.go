package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/config"
	"github.com/cploutarchou/arb-chain-bot/internal/platform"
)

// exchangeInfoFixtureWide is a superset of exchangeInfoFixture: it adds
// BTCUSDC and ETHUSDC so a second, USDC-rooted triangle
// (USDC -> BTC -> ETH -> USDC, sharing the ETHBTC bridge) becomes
// available without changing what the fake REST server returns between
// runs — only which subset of symbols the platform settings document
// selects changes across the restart, exactly like the real bootstrap
// flow re-reading exchangeInfo against a settings-scoped symbol list.
const exchangeInfoFixtureWide = `{
  "symbols": [
    {"symbol":"BTCUSDT","status":"TRADING","baseAsset":"BTC","quoteAsset":"USDT",
     "filters":[{"filterType":"PRICE_FILTER","tickSize":"0.01"},
                {"filterType":"LOT_SIZE","minQty":"0.0001","maxQty":"1000","stepSize":"0.0001"}]},
    {"symbol":"ETHUSDT","status":"TRADING","baseAsset":"ETH","quoteAsset":"USDT",
     "filters":[{"filterType":"PRICE_FILTER","tickSize":"0.01"},
                {"filterType":"LOT_SIZE","minQty":"0.001","maxQty":"1000","stepSize":"0.001"}]},
    {"symbol":"ETHBTC","status":"TRADING","baseAsset":"ETH","quoteAsset":"BTC",
     "filters":[{"filterType":"PRICE_FILTER","tickSize":"0.000001"},
                {"filterType":"LOT_SIZE","minQty":"0.001","maxQty":"1000","stepSize":"0.001"}]},
    {"symbol":"BTCUSDC","status":"TRADING","baseAsset":"BTC","quoteAsset":"USDC",
     "filters":[{"filterType":"PRICE_FILTER","tickSize":"0.01"},
                {"filterType":"LOT_SIZE","minQty":"0.0001","maxQty":"1000","stepSize":"0.0001"}]},
    {"symbol":"ETHUSDC","status":"TRADING","baseAsset":"ETH","quoteAsset":"USDC",
     "filters":[{"filterType":"PRICE_FILTER","tickSize":"0.01"},
                {"filterType":"LOT_SIZE","minQty":"0.001","maxQty":"1000","stepSize":"0.001"}]}
  ]
}`

// TestSupervisorRestartRebuildsTopologyWithRealEngine is the T-057
// design §5 integration path: a real *Engine (not the EngineRunner
// fake used by supervisor_test.go, not the Engine-without-Supervisor
// harness used by engine_test.go) driven by a real *Supervisor across
// one settings-driven restart, proving the composed path — not just
// each half in isolation. It is the regression test for the readiness
// bug the advisor flagged: without clearing Engine.ready when Run
// returns, waitReadyOrFail would observe a stale "ready" from the
// PREVIOUS run and mark the restart done before the new run had
// rebuilt anything.
func TestSupervisorRestartRebuildsTopologyWithRealEngine(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(exchangeInfoFixtureWide))
	}))
	t.Cleanup(srv.Close)

	cfg := config.Bootstrap{
		Mode:           config.ModePaper,
		ShutdownGrace:  3 * time.Second,
		Symbols:        []string{"BTCUSDT", "ETHUSDT", "ETHBTC"},
		StartingAssets: []string{"USDT"},
		PaperBalance:   "10000",
	}

	engine := NewEngine(cfg, testLogger())
	engine.RESTHost = srv.URL
	// Unroutable WS host: the feed dial fails fast and retries with
	// backoff, exercising bootstrap without touching the real network
	// (same seam engine_test.go uses).
	engine.WSHost = "ws://127.0.0.1:1"

	svc := platform.NewService(platform.NewMemoryStore(), testLogger(), nil)
	svc.Catalog = engineCatalog{e: engine}
	ctx := context.Background()
	if _, err := svc.Load(ctx, cfg); err != nil {
		t.Fatalf("settings load: %v", err)
	}
	engine.ApplySettings(svc.Current().Settings, svc.Current().Version)

	sup := &Supervisor{
		Engine:   engine,
		Settings: svc,
		Grace:    3 * time.Second,
		Log:      testLogger(),
		Ready:    func() bool { return engine.Status().Ready },
	}

	runCtx, cancelRun := context.WithCancel(context.Background())
	var supErr atomic.Value
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := sup.Run(runCtx); err != nil && err != context.Canceled {
			supErr.Store(err)
		}
	}()
	t.Cleanup(func() {
		cancelRun()
		<-done
	})

	waitForSupervisorState(t, sup, StateReady)
	if got := len(engine.Status().Markets); got != 3 {
		t.Fatalf("before restart: markets = %d, want 3 (%v)", got, engine.Status().Markets)
	}

	// v2: widen to the USDC-rooted triangle too — a restart-scoped
	// change (venue symbols/starting assets are not hot).
	next := svc.Current().Settings.Clone()
	venue := next.Venues["binance"]
	venue.Symbols = []string{"BTCUSDT", "ETHUSDT", "ETHBTC", "BTCUSDC", "ETHUSDC"}
	venue.StartingAssets = []string{"USDT", "USDC"}
	venue.Fees = platform.FeeSettings{MakerBps: decimal.NewFromInt(10), TakerBps: decimal.NewFromInt(10)}
	next.Venues["binance"] = venue
	next.Paper.Balances["USDC"] = "10000"
	if _, err := svc.Apply(ctx, "test", "test", next); err != nil {
		t.Fatalf("apply v2: %v", err)
	}

	reqDone := make(chan error, 1)
	if err := sup.Request(RestartRequest{Actor: "test", Reason: "widen symbols", Done: reqDone}); err != nil {
		t.Fatalf("request restart: %v", err)
	}
	select {
	case err := <-reqDone:
		if err != nil {
			t.Fatalf("restart failed: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("restart never completed")
	}

	waitForSupervisorState(t, sup, StateReady)
	st := sup.Status()
	if st.SettingsVersion != 2 {
		t.Fatalf("running settings version = %d, want 2", st.SettingsVersion)
	}
	if !engine.Status().Ready {
		t.Fatal("engine not ready after restart")
	}
	if got := len(engine.Status().Markets); got != 5 {
		t.Fatalf("after restart: markets = %d, want 5 (%v)", got, engine.Status().Markets)
	}

	if err, _ := supErr.Load().(error); err != nil {
		t.Fatalf("supervisor.Run returned an unexpected error: %v", err)
	}
}

func waitForSupervisorState(t *testing.T, sup *Supervisor, want RestartState) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		st := sup.Status()
		if st.State == want {
			return
		}
		if st.State == StateFailed {
			t.Fatalf("supervisor entered StateFailed while waiting for %s: %s", want, st.Error)
		}
		if time.Now().After(deadline) {
			t.Fatalf("supervisor never reached state %s (last: %+v)", want, st)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
