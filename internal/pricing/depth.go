package pricing

import (
	"slices"
	"sort"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/exchange"
	"github.com/cploutarchou/arb-chain-bot/internal/fees"
	"github.com/cploutarchou/arb-chain-bot/internal/graph"
)

// legLadder is the continuous (pre-quantization) model of one leg's
// conversion curve: the cumulative depth of the side that leg consumes,
// plus that leg's fee placement. It answers three questions exactly in
// decimal:
//
//   - netOut(input): how much of the To asset an input buys, and how much
//     of the From asset is actually deployed (depth caps both);
//   - inputFor(netOut): how much input a wanted output needs — the walk
//     run backwards, used to project a later leg's depth boundary back
//     into start-asset units;
//   - breakpoints(): the input sizes at which the marginal price changes,
//     i.e. the level boundaries of the consumed side, in input units.
//
// Quantization, min-qty and min-notional are deliberately absent. The
// ladder only proposes candidate sizes and bounds what a size can be
// worth; QuoteLeg/QuoteCycle remain the sole authority on executable
// economics, and every size the sizer returns has been through them.
type legLadder struct {
	buy       bool // consumes asks (quote in, base out); false = consumes bids
	placement fees.Placement
	rate      decimal.Decimal
	feeUp     decimal.Decimal // 1 + rate: usable -> input on input-fee venues
	keep      decimal.Decimal // 1 - rate: gross -> net on output-fee venues
	// worsening is true when the consumed side really is ordered from the
	// touch outwards (asks ascending, bids descending). It is what makes
	// the conversion curve concave, so the sizer verifies it instead of
	// assuming it.
	worsening bool
	// Level prices in walk order, and the cumulative amounts after taking
	// levels 0..j in full. For a buy: cumIn is quote cost, cumOut is base
	// quantity. For a sell: cumIn is base quantity, cumOut is quote
	// proceeds. Both series are strictly increasing.
	price  []decimal.Decimal
	cumIn  []decimal.Decimal
	cumOut []decimal.Decimal
}

// newLegLadder builds the ladder for one leg from the same book side,
// fee schedule and placement rules QuoteLeg uses. ok=false when the leg
// has no usable depth on the side it must consume (QuoteLeg would return
// ErrNoDepth) or the venue convention has no placement for the side.
func newLegLadder(leg graph.Leg, md MarketData, sched *fees.Schedule) (legLadder, bool) {
	placement, err := fees.PlacementFor(sched.Convention, leg.Side)
	if err != nil {
		return legLadder{}, false
	}
	rate := sched.Taker(leg.Market).Rate
	l := legLadder{
		buy:       leg.Side == exchange.SideBuy,
		placement: placement,
		rate:      rate,
		feeUp:     one.Add(rate),
		keep:      one.Sub(rate),
		worsening: true,
	}
	levels := md.View.Bids
	if l.buy {
		levels = md.View.Asks
	}
	l.price = make([]decimal.Decimal, 0, len(levels))
	l.cumIn = make([]decimal.Decimal, 0, len(levels))
	l.cumOut = make([]decimal.Decimal, 0, len(levels))
	var cumIn, cumOut decimal.Decimal
	for _, lv := range levels {
		// A non-positive price or quantity is not depth; skipping keeps
		// the ladder monotone (and never divides by zero).
		if !lv.Price.IsPositive() || !lv.Qty.IsPositive() {
			continue
		}
		if n := len(l.price); n > 0 {
			// Asks must climb and bids must fall as the walk goes deeper.
			if l.buy && lv.Price.LessThan(l.price[n-1]) {
				l.worsening = false
			}
			if !l.buy && lv.Price.GreaterThan(l.price[n-1]) {
				l.worsening = false
			}
		}
		notional := lv.Price.Mul(lv.Qty)
		if l.buy {
			cumIn = cumIn.Add(notional)
			cumOut = cumOut.Add(lv.Qty)
		} else {
			cumIn = cumIn.Add(lv.Qty)
			cumOut = cumOut.Add(notional)
		}
		l.price = append(l.price, lv.Price)
		l.cumIn = append(l.cumIn, cumIn)
		l.cumOut = append(l.cumOut, cumOut)
	}
	if len(l.price) == 0 {
		return legLadder{}, false
	}
	return l, true
}

// feeOnInput reports whether this leg's fee is taken out of the input,
// which is the only case where usable and input differ.
func (l legLadder) feeOnInput() bool {
	return l.placement == fees.FeeOnInput && l.rate.IsPositive()
}

