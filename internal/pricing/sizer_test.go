package pricing

import (
	"math/rand"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/exchange"
	"github.com/cploutarchou/arb-chain-bot/internal/fees"
	"github.com/cploutarchou/arb-chain-bot/internal/graph"
	"github.com/cploutarchou/arb-chain-bot/internal/orderbook"
)

// legacyGridTernary is the size search as it shipped before the
// breakpoint pass: a 13-point uniform grid over [minIn,maxIn] plus 14
// ternary iterations between the best point's neighbours. It is kept here
// as the regression witness for audit T1 (roadmap P1-9) — the tests below
// assert that it misses a profitable size the new search finds.
func legacyGridTernary(quote func(decimal.Decimal) (CycleQuote, error), minIn, maxIn decimal.Decimal, gridPoints, refineIters int) (CycleQuote, bool, int) {
	type point struct {
		size, profit decimal.Decimal
		quote        CycleQuote
		valid        bool
	}
	evals := 0
	eval := func(size decimal.Decimal) point {
		evals++
		cq, err := quote(size)
		if err != nil {
			return point{size: size}
		}
		return point{size: size, profit: cq.GrossProfit, quote: cq, valid: true}
	}
	span := maxIn.Sub(minIn)
	stepDiv := decimal.NewFromInt(int64(gridPoints - 1))
	grid := make([]point, gridPoints)
	bestIdx := -1
	for i := range grid {
		grid[i] = eval(minIn.Add(span.Mul(decimal.NewFromInt(int64(i))).Div(stepDiv)))
		if grid[i].valid && (bestIdx < 0 || grid[i].profit.GreaterThan(grid[bestIdx].profit)) {
			bestIdx = i
		}
	}
	if bestIdx < 0 {
		return CycleQuote{}, false, evals
	}
	best := grid[bestIdx]
	lo, hi := minIn, maxIn
	if bestIdx > 0 {
		lo = grid[bestIdx-1].size
	}
	if bestIdx < len(grid)-1 {
		hi = grid[bestIdx+1].size
	}
	three := decimal.NewFromInt(3)
	for i := 0; i < refineIters && hi.GreaterThan(lo); i++ {
		third := hi.Sub(lo).Div(three)
		m1, m2 := eval(lo.Add(third)), eval(hi.Sub(third))
		for _, p := range []point{m1, m2} {
			if p.valid && p.profit.GreaterThan(best.profit) {
				best = p
			}
		}
		switch {
		case !m1.valid && !m2.valid:
			lo, hi = m1.size, m2.size
		case !m1.valid:
			lo = m1.size
		case !m2.valid:
			hi = m2.size
		case m1.profit.LessThan(m2.profit):
			lo = m1.size
		default:
			hi = m2.size
		}
	}
	return best.quote, true, evals
}

// bruteForce prices n evenly spaced sizes and returns the best profit —
// the reference every search is measured against.
func bruteForce(t *testing.T, quote func(decimal.Decimal) (CycleQuote, error), minIn, maxIn decimal.Decimal, n int) (decimal.Decimal, decimal.Decimal) {
	t.Helper()
	span := maxIn.Sub(minIn)
	div := decimal.NewFromInt(int64(n))
	bestProfit, bestSize := decimal.Zero, decimal.Zero
	found := false
	for i := 0; i <= n; i++ {
		size := minIn.Add(span.Mul(decimal.NewFromInt(int64(i))).Div(div))
		cq, err := quote(size)
		if err != nil {
			continue
		}
		if !found || cq.GrossProfit.GreaterThan(bestProfit) {
			bestProfit, bestSize, found = cq.GrossProfit, size, true
		}
	}
	if !found {
		t.Fatal("brute force found no quotable size")
	}
	return bestProfit, bestSize
}

// pocketBooks is the audit T1 reproduction, scaled to a 3000:1 range.
//
// Leg 1 has 1 000 USDT of cheap depth (50 000 x 0.02) and then a level
// 20% worse, so the cycle is profitable only while the trade stays inside
// the first level: raw cross 2510 / (50 000 x 0.05) = 1.0040 (+40 bps),
// which survives three 10 bps fees as +9.9 bps, while every USDT bought
// at 60 000 returns ~0.834. min_notional 1 000 on leg 1 makes every size
// below the breakpoint unquotable, so the whole profitable window is
// [1000, ~1006] — 0.004% of the search range.
func pocketBooks() (graph.Triangle, [3]MarketData) {
	data := [3]MarketData{
		mdWith(nil, []orderbook.Level{lv("50000", "0.02"), lv("60000", "1000")}, "0.00001"),
		mdWith(nil, []orderbook.Level{lv("0.05", "10000")}, "0.0001"),
		mdWith([]orderbook.Level{lv("2510", "100000")}, nil, "0.0001"),
	}
	data[0].Rules.MinNotional = d("1000")
	return triUSDT(), data
}

