package report

import (
	"math/rand"
	"sort"
	"time"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/screener"
	"github.com/cploutarchou/arb-chain-bot/internal/screener/paperexec"
)

var (
	decZero = decimal.Zero
	decTwo  = decimal.NewFromInt(2)
	decTenK = decimal.NewFromInt(10_000)
	// z for a 95 % two-sided interval (Wilson) and the one-sided 5 %
	// critical value squared (Wilcoxon normal approximation): 1.959964²
	// and 1.644854².
	wilsonZ        = decimal.RequireFromString("1.959964")
	wilcoxonZ2Crit = decimal.RequireFromString("2.705544")
	bootstrapN     = 10_000
	bootstrapAlpha = decimal.RequireFromString("0.025")
)

// Sample is one §7 sample unit: a spot execution or a closed carry /
// funding-harvest position, with the per-sample net bps
// (pnl / notional_deployed × 10 000).
type Sample struct {
	ID       string          `json:"id"`
	At       time.Time       `json:"at"`
	PnLQuote decimal.Decimal `json:"pnl_quote"`
	Notional decimal.Decimal `json:"notional_quote"`
	NetBps   decimal.Decimal `json:"net_bps"`
	Day      string          `json:"day"` // YYYY-MM-DD UTC
}

// WilcoxonResult is the one-sided signed-rank test on per-sample net
// bps (H₀: median ≤ 0, α = 0.05, normal approximation without tie
// correction — stated in the report).
type WilcoxonResult struct {
	N       int64           `json:"n"` // non-zero samples
	WPlus   decimal.Decimal `json:"w_plus"`
	Mean    decimal.Decimal `json:"mean"`
	VarW    decimal.Decimal `json:"var_w"`
	Z2      decimal.Decimal `json:"z_squared"` // signed: negative when W+ < mean
	Rejects bool            `json:"rejects_h0"`
}

// BootstrapResult is the daily-block bootstrap of mean net bps.
type BootstrapResult struct {
	Resamples int             `json:"resamples"`
	Days      int             `json:"days"`
	Mean      decimal.Decimal `json:"mean_net_bps"`
	CILow     decimal.Decimal `json:"ci95_low"`
	CIHigh    decimal.Decimal `json:"ci95_high"`
}

// Stats is the strategy-models §7 table for one (strategy, rule, window).
type Stats struct {
	N            int64 `json:"n"`
	Wins         int64 `json:"wins"`
	MatchedPairs int64 `json:"matched_pairs"`

	NetPnLQuote        decimal.Decimal `json:"net_pnl_quote"`
	FeesQuote          decimal.Decimal `json:"fees_quote"`
	FundingQuote       decimal.Decimal `json:"funding_quote"`
	FundingRows        int64           `json:"funding_rows"`
	PnLAfterRebalance  decimal.Decimal `json:"pnl_after_rebalance"`
	MatchedPairNet     decimal.Decimal `json:"matched_pair_net"`
	UnwindCostQuote    decimal.Decimal `json:"unwind_cost_quote"`
	PartialLegPnLQuote decimal.Decimal `json:"partial_leg_pnl_quote"`
	// ConservativeNet is the §8 item 3 figure: matched-pair net for spot
	// (after-rebalance when no pair matched yet), net incl. unwind costs
	// otherwise.
	ConservativeNet decimal.Decimal `json:"conservative_net_quote"`

	NetBpsMean   *decimal.Decimal `json:"net_bps_mean"`
	NetBpsMedian *decimal.Decimal `json:"net_bps_median"`

	HitRate       *decimal.Decimal `json:"hit_rate"`
	HitRateCILow  *decimal.Decimal `json:"hit_rate_wilson95_low"`
	HitRateCIHigh *decimal.Decimal `json:"hit_rate_wilson95_high"`

	LifetimeMeanS   *decimal.Decimal `json:"lifetime_s_mean"`
	LifetimeMedianS *decimal.Decimal `json:"lifetime_s_median"`
	LifetimeN       int64            `json:"lifetime_n"`

	MaxDrawdownQuote      decimal.Decimal  `json:"max_drawdown_quote"`
	MaxDrawdownFrac       *decimal.Decimal `json:"max_drawdown_frac"`
	AllocatedCapitalQuote decimal.Decimal  `json:"allocated_capital_quote"`

	InventoryDrift []screener.DriftRow `json:"inventory_drift"`
	Skipped        map[string]int64    `json:"skipped"`

	RealisedSlipMeanBps *decimal.Decimal `json:"realised_slip_bps_mean"`
	RealisedSlipP95Bps  *decimal.Decimal `json:"realised_slip_bps_p95"`
	SlipN               int64            `json:"realised_slip_n"`
	SlipAllowanceBps    decimal.Decimal  `json:"slip_allowance_bps"`

	Concentration    *decimal.Decimal `json:"concentration"`
	LargestLossQuote decimal.Decimal  `json:"largest_loss_quote"`
	MedianWinQuote   *decimal.Decimal `json:"median_win_quote"`

	Days          int        `json:"days"`
	WeekendDays   int        `json:"weekend_days"`
	FirstSampleAt *time.Time `json:"first_sample_at"`
	LastSampleAt  *time.Time `json:"last_sample_at"`

	Wilcoxon  *WilcoxonResult  `json:"wilcoxon"`
	Bootstrap *BootstrapResult `json:"bootstrap"`

	// Samples are kept for the tests and the gate; not serialised (a
	// cumulative report would otherwise carry every execution).
	Samples []Sample `json:"-"`
}

