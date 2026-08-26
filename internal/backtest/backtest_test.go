package backtest

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/exchange"
	"github.com/cploutarchou/arb-chain-bot/internal/marketdata"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

var bt0 = time.Unix(1_700_000_000, 0).UTC()

func stepRules() exchange.InstrumentRules {
	return exchange.InstrumentRules{
		QtyMode: exchange.PrecisionStep, QtyStep: d("0.001"),
		PriceMode: exchange.PrecisionStep, PriceTick: d("0.001"),
		MinNotional: d("5"),
	}
}

func market(sym, base, quote string) exchange.Market {
	return exchange.Market{
		ID:   exchange.MarketID{Exchange: "binance", Symbol: exchange.Symbol(sym)},
		Base: exchange.Asset(base), Quote: exchange.Asset(quote),
		Enabled: true, Status: exchange.MarketTrading,
		Rules: stepRules(),
	}
}

func snapshotJSON(lastID int64, bids, asks [][2]string) []byte {
	doc := map[string]any{"lastUpdateId": lastID, "bids": bids, "asks": asks}
	b, _ := json.Marshal(doc)
	return b
}

func deltaJSON(sym string, first, final int64, bids, asks [][2]string) []byte {
	doc := map[string]any{
		"e": "depthUpdate", "E": bt0.UnixMilli(), "s": sym,
		"U": first, "u": final, "b": bids, "a": asks,
	}
	b, _ := json.Marshal(doc)
	return b
}

// writeRecording builds a one-segment synthetic recording of the
// profitable pricing fixture (1000 USDT → 1016.94204 at 10 bps fees):
// three snapshots making every book HEALTHY, then one irrelevant delta
// that lands mid-cycle through the pump waiter.
func writeRecording(t *testing.T) (segments []string, streams map[uint16]exchange.Symbol) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "depth-000001.seg.zst")
	w, err := marketdata.NewSegmentWriter(path)
	if err != nil {
		t.Fatal(err)
	}
	frames := []marketdata.Frame{
		{Recv: bt0, Dir: marketdata.DirREST, StreamID: 1,
			Payload: snapshotJSON(100, nil, [][2]string{{"100", "10"}, {"101", "5"}})},
		{Recv: bt0.Add(10 * time.Millisecond), Dir: marketdata.DirREST, StreamID: 2,
			Payload: snapshotJSON(200, nil, [][2]string{{"0.1", "100"}, {"0.11", "100"}})},
		{Recv: bt0.Add(20 * time.Millisecond), Dir: marketdata.DirREST, StreamID: 3,
			Payload: snapshotJSON(300, [][2]string{{"10.2", "1000"}}, nil)},
		// Far-from-touch delta while the first cycle is in flight.
		{Recv: bt0.Add(100 * time.Millisecond), Dir: marketdata.DirWS, StreamID: 0,
			Payload: deltaJSON("BTCUSDT", 101, 101, [][2]string{{"90", "1"}}, nil)},
	}
	for _, fr := range frames {
		if err := w.Append(fr); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return []string{path}, map[uint16]exchange.Symbol{1: "BTCUSDT", 2: "ETHBTC", 3: "ETHUSDT"}
}

func baseOptions(t *testing.T) Options {
	t.Helper()
	segs, streams := writeRecording(t)
	return Options{
		Segments: segs,
		Streams:  streams,
		Markets: []exchange.Market{
			market("BTCUSDT", "BTC", "USDT"),
			market("ETHBTC", "ETH", "BTC"),
			market("ETHUSDT", "ETH", "USDT"),
		},
		StartingAssets:  []exchange.Asset{"USDT"},
		InitialBalances: map[exchange.Asset]decimal.Decimal{"USDT": d("10000")},
		Seed:            42,
	}
}

// Acceptance: the harness drives the full pipeline over a recording —
// books sync, the scanner qualifies, cycles execute and settle with
// positive PnL, and the mid-flight frame is consumed by the pump.
func TestBaselineRunExecutesProfitableCycles(t *testing.T) {
	res, err := Run(baseOptions(t))
	if err != nil {
		t.Fatal(err)
	}
	if res.Frames != 4 || res.FrameErrors != 0 {
		t.Fatalf("frames=%d errors=%d", res.Frames, res.FrameErrors)
	}
	if res.Qualified == 0 || len(res.Cycles) == 0 {
		t.Fatalf("no cycles: qualified=%d cycles=%d (evals=%d skipped=%d rejected=%d)",
			res.Qualified, len(res.Cycles), res.Evaluations, res.SkippedBook, res.Rejected)
	}
	for _, c := range res.Cycles {
		if c.Outcome != "ALL_FILLED" {
			t.Fatalf("cycle %s outcome = %s", c.CycleID, c.Outcome)
		}
		if !c.RealizedPnL.IsPositive() {
			t.Fatalf("cycle %s pnl = %s", c.CycleID, c.RealizedPnL)
		}
		if c.AvgLegLatency <= 0 {
			t.Fatalf("cycle %s no latency recorded", c.CycleID)
		}
	}
	if !d(res.NetPnL["USDT"]).IsPositive() {
		t.Fatalf("net pnl = %s", res.NetPnL["USDT"])
	}
	if !d(res.Turnover["USDT"]).IsPositive() {
		t.Fatalf("turnover = %s", res.Turnover["USDT"])
	}
	// The mid-cycle frame advanced the world past its receive time.
	if res.To.Before(bt0.Add(100 * time.Millisecond)) {
		t.Fatalf("clock ended at %s; pump never consumed the in-flight frame", res.To)
	}
}

