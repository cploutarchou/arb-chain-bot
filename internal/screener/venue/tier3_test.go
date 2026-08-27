package venue

import (
	"context"
	"testing"

	"github.com/cploutarchou/arb-chain-bot/internal/screener"
)

// T-078 per-venue fixture tests for the Tier-3 collectors: symbol →
// base/quote mapping quirks, decimal parsing of bare-number fields,
// funding interval / next time, liquidity-unknown flags and the
// per-poll call budgets.

func TestCryptoComFixtures(t *testing.T) {
	c, fs := fixtureCollector(t, screener.VenueCryptoCom) // FundingCallsPerPoll: 3
	ctx := context.Background()
	inst, err := c.Instruments(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var btcPerp Instrument
	for _, in := range inst {
		if in.Kind == KindPerp && in.Symbol == "BTCUSD-PERP" {
			btcPerp = in
		}
		if in.Kind == KindPerp && (in.Quote != "USD" || in.IntervalH != 1) {
			t.Fatalf("bad cryptocom perp instrument: %+v", in) // USD-quoted, hourly funding
		}
		if in.Symbol == "BTCUSD-260828" {
			t.Fatalf("dated FUTURE leaked into instruments: %+v", in)
		}
	}
	if btcPerp.Base != "BTC" || btcPerp.CtVal == "" {
		t.Fatalf("BTCUSD-PERP = %+v", btcPerp)
	}
	qs, err := c.Spot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	q, ok := findQuote(qs, "BTC", "USD")
	if !ok || !q.LiquidityUnknown || !q.BidQty.IsZero() || q.At.IsZero() {
		t.Fatalf("BTC/USD = %+v (get-tickers has no sizes → LiquidityUnknown)", q)
	}
	ps, err := c.Perps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 3 { // 3 tradable perps, all visited (FundingCallsPerPoll=3)
		t.Fatalf("perps = %d, want 3", len(ps))
	}
	p, ok := findPerp(ps, "BTC")
	if !ok || p.IntervalH != 1 || p.NextFundingAt.IsZero() || !p.Mark.IsPositive() || !p.Bid.IsPositive() || p.FundingRate.IsZero() {
		t.Fatalf("BTC perp = %+v", p)
	}
	// 3 contracts × (mark + funding_hist + estimated) round-robin calls.
	for _, route := range []string{
		"/public/get-valuations?valuation_type=mark_price",
		"/public/get-valuations?valuation_type=funding_hist",
		"/public/get-valuations?valuation_type=estimated_funding_rate",
	} {
		if fs.hits[route] != 3 {
			t.Fatalf("hits[%s] = %d, want 3 (%v)", route, fs.hits[route], fs.hits)
		}
	}
	nets, err := c.Networks(ctx)
	if err != nil || nets["BTC"].Reason != KeyGatedReason {
		t.Fatalf("networks BTC = %+v (%v)", nets["BTC"], err)
	}
}

func TestBitfinexFixtures(t *testing.T) {
	c, fs := fixtureCollector(t, screener.VenueBitfinex)
	ctx := context.Background()
	inst, err := c.Instruments(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var btcPerp, btcUst Instrument
	for _, in := range inst {
		if in.Kind == KindPerp && in.Symbol == "tBTCF0:USTF0" {
			btcPerp = in
		}
		if in.Kind == KindSpot && in.Symbol == "tBTCUST" {
			btcUst = in
		}
		if in.Kind == KindPerp && (in.Quote != "USDT" || in.IntervalH != 8) {
			t.Fatalf("bad bitfinex perp instrument: %+v", in)
		}
	}
	// BTCF0 → BTC via pub:map:currency:undl; UST → USDt → USDT via
	// pub:map:currency:sym + uppercase.
	if btcPerp.Base != "BTC" || btcUst.Base != "BTC" || btcUst.Quote != "USDT" {
		t.Fatalf("tBTCF0:USTF0 = %+v, tBTCUST = %+v", btcPerp, btcUst)
	}
	qs, err := c.Spot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	q, ok := findQuote(qs, "BTC", "USD")
	if !ok || !q.BidQty.IsPositive() || q.LiquidityUnknown || q.At.IsZero() {
		t.Fatalf("BTC/USD = %+v", q)
	}
	ps, err := c.Perps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := findPerp(ps, "BTC")
	if !ok || p.IntervalH != 8 || p.NextFundingAt.IsZero() || !p.Mark.IsPositive() || !p.Bid.IsPositive() {
		t.Fatalf("BTC perp = %+v", p)
	}
	// Perps() reuses the tickers response Spot() just fetched (30
	// reqs/min endpoint): exactly one bulk tickers call in this test.
	if fs.hits["/v2/tickers"] != 1 || fs.hits["/v2/status/deriv"] != 1 {
		t.Fatalf("hits = %v", fs.hits)
	}
	nets, err := c.Networks(ctx)
	if err != nil || nets["BTC"].Status != screener.NetworkOpen {
		t.Fatalf("networks BTC = %+v (%v)", nets["BTC"], err)
	}
	if _, ok := nets["USDT"]; !ok { // canonicalised key, not "UST"
		t.Fatalf("networks missing USDT: have %d entries", len(nets))
	}
}

func TestBingXFixtures(t *testing.T) {
	c, _ := fixtureCollector(t, screener.VenueBingX)
	ctx := context.Background()
	inst, err := c.Instruments(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var btcSpot, btcPerp Instrument
	for _, in := range inst {
		if in.Kind == KindSpot && in.Symbol == "BTC-USDT" {
			btcSpot = in
		}
		if in.Kind == KindPerp && in.Symbol == "BTC-USDT" {
			btcPerp = in
		}
		if in.Kind == KindPerp && in.Quote != "USDT" {
			t.Fatalf("non-USDT perp leaked: %+v", in)
		}
	}
	// Documented hyphen split (spot) and asset/currency fields (perp);
	// pricePrecision 1 → tick 0.1 on the BTC-USDT contract.
	if btcSpot.Base != "BTC" || btcSpot.Quote != "USDT" || btcPerp.Base != "BTC" || btcPerp.TickSize != "0.1" || btcPerp.MinNotional != "2" {
		t.Fatalf("BTC-USDT spot = %+v perp = %+v", btcSpot, btcPerp)
	}
	qs, err := c.Spot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if q, ok := findQuote(qs, "BTC", "USDT"); !ok || !q.BidQty.IsPositive() || q.At.IsZero() {
		t.Fatalf("BTC/USDT = %+v", q)
	}
	ps, err := c.Perps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := findPerp(ps, "BTC")
	if !ok || p.IntervalH != 8 || p.NextFundingAt.IsZero() || !p.Index.IsPositive() || !p.Bid.IsPositive() || !p.Mark.IsPositive() {
		t.Fatalf("BTC perp = %+v", p)
	}
	nets, err := c.Networks(ctx)
	if err != nil || nets["BTC"].Reason != KeyGatedReason {
		t.Fatalf("networks BTC = %+v (%v)", nets["BTC"], err)
	}
}

func TestWhiteBITFixtures(t *testing.T) {
	c, _ := fixtureCollector(t, screener.VenueWhiteBIT)
	ctx := context.Background()
	inst, err := c.Instruments(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var btcSpot, btcPerp Instrument
	for _, in := range inst {
		if in.Kind == KindSpot && in.Symbol == "BTC_USDT" {
			btcSpot = in
		}
		if in.Kind == KindPerp && in.Symbol == "BTC_PERP" {
			btcPerp = in
		}
		if in.Symbol == "XAU_PERP" || in.Symbol == "XAG_PERP" {
			t.Fatalf("tradfiFutures market leaked: %+v", in)
		}
	}
	if btcSpot.Base != "BTC" || btcSpot.Quote != "USDT" || btcPerp.Base != "BTC" || btcPerp.Quote != "USDT" {
		t.Fatalf("BTC_USDT = %+v, BTC_PERP = %+v", btcSpot, btcPerp)
	}
	qs, err := c.Spot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	q, ok := findQuote(qs, "BTC", "USDT")
	if !ok || !q.LiquidityUnknown || !q.BidQty.IsZero() || q.At.IsZero() {
		t.Fatalf("BTC/USDT = %+v (v1 tickers have no sizes → LiquidityUnknown)", q)
	}
	ps, err := c.Perps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := findPerp(ps, "BTC")
	// funding_interval_minutes 480 → 8 h; mark_price is undocumented →
	// Mark stays 0 with Index set (noBulkMark).
	if !ok || p.IntervalH != 8 || p.NextFundingAt.IsZero() || !p.Mark.IsZero() || !p.Index.IsPositive() || !p.Bid.IsPositive() {
		t.Fatalf("BTC perp = %+v", p)
	}
	nets, err := c.Networks(ctx)
	if err != nil || nets["BTC"].Status != screener.NetworkOpen {
		t.Fatalf("networks BTC = %+v (%v)", nets["BTC"], err)
	}
}

func TestBitMartFixtures(t *testing.T) {
	c, fs := fixtureCollector(t, screener.VenueBitMart) // FundingCallsPerPoll: 3 mark klines
	ctx := context.Background()
	inst, err := c.Instruments(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var btcSpot, btcPerp Instrument
	delisted := 0
	for _, in := range inst {
		if in.Kind == KindSpot && in.Symbol == "BTC_USDT" {
			btcSpot = in
		}
		if in.Kind == KindPerp && in.Symbol == "BTCUSDT" {
			btcPerp = in
		}
		if in.Kind == KindPerp && !in.Tradable {
			delisted++
		}
		if in.Kind == KindPerp && in.Quote != "USDT" {
			t.Fatalf("non-USDT perp leaked: %+v", in)
		}
	}
	if btcSpot.Base != "BTC" || btcSpot.Quote != "USDT" || btcPerp.Base != "BTC" || btcPerp.TickSize != "0.1" || btcPerp.CtVal != "0.001" || btcPerp.IntervalH != 8 {
		t.Fatalf("BTC_USDT = %+v, BTCUSDT = %+v", btcSpot, btcPerp)
	}
	if delisted == 0 {
		t.Fatal("fixture must include a Delisted (non-tradable) contract")
	}
	qs, err := c.Spot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if q, ok := findQuote(qs, "BTC", "USDT"); !ok || !q.BidQty.IsPositive() || q.At.IsZero() {
		t.Fatalf("BTC/USDT = %+v", q)
	}
	ps, err := c.Perps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if fs.hits["/contract/public/markprice-kline"] != 3 {
		t.Fatalf("mark kline calls = %d, want 3", fs.hits["/contract/public/markprice-kline"])
	}
	withMark := 0
	for _, p := range ps {
		if !p.Index.IsPositive() || p.IntervalH == 0 {
			t.Fatalf("bad bitmart perp: %+v", p)
		}
		if p.Mark.IsPositive() {
			withMark++
		}
	}
	if withMark != 3 {
		t.Fatalf("perps with a round-robin mark = %d, want 3", withMark)
	}
	if p, ok := findPerp(ps, "BTC"); !ok || p.NextFundingAt.IsZero() || p.FundingRate.IsZero() || p.PredictedFundingRate.IsZero() {
		t.Fatalf("BTC perp = %+v", p)
	}
	nets, err := c.Networks(ctx)
	if err != nil || nets["BTC"].Status != screener.NetworkOpen {
		t.Fatalf("networks BTC = %+v (%v)", nets["BTC"], err)
	}
}