// MidLookup resolves a venue's spot mid for the drift notional.
type MidLookup func(venue screener.Venue, base, quote string) (decimal.Decimal, int64, bool)

// Inputs is what Compute reads — plain slices so the tests hand it a
// synthetic ledger with known answers.
type Inputs struct {
	Strategy   screener.Strategy
	Rules      []screener.Rule // the rules in scope (one, or every rule of the strategy)
	Positions  []paperexec.Position
	Executions []paperexec.Execution
	Events     []screener.Event
	Window     Window
	// Capital is the allocated paper capital in quote (settings
	// paper.balances quote assets summed) for max_drawdown_frac; zero
	// means unknown → frac nil.
	Capital decimal.Decimal
	Mid     MidLookup
	// SpotFee resolves a venue's spot taker fee (bps) for the §2.3
	// rebalancing charge; nil → no charge is applied and the notes say so.
	SpotFee screener.VenueFeeLookup
	Now     time.Time
	// Seed for the bootstrap resampling indices (reproducible reports).
	Seed int64
}

// Compute builds the §7 statistics for the window. Pure.
func Compute(in Inputs) Stats {
	st := Stats{Skipped: map[string]int64{}, InventoryDrift: []screener.DriftRow{}, AllocatedCapitalQuote: in.Capital}
	st.SlipAllowanceBps = slipAllowance(in.Rules)
	w := in.Window

	ruleByID := map[string]screener.Rule{}
	for _, r := range in.Rules {
		ruleByID[r.ID] = r
	}
	execByID := map[string]paperexec.Execution{}
	for _, e := range in.Executions {
		execByID[e.ID] = e
	}

	var samples []Sample
	var lifetimes []decimal.Decimal

	// Positions: skipped counts (by opened_at), closed perp positions as
	// samples (by closed_at), unwind / partial-leg PnL lines.
	for _, p := range in.Positions {
		switch p.Status {
		case paperexec.StatusSkipped:
			if !w.Contains(p.OpenedAt) {
				continue
			}
			st.Skipped[p.SkippedReason]++
			switch p.SkippedReason {
			case paperexec.SkipUnwind:
				st.UnwindCostQuote = st.UnwindCostQuote.Add(p.PnLQuote)
				st.NetPnLQuote = st.NetPnLQuote.Add(p.PnLQuote)
			case paperexec.SkipPartialLeg:
				st.PartialLegPnLQuote = st.PartialLegPnLQuote.Add(p.PnLQuote)
				st.NetPnLQuote = st.NetPnLQuote.Add(p.PnLQuote)
			}
		case paperexec.StatusClosed:
			if in.Strategy == screener.StrategyCrossVenueSpot || p.ClosedAt == nil || !w.Contains(*p.ClosedAt) {
				continue
			}
			notional := perpNotional(p)
			s := Sample{ID: p.ID, At: *p.ClosedAt, PnLQuote: p.PnLQuote, Notional: notional, Day: dayOf(*p.ClosedAt)}
			if notional.IsPositive() {
				s.NetBps = p.PnLQuote.Div(notional).Mul(decTenK)
			}
			samples = append(samples, s)
			st.NetPnLQuote = st.NetPnLQuote.Add(p.PnLQuote)
			lifetimes = append(lifetimes, decimal.NewFromInt(int64(p.ClosedAt.Sub(p.OpenedAt)/time.Second)))
		}
	}

	// Executions: fees, funding rows, slip, spot samples + drift +
	// matched pairs.
	type laneKey struct {
		base, quote string
		a, b        screener.Venue
	}
	drift := map[laneKey]decimal.Decimal{}
	unmatched := map[laneKey]int{}
	queues := map[laneKey][]decimal.Decimal{}
	var lanes []laneKey
	var slips []decimal.Decimal
	var newest time.Time
	for _, e := range in.Executions {
		if !w.Contains(e.At) {
			continue
		}
		if e.At.After(newest) {
			newest = e.At
		}
		st.FeesQuote = st.FeesQuote.Add(e.FeesQuote)
		if e.RealisedSlipBps != nil {
			slips = append(slips, *e.RealisedSlipBps)
		}
		switch e.Kind {
		case paperexec.KindFunding:
			st.FundingRows++
			st.FundingQuote = st.FundingQuote.Add(e.PnLQuote)
		case paperexec.KindSpot:
			if in.Strategy != screener.StrategyCrossVenueSpot {
				continue
			}
			qty, price := filledLeg(e)
			notional := qty.Mul(price)
			s := Sample{ID: e.ID, At: e.At, PnLQuote: e.PnLQuote, Notional: notional, Day: dayOf(e.At)}
			if notional.IsPositive() {
				s.NetBps = e.PnLQuote.Div(notional).Mul(decTenK)
			}
			samples = append(samples, s)
			st.NetPnLQuote = st.NetPnLQuote.Add(e.PnLQuote)
			k := laneKey{e.Base, e.Quote, e.VenueA, e.VenueB}
			rev := laneKey{e.Base, e.Quote, e.VenueB, e.VenueA}
			if _, seen := drift[k]; !seen {
				if _, seenRev := drift[rev]; !seenRev {
					lanes = append(lanes, k)
				}
			}
			drift[k] = drift[k].Add(qty)
			drift[rev] = drift[rev].Sub(qty)
			if q := queues[rev]; len(q) > 0 {
				st.MatchedPairs++
				st.MatchedPairNet = st.MatchedPairNet.Add(q[0]).Add(e.PnLQuote)
				queues[rev] = q[1:]
				unmatched[rev]--
			} else {
				queues[k] = append(queues[k], e.PnLQuote)
				unmatched[k]++
			}
		}
	}

	// Inventory drift and the rebalancing charge (§2.3) at current mids.
	rebalance := decZero
	for _, k := range lanes {
		d := drift[k]
		row := screener.DriftRow{Base: k.base, VenueA: k.a, VenueB: k.b, DriftBase: d, Unmatched: unmatched[k] + unmatched[laneKey{k.base, k.quote, k.b, k.a}]}
		if in.Mid != nil {
			if mid, age, ok := in.Mid(k.a, k.base, k.quote); ok {
				row.DriftNotional = d.Abs().Mul(mid)
				a := age
				row.MarkAgeMs = &a
				if in.SpotFee != nil {
					fA, _ := in.SpotFee(k.a)
					fB, _ := in.SpotFee(k.b)
					rebalance = rebalance.Add(row.DriftNotional.Mul(fA.Add(fB).Div(decTenK)))
				}
			}
		}
		st.InventoryDrift = append(st.InventoryDrift, row)
	}
	st.PnLAfterRebalance = st.NetPnLQuote.Sub(rebalance)
	switch {
	case in.Strategy == screener.StrategyCrossVenueSpot && st.MatchedPairs > 0:
		st.ConservativeNet = st.MatchedPairNet.Add(st.UnwindCostQuote).Add(st.PartialLegPnLQuote)
	case in.Strategy == screener.StrategyCrossVenueSpot:
		st.ConservativeNet = st.PnLAfterRebalance
	default:
		st.ConservativeNet = st.NetPnLQuote // already includes unwind rows
	}

	// Spot lifetime = closed alert events in the window.
	if in.Strategy == screener.StrategyCrossVenueSpot {
		for _, e := range in.Events {
			if e.ClosedAt != nil && w.Contains(*e.ClosedAt) && ruleInScope(ruleByID, e.RuleID) {
				lifetimes = append(lifetimes, decimal.NewFromInt(e.LifetimeS))
			}
		}
	}

	sort.SliceStable(samples, func(i, j int) bool { return samples[i].At.Before(samples[j].At) })
	st.Samples = samples
	st.N = int64(len(samples))
	if st.N > 0 {
		first, last := samples[0].At, samples[len(samples)-1].At
		st.FirstSampleAt, st.LastSampleAt = &first, &last
	}

	// Per-sample distribution.
	var bps, wins, losses []decimal.Decimal
	byDay := map[string]decimal.Decimal{}
	for _, s := range samples {
		bps = append(bps, s.NetBps)
		byDay[s.Day] = byDay[s.Day].Add(s.PnLQuote)
		if s.PnLQuote.IsPositive() {
			st.Wins++
			wins = append(wins, s.PnLQuote)
		} else if s.PnLQuote.IsNegative() {
			losses = append(losses, s.PnLQuote)
		}
	}
	if len(bps) > 0 {
		m, med := meanDec(bps), medianDec(bps)
		st.NetBpsMean, st.NetBpsMedian = &m, &med
		hr := decimal.NewFromInt(st.Wins).Div(decimal.NewFromInt(st.N))
		lo, hi := wilson(st.Wins, st.N)
		st.HitRate, st.HitRateCILow, st.HitRateCIHigh = &hr, &lo, &hi
	}
	if len(lifetimes) > 0 {
		m, med := meanDec(lifetimes), medianDec(lifetimes)
		st.LifetimeMeanS, st.LifetimeMedianS, st.LifetimeN = &m, &med, int64(len(lifetimes))
	}
	if len(wins) > 0 {
		mw := medianDec(wins)
		st.MedianWinQuote = &mw
	}
	for _, l := range losses {
		if l.LessThan(st.LargestLossQuote) {
			st.LargestLossQuote = l
		}
	}
	// Unwind / partial-leg lines count as single-leg losses for §8 item 6.
	if st.UnwindCostQuote.LessThan(st.LargestLossQuote) {
		st.LargestLossQuote = st.UnwindCostQuote
	}

	// Max drawdown on the realised sample curve in time order.
	st.MaxDrawdownQuote = maxDrawdown(samples)
	if in.Capital.IsPositive() {
		f := st.MaxDrawdownQuote.Div(in.Capital)
		st.MaxDrawdownFrac = &f
	}

	// Days and concentration.
	days := make([]string, 0, len(byDay))
	for d := range byDay {
		days = append(days, d)
	}
	sort.Strings(days)
	st.Days = len(days)
	for _, d := range days {
		if t, err := time.Parse("2006-01-02", d); err == nil && (t.Weekday() == time.Saturday || t.Weekday() == time.Sunday) {
			st.WeekendDays++
		}
	}
	if st.NetPnLQuote.IsPositive() {
		largest := decZero
		for _, v := range byDay {
			if v.GreaterThan(largest) {
				largest = v
			}
		}
		c := largest.Div(st.NetPnLQuote)
		st.Concentration = &c
	}

	// Realised slippage.
	if len(slips) > 0 {
		sort.Slice(slips, func(i, j int) bool { return slips[i].LessThan(slips[j]) })
		m := meanDec(slips)
		p95 := percentileNearestRank(slips, 95)
		st.RealisedSlipMeanBps, st.RealisedSlipP95Bps, st.SlipN = &m, &p95, int64(len(slips))
	}

	// Tests (item 4). Computed whenever there is data; the gate decides
	// whether n is large enough to count them as evidence.
	if len(bps) > 0 {
		wr := wilcoxon(bps)
		st.Wilcoxon = &wr
	}
	if st.Days >= 2 {
		br := bootstrapDaily(samples, in.Seed)
		st.Bootstrap = &br
	}
	return st
}

