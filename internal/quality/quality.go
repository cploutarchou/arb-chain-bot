// Package quality computes the TriangleQualityScore (/100, SKILL §81):
// a deterministic decimal composite over a triangle's recorded history.
// Design rules (audit-hardened):
//   - Profitability gates the score: break-even earns zero profitability
//     points and a non-positive PnL-per-cycle hard-caps the total, so a
//     reliable loser can never outrank a genuine earner.
//   - Absent evidence earns nothing: unmeasured dispersion, drawdown, or
//     severity award zero with an explicit note, never full credit.
//   - The whole composite scales with sample confidence, so a lucky
//     handful of cycles cannot outrank real evidence.
package quality

import (
	"fmt"
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
	AvgSlippageBps decimal.Decimal // mean over slippage-measurable cycles
	SlippageStdBps decimal.Decimal // dispersion over the same population
	// SlippageSamples counts the cycles behind the two numbers above;
	// below 2 the slippage components are unmeasured.
	SlippageSamples int

	EdgeWindows int             // distinct hours with >=1 qualified opportunity
	WindowHours int             // hour buckets the scoring window touches
	WorstLoss   decimal.Decimal // most negative single-cycle PnL (<= 0)

	// MaxDrawdown is a 0..1 fraction; DrawdownKnown=false marks it
	// unmeasured (per-triangle drawdown is not recorded yet) and awards
	// zero rather than pretending a perfect record.
	MaxDrawdown   decimal.Decimal
	DrawdownKnown bool
}

// Config scales the components. Zero values take documented defaults.
type Config struct {
	// ReferenceProfitPerCycle maps profitability to points: this profit
	// per cycle earns the full profitability score; break-even earns 0.
	ReferenceProfitPerCycle decimal.Decimal
	// FullSampleCycles saturates the sample-size component.
	FullSampleCycles int
	// MinCredibleCycles scales the WHOLE composite: below this cycle
	// count the total is multiplied by Cycles/MinCredibleCycles.
	MinCredibleCycles int
	// SlippageLevelCapBps zeroes the slippage-level component at/beyond
	// this mean slippage (align with risk.max_slippage_bps).
	SlippageLevelCapBps decimal.Decimal
	// SlippageStdCapBps zeroes the dispersion component at/beyond this.
	SlippageStdCapBps decimal.Decimal
	// SevereLossMultiple: a single loss of this many reference profits
	// zeroes the failure-severity component.
	SevereLossMultiple decimal.Decimal
	// DrawdownCap zeroes the drawdown component at/beyond this fraction.
	DrawdownCap decimal.Decimal
	// UnprofitableCap is the hard total ceiling for a triangle whose
	// PnL-per-cycle is non-positive in the window.
	UnprofitableCap decimal.Decimal
}

