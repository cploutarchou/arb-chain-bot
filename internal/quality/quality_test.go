package quality

import (
	"strings"
	"testing"

	"github.com/shopspring/decimal"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

// Hand-computed vector (defaults: ref profit 1/cycle, full sample 50,
// min credible 20, slippage level cap 50, std cap 20, severe loss
// 10×ref, drawdown cap 0.05, unprofitable cap 40):
//
//	profitability: 25 cycles, +25 net → 1/cycle = ref       → 25.00
//	sample size:   25/50 × 10                               →  5.00
//	success:       20/25 × 15                               → 12.00
//	slippage lvl:  avg 5 → (1−5/50) × 8                     →  7.20
//	slippage disp: std 5 → (1−5/20) × 7                     →  5.25
//	persistence:   12/24 hours × 15                         →  7.50
//	severity:      worst −2 → (1−2/10) × 10                 →  8.00
//	drawdown:      unmeasured                               →  0.00
//	sum 69.95; 25 ≥ 20 credible cycles → no scaling → total 70
func TestScoreHandComputedVector(t *testing.T) {
	b := Score(Sample{
		TriangleID: "tri-a", Cycles: 25, Successes: 20,
		NetPnL: d("25"), AvgSlippageBps: d("5"), SlippageStdBps: d("5"),
		SlippageSamples: 20,
		EdgeWindows:     12, WindowHours: 24,
		WorstLoss: d("-2"),
	}, Config{})
	if b.Total != 70 || b.TotalExact != "69.9500" {
		t.Fatalf("total = %d / %s, want 70 / 69.9500 (%+v)", b.Total, b.TotalExact, b.Components)
	}
	want := map[string]string{
		"profitability":       "25.00",
		"sample_size":         "5.00",
		"execution_success":   "12.00",
		"slippage_level":      "7.20",
		"slippage_dispersion": "5.25",
		"edge_persistence":    "7.50",
		"failure_severity":    "8.00",
		"drawdown":            "0.00",
	}
	for k, v := range want {
		if b.Components[k] != v {
			t.Errorf("%s = %s, want %s", k, b.Components[k], v)
		}
	}
	if !hasNote(b, "drawdown unmeasured") {
		t.Fatalf("missing drawdown note: %v", b.Notes)
	}
}

// Audit P0-1 counterexample: a reliable money-loser must never outrank
// a genuine earner. A' loses 10 over 50 perfect-looking cycles; B earns
// 50 with rougher execution.
func TestReliableLoserNeverOutranksEarner(t *testing.T) {
	loser := Score(Sample{
		TriangleID: "a-prime", Cycles: 50, Successes: 50,
		NetPnL: d("-10"), SlippageSamples: 50,
		EdgeWindows: 24, WindowHours: 24, WorstLoss: d("-0.5"),
	}, Config{})
	earner := Score(Sample{
		TriangleID: "b", Cycles: 50, Successes: 30,
		NetPnL: d("50"), SlippageStdBps: d("10"), SlippageSamples: 30,
		EdgeWindows: 12, WindowHours: 24, WorstLoss: d("-5"),
	}, Config{})
	if loser.Total >= earner.Total {
		t.Fatalf("loser %d >= earner %d", loser.Total, earner.Total)
	}
	if loser.Total != 40 {
		t.Fatalf("unprofitable cap = %d, want 40 (%+v)", loser.Total, loser.Components)
	}
	if !hasNote(loser, "unprofitable in window") {
		t.Fatalf("missing cap note: %v", loser.Notes)
	}
}

// Property: any non-positive PnL-per-cycle sample stays at or below the
// cap, and adding full-reference profitability to the same evidence
// always wins strictly.
func TestUnprofitableCapProperty(t *testing.T) {
	for _, cycles := range []int{1, 5, 20, 50, 500} {
		for _, pnl := range []string{"0", "-0.01", "-10", "-1000"} {
			for _, ew := range []int{0, 12, 24} {
				base := Sample{
					TriangleID: "x", Cycles: cycles, Successes: cycles,
					NetPnL: d(pnl), SlippageSamples: cycles,
					EdgeWindows: ew, WindowHours: 24,
				}
				x := Score(base, Config{})
				if x.exact.GreaterThan(d("40")) {
					t.Fatalf("cycles=%d pnl=%s ew=%d: total %s exceeds cap", cycles, pnl, ew, x.TotalExact)
				}
				earner := base
				earner.NetPnL = decimal.NewFromInt(int64(cycles)) // ref profit per cycle
				y := Score(earner, Config{})
				if !y.exact.GreaterThan(x.exact) {
					t.Fatalf("cycles=%d pnl=%s ew=%d: earner %s <= loser %s", cycles, pnl, ew, y.TotalExact, x.TotalExact)
				}
			}
		}
	}
}

// Break-even earns zero profitability points (no free half-credit).
func TestBreakEvenEarnsNoProfitabilityPoints(t *testing.T) {
	b := Score(Sample{
		TriangleID: "flat", Cycles: 25, Successes: 25,
		NetPnL: d("0"), SlippageSamples: 25, EdgeWindows: 24, WindowHours: 24,
	}, Config{})
	if b.Components["profitability"] != "0.00" {
		t.Fatalf("break-even profitability = %s", b.Components["profitability"])
	}
}

// Audit P1-2 counterexample: one lucky cycle must not rank near 200
// cycles of real evidence.
func TestThinSampleCannotOutrankRealEvidence(t *testing.T) {
	thin := Score(Sample{
		TriangleID: "thin", Cycles: 1, Successes: 1,
		NetPnL: d("1"), SlippageSamples: 1,
		EdgeWindows: 24, WindowHours: 24,
	}, Config{})
	real := Score(Sample{
		TriangleID: "real", Cycles: 200, Successes: 190,
		NetPnL: d("150"), AvgSlippageBps: d("4"), SlippageStdBps: d("8"),
		SlippageSamples: 190,
		EdgeWindows:     24, WindowHours: 24, WorstLoss: d("-8"),
	}, Config{})
	if thin.Total >= real.Total {
		t.Fatalf("thin %d >= real %d", thin.Total, real.Total)
	}
	if thin.Total > 5 {
		t.Fatalf("thin sample scored %d; confidence scaling ineffective (%+v)", thin.Total, thin.Components)
	}
	if !hasNote(thin, "low sample confidence") {
		t.Fatalf("missing confidence note: %v", thin.Notes)
	}
	if !hasNote(thin, "slippage unmeasured") {
		t.Fatalf("single slippage sample must be unmeasured: %v", thin.Notes)
	}

	// Property: thin (<5 cycles) never beats >=50-cycle evidence with
	// per-cycle PnL at least as good.
	for _, tc := range []int{0, 1, 2, 4} {
		for _, perCycle := range []string{"-1", "0", "0.5", "1"} {
			x := Score(Sample{
				TriangleID: "x", Cycles: tc, Successes: tc,
				NetPnL: d(perCycle).Mul(decimal.NewFromInt(int64(tc))), SlippageSamples: tc,
				EdgeWindows: 24, WindowHours: 24,
			}, Config{})
			y := Score(Sample{
				TriangleID: "y", Cycles: 50, Successes: 50,
				NetPnL: d(perCycle).Mul(decimal.NewFromInt(50)), SlippageSamples: 50,
				EdgeWindows: 24, WindowHours: 24,
			}, Config{})
			if x.exact.GreaterThan(y.exact) {
				t.Fatalf("thin cycles=%d perCycle=%s: %s > %s", tc, perCycle, x.TotalExact, y.TotalExact)
			}
		}
	}
}

// Audit P1-1: absence of evidence must award nothing.
func TestAbsentEvidenceEarnsNothing(t *testing.T) {
	// 50 cycles, zero completions, catastrophic PnL: no stability,
	// severity from real losses, capped and honest.
	b := Score(Sample{
		TriangleID: "broken", Cycles: 50, Successes: 0,
		NetPnL: d("-100"), SlippageSamples: 0,
		EdgeWindows: 24, WindowHours: 24, WorstLoss: d("-50"),
	}, Config{})
	if b.Components["slippage_level"] != "0.00" || b.Components["slippage_dispersion"] != "0.00" {
		t.Fatalf("unmeasured slippage credited: %+v", b.Components)
	}
	if b.Components["failure_severity"] != "0.00" { // -50 loss = 5× severe threshold
		t.Fatalf("severity = %s for a -50 worst loss", b.Components["failure_severity"])
	}
	if b.Components["drawdown"] != "0.00" {
		t.Fatalf("unmeasured drawdown credited: %s", b.Components["drawdown"])
	}

	// The empty sample scores (almost) nothing.
	empty := Score(Sample{TriangleID: "idle", WindowHours: 24}, Config{})
	if empty.Total > 10 {
		t.Fatalf("empty sample total = %d", empty.Total)
	}
	for _, want := range []string{"no settled cycles", "slippage unmeasured", "drawdown unmeasured", "low sample confidence"} {
		if !hasNote(empty, want) {
			t.Fatalf("empty sample missing note %q: %v", want, empty.Notes)
		}
	}
}

// Audit P2-6: reliably-terrible slippage must score below reliably-good.
func TestSlippageLevelMatters(t *testing.T) {
	mk := func(avg string) Breakdown {
		return Score(Sample{
			TriangleID: "s", Cycles: 50, Successes: 50, NetPnL: d("50"),
			AvgSlippageBps: d(avg), SlippageStdBps: d("0"), SlippageSamples: 50,
			EdgeWindows: 24, WindowHours: 24,
		}, Config{})
	}
	good, bad, atCap := mk("0"), mk("100"), mk("50")
	if bad.exact.GreaterThanOrEqual(good.exact) {
		t.Fatalf("avg 100 bps (%s) >= avg 0 (%s)", bad.TotalExact, good.TotalExact)
	}
	if atCap.Components["slippage_level"] != "0.00" {
		t.Fatalf("level at cap = %s, want 0.00", atCap.Components["slippage_level"])
	}
}

// A positive "worst loss" is a profit, not a penalty (P3 guard).
func TestPositiveWorstLossIsNotPenalized(t *testing.T) {
	b := Score(Sample{
		TriangleID: "p", Cycles: 25, Successes: 25, NetPnL: d("25"),
		SlippageSamples: 25, WorstLoss: d("5"), WindowHours: 24,
	}, Config{})
	if b.Components["failure_severity"] != "10.00" {
		t.Fatalf("severity = %s with positive worst loss", b.Components["failure_severity"])
	}
}

// Components always sum to the pre-cap/pre-scaling story and Total is
// consistent with TotalExact.
func TestComponentsSumConsistency(t *testing.T) {
	b := Score(Sample{
		TriangleID: "c", Cycles: 30, Successes: 20, NetPnL: d("7.7777"),
		AvgSlippageBps: d("3.3"), SlippageStdBps: d("1.7"), SlippageSamples: 18,
		EdgeWindows: 5, WindowHours: 24, WorstLoss: d("-1.23"),
	}, Config{ReferenceProfitPerCycle: d("0.33")})
	sum := decimal.Zero
	for _, v := range b.Components {
		sum = sum.Add(d(v))
	}
	// No cap/scaling applies here (profitable, 30 >= 20 cycles).
	if b.TotalExact != sum.StringFixed(4) {
		t.Fatalf("TotalExact %s != component sum %s", b.TotalExact, sum.StringFixed(4))
	}
}

func TestRankDeterministicOnExactTotals(t *testing.T) {
	mk := func(id string, pnl string) Sample {
		return Sample{TriangleID: id, Cycles: 25, Successes: 20, NetPnL: d(pnl),
			SlippageSamples: 20, EdgeWindows: 12, WindowHours: 24}
	}
	best := mk("c", "25")
	best.Successes = 25 // strictly better execution than the tied pair
	ranked := Rank([]Sample{mk("b", "25"), mk("a", "25"), best}, Config{})
	if ranked[0].TriangleID != "c" || ranked[1].TriangleID != "a" || ranked[2].TriangleID != "b" {
		t.Fatalf("order = %s,%s,%s", ranked[0].TriangleID, ranked[1].TriangleID, ranked[2].TriangleID)
	}
	// Near-ties resolve on the exact decimal, not the rounded int
	// (sub-reference PnL so the profitability ratio is not clamped).
	x := Score(mk("x", "20.1"), Config{})
	y := Score(mk("y", "20"), Config{})
	if !x.exact.GreaterThan(y.exact) {
		t.Fatalf("exact ordering lost: %s vs %s", x.TotalExact, y.TotalExact)
	}
}

// Bounds: total stays in [0,100] across a hostile grid.
func TestScoreBounds(t *testing.T) {
	for _, cycles := range []int{-5, 0, 1, 50, 5000} {
		for _, succ := range []int{-1, 0, 10_000} {
			for _, pnl := range []string{"-1e6", "0", "1e6"} {
				b := Score(Sample{
					TriangleID: "b", Cycles: cycles, Successes: succ, NetPnL: d(pnl),
					SlippageSamples: cycles, SlippageStdBps: d("-5"),
					EdgeWindows: 100, WindowHours: 24,
					MaxDrawdown: d("2"), DrawdownKnown: true,
					WorstLoss: d("-1e6"),
				}, Config{})
				if b.Total < 0 || b.Total > 100 {
					t.Fatalf("cycles=%d succ=%d pnl=%s: total %d out of bounds", cycles, succ, pnl, b.Total)
				}
			}
		}
	}
}

func hasNote(b Breakdown, substr string) bool {
	for _, n := range b.Notes {
		if strings.Contains(n, substr) {
			return true
		}
	}
	return false
}