// wilson returns the 95 % Wilson score interval for wins/n.
func wilson(wins, n int64) (lo, hi decimal.Decimal) {
	nn := decimal.NewFromInt(n)
	p := decimal.NewFromInt(wins).Div(nn)
	z2 := wilsonZ.Mul(wilsonZ)
	denom := decimal.NewFromInt(1).Add(z2.Div(nn))
	centre := p.Add(z2.Div(nn.Mul(decTwo))).Div(denom)
	inner := p.Mul(decimal.NewFromInt(1).Sub(p)).Div(nn).Add(z2.Div(nn.Mul(nn).Mul(decimal.NewFromInt(4))))
	half := wilsonZ.Mul(sqrtDec(inner)).Div(denom)
	lo, hi = centre.Sub(half), centre.Add(half)
	if lo.IsNegative() {
		lo = decZero
	}
	if hi.GreaterThan(decimal.NewFromInt(1)) {
		hi = decimal.NewFromInt(1)
	}
	return lo, hi
}

// wilcoxon: one-sided signed-rank, H₁: median > 0. Zeros are dropped,
// ties get average ranks; normal approximation without the tie
// correction (conservative direction is not guaranteed — stated in the
// report notes). Rejects when W+ > mean and (W+ − mean)² > z²crit × Var.
func wilcoxon(xs []decimal.Decimal) WilcoxonResult {
	type item struct {
		abs decimal.Decimal
		pos bool
	}
	var items []item
	for _, x := range xs {
		if x.IsZero() {
			continue
		}
		items = append(items, item{abs: x.Abs(), pos: x.IsPositive()})
	}
	n := int64(len(items))
	res := WilcoxonResult{N: n}
	if n == 0 {
		return res
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].abs.LessThan(items[j].abs) })
	wplus := decZero
	for i := 0; i < len(items); {
		j := i
		for j+1 < len(items) && items[j+1].abs.Equal(items[i].abs) {
			j++
		}
		// average rank of positions i..j (1-based)
		rank := decimal.NewFromInt(int64(i+1) + int64(j+1)).Div(decTwo)
		for k := i; k <= j; k++ {
			if items[k].pos {
				wplus = wplus.Add(rank)
			}
		}
		i = j + 1
	}
	nn := decimal.NewFromInt(n)
	mean := nn.Mul(nn.Add(decimal.NewFromInt(1))).Div(decimal.NewFromInt(4))
	varW := nn.Mul(nn.Add(decimal.NewFromInt(1))).Mul(nn.Mul(decTwo).Add(decimal.NewFromInt(1))).Div(decimal.NewFromInt(24))
	diff := wplus.Sub(mean)
	res.WPlus, res.Mean, res.VarW = wplus, mean, varW
	if varW.IsPositive() {
		res.Z2 = diff.Mul(diff).Div(varW)
		if diff.IsNegative() {
			res.Z2 = res.Z2.Neg()
		}
	}
	res.Rejects = diff.IsPositive() && res.Z2.GreaterThan(wilcoxonZ2Crit)
	return res
}

