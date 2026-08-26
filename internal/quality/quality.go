// Package quality computes the TriangleQualityScore (/100, SKILL §81):
// a deterministic decimal composite over a triangle's recorded history.
// Profitability carries the largest weight and execution success a
// deliberately modest one — the score never optimizes purely for win
// rate, and thin samples are marked as such rather than trusted.
package quality

import (
	"sort"

	"github.com/shopspring/decimal"
)

// Sample is one triangle's aggregated history over the scoring window.
// Money values are in the triangle's start asset.
type Sample struct {
	TriangleID string

	Cycles    int // settled paper cycles (sample size)
	Successes int // ALL_FILLED outcomes

	NetPnL         decimal.Decimal // sum of realized PnL
	AvgSlippageBps decimal.Decimal // mean over complete cycles
	SlippageStdBps decimal.Decimal // dispersion (stability)

	EdgeWindows int             // distinct hours with >=1 qualified opportunity
	WindowHours int             // scoring window length in hours
	WorstLoss   decimal.Decimal // most negative single-cycle PnL (<= 0)
	MaxDrawdown decimal.Decimal // 0..1 fraction observed
}

// Config scales the components. Zero values take documented defaults.
type Config struct {
	// ReferenceProfitPerCycle maps profitability to points: this profit
	// per cycle earns the full profitability score.
	ReferenceProfitPerCycle decimal.Decimal
	// FullSampleCycles saturates the sample-size component.
	FullSampleCycles int
	// SlippageStdCapBps zeroes stability at/beyond this dispersion.
	SlippageStdCapBps decimal.Decimal
	// SevereLossMultiple: a single loss of this many reference profits
	// zeroes the failure-severity component.
	SevereLossMultiple decimal.Decimal
	// DrawdownCap zeroes the drawdown component at/beyond this fraction.
	DrawdownCap decimal.Decimal
}

func (c Config) withDefaults() Config {
	if !c.ReferenceProfitPerCycle.IsPositive() {
		c.ReferenceProfitPerCycle = decimal.NewFromInt(1)
	}
	if c.FullSampleCycles <= 0 {
		c.FullSampleCycles = 50
	}
	if !c.SlippageStdCapBps.IsPositive() {
		c.SlippageStdCapBps = decimal.NewFromInt(20)
	}
	if !c.SevereLossMultiple.IsPositive() {
		c.SevereLossMultiple = decimal.NewFromInt(10)
	}
	if !c.DrawdownCap.IsPositive() {
		c.DrawdownCap = decimal.RequireFromString("0.05")
	}
	return c
}

// Component weights (sum 100). Success is intentionally 15: a triangle
// that wins often but earns nothing ranks below one that earns.
var weights = map[string]decimal.Decimal{
	"profitability":      decimal.NewFromInt(25),
	"sample_size":        decimal.NewFromInt(10),
	"execution_success":  decimal.NewFromInt(15),
	"slippage_stability": decimal.NewFromInt(15),
	"edge_persistence":   decimal.NewFromInt(15),
	"failure_severity":   decimal.NewFromInt(10),
	"drawdown":           decimal.NewFromInt(10),
}

// Breakdown is the scored result.
type Breakdown struct {
	TriangleID string            `json:"triangle_id"`
	Total      int               `json:"total"` // 0..100
	Components map[string]string `json:"components"`
	Notes      []string          `json:"notes,omitempty"`
	Cycles     int               `json:"cycles"`
}

