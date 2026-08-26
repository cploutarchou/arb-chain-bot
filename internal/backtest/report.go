package backtest

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/execution"
)

// DefaultGrid is the §80 stress grid: each axis alone, then an adverse
// combination. A system that only earns in the baseline is flagged.
func DefaultGrid() []Scenario {
	one := decimal.NewFromInt(1)
	half := decimal.RequireFromString("0.5")
	return []Scenario{
		{Name: "baseline", LatencyScale: 1, FillDepthFactor: one, WorldDepthFactor: one},
		{Name: "fees+5bps", FeeBumpBps: 5, LatencyScale: 1, FillDepthFactor: one, WorldDepthFactor: one},
		{Name: "fees+10bps", FeeBumpBps: 10, LatencyScale: 1, FillDepthFactor: one, WorldDepthFactor: one},
		{Name: "latency-x2", LatencyScale: 2, FillDepthFactor: one, WorldDepthFactor: one},
		{Name: "latency-x4", LatencyScale: 4, FillDepthFactor: one, WorldDepthFactor: one},
		{Name: "fills-50pct", LatencyScale: 1, FillDepthFactor: half, WorldDepthFactor: one},
		{Name: "liquidity-50pct", LatencyScale: 1, FillDepthFactor: one, WorldDepthFactor: half},
		{Name: "adverse-combo", FeeBumpBps: 10, LatencyScale: 3, FillDepthFactor: half, WorldDepthFactor: half},
	}
}

// Campaign aggregates every (scenario, seed) run over one recording into
// the §80 report.
type Campaign struct {
	Recording   string    `json:"recording"`
	GeneratedAt time.Time `json:"generated_at"`
	Results     []Result  `json:"results"`
}

// scenarioAgg is the roll-up of one scenario across seeds.
type scenarioAgg struct {
	name        string
	seeds       int
	cycles      int
	allFilled   int
	failed      int // reached execution, did not complete
	netPnL      decimal.Decimal
	feesPaid    decimal.Decimal
	turnover    decimal.Decimal
	worstDD     decimal.Decimal
	grossBpsSum decimal.Decimal
	netBpsSum   decimal.Decimal
	slipSum     decimal.Decimal
	slipN       int
	latSum      time.Duration
	latN        int
}

func (c *Campaign) aggregate(asset string) []scenarioAgg {
	byName := map[string]*scenarioAgg{}
	var order []string
	for _, r := range c.Results {
		a, ok := byName[r.Scenario.Name]
		if !ok {
			a = &scenarioAgg{name: r.Scenario.Name}
			byName[r.Scenario.Name] = a
			order = append(order, r.Scenario.Name)
		}
		a.seeds++
		a.netPnL = a.netPnL.Add(dec(r.NetPnL[asset]))
		a.feesPaid = a.feesPaid.Add(dec(r.FeesPaid[asset]))
		a.turnover = a.turnover.Add(dec(r.Turnover[asset]))
		if dd := dec(r.MaxDrawdown[asset]); dd.GreaterThan(a.worstDD) {
			a.worstDD = dd
		}
		for _, cy := range r.Cycles {
			a.cycles++
			a.grossBpsSum = a.grossBpsSum.Add(cy.GrossBps)
			a.netBpsSum = a.netBpsSum.Add(cy.NetBps)
			switch cy.Outcome {
			case string(execution.OutcomeAllFilled):
				a.allFilled++
			default:
				a.failed++
			}
			if cy.SlippageBps != nil {
				a.slipSum = a.slipSum.Add(dec(*cy.SlippageBps))
				a.slipN++
			}
			if cy.AvgLegLatency > 0 {
				a.latSum += cy.AvgLegLatency
				a.latN++
			}
		}
	}
	out := make([]scenarioAgg, 0, len(order))
	for _, n := range order {
		out = append(out, *byName[n])
	}
	return out
}