// bootstrapDaily resamples whole UTC days with replacement (10 000
// times, fixed seed) and reports the 95 % percentile interval of the
// mean per-sample net bps.
func bootstrapDaily(samples []Sample, seed int64) BootstrapResult {
	type day struct {
		sum   decimal.Decimal
		count int64
	}
	byDay := map[string]*day{}
	var keys []string
	for _, s := range samples {
		d, ok := byDay[s.Day]
		if !ok {
			d = &day{}
			byDay[s.Day] = d
			keys = append(keys, s.Day)
		}
		d.sum = d.sum.Add(s.NetBps)
		d.count++
	}
	sort.Strings(keys)
	days := make([]*day, len(keys))
	for i, k := range keys {
		days[i] = byDay[k]
	}
	res := BootstrapResult{Resamples: bootstrapN, Days: len(days)}
	total, count := decZero, int64(0)
	for _, d := range days {
		total = total.Add(d.sum)
		count += d.count
	}
	if count > 0 {
		res.Mean = total.Div(decimal.NewFromInt(count))
	}
	rng := rand.New(rand.NewSource(seed)) //nolint:gosec // reproducible resampling indices, not security
	means := make([]decimal.Decimal, 0, bootstrapN)
	for i := 0; i < bootstrapN; i++ {
		sum, cnt := decZero, int64(0)
		for j := 0; j < len(days); j++ {
			d := days[rng.Intn(len(days))]
			sum = sum.Add(d.sum)
			cnt += d.count
		}
		if cnt == 0 {
			continue
		}
		means = append(means, sum.Div(decimal.NewFromInt(cnt)))
	}
	if len(means) == 0 {
		return res
	}
	sort.Slice(means, func(i, j int) bool { return means[i].LessThan(means[j]) })
	res.CILow = percentileNearestRankFrac(means, bootstrapAlpha)
	res.CIHigh = percentileNearestRankFrac(means, decimal.NewFromInt(1).Sub(bootstrapAlpha))
	return res
}