// The profitable size sits strictly between two points of the old uniform
// grid at a 3000:1 range (min 50, max 150 000) and is narrower than the
// old refinement's resolution. The breakpoint search prices it exactly;
// the old grid plus ternary pass does not.
func TestSizeSearchFindsPocketBetweenUniformGridPoints(t *testing.T) {
	tri, data := pocketBooks()
	sched := schedReceived(t, "0.001")
	quote := func(in decimal.Decimal) (CycleQuote, error) { return QuoteCycle(tri, data, sched, in) }
	minIn, maxIn := d("50"), d("150000")

	// The range really is 3000:1 and the optimum really is between the
	// first two uniform grid points.
	if !maxIn.Div(minIn).Equal(d("3000")) {
		t.Fatalf("range ratio = %s, want 3000", maxIn.Div(minIn))
	}
	gridStep := maxIn.Sub(minIn).Div(decimal.NewFromInt(12))
	optimum := d("1000") // leg-1 level boundary: 50 000 x 0.02
	if !optimum.GreaterThan(minIn) || !optimum.LessThan(minIn.Add(gridStep)) {
		t.Fatalf("optimum %s is not strictly inside the first grid cell [%s, %s]",
			optimum, minIn, minIn.Add(gridStep))
	}

	// Hand-computed optimum: 1 000 USDT buys 0.02 BTC (cost 1 000, fee
	// 0.00002 BTC, net 0.01998); 0.01998 BTC buys 0.3996 ETH, fee
	// 0.0003996, net 0.3992004; truncation to 0.0001 leaves 0.3992 ETH,
	// sold at 2 510 for 1 001.992, fee 1.001992, net 1 000.990008.
	want := quoteAt(t, quote, optimum)
	eq(t, "hand-computed final", want.FinalAmount, d("1000.990008"))
	eq(t, "hand-computed profit", want.GrossProfit, d("0.990008"))

	res, ok := DefaultSizeSearch.FindCycle(tri, data, sched, minIn, maxIn)
	if !ok {
		t.Fatal("breakpoint search found no viable size")
	}
	if !res.Best.GrossProfit.Equal(want.GrossProfit) {
		t.Fatalf("breakpoint search profit = %s at size %s, want %s at 1000",
			res.Best.GrossProfit, res.Best.InputConsumed, want.GrossProfit)
	}
	if budget := DefaultSizeSearch.MaxCandidates + DefaultSizeSearch.RefineIters + 2; res.Evaluations > budget {
		t.Fatalf("evaluations = %d, budget %d", res.Evaluations, budget)
	}

	// The pre-fix search: 13 uniform points and 14 ternary iterations
	// cannot see a 6-USDT-wide window in a 149 950-wide range.
	legacy, legacyOK, legacyEvals := legacyGridTernary(quote, minIn, maxIn, 13, 14)
	if legacyOK && legacy.GrossProfit.IsPositive() {
		t.Fatalf("the uniform grid was expected to miss the pocket, got profit %s at %s (%d evaluations)",
			legacy.GrossProfit, legacy.InputConsumed, legacyEvals)
	}
	t.Logf("breakpoint search: profit %s at %s in %d evaluations (%d candidates); "+
		"uniform grid: ok=%v profit %s in %d evaluations",
		res.Best.GrossProfit, res.Best.InputConsumed, res.Evaluations, res.Candidates,
		legacyOK, legacy.GrossProfit, legacyEvals)

	// The closure-only path has no books, but its geometric coarse pass
	// must still land in the window instead of stepping over it.
	closure, ok := DefaultSizeSearch.Find(quote, minIn, maxIn)
	if !ok || !closure.Best.GrossProfit.IsPositive() {
		t.Fatalf("closure-only search: ok=%v profit=%s", ok, closure.Best.GrossProfit)
	}
}

func quoteAt(t *testing.T, quote func(decimal.Decimal) (CycleQuote, error), size decimal.Decimal) CycleQuote {
	t.Helper()
	cq, err := quote(size)
	if err != nil {
		t.Fatalf("quote at %s: %v", size, err)
	}
	return cq
}

// flatBooks builds three single-level books deep enough that nothing is
// exhausted: profit is then strictly proportional to size and the search
// must return a range end.
func flatBooks(ask1, qty1, ask2, qty2, bid3, qty3 string) [3]MarketData {
	return [3]MarketData{
		mdWith(nil, []orderbook.Level{lv(ask1, qty1)}, "0.00001"),
		mdWith(nil, []orderbook.Level{lv(ask2, qty2)}, "0.0001"),
		mdWith([]orderbook.Level{lv(bid3, qty3)}, nil, "0.0001"),
	}
}

// Monotone objectives: with one level per leg the profit per unit of size
// is constant, so the optimum is a range end — the top when the cycle
// earns, the floor when it loses. Both directions of the same books are
// covered by flipping the leg-3 bid across the fee wall.
func TestSizeSearchMonotoneObjectives(t *testing.T) {
	tri := triUSDT()
	sched := schedReceived(t, "0.001")
	minIn, maxIn := d("10"), d("30000")

	cases := []struct {
		name     string
		bid3     string
		wantSize decimal.Decimal
		wantSign int
	}{
		// 2510/(50000*0.05) = 1.0040 raw, +9.9 bps after fees: deploy all.
		{name: "increasing", bid3: "2510", wantSize: maxIn, wantSign: 1},
		// 2495/(50000*0.05) = 0.9980 raw: every USDT loses, deploy the least.
		{name: "decreasing", bid3: "2495", wantSize: minIn, wantSign: -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := flatBooks("50000", "100", "0.05", "10000", tc.bid3, "100000")
			res, ok := DefaultSizeSearch.FindCycle(tri, data, sched, minIn, maxIn)
			if !ok {
				t.Fatal("no viable size")
			}
			if !res.Best.InputConsumed.Equal(tc.wantSize) {
				t.Fatalf("size = %s, want %s", res.Best.InputConsumed, tc.wantSize)
			}
			if res.Best.GrossProfit.Sign() != tc.wantSign {
				t.Fatalf("profit = %s, want sign %d", res.Best.GrossProfit, tc.wantSign)
			}
			quote := func(in decimal.Decimal) (CycleQuote, error) { return QuoteCycle(tri, data, sched, in) }
			brute, bruteSize := bruteForce(t, quote, minIn, maxIn, 400)
			if res.Best.GrossProfit.LessThan(brute) {
				t.Fatalf("search %s < brute force %s (at %s)", res.Best.GrossProfit, brute, bruteSize)
			}
		})
	}
}

