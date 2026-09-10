package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/config"
	"github.com/cploutarchou/arb-chain-bot/internal/exchange"
	"github.com/cploutarchou/arb-chain-bot/internal/risk"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func mkt(sym string, tick, step, minNotional string, status exchange.MarketStatus) exchange.Market {
	return exchange.Market{
		ID:     exchange.MarketID{Exchange: "binance", Symbol: exchange.Symbol(sym)},
		Status: status,
		Rules: exchange.InstrumentRules{
			QtyMode: exchange.PrecisionStep, QtyStep: d(step),
			PriceMode: exchange.PrecisionStep, PriceTick: d(tick),
			MinQty:      d("0.0001"),
			MinNotional: d(minNotional),
		},
	}
}

func TestDiffMetadata(t *testing.T) {
	baseline := map[string]exchange.Market{
		"BTCUSDT": mkt("BTCUSDT", "0.01", "0.00001", "5", exchange.MarketTrading),
		"ETHBTC":  mkt("ETHBTC", "0.000001", "0.001", "0", exchange.MarketTrading),
		"ETHUSDT": mkt("ETHUSDT", "0.001", "0.0001", "10", exchange.MarketTrading),
	}

	// Identical snapshot (values re-pointed but equal) → no diff.
	same := map[string]exchange.Market{
		"BTCUSDT": mkt("BTCUSDT", "0.010", "0.000010", "5.0", exchange.MarketTrading),
		"ETHBTC":  mkt("ETHBTC", "0.000001", "0.001", "0", exchange.MarketTrading),
		"ETHUSDT": mkt("ETHUSDT", "0.001", "0.0001", "10", exchange.MarketTrading),
	}
	if diff := diffMetadata(baseline, same); !diff.empty() {
		t.Fatalf("equal snapshot diffed: %+v", diff)
	}

	// A listing the run does not trade is not material.
	extra := map[string]exchange.Market{}
	for k, v := range same {
		extra[k] = v
	}
	extra["SOLUSDT"] = mkt("SOLUSDT", "0.001", "0.01", "10", exchange.MarketTrading)
	if diff := diffMetadata(baseline, extra); !diff.empty() {
		t.Fatalf("unconfigured listing diffed: %+v", diff)
	}

	// Delisted, halted and re-filtered configured symbols all count,
	// with examples for the reason string.
	changed := map[string]exchange.Market{}
	for k, v := range same {
		changed[k] = v
	}
	delete(changed, "ETHBTC")
	halted := mkt("BTCUSDT", "0.01", "0.00001", "5", exchange.MarketHalted)
	changed["BTCUSDT"] = halted
	refiltered := mkt("ETHUSDT", "0.001", "0.0001", "20", exchange.MarketTrading) // min-notional doubled
	changed["ETHUSDT"] = refiltered

	diff := diffMetadata(baseline, changed)
	if diff.empty() {
		t.Fatal("material changes produced no diff")
	}
	if len(diff.Gone) != 1 || diff.Gone[0] != "ETHBTC" {
		t.Fatalf("gone = %v", diff.Gone)
	}
	if len(diff.Status) != 1 {
		t.Fatalf("status = %v", diff.Status)
	}
	if len(diff.Filter) != 1 || diff.Filter[0] != "ETHUSDT" {
		t.Fatalf("filter = %v", diff.Filter)
	}
	if diff.summary() == "" {
		t.Fatal("empty summary")
	}
}

// fakeMetaSource serves snapshots in sequence; a nil entry makes the
// fetch fail (the monitor must log and continue, never trip).
type fakeMetaSource struct {
	snaps  [][]exchange.Market
	called int
}

func (f *fakeMetaSource) ExchangeInfo(context.Context) ([]exchange.Market, error) {
	i := f.called
	f.called++
	if i < len(f.snaps) && f.snaps[i] == nil {
		return nil, errors.New("venue unreachable")
	}
	if i < len(f.snaps) {
		return f.snaps[i], nil
	}
	if len(f.snaps) == 0 {
		return nil, errors.New("venue unreachable")
	}
	return f.snaps[len(f.snaps)-1], nil
}

func TestWatchMetadataTripsOnMaterialChange(t *testing.T) {
	baseline := map[string]exchange.Market{
		"BTCUSDT": mkt("BTCUSDT", "0.01", "0.00001", "5", exchange.MarketTrading),
	}
	// Tick 1: fetch fails — no trip (a venue hiccup is not metadata).
	// Tick 2: unchanged — no trip.
	// Tick 3: min-notional changed — trip with a reason naming it.
	src := &fakeMetaSource{snaps: [][]exchange.Market{
		nil,
		{mkt("BTCUSDT", "0.01", "0.00001", "5", exchange.MarketTrading)},
		{mkt("BTCUSDT", "0.01", "0.00001", "25", exchange.MarketTrading)},
	}}

	e := NewEngine(config.Bootstrap{}, testLogger())
	reg := risk.NewRegistry(func(risk.Transition) {})
	reg.Register(breakerMetadata, "exchange:binance", 0)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		e.watchMetadata(ctx, src, reg, "exchange:binance", baseline, 5*time.Millisecond, time.Now)
		close(done)
	}()

	deadline := time.After(3 * time.Second)
	for {
		if st, ok := reg.State(breakerMetadata, "exchange:binance"); ok && st == risk.BreakerOpen {
			break
		}
		select {
		case <-deadline:
			cancel()
			<-done
			t.Fatal("metadata change did not trip the breaker")
		case <-time.After(2 * time.Millisecond):
		}
	}
	cancel()
	<-done

	if src.called < 3 {
		t.Fatalf("fetches = %d, want ≥3", src.called)
	}
	st, ok := reg.State(breakerMetadata, "exchange:binance")
	if !ok || st != risk.BreakerOpen {
		t.Fatalf("breaker = %v/%v", st, ok)
	}
	if !reg.AnyOpen("exchange:binance") {
		t.Fatal("scoped breaker must gate the venue's scanner scope")
	}
	for _, tr := range reg.States() {
		if tr.Name == breakerMetadata && tr.Reason == "" {
			t.Fatal("trip carried no reason")
		}
	}
}

func TestWatchMetadataDisabled(t *testing.T) {
	e := NewEngine(config.Bootstrap{}, testLogger())
	reg := risk.NewRegistry(func(risk.Transition) {})
	src := &fakeMetaSource{}
	e.watchMetadata(context.Background(), src, reg, "exchange:binance",
		map[string]exchange.Market{"BTCUSDT": mkt("BTCUSDT", "0.01", "0.00001", "5", exchange.MarketTrading)},
		0, time.Now)
	if src.called != 0 {
		t.Fatalf("disabled monitor fetched %d times", src.called)
	}
}

// Exactness guard for the diff's own arithmetic: equal decimals in
// different string forms must not read as a venue change (a re-pointed
// snapshot would otherwise trip the breaker once an hour forever).
func TestRulesEqualExactForms(t *testing.T) {
	a := exchange.InstrumentRules{QtyStep: decimal.RequireFromString("0.001")}
	b := exchange.InstrumentRules{QtyStep: decimal.RequireFromString("1e-3")}
	if !rulesEqual(a, b) {
		t.Fatal("0.001 vs 1e-3 read as different rules")
	}
	c := exchange.InstrumentRules{QtyStep: decimal.RequireFromString("0.002")}
	if rulesEqual(a, c) {
		t.Fatal("different steps read equal")
	}
}