func maxDrawdown(samples []Sample) decimal.Decimal {
	equity, peak, dd := decZero, decZero, decZero
	for _, s := range samples {
		equity = equity.Add(s.PnLQuote)
		if equity.GreaterThan(peak) {
			peak = equity
		}
		if d := peak.Sub(equity); d.GreaterThan(dd) {
			dd = d
		}
	}
	return dd
}

func filledLeg(e paperexec.Execution) (qty, price decimal.Decimal) {
	for _, f := range e.Fills {
		if f.Status == "FILLED" {
			return f.Qty, f.FillPrice
		}
	}
	return decZero, decZero
}

// perpNotional is qty × spot open price from the open payload
// ("spot_open"); zero when absent (net bps then reports 0 and the
// markdown says so via n_notional_unknown).
func perpNotional(p paperexec.Position) decimal.Decimal {
	raw, ok := p.OpenPayload["spot_open"]
	if !ok {
		return decZero
	}
	s, ok := raw.(string)
	if !ok {
		return decZero
	}
	v, err := decimal.NewFromString(s)
	if err != nil {
		return decZero
	}
	return p.Qty.Mul(v)
}

func slipAllowance(rules []screener.Rule) decimal.Decimal {
	// The gate compares realised p95 with the allowance; over several
	// rules the STRICTEST (smallest) allowance is the honest bound.
	var out decimal.Decimal
	for i, r := range rules {
		if i == 0 || r.SlipBps().LessThan(out) {
			out = r.SlipBps()
		}
	}
	if len(rules) == 0 {
		out = screener.Rule{}.SlipBps()
	}
	return out
}

