package report

import (
	"fmt"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/screener"
)

// Gate statuses. There is no third state: an item the ledger cannot
// evidence FAILS with the reason "no evidence yet …" — the checklist
// never softens a missing measurement into a pass.
const (
	GatePass = "pass"
	GateFail = "fail"
)

// NoEvidence is the fixed prefix of every "cannot be evidenced yet"
// reason, so a reader (and the tests) can grep for it.
const NoEvidence = "no evidence yet"

// GateItem is one §8 checklist line.
type GateItem struct {
	Item   int    `json:"item"`
	Name   string `json:"name"`
	Status string `json:"status"`
	Reason string `json:"reason"`
}

// Floors per strategy (strategy-models §8 item 2) — floors, not targets.
type floors struct {
	n, matched, perRegime, fundingRows int64
	days                               int
}

func floorsFor(strategy screener.Strategy) floors {
	switch strategy {
	case screener.StrategyCrossVenueSpot:
		return floors{n: 200, matched: 60, perRegime: 30, days: 30}
	default:
		return floors{n: 30, perRegime: 8, fundingRows: 90, days: 30}
	}
}

var (
	maxDrawdownFrac = decimal.RequireFromString("0.05")
	maxConcentrate  = decimal.RequireFromString("0.40")
	lossMultiple    = decimal.NewFromInt(10)
)

