package pricing

import (
	"errors"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/exchange"
	"github.com/cploutarchou/arb-chain-bot/internal/fees"
	"github.com/cploutarchou/arb-chain-bot/internal/graph"
	"github.com/cploutarchou/arb-chain-bot/internal/orderbook"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func lv(price, qty string) orderbook.Level {
	return orderbook.Level{Price: d(price), Qty: d(qty)}
}

func stepRules(step string) exchange.InstrumentRules {
	return exchange.InstrumentRules{
		QtyMode: exchange.PrecisionStep, QtyStep: d(step),
		PriceMode: exchange.PrecisionStep, PriceTick: d("0.00000001"),
	}
}

func mdWith(bids, asks []orderbook.Level, step string) MarketData {
	return MarketData{
		View:  orderbook.View{Bids: bids, Asks: asks, Version: 7},
		Rules: stepRules(step),
	}
}

func mkLeg(sym string, base, quote, from, to exchange.Asset, side exchange.Side) graph.Leg {
	return graph.Leg{
		Market: exchange.MarketID{Exchange: "binance", Symbol: exchange.Symbol(sym)},
		Base:   base, Quote: quote, From: from, To: to, Side: side,
	}
}

func schedReceived(t *testing.T, taker string) *fees.Schedule {
	t.Helper()
	s, err := fees.NewSchedule("binance", exchange.FeeInReceived,
		fees.Rate{Maker: d(taker), Taker: d(taker)})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func eq(t *testing.T, name string, got, want decimal.Decimal) {
	t.Helper()
	if !got.Equal(want) {
		t.Fatalf("%s = %s, want %s", name, got, want)
	}
}

// Buy across two ask levels, fee in received asset (Binance convention).
// asks: 100x1, 101x2; budget 250 USDT; step 0.001; taker 10 bps.
// walk: 1 @ 100 = 100; partial 150/101 = 1.48514851...; raw 2.48514851...
// quantized 2.485; exact cost 100 + 101*1.485 = 249.985.
// gross 2.485 BTC; fee 0.002485 BTC; net 2.482515 BTC; dust 0.015 USDT.
func TestBuyLegTwoLevelsReceivedFee(t *testing.T) {
	leg := mkLeg("BTCUSDT", "BTC", "USDT", "USDT", "BTC", exchange.SideBuy)
	md := mdWith(nil, []orderbook.Level{lv("100", "1"), lv("101", "2")}, "0.001")
	q, err := QuoteLeg(leg, md, schedReceived(t, "0.001"), d("250"))
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "OrderQty", q.OrderQty, d("2.485"))
	eq(t, "InputConsumed", q.InputConsumed, d("249.985"))
	eq(t, "GrossOut", q.GrossOut, d("2.485"))
	eq(t, "NetOut", q.NetOut, d("2.482515"))
	eq(t, "FeeAmount", q.FeeAmount, d("0.002485"))
	if q.FeeAsset != "BTC" {
		t.Fatalf("FeeAsset = %s", q.FeeAsset)
	}
	eq(t, "Dust", q.Dust, d("0.015"))
	if q.LevelsConsumed != 2 || q.DepthExhausted {
		t.Fatalf("levels=%d exhausted=%v", q.LevelsConsumed, q.DepthExhausted)
	}
	// vwap * qty must equal cost exactly-ish (division precision bound).
	if q.AvgPrice.Mul(q.OrderQty).Sub(q.InputConsumed).Abs().GreaterThan(d("1e-20")) {
		t.Fatalf("vwap %s inconsistent with cost", q.AvgPrice)
	}
	// ~59.76 bps impact vs best 100.
	if q.PriceImpactBps.LessThan(d("59")) || q.PriceImpactBps.GreaterThan(d("60")) {
		t.Fatalf("impact = %s bps", q.PriceImpactBps)
	}
	if q.BookVersion != 7 {
		t.Fatalf("book version = %d", q.BookVersion)
	}
}

// Sell into two bid levels, fee in received (quote) asset.
// bids: 100x1, 99x2; sell 2.5 base; step 0.01; taker 10 bps.
// proceeds: 100 + 1.5*99 = 248.5; fee 0.2485 USDT; net 248.2515.
func TestSellLegReceivedFee(t *testing.T) {
	leg := mkLeg("BTCUSDT", "BTC", "USDT", "BTC", "USDT", exchange.SideSell)
	md := mdWith([]orderbook.Level{lv("100", "1"), lv("99", "2")}, nil, "0.01")
	q, err := QuoteLeg(leg, md, schedReceived(t, "0.001"), d("2.5"))
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "OrderQty", q.OrderQty, d("2.5"))
	eq(t, "GrossOut", q.GrossOut, d("248.5"))
	eq(t, "NetOut", q.NetOut, d("248.2515"))
	eq(t, "FeeAmount", q.FeeAmount, d("0.2485"))
	if q.FeeAsset != "USDT" {
		t.Fatalf("FeeAsset = %s", q.FeeAsset)
	}
	eq(t, "InputConsumed", q.InputConsumed, d("2.5"))
	eq(t, "Dust", q.Dust, d("0"))
}