// Flags returns the §80 honesty verdicts for one start asset. Always at
// least one line — silence would imply an unexamined result.
func (c *Campaign) Flags(asset string) []string {
	aggs := c.aggregate(asset)
	var baseline *scenarioAgg
	var stressed []scenarioAgg
	for i := range aggs {
		if aggs[i].name == "baseline" {
			baseline = &aggs[i]
		} else {
			stressed = append(stressed, aggs[i])
		}
	}
	var flags []string
	if baseline == nil {
		return []string{"NO BASELINE SCENARIO — run the grid with a baseline before drawing any conclusion."}
	}
	if baseline.cycles == 0 {
		return []string{"NO CYCLES EXECUTED in the baseline — the recording produced no qualified opportunities; no profitability statement can be made from it."}
	}
	if !baseline.netPnL.IsPositive() {
		flags = append(flags, fmt.Sprintf(
			"BASELINE UNPROFITABLE: net PnL %s %s over %d cycles. The strategy loses money before any stress is applied.",
			baseline.netPnL, asset, baseline.cycles))
		return flags
	}
	var killed []string
	for _, s := range stressed {
		if !s.netPnL.IsPositive() {
			killed = append(killed, s.name)
		}
	}
	switch {
	case len(stressed) == 0:
		flags = append(flags, "BASELINE ONLY — §80 requires the stress grid (higher fees, higher latency, worse fills, lower liquidity) before any profitability claim.")
	case len(killed) == len(stressed):
		flags = append(flags, fmt.Sprintf(
			"PROFITABLE ONLY UNDER PERFECT CONDITIONS: every stress scenario (%s) turns the edge negative. Per §80 such a system is worthless — flagged.",
			strings.Join(killed, ", ")))
	case len(killed) > 0:
		sort.Strings(killed)
		flags = append(flags, fmt.Sprintf(
			"EDGE FRAGILE under: %s (net PnL ≤ 0 there). Positive elsewhere — size the live-adjacent expectations to the stressed numbers, not the baseline.",
			strings.Join(killed, ", ")))
	default:
		flags = append(flags, "Edge survives the full stress grid in this recording. This is ONE sample window — repeat over more recordings before trusting it.")
	}
	return flags
}

// Markdown renders the §80 report for one start asset.
func (c *Campaign) Markdown(asset string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Profitability validation campaign (§80)\n\n")
	fmt.Fprintf(&b, "Recording: `%s` · generated %s\n\n", c.Recording, c.GeneratedAt.UTC().Format(time.RFC3339))
	b.WriteString("Paper simulation over recorded market data — not a live-trading result. ")
	b.WriteString("Fills are simulated with seeded latency against replayed books that continue moving during the simulated waits; ")
	b.WriteString("stress axes per §80: higher fees, higher latency, worse fills, lower liquidity.\n\n")

	fmt.Fprintf(&b, "| scenario | seeds | cycles | all-filled | fail-rate | mean gross bps | mean net bps | mean slippage bps | mean leg latency | fees paid | net PnL (%s) | turnover | max drawdown |\n", asset)
	b.WriteString("|---|---|---|---|---|---|---|---|---|---|---|---|---|\n")
	for _, a := range c.aggregate(asset) {
		failRate := "n/a"
		if a.cycles > 0 {
			failRate = decimal.NewFromInt(int64(a.failed)).
				Div(decimal.NewFromInt(int64(a.cycles))).Mul(decimal.NewFromInt(100)).StringFixed(1) + "%"
		}
		fmt.Fprintf(&b, "| %s | %d | %d | %d | %s | %s | %s | %s | %s | %s | %s | %s | %s |\n",
			a.name, a.seeds, a.cycles, a.allFilled, failRate,
			meanDec(a.grossBpsSum, a.cycles), meanDec(a.netBpsSum, a.cycles),
			meanDec(a.slipSum, a.slipN), meanLat(a.latSum, a.latN),
			a.feesPaid, a.netPnL, a.turnover, a.worstDD.StringFixed(4))
	}
	b.WriteString("\nMeasures: gross/net bps are the planner's per-cycle economics (fees in; buffers out for net); ")
	b.WriteString("slippage is realized-vs-plan in bps of input, only for cycles that returned to the start asset (positive = worse); ")
	b.WriteString("fail-rate counts executed cycles that did not settle ALL_FILLED; ")
	b.WriteString("turnover is start-asset input deployed; capital efficiency = net PnL ÷ turnover.\n\n")

	b.WriteString("## Verdict\n\n")
	for _, f := range c.Flags(asset) {
		fmt.Fprintf(&b, "- %s\n", f)
	}
	return b.String()
}

func dec(s string) decimal.Decimal {
	if s == "" {
		return decimal.Zero
	}
	d, err := decimal.NewFromString(s)
	if err != nil {
		return decimal.Zero
	}
	return d
}

func meanDec(sum decimal.Decimal, n int) string {
	if n == 0 {
		return "n/a"
	}
	return sum.Div(decimal.NewFromInt(int64(n))).StringFixed(2)
}

func meanLat(sum time.Duration, n int) string {
	if n == 0 {
		return "n/a"
	}
	return (sum / time.Duration(n)).Round(time.Millisecond).String()
}