// Depth exhaustion: the search must stop at the size the books can
// absorb and never chase a bigger number.
func TestSizeSearchExhaustionEdges(t *testing.T) {
	tri := triUSDT()
	sched := schedReceived(t, "0.001")

	t.Run("leg1 capacity caps the range", func(t *testing.T) {
		// Leg 1 holds 1 000 USDT of depth; the caller allows 100 000.
		data := flatBooks("50000", "0.02", "0.05", "10000", "2510", "100000")
		res, ok := DefaultSizeSearch.FindCycle(tri, data, sched, d("10"), d("100000"))
		if !ok {
			t.Fatal("no viable size")
		}
		eq(t, "InputConsumed", res.Best.InputConsumed, d("1000"))
		if !res.Best.LiquidityLimited {
			t.Fatal("want LiquidityLimited at the capacity edge")
		}
		if !res.Best.GrossProfit.Equal(d("0.990008")) {
			t.Fatalf("profit = %s", res.Best.GrossProfit)
		}
	})

	t.Run("leg3 depth caps the size", func(t *testing.T) {
		// Leg 3 absorbs 50 ETH at 10.5 and nothing more; ETH beyond that
		// is stranded and valued at zero, so the optimum is exactly the
		// size that fills the level: 50/0.999 ETH needs 2.502502502…
		// BTC net, i.e. 501.001502… USDT before leg-1 fees.
		data := [3]MarketData{
			mdWith(nil, []orderbook.Level{lv("100", "100")}, "0.001"),
			mdWith(nil, []orderbook.Level{lv("0.1", "10000")}, "0.001"),
			mdWith([]orderbook.Level{lv("10.5", "50")}, nil, "0.001"),
		}
		quote := func(in decimal.Decimal) (CycleQuote, error) { return QuoteCycle(tri, data, sched, in) }
		res, ok := DefaultSizeSearch.FindCycle(tri, data, sched, d("1"), d("10000"))
		if !ok {
			t.Fatal("no viable size")
		}
		if res.Best.InputConsumed.LessThan(d("500")) || res.Best.InputConsumed.GreaterThan(d("502")) {
			t.Fatalf("size = %s, want the leg-3 fill boundary near 501", res.Best.InputConsumed)
		}
		if res.Best.Legs[2].Dust.GreaterThan(d("0.001")) {
			t.Fatalf("leg-3 dust = %s ETH: the sizer stranded inventory", res.Best.Legs[2].Dust)
		}
		brute, bruteSize := bruteForce(t, quote, d("1"), d("10000"), 2000)
		if res.Best.GrossProfit.LessThan(brute) {
			t.Fatalf("search %s < brute force %s (at %s)", res.Best.GrossProfit, brute, bruteSize)
		}
	})

	t.Run("no depth at all", func(t *testing.T) {
		data := flatBooks("50000", "100", "0.05", "10000", "2510", "100000")
		data[1] = mdWith(nil, nil, "0.0001")
		if res, ok := DefaultSizeSearch.FindCycle(tri, data, sched, d("10"), d("1000")); ok {
			t.Fatalf("want no viable size, got %s", res.Best.InputConsumed)
		}
	})

	t.Run("floor above the depth ceiling", func(t *testing.T) {
		// 1 000 USDT of leg-1 depth, but the caller will not trade below
		// 5 000 and leg 1 rejects anything under 4 000 notional.
		data := flatBooks("50000", "0.02", "0.05", "10000", "2510", "100000")
		data[0].Rules.MinNotional = d("4000")
		if res, ok := DefaultSizeSearch.FindCycle(tri, data, sched, d("5000"), d("100000")); ok {
			t.Fatalf("want no viable size, got %s", res.Best.InputConsumed)
		}
	})
}