// Kraken-style SPENT convention on a sell: fee charged in the BASE input.
// input 2.5; rate 10 bps; usable 2.5/1.001 = 2.49750...; quantized 2.497.
// proceeds: 1 @ 100 + 1.497 @ 99 = 248.203; fee 0.002497 base;
// consumed 2.497 + 0.002497 = 2.499497; dust 0.000503.
func TestSellLegSpentFeeInput(t *testing.T) {
	s, err := fees.NewSchedule("kraken", exchange.FeeInSpent,
		fees.Rate{Maker: d("0.001"), Taker: d("0.001")})
	if err != nil {
		t.Fatal(err)
	}
	leg := mkLeg("XBTUSD", "BTC", "USD", "BTC", "USD", exchange.SideSell)
	md := mdWith([]orderbook.Level{lv("100", "1"), lv("99", "2")}, nil, "0.001")
	q, err := QuoteLeg(leg, md, s, d("2.5"))
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "OrderQty", q.OrderQty, d("2.497"))
	eq(t, "GrossOut", q.GrossOut, d("248.203"))
	eq(t, "NetOut", q.NetOut, d("248.203")) // no output-side fee
	eq(t, "FeeAmount", q.FeeAmount, d("0.002497"))
	if q.FeeAsset != "BTC" {
		t.Fatalf("FeeAsset = %s", q.FeeAsset)
	}
	eq(t, "InputConsumed", q.InputConsumed, d("2.499497"))
	eq(t, "Dust", q.Dust, d("0.000503"))
}

// Coinbase-style QUOTE convention on a buy: fee charged in the QUOTE input.
// budget 250; rate 10 bps; usable 249.7502497...; walk gives raw
// 1 + 149.7502497.../101 = 2.48267...; quantized 2.482;
// cost 100 + 101*1.482 = 249.682; fee 0.249682; consumed 249.931682.
func TestBuyLegQuoteFeeInput(t *testing.T) {
	s, err := fees.NewSchedule("coinbase", exchange.FeeInQuote,
		fees.Rate{Maker: d("0.001"), Taker: d("0.001")})
	if err != nil {
		t.Fatal(err)
	}
	leg := mkLeg("BTC-USD", "BTC", "USD", "USD", "BTC", exchange.SideBuy)
	md := mdWith(nil, []orderbook.Level{lv("100", "1"), lv("101", "2")}, "0.001")
	q, err := QuoteLeg(leg, md, s, d("250"))
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "OrderQty", q.OrderQty, d("2.482"))
	eq(t, "NetOut", q.NetOut, d("2.482"))
	eq(t, "FeeAmount", q.FeeAmount, d("0.249682"))
	if q.FeeAsset != "USD" {
		t.Fatalf("FeeAsset = %s", q.FeeAsset)
	}
	eq(t, "InputConsumed", q.InputConsumed, d("249.931682"))
	eq(t, "Dust", q.Dust, d("0.068318"))
}

