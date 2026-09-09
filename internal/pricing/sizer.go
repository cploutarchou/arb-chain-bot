package pricing

import (
	"slices"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/exchange"
	"github.com/cploutarchou/arb-chain-bot/internal/fees"
	"github.com/cploutarchou/arb-chain-bot/internal/graph"
)

// SizeSearch finds the input size maximizing absolute cycle profit.
//
// Shape of the objective: profit rides the three legs' depth curves. It
// is piecewise linear in size with a kink exactly where one of the legs
// starts eating a worse level, concave from the range floor up to leg 1's
// capacity, flat above it (an input the book cannot absorb is never
// deployed), and dented by each leg's quantization sawtooth. Two searches
// are built on that shape:
//
//   - FindCycle, with the books in hand, computes the kinks — every level
//     boundary of all three legs, mapped back into start-asset units
//     through the leg conversions and fee placement — and prices the
//     exact QuoteCycle at the candidates that can still win, ordered by
//     the upper bound the continuous curve puts on each. A profitable
//     pocket narrower than any grid step cannot be stepped over, whatever
//     max_trade_size / min_input is (audit T1, roadmap P1-9).
//   - Find, with only a quote closure in hand, runs a geometric coarse
//     pass plus golden-section refinement, so its resolution is relative
//     to the size rather than to the span: a range covering six orders of
//     magnitude no longer goes blind at the small end.
//
// Evaluation budget (one evaluation = one QuoteCycle over the pinned
// books, the unit of hot-path cost):
//
//	FindCycle: MaxCandidates + RefineIters + 2   (defaults: 40)
//	Find:      GridPoints    + RefineIters + 2   (defaults: 29)
//
// Both ceilings are hard — the fallbacks inside FindCycle draw from the
// same budget — and both are below the 41 evaluations the previous
// uniform-grid-plus-ternary search always spent. Typical FindCycle runs
// use far less: the bound prunes every candidate that cannot beat the
// best exact quote already priced.
//
// The best evaluated quote is returned; a returned quote is always a
// truthful QuoteCycle at the returned size, never an interpolation.
type SizeSearch struct {
	GridPoints    int // coarse pass size (>= 3), closure-only path
	RefineIters   int // golden-section iterations (evaluations = iters + 2)
	MaxCandidates int // exact evaluations spent on depth breakpoints (0 => 24)
}

// defaultMaxCandidates keeps the breakpoint pass inside the hot-path
// budget even on a book with hundreds of levels per side. The pass is
// ordered by upper bound and stops as soon as no unpriced candidate can
// win, so this ceiling is a guard rail rather than the normal cost.
const defaultMaxCandidates = 24

// DefaultSizeSearch is the shipped configuration: ~29 evaluations on the
// closure-only path, typically well under 10 with books in hand.
var DefaultSizeSearch = SizeSearch{GridPoints: 13, RefineIters: 14, MaxCandidates: defaultMaxCandidates}

// SizeResult carries the winning quote and search accounting.
type SizeResult struct {
	Best        CycleQuote
	Evaluations int // QuoteCycle calls spent
	Candidates  int // depth breakpoints considered (FindCycle only)
}

func (s SizeSearch) normalized() SizeSearch {
	if s.GridPoints < 3 {
		s.GridPoints = 3
	}
	if s.RefineIters < 0 {
		s.RefineIters = 0
	}
	if s.MaxCandidates <= 0 {
		s.MaxCandidates = defaultMaxCandidates
	}
	return s
}

// Find runs the closure-only search. quote must be a pure function of
// size (same books, same config). ok=false when no size in range produced
// a valid quote. Callers holding the books should prefer FindCycle: it
// searches the real depth breakpoints instead of sampling around them.
func (s SizeSearch) Find(quote func(decimal.Decimal) (CycleQuote, error), minIn, maxIn decimal.Decimal) (SizeResult, bool) {
	s = s.normalized()
	res := SizeResult{}
	if !searchable(minIn, maxIn) {
		return res, false
	}
	ev := &sizeEval{quote: quote, budget: s.GridPoints + s.RefineIters + 2, res: &res}
	s.coarseThenRefine(ev, minIn, maxIn)
	if !ev.best.valid {
		return res, false
	}
	res.Best = ev.best.quote
	return res, true
}