// The returned size must never be worse than any depth breakpoint: the
// breakpoints are where the marginal economics change, so if the search
// skips one it must be because it priced something better. Books are
// pseudo-random but deterministic, and both leg orientations are covered
// (the reverse triangle sells on legs 2 and 3).
func TestSizeSearchNeverWorseThanAnyBreakpoint(t *testing.T) {
	sched := schedReceived(t, "0.001")
	spent, err := fees.NewSchedule("kraken", exchange.FeeInSpent, fees.Rate{Maker: d("0.0008"), Taker: d("0.0008")})
	if err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewSource(20260909)) //nolint:gosec // deterministic test fixture
	worst, worstAt := decimal.Zero, decimal.Zero

	for _, sch := range []*fees.Schedule{sched, spent} {
		for _, tri := range []graph.Triangle{triUSDT(), triReverseUSDT()} {
			for i := 0; i < 24; i++ {
				data := randomBooks(rng, tri)
				minIn, maxIn := d("25"), d("25000")
				res, ok := DefaultSizeSearch.FindCycle(tri, data, sch, minIn, maxIn)
				ladder, built := newCycleLadder(tri, data, sch)
				if !built {
					t.Fatalf("case %d: ladder not built", i)
				}
				cands := ladder.candidates(minIn, maxIn)
				if capIn := ladder.legs[0].capacity(); capIn.GreaterThan(minIn) && capIn.LessThan(maxIn) {
					cands = ladder.candidates(minIn, capIn)
				}
				var bestCand decimal.Decimal
				bestSize, any := decimal.Zero, false
				for _, c := range cands {
					cq, err := QuoteCycle(tri, data, sch, c)
					if err != nil {
						continue
					}
					if !any || cq.GrossProfit.GreaterThan(bestCand) {
						bestCand, bestSize, any = cq.GrossProfit, c, true
					}
				}
				if any != ok {
					t.Fatalf("case %d: search ok=%v but candidates quotable=%v", i, ok, any)
				}
				if !ok {
					continue
				}
				if res.Best.GrossProfit.LessThan(bestCand) {
					t.Fatalf("case %d: search returned %s at %s, breakpoint %s is worth %s",
						i, res.Best.GrossProfit, res.Best.InputConsumed, bestSize, bestCand)
				}
				// A dense reference grid can still land on a luckier
				// quantization residue between two breakpoints. The
				// search is only allowed to give away that residue: one
				// truncation step on leg 2 and one on leg 3, valued in
				// start-asset units.
				quote := func(in decimal.Decimal) (CycleQuote, error) { return QuoteCycle(tri, data, sch, in) }
				grid, gridSize := bruteForce(t, quote, minIn, maxIn, 400)
				gap := grid.Sub(res.Best.GrossProfit)
				if gap.GreaterThan(worst) {
					worst, worstAt = gap, res.Best.InputConsumed
				}
				if allowed := quantizationDust(tri, data); gap.GreaterThan(allowed) {
					t.Fatalf("case %d: search %s at %s, reference grid %s at %s (gap %s > dust %s)",
						i, res.Best.GrossProfit, res.Best.InputConsumed, grid, gridSize, gap, allowed)
				}
				if budget := DefaultSizeSearch.MaxCandidates + DefaultSizeSearch.RefineIters + 2; res.Evaluations > budget {
					t.Fatalf("case %d: evaluations = %d, budget %d", i, res.Evaluations, budget)
				}
			}
		}
	}
	t.Logf("worst gap against a 400-point reference grid: %s on %s deployed", worst, worstAt)
}

// quantizationDust bounds what truncation on legs 2 and 3 can cost a
// cycle, in start-asset units: less than one quantity step of each
// market's base asset, valued at the fixture's touch prices. Leg-1
// truncation is not in it — what that leaves behind never leaves the
// start asset and is never counted as deployed.
func quantizationDust(tri graph.Triangle, data [3]MarketData) decimal.Decimal {
	value := map[exchange.Asset]decimal.Decimal{"USDT": d("1"), "ETH": d("2500"), "BTC": d("50000")}
	total := decimal.Zero
	for i := 1; i < 3; i++ {
		total = total.Add(data[i].Rules.QtyStep.Mul(value[tri.Legs[i].Base]))
	}
	return total
}

// triReverseUSDT walks the same three markets the other way round:
// USDT→ETH (buy), ETH→BTC (sell), BTC→USDT (sell). Every sell-side
// ladder mapping is exercised by this rotation.
func triReverseUSDT() graph.Triangle {
	return graph.Triangle{
		ID: "binance|USDT|ETHUSDT>ETHBTC>BTCUSDT", Exchange: "binance", Start: "USDT",
		Legs: [3]graph.Leg{
			mkLeg("ETHUSDT", "ETH", "USDT", "USDT", "ETH", exchange.SideBuy),
			mkLeg("ETHBTC", "ETH", "BTC", "ETH", "BTC", exchange.SideSell),
			mkLeg("BTCUSDT", "BTC", "USDT", "BTC", "USDT", exchange.SideSell),
		},
	}
}

// randomBooks builds six-level books whose prices walk away from the
// touch and whose sizes vary, so breakpoints land at irregular places.
func randomBooks(rng *rand.Rand, tri graph.Triangle) [3]MarketData {
	base := []string{"2500", "0.05", "50000"}
	steps := []string{"0.5", "0.00002", "8"}
	qty := []string{"0.4", "3", "0.05"}
	step := []string{"0.0001", "0.0001", "0.00001"}
	var data [3]MarketData
	for i, leg := range tri.Legs {
		// The reverse triangle prices ETHUSDT, ETHBTC, BTCUSDT in that
		// order; pick the price scale by symbol so both rotations use the
		// same market prices.
		scale := 0
		switch leg.Market.Symbol {
		case "ETHBTC":
			scale = 1
		case "BTCUSDT":
			scale = 2
		}
		price := d(base[scale])
		tick := d(steps[scale])
		levels := make([]orderbook.Level, 0, 6)
		for j := 0; j < 6; j++ {
			q := d(qty[scale]).Mul(decimal.NewFromInt(int64(1 + rng.Intn(9))))
			levels = append(levels, orderbook.Level{Price: price, Qty: q})
			if leg.Side == exchange.SideBuy {
				price = price.Add(tick.Mul(decimal.NewFromInt(int64(1 + rng.Intn(4)))))
			} else {
				price = price.Sub(tick.Mul(decimal.NewFromInt(int64(1 + rng.Intn(4)))))
			}
		}
		if leg.Side == exchange.SideBuy {
			data[i] = mdWith(nil, levels, step[scale])
		} else {
			data[i] = mdWith(levels, nil, step[scale])
		}
	}
	return data
}