func triUSDT() graph.Triangle {
	return graph.Triangle{
		ID: "binance|USDT|BTCUSDT>ETHBTC>ETHUSDT", Exchange: "binance", Start: "USDT",
		Legs: [3]graph.Leg{
			mkLeg("BTCUSDT", "BTC", "USDT", "USDT", "BTC", exchange.SideBuy),
			mkLeg("ETHBTC", "ETH", "BTC", "BTC", "ETH", exchange.SideBuy),
			mkLeg("ETHUSDT", "ETH", "USDT", "ETH", "USDT", exchange.SideSell),
		},
	}
}

func cycleData(ethusdtBid string) [3]MarketData {
	return [3]MarketData{
		mdWith(nil, []orderbook.Level{lv("100", "10"), lv("101", "5")}, "0.001"),
		mdWith(nil, []orderbook.Level{lv("0.1", "100"), lv("0.11", "100")}, "0.001"),
		mdWith([]orderbook.Level{lv(ethusdtBid, "1000")}, nil, "0.001"),
	}
}

// Full three-leg cycle, every intermediate value hand-computed:
// 1000 USDT → 10 BTC (cost 1000, fee 0.01 BTC, net 9.99)
// 9.99 BTC → 99.9 ETH (cost 9.99, fee 0.0999 ETH, net 99.8001)
// 99.8001 ETH → quantized 99.8 (dust 0.0001 ETH) → 99.8*10.2 = 1017.96
// → net 1016.94204 USDT. Profit 16.94204 → 169.4204 bps.
func TestFullCycleHandComputed(t *testing.T) {
	cq, err := QuoteCycle(triUSDT(), cycleData("10.2"), schedReceived(t, "0.001"), d("1000"))
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "leg1.NetOut", cq.Legs[0].NetOut, d("9.99"))
	eq(t, "leg2.NetOut", cq.Legs[1].NetOut, d("99.8001"))
	eq(t, "leg3.OrderQty", cq.Legs[2].OrderQty, d("99.8"))
	eq(t, "leg3.Dust", cq.Legs[2].Dust, d("0.0001"))
	eq(t, "InputConsumed", cq.InputConsumed, d("1000"))
	eq(t, "FinalAmount", cq.FinalAmount, d("1016.94204"))
	eq(t, "GrossProfit", cq.GrossProfit, d("16.94204"))
	eq(t, "ReturnBps", cq.ReturnBps, d("169.4204"))
	if cq.LiquidityLimited {
		t.Fatal("not liquidity limited")
	}
}

// Same books, 100 bps fees: profitable before fees, loss after.
// 1000 → 9.9 BTC → 99 ETH gross, net 98.01 → sell 98.01*10.2 = 999.702
// → net 989.70498 < 1000.
func TestProfitableBeforeFeesUnprofitableAfter(t *testing.T) {
	cq, err := QuoteCycle(triUSDT(), cycleData("10.2"), schedReceived(t, "0.01"), d("1000"))
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "FinalAmount", cq.FinalAmount, d("989.70498"))
	if !cq.GrossProfit.IsNegative() {
		t.Fatalf("profit = %s, want negative", cq.GrossProfit)
	}
}

// Top-of-book looks profitable but depth kills it: tiny best ask level,
// then a 20% worse level the size must eat through.
func TestProfitableTopOfBookUnprofitableAfterDepth(t *testing.T) {
	data := cycleData("10.2")
	data[0] = mdWith(nil, []orderbook.Level{lv("100", "0.05"), lv("120", "100")}, "0.001")
	cq, err := QuoteCycle(triUSDT(), data, schedReceived(t, "0.001"), d("1000"))
	if err != nil {
		t.Fatal(err)
	}
	if !cq.GrossProfit.IsNegative() {
		t.Fatalf("profit = %s, want negative (depth impact)", cq.GrossProfit)
	}
	if cq.Legs[0].PriceImpactBps.LessThan(d("1000")) {
		t.Fatalf("leg1 impact = %s bps, expected >1000", cq.Legs[0].PriceImpactBps)
	}
}

