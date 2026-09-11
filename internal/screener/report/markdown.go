package report

import (
	"fmt"
	"sort"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/screener/alerts"
)

// Model is the fixed wording every report and summary carries.
const Model = "Measurement of automatic PAPER execution: simulated top-of-book fills against public quotes, net of configured taker fees, funding and realised slippage. Hypothetical; not a recommendation; nothing here is guaranteed; simulated figures are not indicative of future results. LIVE trading stays disabled."

func optDec(d *decimal.Decimal, places int32) string {
	if d == nil {
		return "n/a"
	}
	return d.StringFixed(places)
}

func optInt(v *int64) string {
	if v == nil {
		return "n/a"
	}
	return fmt.Sprintf("%d", *v)
}

// Markdown renders one report. Numbers only; the §7 table order.
func Markdown(p Payload) string {
	var b strings.Builder
	st := p.Stats
	scope := string(p.Strategy)
	if p.RuleID != "" {
		scope += fmt.Sprintf(" / rule %s (%s)", p.RuleID, p.RuleName)
	}
	fmt.Fprintf(&b, "# Screener paper report — %s — %s\n\n", scope, p.Window.Label)
	fmt.Fprintf(&b, "Window: %s → %s UTC. Generated %s. Data age of newest ledger row: %s ms.\n\n",
		p.Window.Start.Format("2006-01-02 15:04"), p.Window.End.Format("2006-01-02 15:04"), p.GeneratedAt.Format("2006-01-02 15:04:05"), optInt(p.DataAgeMs))
	b.WriteString("## Statistics (strategy-models §7)\n\n| statistic | value |\n|---|---|\n")
	row := func(k, v string) { fmt.Fprintf(&b, "| %s | %s |\n", k, v) }
	row("n", fmt.Sprintf("%d", st.N))
	row("n_regime", "not computed (no stored regime classification)")
	row("matched_pairs", fmt.Sprintf("%d", st.MatchedPairs))
	row("realised_net_pnl_quote", st.NetPnLQuote.StringFixed(4))
	row("open_positions", fmt.Sprintf("%d (unmarked %d)", st.OpenPositions, st.UnmarkedOpenPositions))
	row("unrealised_mark_quote", fmt.Sprintf("%s (mark age max %s ms)", st.UnrealisedMarkQuote.StringFixed(4), optInt(st.OpenMarkAgeMsMax)))
	row("funding_accrued_open", st.FundingAccruedOpen.StringFixed(4))
	row("pnl_after_rebalance", st.PnLAfterRebalance.StringFixed(4))
	row("matched_pair_net", st.MatchedPairNet.StringFixed(4))
	row("unwind_cost_quote", st.UnwindCostQuote.StringFixed(4))
	row("partial_leg_pnl_quote", st.PartialLegPnLQuote.StringFixed(4))
	row("conservative_net_quote", st.ConservativeNet.StringFixed(4))
	row("fees_quote", st.FeesQuote.StringFixed(4))
	row("net_bps_mean", optDec(st.NetBpsMean, 3))
	row("net_bps_median", optDec(st.NetBpsMedian, 3))
	row("hit_rate", fmt.Sprintf("%s (Wilson 95 %%: %s – %s; wins %d)", optDec(st.HitRate, 3), optDec(st.HitRateCILow, 3), optDec(st.HitRateCIHigh, 3), st.Wins))
	row("lifetime_s mean / median", fmt.Sprintf("%s / %s (n=%d)", optDec(st.LifetimeMeanS, 1), optDec(st.LifetimeMedianS, 1), st.LifetimeN))
	row("max_drawdown_quote / frac", fmt.Sprintf("%s / %s (allocated paper capital %s; realised samples only)", st.MaxDrawdownQuote.StringFixed(4), optDec(st.MaxDrawdownFrac, 4), st.AllocatedCapitalQuote.String()))
	if len(st.InventoryDrift) == 0 {
		row("inventory_drift", "none")
	} else {
		var parts []string
		for _, d := range st.InventoryDrift {
			parts = append(parts, fmt.Sprintf("%s %s→%s drift %s (notional %s, unmatched %d, mark age %s ms)", d.Base, d.VenueA, d.VenueB, d.DriftBase.String(), d.DriftNotional.StringFixed(2), d.Unmatched, optInt(d.MarkAgeMs)))
		}
		row("inventory_drift", strings.Join(parts, "; "))
	}
	reasons := make([]string, 0, len(st.Skipped))
	for r := range st.Skipped {
		reasons = append(reasons, r)
	}
	sort.Strings(reasons)
	if len(reasons) == 0 {
		row("skipped{reason}", "none")
	} else {
		var parts []string
		for _, r := range reasons {
			parts = append(parts, fmt.Sprintf("%s %d", r, st.Skipped[r]))
		}
		row("skipped{reason}", strings.Join(parts, ", "))
	}
	row("funding_rows / funding_net_quote", fmt.Sprintf("%d / %s", st.FundingRows, st.FundingQuote.StringFixed(4)))
	row("realised_slip_bps mean / p95", fmt.Sprintf("%s / %s over %d leg(s); allowance %s", optDec(st.RealisedSlipMeanBps, 3), optDec(st.RealisedSlipP95Bps, 3), st.SlipN, st.SlipAllowanceBps.String()))
	row("concentration", optDec(st.Concentration, 3))
	row("days / weekend_days", fmt.Sprintf("%d / %d", st.Days, st.WeekendDays))
	if st.Wilcoxon != nil {
		row("wilcoxon (one-sided, H₀ median ≤ 0)", fmt.Sprintf("n=%d W+=%s mean=%s z²=%s rejects=%v", st.Wilcoxon.N, st.Wilcoxon.WPlus.StringFixed(1), st.Wilcoxon.Mean.StringFixed(1), st.Wilcoxon.Z2.StringFixed(3), st.Wilcoxon.Rejects))
	}
	if st.Bootstrap != nil {
		row("bootstrap mean net_bps 95 % CI", fmt.Sprintf("%s [%s, %s] (%d resamples, %d day blocks)", st.Bootstrap.Mean.StringFixed(3), st.Bootstrap.CILow.StringFixed(3), st.Bootstrap.CIHigh.StringFixed(3), st.Bootstrap.Resamples, st.Bootstrap.Days))
	}
	fmt.Fprintf(&b, "\n## Production gate (strategy-models §8): %d / %d pass\n\n| # | item | status | reason |\n|---|---|---|---|\n", p.GatePassed, p.GateTotal)
	for _, it := range p.Gate {
		fmt.Fprintf(&b, "| %d | %s | %s | %s |\n", it.Item, it.Name, strings.ToUpper(it.Status), it.Reason)
	}
	b.WriteString("\nFailing any item means LIVE stays disabled.\n\n## Notes\n\n")
	for _, n := range p.Notes {
		fmt.Fprintf(&b, "- %s\n", n)
	}
	fmt.Fprintf(&b, "\n%s\n\n%s\n", p.Model, alerts.Footer)
	return b.String()
}