// The ladder is only allowed to propose sizes; these are the exact
// mappings it proposes them with. Values are hand-computed.
func TestLegLadderMappings(t *testing.T) {
	sched := schedReceived(t, "0.001")

	// Buy leg, fee in the received asset: 100x1 then 101x2 of asks.
	// Boundaries in input (= usable, the fee is on the output) are the
	// cumulative costs 100 and 100 + 202 = 302.
	buy, ok := newLegLadder(
		mkLeg("BTCUSDT", "BTC", "USDT", "USDT", "BTC", exchange.SideBuy),
		mdWith(nil, []orderbook.Level{lv("100", "1"), lv("101", "2")}, "0.001"), sched)
	if !ok {
		t.Fatal("buy ladder not built")
	}
	bps := buy.breakpoints()
	if len(bps) != 2 || !bps[0].Equal(d("100")) || !bps[1].Equal(d("302")) {
		t.Fatalf("buy breakpoints = %v", bps)
	}
	eq(t, "buy capacity", buy.capacity(), d("302"))
	// 250 USDT buys 1 BTC at 100 plus 150/101 at 101 = 2.485148514851…
	// gross; the 10 bps fee leaves 0.999 of it.
	net, consumed := buy.netOut(d("250"))
	eq(t, "buy consumed", consumed, d("250"))
	if net.Sub(d("2.48266336633663366336633663")).Abs().GreaterThan(d("1e-20")) {
		t.Fatalf("buy netOut = %s", net)
	}
	// Backwards: the same net must ask for the same input.
	in, ok := buy.inputFor(net)
	if !ok || in.Sub(d("250")).Abs().GreaterThan(d("1e-20")) {
		t.Fatalf("buy inputFor(%s) = %s ok=%v", net, in, ok)
	}
	if _, ok := buy.inputFor(d("3")); ok {
		t.Fatal("buy inputFor beyond visible depth must fail")
	}

	// Sell leg on a SPENT venue: the fee is charged on the base input, so
	// a boundary of 1 base unit costs 1.0008 of input at 8 bps.
	spent, err := fees.NewSchedule("kraken", exchange.FeeInSpent, fees.Rate{Maker: d("0.0008"), Taker: d("0.0008")})
	if err != nil {
		t.Fatal(err)
	}
	sell, ok := newLegLadder(
		mkLeg("XBTUSD", "BTC", "USD", "BTC", "USD", exchange.SideSell),
		mdWith([]orderbook.Level{lv("100", "1"), lv("99", "2")}, nil, "0.001"), spent)
	if !ok {
		t.Fatal("sell ladder not built")
	}
	bps = sell.breakpoints()
	if len(bps) != 2 || !bps[0].Equal(d("1.0008")) || !bps[1].Equal(d("3.0024")) {
		t.Fatalf("sell breakpoints = %v", bps)
	}
	eq(t, "sell capacity", sell.capacity(), d("3.0024"))
	// 2.502 input → 2.502/1.0008 = 2.5 base on the book → 100 + 1.5*99
	// = 248.5 quote, and no output-side fee on a SPENT venue.
	net, consumed = sell.netOut(d("2.502"))
	if net.Sub(d("248.5")).Abs().GreaterThan(d("1e-20")) {
		t.Fatalf("sell netOut = %s", net)
	}
	if consumed.Sub(d("2.502")).Abs().GreaterThan(d("1e-20")) {
		t.Fatalf("sell consumed = %s", consumed)
	}
	in, ok = sell.inputFor(d("248.5"))
	if !ok || in.Sub(d("2.502")).Abs().GreaterThan(d("1e-20")) {
		t.Fatalf("sell inputFor = %s ok=%v", in, ok)
	}
	// Input past the visible depth is capped, not extrapolated.
	net, consumed = sell.netOut(d("100"))
	eq(t, "sell capped netOut", net, d("298"))
	eq(t, "sell capped consumed", consumed, d("3.0024"))
}

// The continuous ladder must bound the exact cycle: no size may be worth
// more than the ladder says plus the leg-1 truncation slack. That is the
// inequality the search prunes candidates with.
func TestLadderBoundsExactProfit(t *testing.T) {
	tri := triUSDT()
	sched := schedReceived(t, "0.001")
	rng := rand.New(rand.NewSource(4242)) //nolint:gosec // deterministic test fixture
	for i := 0; i < 12; i++ {
		data := randomBooks(rng, tri)
		ladder, ok := newCycleLadder(tri, data, sched)
		if !ok {
			t.Fatalf("case %d: ladder not built", i)
		}
		for size := 25; size <= 25000; size += 137 {
			in := decimal.NewFromInt(int64(size))
			cq, err := QuoteCycle(tri, data, sched, in)
			if err != nil {
				continue
			}
			bound := ladder.profit(in).Add(ladder.dustSlack)
			if cq.GrossProfit.GreaterThan(bound) {
				t.Fatalf("case %d size %s: exact profit %s exceeds continuous bound %s",
					i, in, cq.GrossProfit, bound)
			}
		}
	}
}