func ruleInScope(rules map[string]screener.Rule, id string) bool {
	if len(rules) == 0 {
		return true
	}
	_, ok := rules[id]
	return ok
}

func dayOf(t time.Time) string { return t.UTC().Format("2006-01-02") }

func meanDec(xs []decimal.Decimal) decimal.Decimal {
	sum := decZero
	for _, x := range xs {
		sum = sum.Add(x)
	}
	return sum.Div(decimal.NewFromInt(int64(len(xs))))
}

func medianDec(xs []decimal.Decimal) decimal.Decimal {
	s := append([]decimal.Decimal(nil), xs...)
	sort.Slice(s, func(i, j int) bool { return s[i].LessThan(s[j]) })
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return s[n/2-1].Add(s[n/2]).Div(decTwo)
}

// percentileNearestRank on an ASCENDING slice (p in 1..100).
func percentileNearestRank(sorted []decimal.Decimal, p int) decimal.Decimal {
	idx := (len(sorted)*p + 99) / 100
	if idx > 0 {
		idx--
	}
	return sorted[idx]
}

func percentileNearestRankFrac(sorted []decimal.Decimal, frac decimal.Decimal) decimal.Decimal {
	n := decimal.NewFromInt(int64(len(sorted)))
	idx := int(frac.Mul(n).Ceil().IntPart())
	if idx > 0 {
		idx--
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

// sqrtDec is Newton's method on decimals (the only root the report
// needs, Wilson's half-width); 40 iterations converge far past the
// printed precision for any n we report.
func sqrtDec(v decimal.Decimal) decimal.Decimal {
	if !v.IsPositive() {
		return decZero
	}
	x := v
	if x.LessThan(decimal.NewFromInt(1)) {
		x = decimal.NewFromInt(1)
	}
	for i := 0; i < 40; i++ {
		x = x.Add(v.Div(x)).Div(decTwo)
	}
	return x
}
