package pricing

import (
	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/exchange"
	"github.com/cploutarchou/arb-chain-bot/internal/fees"
	"github.com/cploutarchou/arb-chain-bot/internal/graph"
)

// SizeSearch finds the input size maximizing absolute cycle profit.
// Profit(size) follows the depth curve's breakpoints and quantization
// plateaus, so the search is a bounded coarse grid over [min,max] plus
// ternary refinement between the best point's neighbors — never an
// unbounded brute force (SKILL.md §17). The best evaluated quote is
// returned even when refinement plateaus.
type SizeSearch struct {
	GridPoints  int // coarse pass size (>= 3)
	RefineIters int // ternary iterations between best neighbors
}

// DefaultSizeSearch is tuned for sub-millisecond total search cost at
// 50-level books (each evaluation is one QuoteCycle).
var DefaultSizeSearch = SizeSearch{GridPoints: 13, RefineIters: 14}

// SizeResult carries the winning quote and search accounting.
type SizeResult struct {
	Best        CycleQuote
	Evaluations int
}

// Find runs the search. quote must be a pure function of size (same books,
// same config). ok=false when no size in range produced a valid quote.
func (s SizeSearch) Find(quote func(decimal.Decimal) (CycleQuote, error), minIn, maxIn decimal.Decimal) (SizeResult, bool) {
	if s.GridPoints < 3 {
		s.GridPoints = 3
	}
	res := SizeResult{}
	if !maxIn.GreaterThan(minIn) || !maxIn.IsPositive() {
		return res, false
	}

	type point struct {
		size   decimal.Decimal
		profit decimal.Decimal
		quote  CycleQuote
		valid  bool
	}
	eval := func(size decimal.Decimal) point {
		res.Evaluations++
		cq, err := quote(size)
		if err != nil {
			return point{size: size}
		}
		return point{size: size, profit: cq.GrossProfit, quote: cq, valid: true}
	}

	span := maxIn.Sub(minIn)
	stepDiv := decimal.NewFromInt(int64(s.GridPoints - 1))
	grid := make([]point, s.GridPoints)
	bestIdx := -1
	for i := range grid {
		size := minIn.Add(span.Mul(decimal.NewFromInt(int64(i))).Div(stepDiv))
		grid[i] = eval(size)
		if grid[i].valid && (bestIdx < 0 || grid[i].profit.GreaterThan(grid[bestIdx].profit)) {
			bestIdx = i
		}
	}
	if bestIdx < 0 {
		return res, false
	}
	best := grid[bestIdx]

	lo := minIn
	if bestIdx > 0 {
		lo = grid[bestIdx-1].size
	}
	hi := maxIn
	if bestIdx < len(grid)-1 {
		hi = grid[bestIdx+1].size
	}
	three := decimal.NewFromInt(3)
	for i := 0; i < s.RefineIters && hi.GreaterThan(lo); i++ {
		third := hi.Sub(lo).Div(three)
		m1 := eval(lo.Add(third))
		m2 := eval(hi.Sub(third))
		for _, p := range []point{m1, m2} {
			if p.valid && p.profit.GreaterThan(best.profit) {
				best = p
			}
		}
		// Invalid points (dust/min-notional at small sizes) steer upward.
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
	res.Best = best.quote
	return res, true
}

// CapacityHint upper-bounds the leg-1 input the visible depth could
// absorb (used as the search's max, before config caps). It intentionally
// over-estimates slightly; the walk itself is the exact authority.
func CapacityHint(leg graph.Leg, md MarketData, sched *fees.Schedule) decimal.Decimal {
	eff := sched.Taker(leg.Market)
	placement, err := fees.PlacementFor(sched.Convention, leg.Side)
	if err != nil {
		return decimal.Zero
	}
	var capacity decimal.Decimal
	switch leg.Side {
	case exchange.SideBuy:
		for _, lv := range md.View.Asks {
			capacity = capacity.Add(lv.Price.Mul(lv.Qty))
		}
	case exchange.SideSell:
		for _, lv := range md.View.Bids {
			capacity = capacity.Add(lv.Qty)
		}
	}
	if placement == fees.FeeOnInput {
		capacity = capacity.Mul(one.Add(eff.Rate))
	}
	return capacity
}