// The coarse pass must cover the range end to end and keep its resolution
// relative to the size, which is what a uniform grid fails to do.
func TestGeometricSizes(t *testing.T) {
	cases := []struct {
		name           string
		minIn, maxIn   decimal.Decimal
		points         int
		wantMaxRatio   decimal.Decimal
		wantFirstIsMin bool
	}{
		{name: "narrow", minIn: d("50"), maxIn: d("1000"), points: 13, wantMaxRatio: d("2"), wantFirstIsMin: true},
		{name: "wide", minIn: d("1"), maxIn: d("6001000"), points: 13, wantMaxRatio: d("4"), wantFirstIsMin: true},
		{name: "extreme", minIn: d("0.5"), maxIn: d("5000000000"), points: 13, wantMaxRatio: d("8"), wantFirstIsMin: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := geometricSizes(tc.minIn, tc.maxIn, tc.points)
			if len(got) > tc.points {
				t.Fatalf("points = %d, cap %d", len(got), tc.points)
			}
			if tc.wantFirstIsMin && !got[0].Equal(tc.minIn) {
				t.Fatalf("first = %s, want %s", got[0], tc.minIn)
			}
			if !got[len(got)-1].Equal(tc.maxIn) {
				t.Fatalf("last = %s, want %s", got[len(got)-1], tc.maxIn)
			}
			for i := 1; i < len(got); i++ {
				if !got[i].GreaterThan(got[i-1]) {
					t.Fatalf("not ascending at %d: %s <= %s", i, got[i], got[i-1])
				}
				if ratio := got[i].Div(got[i-1]); ratio.GreaterThan(tc.wantMaxRatio) {
					t.Fatalf("step %d ratio = %s, want <= %s", i, ratio, tc.wantMaxRatio)
				}
			}
		})
	}
	// A range starting at zero has no geometric ladder; the uniform
	// fallback must still be well formed.
	got := geometricSizes(decimal.Zero, d("120"), 13)
	if len(got) != 13 || !got[0].IsZero() || !got[12].Equal(d("120")) {
		t.Fatalf("zero-floor fallback = %v", got)
	}
}

// Both searches must respect their documented evaluation ceilings, on
// tradeable and untradeable triangles alike.
func TestSizeSearchEvaluationBudget(t *testing.T) {
	tri, data := pocketBooks()
	sched := schedReceived(t, "0.001")
	quote := func(in decimal.Decimal) (CycleQuote, error) { return QuoteCycle(tri, data, sched, in) }
	search := DefaultSizeSearch

	cycleBudget := search.MaxCandidates + search.RefineIters + 2
	closureBudget := search.GridPoints + search.RefineIters + 2

	res, _ := search.FindCycle(tri, data, sched, d("50"), d("150000"))
	if res.Evaluations > cycleBudget {
		t.Fatalf("FindCycle evaluations = %d, budget %d", res.Evaluations, cycleBudget)
	}
	res, _ = search.Find(quote, d("50"), d("150000"))
	if res.Evaluations > closureBudget {
		t.Fatalf("Find evaluations = %d, budget %d", res.Evaluations, closureBudget)
	}

	// Untradeable at every size: min notional above the whole range.
	data[0].Rules.MinNotional = d("10000000")
	res, ok := search.FindCycle(tri, data, sched, d("50"), d("150000"))
	if ok {
		t.Fatalf("want no viable size, got %s", res.Best.InputConsumed)
	}
	if res.Evaluations > cycleBudget {
		t.Fatalf("unquotable FindCycle evaluations = %d, budget %d", res.Evaluations, cycleBudget)
	}

	// Degenerate ranges are refused without pricing anything.
	for _, tc := range [][2]decimal.Decimal{
		{d("100"), d("100")}, {d("100"), d("10")}, {d("-5"), d("-1")},
	} {
		res, ok := search.FindCycle(tri, data, sched, tc[0], tc[1])
		if ok || res.Evaluations != 0 {
			t.Fatalf("range [%s,%s]: ok=%v evaluations=%d", tc[0], tc[1], ok, res.Evaluations)
		}
	}
}

// When a venue rule cuts the range between two breakpoints the optimum
// is not a breakpoint at all, and the refinement between neighbouring
// candidates is the only thing that can find it: leg 1 here holds 5 000
// USDT of cheap depth (the breakpoint) but the market's max notional
// stops the order at 3 000.
func TestSizeSearchRefinesInsideACutGap(t *testing.T) {
	tri := triUSDT()
	sched := schedReceived(t, "0.001")
	data := [3]MarketData{
		mdWith(nil, []orderbook.Level{lv("50000", "0.1"), lv("60000", "1000")}, "0.00001"),
		mdWith(nil, []orderbook.Level{lv("0.05", "10000")}, "0.0001"),
		mdWith([]orderbook.Level{lv("2510", "100000")}, nil, "0.0001"),
	}
	data[0].Rules.MaxNotional = d("3000")
	minIn, maxIn := d("50"), d("150000")

	res, ok := DefaultSizeSearch.FindCycle(tri, data, sched, minIn, maxIn)
	if !ok {
		t.Fatal("no viable size")
	}
	if res.Best.InputConsumed.LessThan(d("2900")) || res.Best.InputConsumed.GreaterThan(d("3000")) {
		t.Fatalf("size = %s, want just under the 3 000 notional cap", res.Best.InputConsumed)
	}
	// +9.9 bps of ~3 000 deployed.
	if res.Best.GrossProfit.LessThan(d("2.85")) {
		t.Fatalf("profit = %s, want ~2.97", res.Best.GrossProfit)
	}
	quote := func(in decimal.Decimal) (CycleQuote, error) { return QuoteCycle(tri, data, sched, in) }
	brute, bruteSize := bruteForce(t, quote, minIn, maxIn, 3000)
	if brute.Sub(res.Best.GrossProfit).GreaterThan(d("0.05")) {
		t.Fatalf("search %s vs brute force %s (at %s)", res.Best.GrossProfit, brute, bruteSize)
	}
}