// FindCycle is the breakpoint-exact search: it prices the triangle at the
// sizes where the executable economics actually change, so a profitable
// region narrower than any grid step is still found. The books it derives
// candidates from are the books it quotes, which is why it builds the
// closure itself.
func (s SizeSearch) FindCycle(tri graph.Triangle, data [3]MarketData, sched *fees.Schedule, minIn, maxIn decimal.Decimal) (SizeResult, bool) {
	s = s.normalized()
	quote := func(in decimal.Decimal) (CycleQuote, error) {
		return QuoteCycle(tri, data, sched, in)
	}
	res := SizeResult{}
	if !searchable(minIn, maxIn) {
		return res, false
	}
	ladder, ok := newCycleLadder(tri, data, sched)
	if !ok {
		// A leg has no depth on the side it must consume: pricing itself
		// would fail at every size, but let the closure say so.
		return s.Find(quote, minIn, maxIn)
	}
	// Sizes above leg 1's capacity are all the same trade — the surplus
	// is never deployed — so the search domain ends there.
	if capIn := ladder.legs[0].capacity(); capIn.GreaterThan(minIn) && capIn.LessThan(maxIn) {
		maxIn = capIn
	}

	ev := &sizeEval{quote: quote, budget: s.MaxCandidates + s.RefineIters + 2, res: &res}
	cands := ladder.candidates(minIn, maxIn)
	res.Candidates = len(cands)
	bounds := newCandidateBounds(ladder, cands)

	if bounds.peak() < 0 {
		return res, false
	}
	if ladder.concave {
		s.priceOutwards(ev, bounds)
	} else {
		s.priceByCeiling(ev, bounds)
	}
	if !ev.best.valid {
		// Every breakpoint is unquotable (dust or min-notional at the low
		// end, a rule violation at the high end): fall back to sweeping
		// the range with whatever budget is left.
		s.golden(ev, minIn, maxIn)
		if !ev.best.valid {
			return res, false
		}
		res.Best = ev.best.quote
		return res, true
	}
	winner, _ := slices.BinarySearchFunc(cands, ev.best.size, func(a, b decimal.Decimal) int { return a.Cmp(b) })
	s.refineBetween(ev, bounds, winner)
	res.Best = ev.best.quote
	return res, true
}

// priceOutwards prices candidates from the ceiling's peak outwards. The
// ceilings fall away from the peak, so the first candidate on a side that
// cannot beat the best exact quote already priced ends that side — no
// candidate further out on it can win either. What is left is a walk over
// a handful of candidates around the optimum, with none silently skipped.
func (s SizeSearch) priceOutwards(ev *sizeEval, bounds *candidateBounds) {
	cands := bounds.sizes
	peak := bounds.peak()
	ev.at(cands[peak])
	lo, hi := peak-1, peak+1
	for (lo >= 0 || hi < len(cands)) && !ev.spent() {
		next := lo
		switch {
		case lo < 0:
			next = hi
		case hi >= len(cands):
			next = lo
		case bounds.ceiling(hi).GreaterThan(bounds.ceiling(lo)):
			next = hi
		}
		if ev.best.valid && !bounds.ceiling(next).GreaterThan(ev.best.profit) {
			if next == hi { // this side is done; the other may go on
				hi = len(cands)
			} else {
				lo = -1
			}
			continue
		}
		ev.at(cands[next])
		if next == hi {
			hi++
		} else {
			lo--
		}
	}
}

// priceByCeiling is the same pass for a book that does not worsen with
// depth: without a single maximum to walk away from, candidates are
// priced highest-ceiling first and the pass stops when the next ceiling
// cannot beat the best exact quote.
func (s SizeSearch) priceByCeiling(ev *sizeEval, bounds *candidateBounds) {
	cands := bounds.sizes
	order := make([]int, len(cands))
	for i := range order {
		order[i] = i
	}
	slices.SortFunc(order, func(a, b int) int {
		if cmp := bounds.ceiling(b).Cmp(bounds.ceiling(a)); cmp != 0 {
			return cmp
		}
		return cands[a].Cmp(cands[b]) // equal ceilings: cheaper size first
	})
	for _, i := range order {
		if ev.spent() || (ev.best.valid && !bounds.ceiling(i).GreaterThan(ev.best.profit)) {
			return
		}
		ev.at(cands[i])
	}
}

// refineFloorBps is the smallest headroom worth spending refinement
// evaluations on, in basis points of the deployed input. Between two
// adjacent breakpoints the continuous curve is a straight line whose ends
// are already priced, so all that can still be hiding in the gap is the
// per-leg quantization sawtooth. Below a tenth of a basis point that
// residue cannot move any downstream decision — buffers and
// min_net_edge_bps are configured in whole basis points — while each
// refinement iteration costs a full QuoteCycle on the hot path.
var refineFloorBps = decimal.RequireFromString("0.1")

// refineBetween spends the refinement budget between the winning
// candidate's neighbours, and only when that gap can still hide a
// materially better size.
func (s SizeSearch) refineBetween(ev *sizeEval, bounds *candidateBounds, bestIdx int) {
	cands := bounds.sizes
	if bestIdx < 0 || bestIdx >= len(cands) || len(cands) < 2 || ev.spent() {
		return
	}
	lo, hi := cands[max(bestIdx-1, 0)], cands[min(bestIdx+1, len(cands)-1)]
	if !hi.GreaterThan(lo) {
		return
	}
	ceiling := bounds.ceiling(bestIdx)
	for _, i := range []int{bestIdx - 1, bestIdx + 1} {
		if i >= 0 && i < len(cands) && bounds.ceiling(i).GreaterThan(ceiling) {
			ceiling = bounds.ceiling(i)
		}
	}
	headroom := ceiling.Sub(ev.best.profit)
	floor := ev.best.quote.InputConsumed.Mul(refineFloorBps).Div(tenK)
	if !headroom.GreaterThan(floor) {
		return // nothing worth pricing is left in the gap
	}
	s.golden(ev, lo, hi)
}