// Score computes the breakdown for one sample.
func Score(s Sample, cfg Config) Breakdown {
	cfg = cfg.withDefaults()
	one := decimal.NewFromInt(1)
	half := decimal.RequireFromString("0.5")
	comp := map[string]decimal.Decimal{}
	var notes []string

	// Profitability (25): linear around zero — zero PnL/cycle earns
	// half, +reference earns full, -reference earns none.
	if s.Cycles > 0 {
		perCycle := s.NetPnL.Div(decimal.NewFromInt(int64(s.Cycles)))
		ratio := clamp(perCycle.Div(cfg.ReferenceProfitPerCycle), one.Neg(), one)
		comp["profitability"] = weights["profitability"].Mul(half.Add(half.Mul(ratio)))
	} else {
		comp["profitability"] = decimal.Zero
		notes = append(notes, "no settled cycles: profitability unmeasured")
	}

	// Sample size (10): saturates at FullSampleCycles.
	sizeFrac := clamp(decimal.NewFromInt(int64(s.Cycles)).
		Div(decimal.NewFromInt(int64(cfg.FullSampleCycles))), decimal.Zero, one)
	comp["sample_size"] = weights["sample_size"].Mul(sizeFrac)

	// Execution success (15): success ratio; capped at half credit on
	// tiny samples so a lucky 2/2 cannot outrank real evidence.
	if s.Cycles > 0 {
		ratio := decimal.NewFromInt(int64(s.Successes)).Div(decimal.NewFromInt(int64(s.Cycles)))
		points := weights["execution_success"].Mul(clamp(ratio, decimal.Zero, one))
		if s.Cycles < 5 {
			capped := weights["execution_success"].Mul(half)
			if points.GreaterThan(capped) {
				points = capped
				notes = append(notes, "success capped: fewer than 5 cycles")
			}
		}
		comp["execution_success"] = points
	} else {
		comp["execution_success"] = decimal.Zero
	}

	// Slippage stability (15): full at zero dispersion, zero at cap.
	stabFrac := clamp(one.Sub(s.SlippageStdBps.Div(cfg.SlippageStdCapBps)), decimal.Zero, one)
	comp["slippage_stability"] = weights["slippage_stability"].Mul(stabFrac)

	// Edge persistence (15): fraction of window hours with an edge.
	if s.WindowHours > 0 {
		persist := clamp(decimal.NewFromInt(int64(s.EdgeWindows)).
			Div(decimal.NewFromInt(int64(s.WindowHours))), decimal.Zero, one)
		comp["edge_persistence"] = weights["edge_persistence"].Mul(persist)
	} else {
		comp["edge_persistence"] = decimal.Zero
	}

	// Failure severity (10): the worst single loss relative to the
	// severe-loss threshold (SevereLossMultiple × reference profit).
	severe := cfg.ReferenceProfitPerCycle.Mul(cfg.SevereLossMultiple)
	sevFrac := clamp(one.Sub(s.WorstLoss.Abs().Div(severe)), decimal.Zero, one)
	comp["failure_severity"] = weights["failure_severity"].Mul(sevFrac)

	// Drawdown (10): full at zero, zero at the cap.
	ddFrac := clamp(one.Sub(s.MaxDrawdown.Div(cfg.DrawdownCap)), decimal.Zero, one)
	comp["drawdown"] = weights["drawdown"].Mul(ddFrac)

	total := decimal.Zero
	rendered := make(map[string]string, len(comp))
	for name, v := range comp {
		total = total.Add(v)
		rendered[name] = v.StringFixed(2)
	}
	return Breakdown{
		TriangleID: s.TriangleID,
		Total:      int(total.Round(0).IntPart()),
		Components: rendered,
		Notes:      notes,
		Cycles:     s.Cycles,
	}
}

// Rank scores every sample and orders by total descending (ties by ID
// for determinism).
func Rank(samples []Sample, cfg Config) []Breakdown {
	out := make([]Breakdown, 0, len(samples))
	for _, s := range samples {
		out = append(out, Score(s, cfg))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Total != out[j].Total {
			return out[i].Total > out[j].Total
		}
		return out[i].TriangleID < out[j].TriangleID
	})
	return out
}

func clamp(v, lo, hi decimal.Decimal) decimal.Decimal {
	if v.LessThan(lo) {
		return lo
	}
	if v.GreaterThan(hi) {
		return hi
	}
	return v
}