// A book that is not ordered from the touch cannot promise a single
// maximum, so the search must fall back to ranking every candidate
// instead of walking outwards from one peak — and must still come back
// with the best breakpoint.
func TestSizeSearchNonMonotoneBook(t *testing.T) {
	tri := triUSDT()
	sched := schedReceived(t, "0.001")
	data := [3]MarketData{
		// Second ask is cheaper than the first: not a well-formed book.
		mdWith(nil, []orderbook.Level{lv("60000", "0.02"), lv("50000", "0.05"), lv("70000", "10")}, "0.00001"),
		mdWith(nil, []orderbook.Level{lv("0.05", "10000")}, "0.0001"),
		mdWith([]orderbook.Level{lv("2510", "100000")}, nil, "0.0001"),
	}
	ladder, ok := newCycleLadder(tri, data, sched)
	if !ok {
		t.Fatal("ladder not built")
	}
	if ladder.concave {
		t.Fatal("a book that improves with depth must not be treated as concave")
	}
	minIn, maxIn := d("50"), d("100000")
	res, found := DefaultSizeSearch.FindCycle(tri, data, sched, minIn, maxIn)
	if !found {
		t.Fatal("no viable size")
	}
	for _, c := range ladder.candidates(minIn, maxIn) {
		cq, err := QuoteCycle(tri, data, sched, c)
		if err != nil {
			continue
		}
		if cq.GrossProfit.GreaterThan(res.Best.GrossProfit) {
			t.Fatalf("candidate %s is worth %s, search returned %s at %s",
				c, cq.GrossProfit, res.Best.GrossProfit, res.Best.InputConsumed)
		}
	}
}

// A zero-value SizeSearch must still search (the config layer may leave
// the knobs unset) and must not spin.
func TestSizeSearchZeroValueConfig(t *testing.T) {
	tri, data := pocketBooks()
	sched := schedReceived(t, "0.001")
	var zero SizeSearch
	res, ok := zero.FindCycle(tri, data, sched, d("50"), d("150000"))
	if !ok || !res.Best.GrossProfit.IsPositive() {
		t.Fatalf("ok=%v profit=%s", ok, res.Best.GrossProfit)
	}
	if res.Evaluations > defaultMaxCandidates+2 {
		t.Fatalf("evaluations = %d", res.Evaluations)
	}
}

func benchBooks() [3]MarketData {
	mkLevels := func(start, step string, n int, qty string) []orderbook.Level {
		out := make([]orderbook.Level, n)
		p := d(start)
		for i := 0; i < n; i++ {
			out[i] = orderbook.Level{Price: p, Qty: d(qty)}
			p = p.Add(d(step))
		}
		return out
	}
	return [3]MarketData{
		mdWith(nil, mkLevels("100", "0.01", 50, "2"), "0.001"),
		mdWith(nil, mkLevels("0.1", "0.00001", 50, "50"), "0.001"),
		mdWith(mkLevels("10.2", "-0.001", 50, "100"), nil, "0.001"),
	}
}

// The breakpoint search over the same 50-level books the closure-only
// benchmark uses.
func BenchmarkSizeSearchCycle50Levels(b *testing.B) {
	data := benchBooks()
	sched, err := fees.NewSchedule("binance", exchange.FeeInReceived,
		fees.Rate{Maker: d("0.001"), Taker: d("0.001")})
	if err != nil {
		b.Fatal(err)
	}
	tri := triUSDT()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, ok := DefaultSizeSearch.FindCycle(tri, data, sched, d("10"), d("10000")); !ok {
			b.Fatal("no viable size")
		}
	}
}

// The same search over a 3000:1 range: the case audit T1 reported as a
// false negative.
func BenchmarkSizeSearchCycleWideRange(b *testing.B) {
	tri, data := pocketBooks()
	sched, err := fees.NewSchedule("binance", exchange.FeeInReceived,
		fees.Rate{Maker: d("0.001"), Taker: d("0.001")})
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, ok := DefaultSizeSearch.FindCycle(tri, data, sched, d("50"), d("150000")); !ok {
			b.Fatal("no viable size")
		}
	}
}

// deepLevels builds a book side of n levels walking away from the touch.
func deepLevels(start, step string, n int, qty string) []orderbook.Level {
	out := make([]orderbook.Level, n)
	p := d(start)
	for i := 0; i < n; i++ {
		out[i] = orderbook.Level{Price: p, Qty: d(qty)}
		p = p.Add(d(step))
	}
	return out
}

// The deepest book the scanner can be configured with (depth 500) over a
// range that reaches the end of it: this is where deriving candidates
// costs the most, because every level of every leg is a breakpoint in
// range.
func BenchmarkSizeSearchCycle500Levels(b *testing.B) {
	data := [3]MarketData{
		mdWith(nil, deepLevels("100", "0.001", 500, "2"), "0.001"),
		mdWith(nil, deepLevels("0.1", "0.000001", 500, "50"), "0.001"),
		mdWith(deepLevels("10.2", "-0.0001", 500, "100"), nil, "0.001"),
	}
	sched, err := fees.NewSchedule("binance", exchange.FeeInReceived,
		fees.Rate{Maker: d("0.001"), Taker: d("0.001")})
	if err != nil {
		b.Fatal(err)
	}
	tri := triUSDT()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, ok := DefaultSizeSearch.FindCycle(tri, data, sched, d("10"), d("100000")); !ok {
			b.Fatal("no viable size")
		}
	}
}