// usableFor converts a leg input into the amount that reaches the book
// (input-side fees are taken off the top), and inputFromUsable is its
// inverse — the input a walk of that size consumes, fee included. Both
// mirror QuoteLeg exactly: it deploys usable = input/(1+rate) (the same
// expression as fees.UsableInput, with 1+rate precomputed) and books
// InputConsumed = cost*(1+rate) on input-fee venues.
func (l legLadder) usableFor(input decimal.Decimal) decimal.Decimal {
	if l.feeOnInput() {
		return input.Div(l.feeUp)
	}
	return input
}

func (l legLadder) inputFromUsable(usable decimal.Decimal) decimal.Decimal {
	if l.feeOnInput() {
		return usable.Mul(l.feeUp)
	}
	return usable
}

// netFromGross applies an output-side fee, mirroring fees.NetOutput with
// 1-rate precomputed (gross*(1-rate) and gross-gross*rate are the same
// number; decimal multiplication and subtraction are both exact).
func (l legLadder) netFromGross(gross decimal.Decimal) decimal.Decimal {
	if l.placement == fees.FeeOnOutput && l.rate.IsPositive() {
		return gross.Mul(l.keep)
	}
	return gross
}

// capacity is the largest input the visible depth can absorb, in input
// units (anything beyond it is simply not deployed).
func (l legLadder) capacity() decimal.Decimal {
	return l.inputFromUsable(l.cumIn[len(l.cumIn)-1])
}

// netOut walks the ladder forward: the To-asset amount the next leg
// receives, and the From-asset amount actually consumed.
func (l legLadder) netOut(input decimal.Decimal) (net, consumed decimal.Decimal) {
	if !input.IsPositive() {
		return decimal.Zero, decimal.Zero
	}
	usable := l.usableFor(input)
	last := len(l.cumIn) - 1
	var gross, used decimal.Decimal
	if usable.GreaterThanOrEqual(l.cumIn[last]) {
		gross, used = l.cumOut[last], l.cumIn[last] // depth exhausted
	} else {
		j := seek(l.cumIn, usable)
		rem := usable.Sub(cumBefore(l.cumIn, j))
		gross, used = cumBefore(l.cumOut, j).Add(l.convert(rem, j)), usable
	}
	return l.netFromGross(gross), l.inputFromUsable(used)
}

// inputFor walks the ladder backwards: the input needed to hand the next
// leg exactly net. ok=false when the visible depth cannot produce it.
func (l legLadder) inputFor(net decimal.Decimal) (decimal.Decimal, bool) {
	if !net.IsPositive() {
		return decimal.Zero, false
	}
	gross := net
	if l.placement == fees.FeeOnOutput && l.rate.IsPositive() {
		if !l.keep.IsPositive() {
			return decimal.Zero, false // a fee of 100% leaves nothing to invert
		}
		gross = net.Div(l.keep)
	}
	last := len(l.cumOut) - 1
	if gross.GreaterThan(l.cumOut[last]) {
		return decimal.Zero, false
	}
	j := seek(l.cumOut, gross)
	rem := gross.Sub(cumBefore(l.cumOut, j))
	return l.inputFromUsable(cumBefore(l.cumIn, j).Add(l.unconvert(rem, j))), true
}

// breakpoints are the input sizes where this leg starts eating a worse
// level — the kinks of the profit curve, in input units.
func (l legLadder) breakpoints() []decimal.Decimal {
	out := make([]decimal.Decimal, len(l.cumIn))
	for i, c := range l.cumIn {
		out[i] = l.inputFromUsable(c)
	}
	return out
}

// convert prices an in-asset amount at level j into out-asset units
// (buying divides by the ask, selling multiplies by the bid); unconvert
// is the exact inverse.
func (l legLadder) convert(amount decimal.Decimal, j int) decimal.Decimal {
	if l.buy {
		return amount.Div(l.price[j])
	}
	return amount.Mul(l.price[j])
}

func (l legLadder) unconvert(amount decimal.Decimal, j int) decimal.Decimal {
	if l.buy {
		return amount.Mul(l.price[j])
	}
	return amount.Div(l.price[j])
}

// seek returns the first index whose cumulative amount reaches v. The
// caller guarantees v <= cum[last].
func seek(cum []decimal.Decimal, v decimal.Decimal) int {
	return sort.Search(len(cum), func(i int) bool {
		return cum[i].GreaterThanOrEqual(v)
	})
}

func cumBefore(cum []decimal.Decimal, j int) decimal.Decimal {
	if j <= 0 {
		return decimal.Zero
	}
	return cum[j-1]
}

