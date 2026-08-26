package replay

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/exchange"
	"github.com/cploutarchou/arb-chain-bot/internal/marketdata"
	"github.com/cploutarchou/arb-chain-bot/internal/strategy"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

var rt0 = time.Unix(1_700_000_000, 0).UTC()

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

// writeRecording is the same profitable fixture internal/backtest's own
// tests use (three HEALTHY books, 1000 USDT -> profitable at 10bps) —
// this package calls backtest.Run directly, so reusing the fixture
// proves Execute wires it correctly rather than re-testing backtest.Run
// itself.
func writeRecording(t *testing.T, dir string) (segments []string, streams map[uint16]exchange.Symbol) {
	t.Helper()
	path := filepath.Join(dir, "depth-000001.seg.zst")
	w, err := marketdata.NewSegmentWriter(path)
	if err != nil {
		t.Fatal(err)
	}
	frames := []marketdata.Frame{
		{Recv: rt0, Dir: marketdata.DirREST, StreamID: 1,
			Payload: snapshotJSON(100, nil, [][2]string{{"100", "10"}, {"101", "5"}})},
		{Recv: rt0.Add(10 * time.Millisecond), Dir: marketdata.DirREST, StreamID: 2,
			Payload: snapshotJSON(200, nil, [][2]string{{"0.1", "100"}, {"0.11", "100"}})},
		{Recv: rt0.Add(20 * time.Millisecond), Dir: marketdata.DirREST, StreamID: 3,
			Payload: snapshotJSON(300, [][2]string{{"10.2", "1000"}}, nil)},
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

type fakeSources struct {
	streams map[uint16]exchange.Symbol
	markets []exchange.Market
	err     error
}

func (f fakeSources) RecordingStreams(context.Context, string) (map[uint16]exchange.Symbol, error) {
	return f.streams, f.err
}

func (f fakeSources) LoadMarkets(context.Context, exchange.ExchangeID) ([]exchange.Market, error) {
	return f.markets, nil
}

type fakeParams struct {
	snap strategy.Snapshot
	err  error
}

func (f fakeParams) Get(context.Context, int64) (strategy.Snapshot, error) { return f.snap, f.err }

func TestRequestNormalizeDefaultsAndValidation(t *testing.T) {
	r, err := Request{Recording: "REC1"}.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	if r.Recording != "REC1" || r.ConfigVersion != 0 || r.Speed != 0 {
		t.Fatalf("normalized = %+v", r)
	}
	for _, bad := range []Request{
		{},
		{Recording: "../etc"},
		{Recording: "R/../../x"},
		{Recording: "R", ConfigVersion: -1},
		{Recording: "R", Speed: -1},
		{Recording: "R", Speed: 5000},
	} {
		if _, err := bad.Normalize(); err == nil {
			t.Fatalf("expected error for %+v", bad)
		}
	}
}

func TestExecuteRunsBaselineAgainstRecording(t *testing.T) {
	dir := t.TempDir()
	_, streams := writeRecording(t, dir)
	src := fakeSources{
		streams: streams,
		markets: []exchange.Market{market("BTCUSDT", "BTC", "USDT"), market("ETHBTC", "ETH", "BTC"), market("ETHUSDT", "ETH", "USDT")},
	}
	var progressed []Progress
	res, err := Execute(context.Background(), src, nil, dir, Request{Recording: "REC1"}, func(p Progress) {
		progressed = append(progressed, p)
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Evaluations == 0 {
		t.Fatalf("no evaluations recorded: %+v", res)
	}
	if len(progressed) != 2 || progressed[0].Done != 0 || progressed[1].Done != 1 {
		t.Fatalf("progress = %+v", progressed)
	}
	// Top is sorted descending by NetBps and never exceeds topN.
	for i := 1; i < len(res.Top); i++ {
		if res.Top[i].NetBps.GreaterThan(res.Top[i-1].NetBps) {
			t.Fatalf("top not sorted descending: %+v", res.Top)
		}
	}
	if len(res.Top) > topN {
		t.Fatalf("top = %d rows, want at most %d", len(res.Top), topN)
	}
}

func TestExecuteFailsHonestlyOnUnresolvableConfigVersion(t *testing.T) {
	dir := t.TempDir()
	_, streams := writeRecording(t, dir)
	src := fakeSources{streams: streams, markets: []exchange.Market{market("BTCUSDT", "BTC", "USDT"), market("ETHBTC", "ETH", "BTC"), market("ETHUSDT", "ETH", "USDT")}}

	// No ParamsSource at all: a request naming a version must fail, not
	// silently fall back to strategy.DefaultParams().
	if _, err := Execute(context.Background(), src, nil, dir, Request{Recording: "REC1", ConfigVersion: 7}, nil); err == nil {
		t.Fatal("expected an error when config_version is requested but no strategy service is wired")
	}

	// ParamsSource present but the version doesn't resolve: same honesty.
	boom := errors.New("no such version")
	if _, err := Execute(context.Background(), src, fakeParams{err: boom}, dir, Request{Recording: "REC1", ConfigVersion: 7}, nil); err == nil {
		t.Fatal("expected the resolver's error to propagate")
	}

	// A resolvable version is used (smoke: no error, still produces evaluations).
	params := fakeParams{snap: strategy.Snapshot{Version: 7, Params: strategy.DefaultParams()}}
	res, err := Execute(context.Background(), src, params, dir, Request{Recording: "REC1", ConfigVersion: 7}, nil)
	if err != nil {
		t.Fatalf("resolvable config_version: %v", err)
	}
	if res.Evaluations == 0 {
		t.Fatalf("no evaluations with resolved params: %+v", res)
	}
}

func TestExecuteMissingSegmentsFailsHonestly(t *testing.T) {
	if _, err := Execute(context.Background(), fakeSources{}, nil, t.TempDir(), Request{Recording: "REC1"}, nil); err == nil {
		t.Fatal("expected an error for an empty segment directory")
	}
}