func TestZeroDepthFails(t *testing.T) {
	data := cycleData("10.2")
	data[1] = mdWith(nil, nil, "0.001")
	_, err := QuoteCycle(triUSDT(), data, schedReceived(t, "0.001"), d("1000"))
	if !errors.Is(err, ErrNoDepth) {
		t.Fatalf("err = %v", err)
	}
}

func TestDustInputFails(t *testing.T) {
	leg := mkLeg("BTCUSDT", "BTC", "USDT", "USDT", "BTC", exchange.SideBuy)
	md := mdWith(nil, []orderbook.Level{lv("100", "1")}, "0.001")
	// 0.05 USDT buys 0.0005 BTC → quantizes to zero.
	_, err := QuoteLeg(leg, md, schedReceived(t, "0.001"), d("0.05"))
	if !errors.Is(err, ErrDustInput) {
		t.Fatalf("err = %v", err)
	}
}

func TestMinNotionalViolationSurfaces(t *testing.T) {
	leg := mkLeg("BTCUSDT", "BTC", "USDT", "USDT", "BTC", exchange.SideBuy)
	md := mdWith(nil, []orderbook.Level{lv("100", "1")}, "0.001")
	md.Rules.MinNotional = d("10")
	_, err := QuoteLeg(leg, md, schedReceived(t, "0.001"), d("5"))
	if !errors.Is(err, ErrRuleViolation) {
		t.Fatalf("err = %v", err)
	}
}

func TestInsufficientDepthSetsLiquidityLimited(t *testing.T) {
	// Leg-1 asks absorb only 1500 USDT; ask for 5000.
	data := cycleData("10.2")
	cq, err := QuoteCycle(triUSDT(), data, schedReceived(t, "0.001"), d("5000"))
	if err != nil {
		t.Fatal(err)
	}
	if !cq.LiquidityLimited {
		t.Fatal("want LiquidityLimited")
	}
	// Deployed at most the visible capacity.
	if cq.InputConsumed.GreaterThan(d("1505")) {
		t.Fatalf("InputConsumed = %s", cq.InputConsumed)
	}
}

// Conservation invariants that must hold for every RECEIVED-convention leg:
// GrossOut = NetOut + Fee; input = InputConsumed + Dust (buy legs).
func TestLegConservation(t *testing.T) {
	cq, err := QuoteCycle(triUSDT(), cycleData("10.2"), schedReceived(t, "0.001"), d("777.777"))
	if err != nil {
		t.Fatal(err)
	}
	in := d("777.777")
	for i, l := range cq.Legs {
		if !l.NetOut.Add(l.FeeAmount).Equal(l.GrossOut) {
			t.Fatalf("leg %d: net %s + fee %s != gross %s", i+1, l.NetOut, l.FeeAmount, l.GrossOut)
		}
		if !l.InputConsumed.Add(l.Dust).Equal(in) {
			t.Fatalf("leg %d: consumed %s + dust %s != input %s", i+1, l.InputConsumed, l.Dust, in)
		}
		in = l.NetOut
	}
}