// cycleLadder is the continuous model of a whole triangle: the three leg
// ladders chained the way QuoteCycle chains legs, plus the truncation
// slack that separates this model from the exact one.
//
// Shape of the curve it describes: each leg's forward map is concave and
// non-decreasing in its input — the next unit of input always converts at
// a price no better than the last — so their composition is too, and
// profit = final − deployed is concave in size up to leg 1's capacity and
// flat above it (input the book cannot absorb is never deployed). The
// only way the exact cycle can be worth more than this model says at a
// given size is leg-1 truncation leaving part of the size undeployed,
// which is bounded by dustSlack.
type cycleLadder struct {
	legs      [3]legLadder
	dustSlack decimal.Decimal
	// concave is true when every leg's book really does worsen with
	// depth. Only then may the sizer assume a single maximum and stop
	// looking outwards from it; a book that is not ordered from the touch
	// falls back to scanning every candidate.
	concave bool
}

func newCycleLadder(tri graph.Triangle, data [3]MarketData, sched *fees.Schedule) (cycleLadder, bool) {
	c := cycleLadder{concave: true}
	for i, leg := range tri.Legs {
		l, ok := newLegLadder(leg, data[i], sched)
		if !ok {
			return cycleLadder{}, false
		}
		c.legs[i] = l
		c.concave = c.concave && l.worsening
	}
	c.dustSlack = c.legs[0].truncationSlack(data[0].Rules)
	return c, true
}

// truncationSlack bounds, in input units, how much of a requested size
// leg 1 can leave undeployed because the order quantity is truncated to
// the venue's step: at most one step, valued at the worst visible price
// on the side being consumed, fee included. Profit as a function of size
// never falls faster than one unit of profit per unit of size, so this is
// also the most the exact cycle can beat the continuous model by at a
// given size — which is exactly what the size search needs to know
// before it skips a candidate.
func (l legLadder) truncationSlack(rules exchange.InstrumentRules) decimal.Decimal {
	var step decimal.Decimal
	switch rules.QtyMode {
	case exchange.PrecisionStep:
		step = rules.QtyStep
	case exchange.PrecisionDecimals:
		step = decimal.New(1, -rules.QtyDecimals)
	default:
		return decimal.Zero
	}
	if !step.IsPositive() {
		return decimal.Zero
	}
	if l.buy {
		// One step of base costs at most the worst visible ask.
		step = step.Mul(l.price[len(l.price)-1])
	}
	return l.inputFromUsable(step)
}

// profit is the continuous cycle profit at a start-asset size: final
// amount minus the input actually deployed, fees included, quantization
// excluded. It is an upper bound on the exact QuoteCycle profit at the
// same size up to dustSlack.
func (c cycleLadder) profit(in decimal.Decimal) decimal.Decimal {
	net1, consumed := c.legs[0].netOut(in)
	net2, _ := c.legs[1].netOut(net1)
	net3, _ := c.legs[2].netOut(net2)
	return net3.Sub(consumed)
}

// candidateBounds memoizes the continuous profit at each candidate size
// and turns it into a ceiling on what the exact cycle can be worth there.
// A ladder walk costs a fraction of a QuoteCycle, so a candidate the
// ceiling rules out is skipped for almost nothing.
type candidateBounds struct {
	ladder cycleLadder
	sizes  []decimal.Decimal
	value  []decimal.Decimal
	known  []bool
	peaked bool
	peakAt int
}

func newCandidateBounds(l cycleLadder, sizes []decimal.Decimal) *candidateBounds {
	return &candidateBounds{
		ladder: l,
		sizes:  sizes,
		value:  make([]decimal.Decimal, len(sizes)),
		known:  make([]bool, len(sizes)),
		peakAt: -1,
	}
}

// profitAt is the continuous cycle profit at candidate i.
func (b *candidateBounds) profitAt(i int) decimal.Decimal {
	if !b.known[i] {
		b.value[i] = b.ladder.profit(b.sizes[i])
		b.known[i] = true
	}
	return b.value[i]
}

// ceiling is the most the exact cycle can be worth at candidate i.
//
// The exact walk deploys somewhere in [size - slack, size], never more,
// and the continuous profit is an upper bound at whatever it deploys.
// Strictly below the peak the continuous curve only rises, so deploying
// less can only be worth less and the bound needs no slack at all. From
// the peak outwards the curve falls by at most one unit of profit per
// unit of size, so the slack is the leg-1 truncation — and the result can
// never exceed the peak itself (the peak candidate is where the whole
// curve tops out, the truncation only moves within one slack of a size).
// A book that does not worsen with depth gets the slack everywhere,
// which holds whatever shape the curve has.
func (b *candidateBounds) ceiling(i int) decimal.Decimal {
	p := b.profitAt(i)
	if b.ladder.concave && i < b.peak() {
		return p
	}
	loose := p.Add(b.ladder.dustSlack)
	if b.ladder.concave {
		if top := b.profitAt(b.peak()); top.LessThan(loose) {
			return top
		}
	}
	return loose
}