// coarseThenRefine is the closure-only search: a geometric coarse pass to
// bracket the maximum, then golden-section refinement between the best
// point's neighbours. Profit is concave in size on the continuous depth
// curve, so the bracket around the best coarse point contains the
// maximum.
func (s SizeSearch) coarseThenRefine(ev *sizeEval, minIn, maxIn decimal.Decimal) {
	sizes := geometricSizes(minIn, maxIn, s.GridPoints)
	type coarse struct {
		size   decimal.Decimal
		profit decimal.Decimal
		valid  bool
	}
	probes := make([]coarse, len(sizes))
	bestIdx := -1
	for i, size := range sizes {
		p := ev.at(size)
		probes[i] = coarse{size: p.size, profit: p.profit, valid: p.valid}
		if p.valid && (bestIdx < 0 || p.profit.GreaterThan(probes[bestIdx].profit)) {
			bestIdx = i
		}
	}
	if bestIdx < 0 {
		return
	}
	lo, hi := minIn, maxIn
	if bestIdx > 0 {
		lo = probes[bestIdx-1].size
	}
	if bestIdx < len(probes)-1 {
		hi = probes[bestIdx+1].size
	}
	s.golden(ev, lo, hi)
}

// Golden-section constants: 1/phi and 1/phi^2 to the division precision.
// They position probes, never money — every candidate they produce is
// priced by an exact QuoteCycle.
var (
	invPhi  = decimal.RequireFromString("0.6180339887498948482045868344")
	invPhi2 = decimal.RequireFromString("0.3819660112501051517954131656")
)

// golden narrows [lo,hi] onto the maximum, one evaluation per iteration
// after the two opening probes and a 0.618 shrink each time; a ternary
// pass needs two evaluations to shrink by 0.667. Concavity of the depth
// curve is what makes the bracket valid; the quantization sawtooth on top
// of it is handled by keeping the best point ever priced rather than the
// final bracket. Sizes that cannot be quoted at all (dust, min-notional,
// rule violations) rank below every valid size, which steers the bracket
// away from the unquotable end instead of stalling on it.
func (s SizeSearch) golden(ev *sizeEval, lo, hi decimal.Decimal) {
	if !hi.GreaterThan(lo) || ev.spent() {
		return
	}
	h := hi.Sub(lo)
	x1, x2 := lo.Add(h.Mul(invPhi2)), lo.Add(h.Mul(invPhi))
	p1, p2 := ev.at(x1), ev.at(x2)
	for i := 0; i < s.RefineIters && hi.GreaterThan(lo) && !ev.spent(); i++ {
		h = h.Mul(invPhi)
		if better(p1, p2) {
			hi, x2, p2 = x2, x1, p1
			x1 = lo.Add(h.Mul(invPhi2))
			p1 = ev.at(x1)
		} else {
			lo, x1, p1 = x1, x2, p2
			x2 = lo.Add(h.Mul(invPhi))
			p2 = ev.at(x2)
		}
	}
}

// sizePoint is one priced size.
type sizePoint struct {
	size   decimal.Decimal
	profit decimal.Decimal
	quote  CycleQuote
	valid  bool
}

// better ranks two priced sizes: more profit wins; on an exact tie the
// smaller size wins, because the same profit on less deployed capital is
// the better trade. An unquotable size loses to every quotable one.
func better(a, b sizePoint) bool {
	if !a.valid {
		return false
	}
	if !b.valid {
		return true
	}
	if cmp := a.profit.Cmp(b.profit); cmp != 0 {
		return cmp > 0
	}
	return a.size.LessThan(b.size)
}

// sizeEval prices sizes against a fixed evaluation budget and remembers
// the best result. Evaluations are counted even when the quote fails, so
// an untradeable triangle cannot spin the hot path.
type sizeEval struct {
	quote  func(decimal.Decimal) (CycleQuote, error)
	budget int
	res    *SizeResult
	best   sizePoint
}

func (e *sizeEval) spent() bool { return e.res.Evaluations >= e.budget }

func (e *sizeEval) at(size decimal.Decimal) sizePoint {
	if e.spent() || !size.IsPositive() {
		return sizePoint{size: size}
	}
	e.res.Evaluations++
	cq, err := e.quote(size)
	if err != nil {
		return sizePoint{size: size}
	}
	p := sizePoint{size: size, profit: cq.GrossProfit, quote: cq, valid: true}
	if better(p, e.best) {
		e.best = p
	}
	return p
}

func searchable(minIn, maxIn decimal.Decimal) bool {
	return maxIn.GreaterThan(minIn) && maxIn.IsPositive()
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
