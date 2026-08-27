package venue

import (
	"context"
	"testing"

	"github.com/cploutarchou/arb-chain-bot/internal/screener"
)

// T-075 per-venue fixture tests: symbol → base/quote mapping, decimal
// parsing of bare-number fields, funding interval / next time, and the
// per-poll call budget.

func TestKuCoinFixtures(t *testing.T) {
	c, fs := fixtureCollector(t, screener.VenueKuCoin)
	ctx := context.Background()
	inst, err := c.Instruments(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var btcPerp Instrument
	for _, in := range inst {
		if in.Kind == KindPerp && in.Symbol == "XBTUSDTM" {
			btcPerp = in
		}
		if in.Kind == KindPerp && in.Quote != "USDT" {
			t.Fatalf("non-USDT perp leaked: %+v", in)
		}
	}
	// XBT (futures) → BTC (spot name), multiplier is the contract value.
	if btcPerp.Base != "BTC" || btcPerp.CtVal != "0.001" || btcPerp.IntervalH != 8 || btcPerp.TickSize != "0.1" {
		t.Fatalf("XBTUSDTM = %+v", btcPerp)
	}
	qs, err := c.Spot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	q, ok := findQuote(qs, "BTC", "USDT")
	if !ok || !q.BidQty.IsPositive() || q.At.IsZero() {
		t.Fatalf("BTC/USDT = %+v", q)
	}
	ps, err := c.Perps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := findPerp(ps, "BTC")
	if !ok || p.IntervalH != 8 || p.NextFundingAt.IsZero() || !p.Index.IsPositive() || !p.Bid.IsPositive() || p.FundingRate.IsZero() {
		t.Fatalf("BTC perp = %+v", p)
	}
	if fs.hits["/api/v1/market/allTickers"] != 1 || fs.hits["/api/v1/contracts/active"] != 2 || fs.hits["/api/v1/allTickers"] != 1 {
		t.Fatalf("hits = %v", fs.hits)
	}
	nets, err := c.Networks(ctx)
	if err != nil || nets["BTC"].Status != screener.NetworkOpen {
		t.Fatalf("networks BTC = %+v (%v)", nets["BTC"], err)
	}
}

func TestHTXFixtures(t *testing.T) {
	c, fs := fixtureCollector(t, screener.VenueHTX) // FundingCallsPerPoll: 3 mark klines
	ctx := context.Background()
	inst, err := c.Instruments(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var btc Instrument
	for _, in := range inst {
		if in.Kind == KindSpot && in.Symbol == "btcusdt" {
			btc = in
		}
	}
	if btc.Base != "BTC" || btc.Quote != "USDT" || btc.TickSize != "0.01" || btc.StepSize != "0.000001" {
		t.Fatalf("btcusdt = %+v", btc)
	}
	qs, err := c.Spot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if q, ok := findQuote(qs, "BTC", "USDT"); !ok || !q.AskQty.IsPositive() {
		t.Fatalf("BTC/USDT = %+v", q)
	}
	ps, err := c.Perps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if fs.hits["/index/market/history/linear_swap_mark_price_kline"] != 3 {
		t.Fatalf("mark kline calls = %d, want 3", fs.hits["/index/market/history/linear_swap_mark_price_kline"])
	}
	withMark := 0
	for _, p := range ps {
		if p.Quote != "USDT" || !p.Index.IsPositive() || p.IntervalH == 0 {
			t.Fatalf("bad HTX perp: %+v", p)
		}
		if p.Mark.IsPositive() {
			withMark++
		}
	}
	if withMark != 3 {
		t.Fatalf("perps with a round-robin mark = %d, want 3", withMark)
	}
	if p, ok := findPerp(ps, "BTC"); !ok || p.NextFundingAt.IsZero() || p.IntervalH != 8 {
		t.Fatalf("BTC perp = %+v", p)
	}
	nets, err := c.Networks(ctx)
	if err != nil || nets["BTC"].Status == screener.NetworkUnknown {
		t.Fatalf("networks BTC = %+v (%v)", nets["BTC"], err)
	}
}

func TestKrakenFixtures(t *testing.T) {
	c, fs := fixtureCollector(t, screener.VenueKraken)
	ctx := context.Background()
	inst, err := c.Instruments(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var btc, pf Instrument
	for _, in := range inst {
		if in.Kind == KindSpot && in.Symbol == "BTC/USD" {
			btc = in
		}
		if in.Kind == KindPerp && in.Symbol == "PF_XBTUSD" {
			pf = in
		}
		if in.Kind == KindPerp && (in.Quote != "USD" || in.IntervalH != 1) {
			t.Fatalf("bad Kraken perp instrument: %+v", in)
		}
	}
	// assetVersion=1 display names: base BTC (not XXBT), quote USD.
	if btc.Base != "BTC" || btc.Quote != "USD" || btc.TickSize != "0.1" || btc.StepSize != "0.00000001" || btc.MinNotional == "" {
		t.Fatalf("BTC/USD = %+v", btc)
	}
	if pf.Base != "BTC" || pf.CtVal != "1" {
		t.Fatalf("PF_XBTUSD = %+v", pf)
	}
	qs, err := c.Spot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if q, ok := findQuote(qs, "BTC", "USD"); !ok || !q.BidQty.IsPositive() {
		t.Fatalf("BTC/USD quote = %+v", q)
	}
	ps, err := c.Perps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := findPerp(ps, "BTC")
	if !ok || p.IntervalH != 1 || p.NextFundingAt.IsZero() || !p.Index.IsPositive() || p.FundingRate.IsZero() {
		t.Fatalf("BTC perp = %+v", p)
	}
	// Relative rate magnitude: hourly, so |rate| < 0.5 % (venue cap).
	if p.FundingRate.Abs().GreaterThan(dec("0.005")) || p.PredictedFundingRate.Abs().GreaterThan(dec("0.005")) {
		t.Fatalf("funding not relative: %+v", p)
	}
	if fs.hits["/derivatives/api/v4/historicalfundingrates"] != 3 {
		t.Fatalf("history calls = %d, want 3", fs.hits["/derivatives/api/v4/historicalfundingrates"])
	}
}

func TestCoinbaseFixtures(t *testing.T) {
	c, fs := fixtureCollector(t, screener.VenueCoinbase) // BooksPerPoll: 40
	ctx := context.Background()
	qs, err := c.Spot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	q, ok := findQuote(qs, "BTC", "USD")
	if !ok || !q.BidQty.IsPositive() || !q.AskQty.IsPositive() || q.At.IsZero() || !q.Ask.GreaterThan(q.Bid) {
		t.Fatalf("BTC/USD = %+v", q)
	}
	books := fs.hits["/api/v3/brokerage/market/product_book?product_id="]
	if books == 0 || books != len(qs) {
		t.Fatalf("books fetched = %d, quotes = %d", books, len(qs))
	}
	// Second poll: the round-robin wraps, quotes are still carried.
	qs2, err := c.Spot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(qs2) != len(qs) {
		t.Fatalf("second poll returned %d quotes, want %d", len(qs2), len(qs))
	}
	ps, err := c.Perps(ctx)
	if err != nil || len(ps) != 0 {
		t.Fatalf("Perps = %d, %v; want none", len(ps), err)
	}
	nets, err := c.Networks(ctx)
	if err != nil || nets["BTC"].Status != screener.NetworkOpen {
		t.Fatalf("networks BTC = %+v (%v)", nets["BTC"], err)
	}
}