// peak is the index of the highest continuous profit. When every leg's
// book worsens with depth the curve rises and then falls — one maximum —
// so a binary search on "does the next candidate still improve?" finds it
// with about 2*log2(n) ladder walks instead of n. A book that is not
// ordered from the touch cannot promise that shape and is scanned in
// full.
func (b *candidateBounds) peak() int {
	if b.peaked {
		return b.peakAt
	}
	b.peaked = true
	n := len(b.sizes)
	switch {
	case n == 0:
		b.peakAt = -1
	case !b.ladder.concave:
		best := 0
		for i := 1; i < n; i++ {
			if b.profitAt(i).GreaterThan(b.profitAt(best)) {
				best = i
			}
		}
		b.peakAt = best
	default:
		b.peakAt = sort.Search(n-1, func(i int) bool {
			return !b.profitAt(i + 1).GreaterThan(b.profitAt(i))
		})
	}
	return b.peakAt
}

// candidates are the sizes at which the marginal economics of the cycle
// change: every level boundary of all three legs, mapped back into
// start-asset input units through the leg conversions and their fee
// placement, clamped to [minIn, maxIn] and joined by the two range ends.
// Between two neighbouring candidates the continuous profit curve is a
// straight line, so its maximum over the range is always one of them.
//
// Cost: leg-2/leg-3 boundaries outside the range are dropped by
// comparing them with the range ends mapped forward, so the backward
// walks only run for boundaries that can actually be reached.
func (c cycleLadder) candidates(minIn, maxIn decimal.Decimal) []decimal.Decimal {
	out := make([]decimal.Decimal, 0, len(c.legs[0].cumIn)+len(c.legs[1].cumIn)+len(c.legs[2].cumIn)+2)
	out = append(out, minIn, maxIn)
	inRange := func(v decimal.Decimal) bool {
		return !v.LessThan(minIn) && !v.GreaterThan(maxIn)
	}
	for _, b := range c.legs[0].breakpoints() {
		if inRange(b) {
			out = append(out, b)
		}
	}
	lo2, _ := c.legs[0].netOut(minIn)
	hi2, _ := c.legs[0].netOut(maxIn)
	for _, b := range c.legs[1].breakpoints() {
		if b.LessThan(lo2) || b.GreaterThan(hi2) {
			continue
		}
		if in1, ok := c.legs[0].inputFor(b); ok && inRange(in1) {
			out = append(out, in1)
		}
	}
	lo3, _ := c.legs[1].netOut(lo2)
	hi3, _ := c.legs[1].netOut(hi2)
	for _, b := range c.legs[2].breakpoints() {
		if b.LessThan(lo3) || b.GreaterThan(hi3) {
			continue
		}
		in2, ok := c.legs[1].inputFor(b)
		if !ok {
			continue
		}
		if in1, ok := c.legs[0].inputFor(in2); ok && inRange(in1) {
			out = append(out, in1)
		}
	}
	slices.SortFunc(out, func(a, b decimal.Decimal) int { return a.Cmp(b) })
	return slices.CompactFunc(out, func(a, b decimal.Decimal) bool { return a.Equal(b) })
}

// geometricSizes lays out a coarse pass over [minIn, maxIn] whose
// resolution is relative to the size rather than to the span: successive
// doublings, or 4x, 8x ... when the span needs more doublings than there
// are points. A uniform grid resolves span/(n-1) everywhere, so on a
// range spanning orders of magnitude it steps straight over a profitable
// pocket at the small end (audit T1 / roadmap P1-9). Both range ends are
// always included and every point is exact: the multiplier is a power of
// two.
func geometricSizes(minIn, maxIn decimal.Decimal, n int) []decimal.Decimal {
	out := make([]decimal.Decimal, 0, n)
	if !minIn.IsPositive() {
		// No geometric ladder starts at zero; a uniform pass is the only
		// option and the range is bounded anyway.
		span := maxIn.Sub(minIn)
		div := decimal.NewFromInt(int64(n - 1))
		for i := 0; i < n; i++ {
			out = append(out, minIn.Add(span.Mul(decimal.NewFromInt(int64(i))).Div(div)))
		}
		return out
	}
	// Doublings needed to span the range, capped so an absurd ratio can
	// never spin here.
	const maxDoublings = 512
	doublings := 0
	for cur := minIn; cur.LessThan(maxIn) && doublings < maxDoublings; doublings++ {
		cur = cur.Add(cur)
	}
	stride := (doublings + n - 2) / (n - 1) // ceil(doublings / (n-1))
	if stride < 1 {
		stride = 1
	}
	mul := one
	for i := 0; i < stride; i++ {
		mul = mul.Add(mul) // 2^stride, exact
	}
	cur := minIn
	for len(out) < n-1 && cur.LessThan(maxIn) {
		out = append(out, cur)
		cur = cur.Mul(mul)
	}
	return append(out, maxIn)
}