// The pre-fix search on the books of BenchmarkSizeSearch50Levels, so the
// cost of the uniform grid plus ternary refinement can be measured in the
// same run as the searches that replaced it.
func BenchmarkLegacyGridTernary50Levels(b *testing.B) {
	data := benchBooks()
	sched, err := fees.NewSchedule("binance", exchange.FeeInReceived,
		fees.Rate{Maker: d("0.001"), Taker: d("0.001")})
	if err != nil {
		b.Fatal(err)
	}
	tri := triUSDT()
	quote := func(in decimal.Decimal) (CycleQuote, error) { return QuoteCycle(tri, data, sched, in) }
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, ok, _ := legacyGridTernary(quote, d("10"), d("10000"), 13, 14); !ok {
			b.Fatal("no viable size")
		}
	}
}

// The whole fix rests on projecting a later leg's level boundary back
// into start-asset units, fees included. These two cases check that the
// projection lands exactly on the boundary: at the candidate the leg
// stops inside its first level, and a hair above it the walk spills into
// the second.
func TestCandidatesLandOnLaterLegBoundaries(t *testing.T) {
	tri := triUSDT()

	t.Run("leg 3 boundary, fee in the received asset", func(t *testing.T) {
		sched := schedReceived(t, "0.001")
		data := [3]MarketData{
			mdWith(nil, []orderbook.Level{lv("100", "1000")}, "0.001"),
			mdWith(nil, []orderbook.Level{lv("0.1", "10000")}, "0.001"),
			mdWith([]orderbook.Level{lv("10.5", "50"), lv("9.5", "1000")}, nil, "0.001"),
		}
		// Leg 3 fills its 50 ETH level when leg 2 hands it 50 ETH net:
		// gross 50/0.999 ETH costs 50/0.999*0.1 = 5/0.999 BTC on leg 2,
		// which leg 1 must deliver net, so leg 1 buys 5/0.998001 BTC at
		// 100 = 500/0.998001 = 501.00150200250300350400450500... USDT.
		want := d("501.0015020025030035040045050")
		size := findCandidate(t, tri, data, sched, d("1"), d("10000"), want, d("1e-20"))

		cq := quoteAt(t, func(in decimal.Decimal) (CycleQuote, error) {
			return QuoteCycle(tri, data, sched, in)
		}, size)
		if cq.Legs[2].LevelsConsumed != 1 {
			t.Fatalf("leg-3 levels at the boundary = %d, want 1", cq.Legs[2].LevelsConsumed)
		}
		if cq.Legs[2].OrderQty.LessThan(d("49.99")) {
			t.Fatalf("leg-3 qty = %s, want the 50 level filled", cq.Legs[2].OrderQty)
		}
		spill := quoteAt(t, func(in decimal.Decimal) (CycleQuote, error) {
			return QuoteCycle(tri, data, sched, in)
		}, size.Add(d("5")))
		if spill.Legs[2].LevelsConsumed != 2 {
			t.Fatalf("5 USDT above the boundary consumed %d levels, want 2", spill.Legs[2].LevelsConsumed)
		}
	})

	t.Run("leg 2 boundary, fee charged on the input", func(t *testing.T) {
		sched, err := fees.NewSchedule("kraken", exchange.FeeInSpent,
			fees.Rate{Maker: d("0.0008"), Taker: d("0.0008")})
		if err != nil {
			t.Fatal(err)
		}
		data := [3]MarketData{
			mdWith(nil, []orderbook.Level{lv("100", "1000")}, "0.001"),
			mdWith(nil, []orderbook.Level{lv("0.1", "40"), lv("0.2", "1000")}, "0.001"),
			mdWith([]orderbook.Level{lv("10.5", "10000")}, nil, "0.001"),
		}
		// Leg 2 fills its 40 ETH level with 4 BTC on the book plus the
		// 8 bps input fee: 4.0032 BTC. On a SPENT venue leg 1 pays its fee
		// on the input too, so 4.0032 BTC costs 400.32 USDT on the book
		// and 400.32 * 1.0008 = 400.640256 USDT of input.
		want := d("400.640256")
		size := findCandidate(t, tri, data, sched, d("1"), d("10000"), want, d("1e-20"))

		cq := quoteAt(t, func(in decimal.Decimal) (CycleQuote, error) {
			return QuoteCycle(tri, data, sched, in)
		}, size)
		if cq.Legs[1].LevelsConsumed != 1 {
			t.Fatalf("leg-2 levels at the boundary = %d, want 1", cq.Legs[1].LevelsConsumed)
		}
		if cq.Legs[1].OrderQty.LessThan(d("39.99")) {
			t.Fatalf("leg-2 qty = %s, want the 40 level filled", cq.Legs[1].OrderQty)
		}
		spill := quoteAt(t, func(in decimal.Decimal) (CycleQuote, error) {
			return QuoteCycle(tri, data, sched, in)
		}, size.Add(d("1")))
		if spill.Legs[1].LevelsConsumed != 2 {
			t.Fatalf("1 USDT above the boundary consumed %d levels, want 2", spill.Legs[1].LevelsConsumed)
		}
	})
}

// findCandidate asserts the candidate set contains want (within tol) and
// returns the candidate as the search derived it.
func findCandidate(t *testing.T, tri graph.Triangle, data [3]MarketData, sched *fees.Schedule,
	minIn, maxIn, want, tol decimal.Decimal) decimal.Decimal {
	t.Helper()
	ladder, ok := newCycleLadder(tri, data, sched)
	if !ok {
		t.Fatal("ladder not built")
	}
	for _, c := range ladder.candidates(minIn, maxIn) {
		if c.Sub(want).Abs().LessThanOrEqual(tol) {
			return c
		}
	}
	t.Fatalf("no candidate within %s of %s; candidates: %v", tol, want, ladder.candidates(minIn, maxIn))
	return decimal.Zero
}