// The sizer must find the size where marginal depth stops being
// profitable: leg-3 has a rich 10.5 level for 50 ETH, then a losing 9.5
// tail. Optimal deploys ≈ the first level's capacity, beating both a tiny
// and a max-size deployment; a brute-force fine grid must not beat the
// sizer by more than a whisker.
func TestOptimalSizeSearch(t *testing.T) {
	data := [3]MarketData{
		mdWith(nil, []orderbook.Level{lv("100", "100")}, "0.001"),
		mdWith(nil, []orderbook.Level{lv("0.1", "10000")}, "0.001"),
		mdWith([]orderbook.Level{lv("10.5", "50"), lv("9.5", "10000")}, nil, "0.001"),
	}
	sched := schedReceived(t, "0.001")
	tri := triUSDT()
	quote := func(in decimal.Decimal) (CycleQuote, error) {
		return QuoteCycle(tri, data, sched, in)
	}

	maxIn := CapacityHint(tri.Legs[0], data[0], sched)
	if !maxIn.Equal(d("10000")) {
		t.Fatalf("capacity hint = %s", maxIn)
	}
	res, ok := DefaultSizeSearch.Find(quote, d("1"), maxIn)
	if !ok {
		t.Fatal("no viable size")
	}
	if !res.Best.GrossProfit.IsPositive() {
		t.Fatalf("best profit = %s", res.Best.GrossProfit)
	}
	// ~50 ETH * ~0.1*100 → optimal leg-1 deployment near 505 USDT.
	if res.Best.InputConsumed.LessThan(d("400")) || res.Best.InputConsumed.GreaterThan(d("650")) {
		t.Fatalf("optimal input = %s, expected near first-level capacity", res.Best.InputConsumed)
	}
	// Endpoints must not beat the optimum.
	for _, size := range []string{"1", "10000"} {
		cq, err := quote(d(size))
		if err == nil && cq.GrossProfit.GreaterThan(res.Best.GrossProfit) {
			t.Fatalf("size %s beats sizer: %s > %s", size, cq.GrossProfit, res.Best.GrossProfit)
		}
	}
	// Brute-force cross-check on a fine grid.
	bestBrute := decimal.Zero
	for i := 1; i <= 200; i++ {
		cq, err := quote(d("50").Mul(decimal.NewFromInt(int64(i))))
		if err == nil && cq.GrossProfit.GreaterThan(bestBrute) {
			bestBrute = cq.GrossProfit
		}
	}
	if bestBrute.Sub(res.Best.GrossProfit).GreaterThan(bestBrute.Mul(d("0.02"))) {
		t.Fatalf("sizer %s vs brute %s: >2%% behind", res.Best.GrossProfit, bestBrute)
	}
	if res.Evaluations > 60 {
		t.Fatalf("evaluations = %d, budget 60", res.Evaluations)
	}
}

func BenchmarkQuoteCycle50Levels(b *testing.B) {
	mkLevels := func(start string, step string, n int, qty string) []orderbook.Level {
		out := make([]orderbook.Level, n)
		p := d(start)
		for i := 0; i < n; i++ {
			out[i] = orderbook.Level{Price: p, Qty: d(qty)}
			p = p.Add(d(step))
		}
		return out
	}
	data := [3]MarketData{
		mdWith(nil, mkLevels("100", "0.01", 50, "2"), "0.001"),
		mdWith(nil, mkLevels("0.1", "0.00001", 50, "50"), "0.001"),
		mdWith(mkLevels("10.2", "-0.001", 50, "100"), nil, "0.001"),
	}
	sched, _ := fees.NewSchedule("binance", exchange.FeeInReceived,
		fees.Rate{Maker: d("0.001"), Taker: d("0.001")})
	tri := triUSDT()
	in := d("5000")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := QuoteCycle(tri, data, sched, in); err != nil {
			b.Fatal(err)
		}
	}
}

// Optimal-size search (§73) without the books in hand: geometric coarse
// pass plus golden-section refinement, each point one full QuoteCycle
// over 50-level books. BenchmarkSizeSearchCycle50Levels is the same
// search with the books, and BenchmarkLegacyGridTernary50Levels is what
// both replaced.
func BenchmarkSizeSearch50Levels(b *testing.B) {
	mkLevels := func(start string, step string, n int, qty string) []orderbook.Level {
		out := make([]orderbook.Level, n)
		p := d(start)
		for i := 0; i < n; i++ {
			out[i] = orderbook.Level{Price: p, Qty: d(qty)}
			p = p.Add(d(step))
		}
		return out
	}
	data := [3]MarketData{
		mdWith(nil, mkLevels("100", "0.01", 50, "2"), "0.001"),
		mdWith(nil, mkLevels("0.1", "0.00001", 50, "50"), "0.001"),
		mdWith(mkLevels("10.2", "-0.001", 50, "100"), nil, "0.001"),
	}
	sched, _ := fees.NewSchedule("binance", exchange.FeeInReceived,
		fees.Rate{Maker: d("0.001"), Taker: d("0.001")})
	tri := triUSDT()
	quote := func(in decimal.Decimal) (CycleQuote, error) {
		return QuoteCycle(tri, data, sched, in)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, ok := DefaultSizeSearch.Find(quote, d("10"), d("10000")); !ok {
			b.Fatal("no viable size")
		}
	}
}