func (c Config) withDefaults() Config {
	if !c.ReferenceProfitPerCycle.IsPositive() {
		c.ReferenceProfitPerCycle = decimal.NewFromInt(1)
	}
	if c.FullSampleCycles <= 0 {
		c.FullSampleCycles = 50
	}
	if c.MinCredibleCycles <= 0 {
		c.MinCredibleCycles = 20
	}
	if !c.SlippageLevelCapBps.IsPositive() {
		c.SlippageLevelCapBps = decimal.NewFromInt(50)
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
	if !c.UnprofitableCap.IsPositive() {
		c.UnprofitableCap = decimal.NewFromInt(40)
	}
	return c
}

// Component weights (sum 100). Success is deliberately modest and
// profitability both weighs the most AND gates the total: winning often
// while earning nothing never outranks earning.
var weights = map[string]decimal.Decimal{
	"profitability":       decimal.NewFromInt(25),
	"sample_size":         decimal.NewFromInt(10),
	"execution_success":   decimal.NewFromInt(15),
	"slippage_level":      decimal.NewFromInt(8),
	"slippage_dispersion": decimal.NewFromInt(7),
	"edge_persistence":    decimal.NewFromInt(15),
	"failure_severity":    decimal.NewFromInt(10),
	"drawdown":            decimal.NewFromInt(10),
}

// Breakdown is the scored result. Components are rounded to 2dp and
// Total is derived from those same rendered values, so the parts always
// sum to the whole; TotalExact carries the pre-rounding decimal.
type Breakdown struct {
	TriangleID string            `json:"triangle_id"`
	Total      int               `json:"total"` // 0..100
	TotalExact string            `json:"total_exact"`
	Components map[string]string `json:"components"`
	Notes      []string          `json:"notes,omitempty"`
	Cycles     int               `json:"cycles"`

	exact decimal.Decimal
}

// Score computes the breakdown for one sample.
func Score(s Sample, cfg Config) Breakdown {
	cfg = cfg.withDefaults()
	one := decimal.NewFromInt(1)
	half := decimal.RequireFromString("0.5")
	comp := map[string]decimal.Decimal{}
	var notes []string
	unprofitable := false

	// Profitability (25): zero at break-even, full at +reference. A
	// non-positive per-cycle PnL additionally caps the whole score.
	if s.Cycles > 0 {
		perCycle := s.NetPnL.Div(decimal.NewFromInt(int64(s.Cycles)))
		ratio := clamp(perCycle.Div(cfg.ReferenceProfitPerCycle), decimal.Zero, one)
		comp["profitability"] = weights["profitability"].Mul(ratio)
		if !perCycle.IsPositive() {
			unprofitable = true
			notes = append(notes, fmt.Sprintf(
				"unprofitable in window (PnL/cycle %s): total capped at %s",
				perCycle.StringFixed(6), cfg.UnprofitableCap.StringFixed(0)))
		}
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

	// Slippage (8 level + 7 dispersion): measured only with >= 2
	// slippage-bearing cycles; NULL/absent dispersion is not "stable".
	if s.SlippageSamples >= 2 {
		levelFrac := clamp(one.Sub(s.AvgSlippageBps.Div(cfg.SlippageLevelCapBps)), decimal.Zero, one)
		comp["slippage_level"] = weights["slippage_level"].Mul(levelFrac)
		stdFrac := clamp(one.Sub(s.SlippageStdBps.Div(cfg.SlippageStdCapBps)), decimal.Zero, one)
		comp["slippage_dispersion"] = weights["slippage_dispersion"].Mul(stdFrac)
	} else {
		comp["slippage_level"] = decimal.Zero
		comp["slippage_dispersion"] = decimal.Zero
		notes = append(notes, "slippage unmeasured: fewer than 2 slippage-bearing cycles")
	}

	// Edge persistence (15): fraction of window hours with an edge.
	if s.WindowHours > 0 {
		persist := clamp(decimal.NewFromInt(int64(s.EdgeWindows)).
			Div(decimal.NewFromInt(int64(s.WindowHours))), decimal.Zero, one)
		comp["edge_persistence"] = weights["edge_persistence"].Mul(persist)
	} else {
		comp["edge_persistence"] = decimal.Zero
	}

	// Failure severity (10): needs at least one cycle of evidence; a
	// positive "worst loss" is a profit and counts as zero loss.
	if s.Cycles > 0 {
		loss := decimal.Min(s.WorstLoss, decimal.Zero)
		severe := cfg.ReferenceProfitPerCycle.Mul(cfg.SevereLossMultiple)
		sevFrac := clamp(one.Sub(loss.Abs().Div(severe)), decimal.Zero, one)
		comp["failure_severity"] = weights["failure_severity"].Mul(sevFrac)
	} else {
		comp["failure_severity"] = decimal.Zero
	}

	// Drawdown (10): only when actually measured.
	if s.DrawdownKnown {
		ddFrac := clamp(one.Sub(s.MaxDrawdown.Div(cfg.DrawdownCap)), decimal.Zero, one)
		comp["drawdown"] = weights["drawdown"].Mul(ddFrac)
	} else {
		comp["drawdown"] = decimal.Zero
		notes = append(notes, "drawdown unmeasured: per-triangle drawdown not recorded")
	}

	// Round each component once; the total is the sum of what is shown.
	rendered := make(map[string]string, len(comp))
	total := decimal.Zero
	for name, v := range comp {
		r := v.Round(2)
		rendered[name] = r.StringFixed(2)
		total = total.Add(r)
	}

	// Unprofitable cap, then sample-confidence scaling.
	if unprofitable && total.GreaterThan(cfg.UnprofitableCap) {
		total = cfg.UnprofitableCap
	}
	if s.Cycles < cfg.MinCredibleCycles {
		confidence := decimal.NewFromInt(int64(max(s.Cycles, 0))).
			Div(decimal.NewFromInt(int64(cfg.MinCredibleCycles)))
		total = total.Mul(confidence)
		notes = append(notes, fmt.Sprintf(
			"low sample confidence: %d/%d cycles scales the total", s.Cycles, cfg.MinCredibleCycles))
	}

	return Breakdown{
		TriangleID: s.TriangleID,
		Total:      int(total.Round(0).IntPart()),
		TotalExact: total.StringFixed(4),
		Components: rendered,
		Notes:      notes,
		Cycles:     s.Cycles,
		exact:      total,
	}
}

// Rank scores every sample and orders by the exact total descending
// (ties by ID for determinism).
func Rank(samples []Sample, cfg Config) []Breakdown {
	out := make([]Breakdown, 0, len(samples))
	for _, s := range samples {
		out = append(out, Score(s, cfg))
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].exact.Equal(out[j].exact) {
			return out[i].exact.GreaterThan(out[j].exact)
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