// Acceptance: identical inputs reproduce byte-identical results —
// the §64 determinism contract extended to the whole campaign harness.
func TestRunIsDeterministic(t *testing.T) {
	a, err := Run(baseOptions(t))
	if err != nil {
		t.Fatal(err)
	}
	b, err := Run(baseOptions(t))
	if err != nil {
		t.Fatal(err)
	}
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	if string(ja) != string(jb) {
		t.Fatalf("non-deterministic results:\n%s\n---\n%s", ja, jb)
	}
	if !reflect.DeepEqual(a.NetPnL, b.NetPnL) {
		t.Fatalf("pnl differs: %v vs %v", a.NetPnL, b.NetPnL)
	}
}

// Acceptance (§80 "higher fees"): a fee bump that eats the whole edge
// stops cycles from qualifying — and the campaign flags a strategy that
// only earns in the baseline.
func TestFeeStressKillsThinEdgeAndIsFlagged(t *testing.T) {
	opts := baseOptions(t)
	opts.Scenario = Scenario{Name: "fees+80bps", FeeBumpBps: 80}
	stressed, err := Run(opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(stressed.Cycles) != 0 {
		t.Fatalf("cycles under +80bps fees = %d, want 0", len(stressed.Cycles))
	}

	baseline, err := Run(baseOptions(t))
	if err != nil {
		t.Fatal(err)
	}
	c := Campaign{Recording: "test", Results: []Result{baseline, stressed}}
	flags := strings.Join(c.Flags("USDT"), " | ")
	if !strings.Contains(flags, "PROFITABLE ONLY UNDER PERFECT CONDITIONS") {
		t.Fatalf("missing §80 flag, got: %s", flags)
	}
	md := c.Markdown("USDT")
	for _, want := range []string{"baseline", "fees+80bps", "Verdict", "not a live-trading result"} {
		if !strings.Contains(md, want) {
			t.Fatalf("markdown missing %q:\n%s", want, md)
		}
	}
}

// Acceptance (§80 "lower liquidity"): halving world depth halves the
// deployable size — turnover must drop against the baseline.
func TestWorldDepthStressReducesTurnover(t *testing.T) {
	baseline, err := Run(baseOptions(t))
	if err != nil {
		t.Fatal(err)
	}
	opts := baseOptions(t)
	opts.Scenario = Scenario{Name: "liquidity-50pct", WorldDepthFactor: d("0.5")}
	thin, err := Run(opts)
	if err != nil {
		t.Fatal(err)
	}
	bt, tt := d(baseline.Turnover["USDT"]), d(thin.Turnover["USDT"])
	if !tt.LessThan(bt) {
		t.Fatalf("turnover thin=%s vs baseline=%s; depth haircut had no effect", tt, bt)
	}
}

// Acceptance (§80 "worse fills"): with fills capped below the planned
// size, cycles settle partial — realized economics degrade vs plan.
func TestFillDepthStressDegradesOutcomes(t *testing.T) {
	opts := baseOptions(t)
	opts.Scenario = Scenario{Name: "fills-40pct", FillDepthFactor: d("0.4")}
	res, err := Run(opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Cycles) == 0 {
		t.Fatal("no cycles executed under fill haircut")
	}
	for _, c := range res.Cycles {
		if c.Outcome == "ALL_FILLED" && c.Input.Equal(d("1000")) {
			t.Fatalf("full fill at full size despite 0.4 fill haircut: %+v", c)
		}
	}
}

// Acceptance (§80 "higher latency"): scaling latency stretches simulated
// leg latencies by the same factor (seeded draws, deterministic).
func TestLatencyScaleStretchesLegLatency(t *testing.T) {
	base, err := Run(baseOptions(t))
	if err != nil {
		t.Fatal(err)
	}
	opts := baseOptions(t)
	opts.Scenario = Scenario{Name: "latency-x4", LatencyScale: 4}
	slow, err := Run(opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(base.Cycles) == 0 || len(slow.Cycles) == 0 {
		t.Fatalf("cycles base=%d slow=%d", len(base.Cycles), len(slow.Cycles))
	}
	if slow.Cycles[0].AvgLegLatency <= base.Cycles[0].AvgLegLatency {
		t.Fatalf("latency did not stretch: base=%s slow=%s",
			base.Cycles[0].AvgLegLatency, slow.Cycles[0].AvgLegLatency)
	}
}

func TestDefaultGridShape(t *testing.T) {
	grid := DefaultGrid()
	if grid[0].Name != "baseline" {
		t.Fatalf("grid[0] = %s", grid[0].Name)
	}
	seen := map[string]bool{}
	for _, s := range grid {
		if seen[s.Name] {
			t.Fatalf("duplicate scenario %s", s.Name)
		}
		seen[s.Name] = true
		n := s.Normalize()
		if n.LatencyScale <= 0 || !n.FillDepthFactor.IsPositive() || !n.WorldDepthFactor.IsPositive() {
			t.Fatalf("scenario %s not normalized: %+v", s.Name, n)
		}
	}
	for _, want := range []string{"fees", "latency", "fills", "liquidity", "adverse"} {
		found := false
		for _, s := range grid {
			if strings.Contains(s.Name, want) {
				found = true
			}
		}
		if !found {
			t.Fatalf("grid lacks a %q axis: %v", want, grid)
		}
	}
	_ = fmt.Sprint(grid)
}