// Checklist evaluates the §8 gate for one strategy over the CUMULATIVE
// statistics (a daily window can never satisfy "≥ 30 consecutive
// days"; the daily report still prints the checklist, every duration /
// sample item then failing with the floor it is short of).
func Checklist(strategy screener.Strategy, st Stats) []GateItem {
	f := floorsFor(strategy)
	items := make([]GateItem, 0, 8)
	add := func(n int, name string, pass bool, reason string) {
		status := GateFail
		if pass {
			status = GatePass
		}
		items = append(items, GateItem{Item: n, Name: name, Status: status, Reason: reason})
	}

	// 1. Duration and coverage.
	{
		reason := fmt.Sprintf("%s: %d day(s) with samples (need ≥ %d), %d weekend day(s) (need ≥ 4); calm/volatile day counts not computed — regime classification (rv24 of BTC/USDT 1-min mids) is not stored by the screener",
			NoEvidence, st.Days, f.days, st.WeekendDays)
		add(1, "Duration and coverage (≥ 30 days; ≥ 20 calm, ≥ 5 volatile, ≥ 4 weekend)", false, reason)
	}
	// 2. Minimum sample.
	{
		pass := st.N >= f.n
		reason := fmt.Sprintf("n = %d (floor %d)", st.N, f.n)
		if strategy == screener.StrategyCrossVenueSpot {
			pass = pass && st.MatchedPairs >= f.matched
			reason += fmt.Sprintf("; matched pairs = %d (floor %d)", st.MatchedPairs, f.matched)
		} else {
			pass = pass && st.FundingRows >= f.fundingRows
			reason += fmt.Sprintf("; funding settlements = %d (floor %d)", st.FundingRows, f.fundingRows)
		}
		reason += fmt.Sprintf("; per-regime floor %d not evaluable (no regime classification)", f.perRegime)
		// Per-regime counts cannot be evidenced → the item cannot pass.
		if pass {
			reason = NoEvidence + " per regime: " + reason
		} else {
			reason = NoEvidence + ": " + reason
		}
		add(2, "Minimum sample", false, reason)
	}
	// 3. Positive net in every regime (conservative figure).
	{
		pos := st.ConservativeNet.IsPositive() && st.N >= f.n
		reason := fmt.Sprintf("conservative net = %s quote over %d sample(s)", st.ConservativeNet.StringFixed(4), st.N)
		if st.N < f.n {
			reason = fmt.Sprintf("%s: %s (n below floor %d)", NoEvidence, reason, f.n)
		} else if !pos {
			reason += " (not positive)"
		}
		reason += "; per-regime split not evaluable (no regime classification)"
		add(3, "Positive net in every regime (conservative figure)", false, reason)
		_ = pos
	}
	// 4. Statistical test.
	{
		pass := false
		var reason string
		switch {
		case st.N < f.n || st.Wilcoxon == nil || st.Bootstrap == nil:
			reason = fmt.Sprintf("%s: n = %d (floor %d), days = %d (bootstrap needs ≥ 2)", NoEvidence, st.N, f.n, st.Days)
		default:
			w, b := st.Wilcoxon, st.Bootstrap
			pass = w.Rejects && b.CILow.IsPositive()
			reason = fmt.Sprintf("Wilcoxon one-sided: W+ = %s, mean %s, z² = %s (crit 2.7055) → rejects H₀: %v; daily-block bootstrap (%d resamples over %d days): mean net bps %s, 95%% CI [%s, %s] → lower bound > 0: %v",
				w.WPlus.StringFixed(1), w.Mean.StringFixed(1), w.Z2.StringFixed(3), w.Rejects,
				b.Resamples, b.Days, b.Mean.StringFixed(3), b.CILow.StringFixed(3), b.CIHigh.StringFixed(3), b.CILow.IsPositive())
		}
		add(4, "Statistical test (Wilcoxon + daily-block bootstrap)", pass, reason)
	}
	// 5. Stress grid — not computable from the ledger.
	add(5, "Stress (fees +5 bps/leg, fills 50 %)", false,
		NoEvidence+": the §80 stress grid is a re-simulation over the recorded window, not derivable from the paper ledger; run it separately and file it under docs/campaigns/")
	// 6. Drawdown and concentration.
	{
		var reason string
		pass := st.N >= f.n
		if st.N < f.n {
			reason = fmt.Sprintf("%s: n = %d (floor %d); ", NoEvidence, st.N, f.n)
		}
		if st.MaxDrawdownFrac == nil {
			pass = false
			reason += fmt.Sprintf("max_drawdown %s quote, fraction unknown (allocated paper capital %s)", st.MaxDrawdownQuote.StringFixed(4), st.AllocatedCapitalQuote.String())
		} else {
			ok := !st.MaxDrawdownFrac.GreaterThan(maxDrawdownFrac)
			pass = pass && ok
			reason += fmt.Sprintf("max_drawdown_frac = %s (≤ 0.05: %v)", st.MaxDrawdownFrac.StringFixed(4), ok)
		}
		if st.Concentration == nil {
			reason += "; concentration undefined (net ≤ 0 or no samples)"
			pass = false
		} else {
			ok := !st.Concentration.GreaterThan(maxConcentrate)
			pass = pass && ok
			reason += fmt.Sprintf("; concentration = %s (≤ 0.40: %v)", st.Concentration.StringFixed(3), ok)
		}
		if st.MedianWinQuote != nil {
			limit := st.MedianWinQuote.Mul(lossMultiple)
			ok := st.LargestLossQuote.Abs().LessThanOrEqual(limit)
			pass = pass && ok
			reason += fmt.Sprintf("; largest single loss %s vs 10 × median win %s: %v", st.LargestLossQuote.StringFixed(4), limit.StringFixed(4), ok)
		} else {
			reason += "; no winning sample yet for the 10 × median-win bound"
			pass = false
		}
		add(6, "Drawdown and concentration", pass, reason)
	}
	// 7. Model honesty.
	{
		var reason string
		pass := false
		if st.RealisedSlipP95Bps == nil {
			reason = fmt.Sprintf("%s: no realised-slippage measurement (allowance %s bps)", NoEvidence, st.SlipAllowanceBps.String())
		} else {
			ok := !st.RealisedSlipP95Bps.GreaterThan(st.SlipAllowanceBps)
			reason = fmt.Sprintf("realised slip p95 = %s bps over %d leg(s) vs allowance %s bps: %v", st.RealisedSlipP95Bps.StringFixed(3), st.SlipN, st.SlipAllowanceBps.String(), ok)
			if st.N < f.n {
				reason = fmt.Sprintf("%s (n = %d below floor %d): %s", NoEvidence, st.N, f.n, reason)
			} else {
				pass = ok
			}
		}
		reason += "; fee verification (no UNVERIFIED fee in the executed venue set) is a manual check against docs/research/screener-endpoints.md and is NOT evidenced by this report"
		// A manual sub-check cannot be evidenced here → the item fails
		// until a human records it.
		add(7, "Model honesty (slip p95 ≤ allowance; no UNVERIFIED fee)", false, reason+fmt.Sprintf(" [slippage sub-check: %v]", pass))
	}
	// 8. Non-statistical items.
	add(8, "Non-statistical items (security review, decision record, human-merged removal of ErrLiveTradingDisabled)", false,
		NoEvidence+": manual items; LIVE stays disabled")
	return items
}

// Passed counts pass items.
func Passed(items []GateItem) int {
	n := 0
	for _, it := range items {
		if it.Status == GatePass {
			n++
		}
	}
	return n
}