// A sell into thinner depth than the submitted quantity is a partial
// fill — validated as submitted, dust stranded — not a rule violation
// (audit T6); a submitted quantity below the minimum still is one.
func TestSellLegValidatesSubmittedQuantity(t *testing.T) {
	leg := mkLeg("ETHUSDT", "ETH", "USDT", "ETH", "USDT", exchange.SideSell)
	md := mdWith([]orderbook.Level{lv("2000", "0.5")}, nil, "0.001")
	md.Rules.MinQty = d("1")
	sched := schedReceived(t, "0.001")

	q, err := QuoteLeg(leg, md, sched, d("10"))
	if err != nil {
		t.Fatalf("depth-limited sell rejected: %v", err)
	}
	if !q.OrderQty.Equal(d("0.5")) || !q.DepthExhausted || !q.Dust.Equal(d("9.5")) {
		t.Fatalf("partial = qty %s exhausted %v dust %s", q.OrderQty, q.DepthExhausted, q.Dust)
	}
	if _, err := QuoteLeg(leg, md, sched, d("0.7")); err == nil {
		t.Fatal("submitted quantity below the minimum accepted")
	}
}

// TestExactCostNotionalAndQuantizedExhaustion (audit T12): the notional
// floor is checked against the walk's EXACT cost — a VWAP×qty
// reconstruction rounds and can pass a figure the exact cost fails —
// and DepthExhausted reflects the QUANTIZED order's walk: an exact fit
// on the final level exhausts, a truncation that lands inside the book
// does not inherit the budget walk's verdict.
func TestExactCostNotionalAndQuantizedExhaustion(t *testing.T) {
	md := mdWith(nil, []orderbook.Level{lv("3", "1"), lv("3.0000000001", "1")}, "1")
	md.Rules.MinNotional = d("4")
	// Budget 4 buys 1.333.. → quantized 1 → exact cost 3 < 4 → refused
	// on the EXACT figure (VWAP×qty = 3 here too, but see below).
	if _, err := QuoteLeg(mkLeg("XUSDT", "X", "USDT", "USDT", "X", exchange.SideBuy), md, schedReceived(t, "1"), d("4")); err == nil {
		t.Fatal("cost below min-notional accepted")
	}

	// A quantized order landing inside the book is not depth-limited
	// even when the budget walk exhausted the side: budget 25 drains
	// both levels (raw 7, walk off the end), truncation to step 2 buys
	// 6 — strictly inside the second level.
	md2 := mdWith(nil, []orderbook.Level{lv("3", "2"), lv("3.1", "5")}, "2")
	lq, err := QuoteLeg(mkLeg("XUSDT", "X", "USDT", "USDT", "X", exchange.SideBuy), md2, schedReceived(t, "1"), d("25"))
	if err != nil {
		t.Fatal(err)
	}
	if lq.DepthExhausted {
		t.Fatal("quantized order inside the book reported exhausted")
	}

	// An exact fit on the FINAL level exhausts: the order consumed
	// everything visible.
	md3 := mdWith(nil, []orderbook.Level{lv("3", "1"), lv("4", "1")}, "1")
	lq, err = QuoteLeg(mkLeg("XUSDT", "X", "USDT", "USDT", "X", exchange.SideBuy), md3, schedReceived(t, "1"), d("10"))
	if err != nil {
		t.Fatal(err)
	}
	if !lq.DepthExhausted || lq.OrderQty.Cmp(d("2")) != 0 {
		t.Fatalf("exact final-level fit: qty=%s exhausted=%v", lq.OrderQty, lq.DepthExhausted)
	}
}
