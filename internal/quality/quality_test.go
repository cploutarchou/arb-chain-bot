package quality

import (
	"testing"

	"github.com/shopspring/decimal"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

// Hand-computed vector (defaults: ref profit 1/cycle, full sample 50,
// slippage cap 20 bps, severe loss 10×ref, drawdown cap 0.05):
//
//	profitability: 25 cycles, +25 net → 1/cycle = ref → 25.00
//	sample size:   25/50 × 10                        →  5.00
//	success:       20/25 × 15                        → 12.00
//	stability:     std 5 → (1−5/20) × 15             → 11.25
//	persistence:   12/24 hours × 15                  →  7.50
//	severity:      worst −2 → (1−2/10) × 10          →  8.00
//	drawdown:      0.01 → (1−0.01/0.05) × 10         →  8.00
//	total 76.75 → 77
func TestScoreHandComputedVector(t *testing.T) {
	b := Score(Sample{
		TriangleID: "tri-a", Cycles: 25, Successes: 20,
		NetPnL: d("25"), SlippageStdBps: d("5"),
		EdgeWindows: 12, WindowHours: 24,
		WorstLoss: d("-2"), MaxDrawdown: d("0.01"),
	}, Config{})
	if b.Total != 77 {
		t.Fatalf("total = %d, want 77 (%+v)", b.Total, b.Components)
	}
	want := map[string]string{
		"profitability":      "25.00",
		"sample_size":        "5.00",
		"execution_success":  "12.00",
		"slippage_stability": "11.25",
		"edge_persistence":   "7.50",
		"failure_severity":   "8.00",
		"drawdown":           "8.00",
	}
	for k, v := range want {
		if b.Components[k] != v {
			t.Errorf("%s = %s, want %s", k, b.Components[k], v)
		}
	}
}

// Win rate alone must not dominate: a 100%-success triangle that earns
// nothing ranks below a 60%-success one that earns the reference.
func TestNeverOptimizesPurelyForWinRate(t *testing.T) {
	winner := Score(Sample{
		TriangleID: "earns", Cycles: 25, Successes: 15,
		NetPnL: d("25"), SlippageStdBps: d("5"),
		EdgeWindows: 12, WindowHours: 24, MaxDrawdown: d("0.01"),
	}, Config{})
	flat := Score(Sample{
		TriangleID: "wins-flat", Cycles: 25, Successes: 25,
		NetPnL: d("0"), SlippageStdBps: d("5"),
		EdgeWindows: 12, WindowHours: 24, MaxDrawdown: d("0.01"),
	}, Config{})
	if flat.Total >= winner.Total {
		t.Fatalf("flat 100%% win rate (%d) outranked profitable 60%% (%d)", flat.Total, winner.Total)
	}
}

func TestThinSampleIsCappedAndNoted(t *testing.T) {
	b := Score(Sample{
		TriangleID: "thin", Cycles: 2, Successes: 2,
		NetPnL: d("2"), EdgeWindows: 1, WindowHours: 24,
	}, Config{})
	if b.Components["execution_success"] != "7.50" {
		t.Fatalf("thin success = %s, want capped 7.50", b.Components["execution_success"])
	}
	found := false
	for _, n := range b.Notes {
		if n == "success capped: fewer than 5 cycles" {
			found = true
		}
	}
	if !found {
		t.Fatalf("cap note missing: %v", b.Notes)
	}
}

func TestZeroCyclesHonest(t *testing.T) {
	b := Score(Sample{TriangleID: "idle", WindowHours: 24}, Config{})
	if b.Components["profitability"] != "0.00" || len(b.Notes) == 0 {
		t.Fatalf("idle = %+v", b)
	}
}

func TestSevereLossAndDrawdownZeroTheirComponents(t *testing.T) {
	b := Score(Sample{
		TriangleID: "bad", Cycles: 10, Successes: 5,
		NetPnL: d("-30"), WorstLoss: d("-15"), MaxDrawdown: d("0.10"),
		SlippageStdBps: d("40"), WindowHours: 24,
	}, Config{})
	for _, k := range []string{"failure_severity", "drawdown", "slippage_stability", "profitability"} {
		if b.Components[k] != "0.00" {
			t.Errorf("%s = %s, want 0.00", k, b.Components[k])
		}
	}
}

func TestRankDeterministic(t *testing.T) {
	samples := []Sample{
		{TriangleID: "b", Cycles: 25, Successes: 20, NetPnL: d("25"), EdgeWindows: 12, WindowHours: 24},
		{TriangleID: "a", Cycles: 25, Successes: 20, NetPnL: d("25"), EdgeWindows: 12, WindowHours: 24},
		{TriangleID: "c", Cycles: 25, Successes: 25, NetPnL: d("50"), EdgeWindows: 24, WindowHours: 24},
	}
	ranked := Rank(samples, Config{})
	if ranked[0].TriangleID != "c" || ranked[1].TriangleID != "a" || ranked[2].TriangleID != "b" {
		t.Fatalf("order = %s,%s,%s", ranked[0].TriangleID, ranked[1].TriangleID, ranked[2].TriangleID)
	}
}
