package binance

import (
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/exchange"
	"github.com/cploutarchou/arb-chain-bot/internal/orderbook"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

var recvT = time.Unix(1_700_000_000, 0)

// Fixture shapes follow the official spot docs (web-socket-streams.md,
// rest-api.md) verified in docs/research/exchanges.md.

const combinedFrame = `{"stream":"btcusdt@depth@100ms","data":{"e":"depthUpdate","E":1700000000123,"s":"BTCUSDT","U":157,"u":160,"b":[["0.0024","10.00000000"],["0.0022","0.00000000"]],"a":[["0.0026","100.50000000"]]}}`

const bareFrame = `{"e":"depthUpdate","E":1700000000123,"s":"BTCUSDT","U":161,"u":162,"b":[],"a":[["0.0027","5"]]}`

const snapshotBody = `{"lastUpdateId":1027024,"bids":[["4.00000000","431.00000000"],["3.99000000","9.00000000"]],"asks":[["4.00000200","12.00000000"]]}`

func TestDecodeCombinedFrame(t *testing.T) {
	ev, err := DecodeWSFrame([]byte(combinedFrame), recvT)
	if err != nil {
		t.Fatal(err)
	}
	if ev.Market.Symbol != "BTCUSDT" || ev.Market.Exchange != ID {
		t.Fatalf("market = %v", ev.Market)
	}
	if ev.FirstUpdateID != 157 || ev.FinalUpdateID != 160 || ev.IsSnapshot {
		t.Fatalf("ids = %d/%d snapshot=%v", ev.FirstUpdateID, ev.FinalUpdateID, ev.IsSnapshot)
	}
	if !ev.Bids[0].Price.Equal(d("0.0024")) || !ev.Bids[0].Qty.Equal(d("10")) {
		t.Fatalf("bid0 = %+v", ev.Bids[0])
	}
	// Zero-qty deletion level survives decoding (the book applies it).
	if !ev.Bids[1].Qty.IsZero() {
		t.Fatalf("bid1 = %+v", ev.Bids[1])
	}
	if !ev.EventTime.Equal(time.UnixMilli(1700000000123)) || !ev.ReceiveTime.Equal(recvT) {
		t.Fatalf("times = %s / %s", ev.EventTime, ev.ReceiveTime)
	}
}

func TestDecodeBareFrameAndNonDepth(t *testing.T) {
	if _, err := DecodeWSFrame([]byte(bareFrame), recvT); err != nil {
		t.Fatal(err)
	}
	_, err := DecodeWSFrame([]byte(`{"e":"trade","s":"BTCUSDT"}`), recvT)
	if !errors.Is(err, ErrNotDepthEvent) {
		t.Fatalf("non-depth: %v", err)
	}
	if _, err := DecodeWSFrame([]byte(`{"e":"depthUpdate","s":"BTCUSDT","U":1,"u":2,"b":[["x","1"]],"a":[]}`), recvT); err == nil {
		t.Fatal("malformed price accepted")
	}
}

func TestDecodeSnapshot(t *testing.T) {
	ev, err := DecodeRESTSnapshot("BTCUSDT", []byte(snapshotBody), recvT)
	if err != nil {
		t.Fatal(err)
	}
	if !ev.IsSnapshot || ev.FinalUpdateID != 1027024 {
		t.Fatalf("snapshot = %+v", ev)
	}
	if len(ev.Bids) != 2 || !ev.Asks[0].Price.Equal(d("4.000002")) {
		t.Fatalf("levels = %+v", ev)
	}
}

func delta(U, u int64, bids ...orderbook.Level) orderbook.DepthEvent {
	return orderbook.DepthEvent{
		Market:        exchange.MarketID{Exchange: ID, Symbol: "BTCUSDT"},
		FirstUpdateID: U, FinalUpdateID: u,
		Bids:        bids,
		ReceiveTime: recvT,
	}
}

func snap(lastID int64) orderbook.DepthEvent {
	return orderbook.DepthEvent{
		Market:        exchange.MarketID{Exchange: ID, Symbol: "BTCUSDT"},
		IsSnapshot:    true,
		FinalUpdateID: lastID,
		Bids:          []orderbook.Level{{Price: d("100"), Qty: d("1")}},
		ReceiveTime:   recvT,
	}
}

func TestValidatorTable(t *testing.T) {
	v := Validator{}
	cases := []struct {
		meta orderbook.Meta
		U, u int64
		want orderbook.Action
	}{
		{orderbook.Meta{Initialized: false}, 5, 6, orderbook.ActionDrop},                        // pre-init
		{orderbook.Meta{Initialized: true, LastUpdateID: 100}, 95, 100, orderbook.ActionDrop},   // stale
		{orderbook.Meta{Initialized: true, LastUpdateID: 100}, 101, 105, orderbook.ActionApply}, // exact chain
		{orderbook.Meta{Initialized: true, LastUpdateID: 100}, 99, 104, orderbook.ActionApply},  // overlapping (U<=last+1<=u)
		{orderbook.Meta{Initialized: true, LastUpdateID: 100}, 102, 110, orderbook.ActionGap},   // hole
	}
	for i, c := range cases {
		got := v.Validate(c.meta, delta(c.U, c.u))
		if got != c.want {
			t.Fatalf("case %d: %s, want %s", i, got, c.want)
		}
	}
}

// The official init example: buffer deltas, splice snapshot L, drop
// u <= L, first applied must satisfy U <= L+1 <= u.
func TestSyncerOfficialSpliceFlow(t *testing.T) {
	book := orderbook.New(exchange.MarketID{Exchange: ID, Symbol: "BTCUSDT"}, 0)
	s := NewSyncer(book, 100)

	// Deltas arrive while the snapshot is in flight.
	for _, ev := range []orderbook.DepthEvent{
		delta(95, 99, orderbook.Level{Price: d("99"), Qty: d("1")}),    // fully stale
		delta(100, 104, orderbook.Level{Price: d("101"), Qty: d("2")}), // straddles L+1
		delta(105, 110, orderbook.Level{Price: d("102"), Qty: d("3")}), // tail
	} {
		if _, err := s.OnDelta(ev); err != nil {
			t.Fatal(err)
		}
	}
	if s.Synced() {
		t.Fatal("synced before snapshot")
	}
	if err := s.OnSnapshot(snap(100)); err != nil {
		t.Fatal(err)
	}
	if !s.Synced() {
		t.Fatal("not synced after splice")
	}
	v := book.View(0)
	if v.State != orderbook.StateHealthy || v.Version != 3 { // snapshot + 2 applied deltas
		t.Fatalf("state=%s version=%d", v.State, v.Version)
	}
	if book.Meta().LastUpdateID != 110 {
		t.Fatalf("lastUpdateID = %d", book.Meta().LastUpdateID)
	}
	// Steady state continues through the syncer.
	if action, _ := s.OnDelta(delta(111, 112)); action != orderbook.ActionApply {
		t.Fatalf("steady action = %s", action)
	}
}

func TestSyncerSnapshotBehindBuffer(t *testing.T) {
	book := orderbook.New(exchange.MarketID{Exchange: ID, Symbol: "BTCUSDT"}, 0)
	s := NewSyncer(book, 100)
	_, _ = s.OnDelta(delta(105, 110))
	// L+1 = 101 < first buffered U=105: impossible splice → refetch newer.
	if err := s.OnSnapshot(snap(100)); !errors.Is(err, ErrSnapshotBehindBuffer) {
		t.Fatalf("err = %v", err)
	}
	// Newer snapshot heals it.
	if err := s.OnSnapshot(snap(107)); err != nil {
		t.Fatal(err)
	}
	if book.Meta().LastUpdateID != 110 {
		t.Fatalf("lastUpdateID = %d", book.Meta().LastUpdateID)
	}
}

func TestSyncerBufferOverflow(t *testing.T) {
	book := orderbook.New(exchange.MarketID{Exchange: ID, Symbol: "BTCUSDT"}, 0)
	s := NewSyncer(book, 2)
	_, _ = s.OnDelta(delta(1, 2))
	_, _ = s.OnDelta(delta(3, 4))
	if _, err := s.OnDelta(delta(5, 6)); !errors.Is(err, ErrBufferOverflow) {
		t.Fatalf("err = %v", err)
	}
}

// A steady-state gap corrupts the book and flips the syncer back to
// buffering; the next snapshot heals.
func TestSyncerGapTriggersResync(t *testing.T) {
	book := orderbook.New(exchange.MarketID{Exchange: ID, Symbol: "BTCUSDT"}, 0)
	s := NewSyncer(book, 100)
	if err := s.OnSnapshot(snap(100)); err != nil {
		t.Fatal(err)
	}
	if action, _ := s.OnDelta(delta(101, 102)); action != orderbook.ActionApply {
		t.Fatal("apply failed")
	}
	action, err := s.OnDelta(delta(150, 151)) // hole
	if err != nil || action != orderbook.ActionGap {
		t.Fatalf("gap: action=%s err=%v", action, err)
	}
	if s.Synced() {
		t.Fatal("still synced after gap")
	}
	// Buffer next deltas, then heal with a newer snapshot.
	_, _ = s.OnDelta(delta(160, 161))
	if err := s.OnSnapshot(snap(159)); err != nil {
		t.Fatal(err)
	}
	if book.Meta().LastUpdateID != 161 || book.View(0).State != orderbook.StateHealthy {
		t.Fatalf("post-heal: %d %s", book.Meta().LastUpdateID, book.View(0).State)
	}
}

const exchangeInfoBody = `{
  "symbols": [
    {
      "symbol": "BTCUSDT", "status": "TRADING",
      "baseAsset": "BTC", "quoteAsset": "USDT",
      "filters": [
        {"filterType": "PRICE_FILTER", "minPrice": "0.01", "maxPrice": "1000000", "tickSize": "0.01"},
        {"filterType": "LOT_SIZE", "minQty": "0.00001000", "maxQty": "9000.0", "stepSize": "0.00001000"},
        {"filterType": "NOTIONAL", "minNotional": "5.00000000", "applyMinToMarket": true, "maxNotional": "9000000"},
        {"filterType": "PERCENT_PRICE_BY_SIDE", "bidMultiplierUp": "5"}
      ]
    },
    {
      "symbol": "OLDCOIN", "status": "BREAK",
      "baseAsset": "OLD", "quoteAsset": "USDT",
      "filters": [
        {"filterType": "PRICE_FILTER", "tickSize": "0.001"},
        {"filterType": "LOT_SIZE", "minQty": "1", "maxQty": "100000", "stepSize": "1"},
        {"filterType": "MIN_NOTIONAL", "minNotional": "10.0"}
      ]
    }
  ]
}`

func TestParseExchangeInfo(t *testing.T) {
	markets, err := ParseExchangeInfo([]byte(exchangeInfoBody))
	if err != nil {
		t.Fatal(err)
	}
	if len(markets) != 2 {
		t.Fatalf("markets = %d", len(markets))
	}
	btc := markets[0]
	if btc.Base != "BTC" || btc.Quote != "USDT" || btc.Status != exchange.MarketTrading {
		t.Fatalf("btc = %+v", btc)
	}
	if !btc.Rules.Usable() {
		t.Fatal("rules unusable")
	}
	if !btc.Rules.PriceTick.Equal(d("0.01")) || !btc.Rules.QtyStep.Equal(d("0.00001")) {
		t.Fatalf("precision = %+v", btc.Rules)
	}
	if !btc.Rules.MinQty.Equal(d("0.00001")) || !btc.Rules.MinNotional.Equal(d("5")) || !btc.Rules.MaxNotional.Equal(d("9000000")) {
		t.Fatalf("limits = %+v", btc.Rules)
	}
	// Legacy MIN_NOTIONAL shape + halted status.
	old := markets[1]
	if old.Status != exchange.MarketHalted || !old.Rules.MinNotional.Equal(d("10")) {
		t.Fatalf("old = %+v", old)
	}
	if old.Tradeable() {
		t.Fatal("BREAK market must not be tradeable")
	}
}

// Order-book application feed path (§73): decode one combined-stream
// depth frame (JSON envelope + decimal level parsing).
func BenchmarkDecodeWSFrame(b *testing.B) {
	frame := []byte(combinedFrame)
	recv := time.Unix(1_700_000_000, 0)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := DecodeWSFrame(frame, recv); err != nil {
			b.Fatal(err)
		}
	}
}
