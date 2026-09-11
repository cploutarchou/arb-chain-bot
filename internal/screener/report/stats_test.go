package report

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/screener"
	"github.com/cploutarchou/arb-chain-bot/internal/screener/paperexec"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

var t0 = time.Date(2026, 8, 25, 0, 0, 0, 0, time.UTC) // Tuesday

func spotExec(id string, at time.Time, a, b screener.Venue, qty, price, pnl string, slip *string) paperexec.Execution {
	e := paperexec.Execution{ID: id, RuleID: "r1", Strategy: screener.StrategyCrossVenueSpot, Kind: paperexec.KindSpot,
		Base: "BTC", Quote: "USDT", VenueA: a, VenueB: b, At: at, PnLQuote: d(pnl), FeesQuote: d("1"),
		Fills: []paperexec.Fill{{Leg: 1, Venue: a, Side: "BUY", Qty: d(qty), FillPrice: d(price), Status: "FILLED"}, {Leg: 2, Venue: b, Side: "SELL", Qty: d(qty), FillPrice: d(price), Status: "FILLED"}}}
	if slip != nil {
		v := d(*slip)
		e.RealisedSlipBps = &v
	}
	return e
}

func strp(s string) *string { return &s }

// synthetic ledger: 6 spot executions over 3 days (Tue/Wed/Sat) with
// hand-computed answers.
//
//	id  day        A→B          qty   price  notional  pnl    net_bps
//	e1  08-25      binance→okx  0.1   50000  5000      +5     10
//	e2  08-25      okx→binance  0.1   50000  5000      +3     6      (matches e1 → pair net 8)
//	e3  08-26      binance→okx  0.2   50000  10000     -4     -4
//	e4  08-26      binance→okx  0.1   50000  5000      +10    20
//	e5  08-29(Sat) binance→okx  0.1   50000  5000      +2     4
//	e6  08-29      okx→binance  0.1   50000  5000      +1     2      (matches e3 → pair net -3)
//
// n = 6, wins = 5, net = 17, matched pairs 2 (net 5), drift binance→okx
// = 0.1+0.2+0.1+0.1 − 0.1 − 0.1 = 0.3 BTC, unmatched e4, e5 = 2.
// net_bps mean = (10+6−4+20+4+2)/6 = 38/6 = 6.333…, median of
// [-4,2,4,6,10,20] = 5. Equity curve 5,8,4,14,16,17 → peak 8 → trough 4:
// max drawdown 4. Days 3, weekend 1. Concentration: day sums 8, 6, 3 →
// 8/17 = 0.4706. Slips 1, 3, 2 → mean 2, p95 (nearest rank of 3) = 3.
func synthetic() Inputs {
	execs := []paperexec.Execution{
		spotExec("e1", t0.Add(1*time.Hour), screener.VenueBinance, screener.VenueOKX, "0.1", "50000", "5", strp("1")),
		spotExec("e2", t0.Add(2*time.Hour), screener.VenueOKX, screener.VenueBinance, "0.1", "50000", "3", nil),
		spotExec("e3", t0.Add(25*time.Hour), screener.VenueBinance, screener.VenueOKX, "0.2", "50000", "-4", strp("3")),
		spotExec("e4", t0.Add(26*time.Hour), screener.VenueBinance, screener.VenueOKX, "0.1", "50000", "10", nil),
		spotExec("e5", t0.Add(97*time.Hour), screener.VenueBinance, screener.VenueOKX, "0.1", "50000", "2", strp("2")),
		spotExec("e6", t0.Add(98*time.Hour), screener.VenueOKX, screener.VenueBinance, "0.1", "50000", "1", nil),
	}
	closed := t0.Add(3 * time.Hour)
	positions := []paperexec.Position{
		{ID: "p-skip1", RuleID: "r1", Strategy: screener.StrategyCrossVenueSpot, Status: paperexec.StatusSkipped, SkippedReason: paperexec.SkipDepth, OpenedAt: t0.Add(time.Hour)},
		{ID: "p-skip2", RuleID: "r1", Strategy: screener.StrategyCrossVenueSpot, Status: paperexec.StatusSkipped, SkippedReason: paperexec.SkipDepth, OpenedAt: t0.Add(30 * time.Hour)},
		{ID: "p-skip3", RuleID: "r1", Strategy: screener.StrategyCrossVenueSpot, Status: paperexec.StatusSkipped, SkippedReason: screener.SkipSuspectMismatch, OpenedAt: t0.Add(2 * time.Hour)},
	}
	events := []screener.Event{
		{ID: "ev1", RuleID: "r1", OpenedAt: t0, ClosedAt: &closed, LifetimeS: 30},
		{ID: "ev2", RuleID: "r1", OpenedAt: t0, ClosedAt: &closed, LifetimeS: 50},
	}
	min := d("5")
	rule := screener.Rule{ID: "r1", Name: "r1", Kind: screener.RuleKindSpread, MinSpreadBps: &min, AutoPaper: true, PaperSizeQuote: d("5000")}
	return Inputs{
		Strategy: screener.StrategyCrossVenueSpot, Rules: []screener.Rule{rule},
		Positions: positions, Executions: execs, Events: events,
		Window:  Window{Label: PeriodCumulative, Start: t0, End: t0.Add(5 * 24 * time.Hour)},
		Capital: d("100"), Now: t0.Add(5 * 24 * time.Hour), Seed: 1,
		Mid:     func(screener.Venue, string, string) (decimal.Decimal, int64, bool) { return d("50000"), 1500, true },
		SpotFee: func(screener.Venue) (decimal.Decimal, bool) { return d("10"), true },
	}
}

// eq compares at 10 decimal places (repeating decimals such as 38/6 are
// carried at the package division precision and rounded for the check).
func eq(t *testing.T, name string, got decimal.Decimal, want string) {
	t.Helper()
	if !got.Round(10).Equal(d(want).Round(10)) {
		t.Fatalf("%s = %s, want %s", name, got, want)
	}
}

func eqp(t *testing.T, name string, got *decimal.Decimal, want string) {
	t.Helper()
	if got == nil {
		t.Fatalf("%s = nil, want %s", name, want)
	}
	eq(t, name, *got, want)
}

func TestComputeSpotKnownAnswers(t *testing.T) {
	st := Compute(synthetic())
	if st.N != 6 || st.Wins != 5 || st.MatchedPairs != 2 {
		t.Fatalf("n=%d wins=%d matched=%d", st.N, st.Wins, st.MatchedPairs)
	}
	eq(t, "net_pnl", st.NetPnLQuote, "17")
	eq(t, "fees", st.FeesQuote, "6")
	eq(t, "matched_pair_net", st.MatchedPairNet, "5")
	eq(t, "conservative (matched pairs)", st.ConservativeNet, "5")
	// drift 0.3 BTC × 50 000 = 15 000 notional × (10+10) bps = 30 rebalance
	eq(t, "pnl_after_rebalance", st.PnLAfterRebalance, "-13")
	if len(st.InventoryDrift) != 1 {
		t.Fatalf("drift rows = %+v", st.InventoryDrift)
	}
	dr := st.InventoryDrift[0]
	eq(t, "drift_base", dr.DriftBase, "0.3")
	eq(t, "drift_notional", dr.DriftNotional, "15000")
	if dr.Unmatched != 2 || dr.MarkAgeMs == nil || *dr.MarkAgeMs != 1500 {
		t.Fatalf("drift row = %+v", dr)
	}
	eqp(t, "net_bps_mean", st.NetBpsMean, "6.3333333333")
	eqp(t, "net_bps_median", st.NetBpsMedian, "5")
	eqp(t, "hit_rate", st.HitRate, "0.8333333333")
	// Wilson 95 % for 5/6 (z = 1.959964): centre 0.703223, half 0.266714
	// → [0.4365, 0.9699] at 4 dp.
	if st.HitRateCILow == nil || st.HitRateCIHigh == nil {
		t.Fatal("wilson CI nil")
	}
	if got := st.HitRateCILow.StringFixed(4); got != "0.4365" {
		t.Fatalf("wilson low = %s, want 0.4365", got)
	}
	if got := st.HitRateCIHigh.StringFixed(4); got != "0.9699" {
		t.Fatalf("wilson high = %s, want 0.9699", got)
	}
	eqp(t, "lifetime_mean", st.LifetimeMeanS, "40")
	eqp(t, "lifetime_median", st.LifetimeMedianS, "40")
	eq(t, "max_drawdown", st.MaxDrawdownQuote, "4")
	eqp(t, "max_drawdown_frac", st.MaxDrawdownFrac, "0.04")
	if st.Skipped[paperexec.SkipDepth] != 2 || st.Skipped[screener.SkipSuspectMismatch] != 1 {
		t.Fatalf("skipped = %v", st.Skipped)
	}
	eqp(t, "slip_mean", st.RealisedSlipMeanBps, "2")
	eqp(t, "slip_p95", st.RealisedSlipP95Bps, "3")
	if st.SlipN != 3 {
		t.Fatalf("slip n = %d", st.SlipN)
	}
	if st.Concentration == nil || st.Concentration.StringFixed(4) != "0.4706" {
		t.Fatalf("concentration = %v", st.Concentration)
	}
	if st.Days != 3 || st.WeekendDays != 1 {
		t.Fatalf("days = %d weekend = %d", st.Days, st.WeekendDays)
	}
	eq(t, "largest_loss", st.LargestLossQuote, "-4")
	eqp(t, "median_win", st.MedianWinQuote, "3")
	// Wilcoxon on [10,6,-4,20,4,2]: |x| ranks: 2→1, 4→2.5 (tie 4,-4), 6→4,
	// 10→5, 20→6; W+ = 1+2.5+4+5+6 = 18.5; mean 10.5; var 22.75;
	// z² = 64/22.75 = 2.813 > 2.7055 → rejects.
	w := st.Wilcoxon
	if w == nil || w.N != 6 {
		t.Fatalf("wilcoxon = %+v", w)
	}
	eq(t, "W+", w.WPlus, "18.5")
	eq(t, "wilcoxon mean", w.Mean, "10.5")
	eq(t, "wilcoxon var", w.VarW, "22.75")
	if w.Z2.StringFixed(3) != "2.813" || !w.Rejects {
		t.Fatalf("wilcoxon z²=%s rejects=%v", w.Z2, w.Rejects)
	}
	// Bootstrap over 3 day blocks: the mean over the whole set is 38/6;
	// every resample is a mix of day means {8, 8, 3} → CI within [3, 8].
	b := st.Bootstrap
	if b == nil || b.Days != 3 || b.Resamples != 10_000 {
		t.Fatalf("bootstrap = %+v", b)
	}
	eq(t, "bootstrap mean", b.Mean, "6.3333333333")
	if b.CILow.LessThan(d("3")) || b.CIHigh.GreaterThan(d("8")) || !b.CILow.LessThanOrEqual(b.CIHigh) {
		t.Fatalf("bootstrap CI = [%s, %s]", b.CILow, b.CIHigh)
	}
}

// TestComputeDayWindow: the previous-day window sees only that day's
// rows; a UTC day boundary is respected exactly.
func TestComputeDayWindow(t *testing.T) {
	in := synthetic()
	in.Window = Window{Label: PeriodDay, Start: t0.Add(24 * time.Hour), End: t0.Add(48 * time.Hour)}
	st := Compute(in)
	if st.N != 2 || st.Wins != 1 {
		t.Fatalf("day n=%d wins=%d", st.N, st.Wins)
	}
	eq(t, "day net", st.NetPnLQuote, "6")
	if st.MatchedPairs != 0 || st.Skipped[paperexec.SkipDepth] != 1 || len(st.Skipped) != 1 {
		t.Fatalf("day matched=%d skipped=%v", st.MatchedPairs, st.Skipped)
	}
	if st.LifetimeN != 0 {
		t.Fatalf("events closed on day 1 counted in day 2: %d", st.LifetimeN)
	}
	if st.Bootstrap != nil {
		t.Fatal("bootstrap computed on a single day")
	}
	if st.Concentration == nil || !st.Concentration.Equal(d("1")) {
		t.Fatalf("single-day concentration = %v, want 1", st.Concentration)
	}
}

// TestComputeCarryClosedPositions: carry samples are closed positions
// (holding time = lifetime; notional from spot_open) and funding rows
// come from the ledger's funding executions.
func TestComputeCarryClosedPositions(t *testing.T) {
	closed1, closed2 := t0.Add(48*time.Hour), t0.Add(72*time.Hour)
	positions := []paperexec.Position{
		{ID: "c1", RuleID: "c", Strategy: screener.StrategyCarry, Base: "BTC", Quote: "USDT", Qty: d("0.1"), OpenedAt: t0, ClosedAt: &closed1, Status: paperexec.StatusClosed, PnLQuote: d("25"), OpenPayload: map[string]any{"spot_open": "50000"}},
		{ID: "c2", RuleID: "c", Strategy: screener.StrategyCarry, Base: "ETH", Quote: "USDT", Qty: d("1"), OpenedAt: t0, ClosedAt: &closed2, Status: paperexec.StatusClosed, PnLQuote: d("-5"), OpenPayload: map[string]any{"spot_open": "2500"}},
		{ID: "c3", RuleID: "c", Strategy: screener.StrategyCarry, Base: "SOL", Quote: "USDT", Qty: d("10"), OpenedAt: t0, Status: paperexec.StatusOpen, OpenPayload: map[string]any{"spot_open": "100"}},
	}
	execs := []paperexec.Execution{
		{ID: "f1", RuleID: "c", Strategy: screener.StrategyCarry, Kind: paperexec.KindFunding, At: t0.Add(8 * time.Hour), PnLQuote: d("0.5")},
		{ID: "f2", RuleID: "c", Strategy: screener.StrategyCarry, Kind: paperexec.KindFunding, At: t0.Add(16 * time.Hour), PnLQuote: d("0.7")},
	}
	st := Compute(Inputs{Strategy: screener.StrategyCarry, Positions: positions, Executions: execs,
		Window: Window{Label: PeriodCumulative, Start: t0, End: t0.Add(100 * time.Hour)}, Now: t0.Add(100 * time.Hour)})
	if st.N != 2 || st.Wins != 1 || st.FundingRows != 2 {
		t.Fatalf("n=%d wins=%d funding_rows=%d", st.N, st.Wins, st.FundingRows)
	}
	eq(t, "net", st.NetPnLQuote, "20")
	eq(t, "funding", st.FundingQuote, "1.2")
	eq(t, "conservative", st.ConservativeNet, "20")
	// net bps: 25/5000 = 50 bps; -5/2500 = -20 bps → mean 15, median 15.
	eqp(t, "net_bps_mean", st.NetBpsMean, "15")
	// holding time 48 h and 72 h → mean 60 h = 216 000 s.
	eqp(t, "lifetime_mean", st.LifetimeMeanS, "216000")
	if st.MaxDrawdownFrac != nil {
		t.Fatal("drawdown fraction with zero capital must be nil")
	}
	eq(t, "max_drawdown", st.MaxDrawdownQuote, "5")
}

func TestWilsonKnownValues(t *testing.T) {
	// 0/10 → [0, 0.2775]; 10/10 → [0.7225, 1]; 50/100 → [0.4038, 0.5962].
	lo, hi := wilson(0, 10)
	if lo.StringFixed(4) != "0.0000" || hi.StringFixed(4) != "0.2775" {
		t.Fatalf("0/10 = [%s, %s]", lo.StringFixed(4), hi.StringFixed(4))
	}
	lo, hi = wilson(10, 10)
	if lo.StringFixed(4) != "0.7225" || hi.StringFixed(4) != "1.0000" {
		t.Fatalf("10/10 = [%s, %s]", lo.StringFixed(4), hi.StringFixed(4))
	}
	lo, hi = wilson(50, 100)
	if lo.StringFixed(4) != "0.4038" || hi.StringFixed(4) != "0.5962" {
		t.Fatalf("50/100 = [%s, %s]", lo.StringFixed(4), hi.StringFixed(4))
	}
}

func TestWilcoxonDoesNotRejectOnNoise(t *testing.T) {
	// Symmetric sample around zero: W+ near the mean → no rejection.
	xs := []decimal.Decimal{d("3"), d("-3"), d("2"), d("-2"), d("1"), d("-1"), d("5"), d("-5")}
	w := wilcoxon(xs)
	if w.Rejects || !w.WPlus.Equal(w.Mean) {
		t.Fatalf("wilcoxon = %+v", w)
	}
	// All positive: W+ = n(n+1)/2 → rejects for n ≥ 5 (z² = 4.5 at n = 5? no:
	// n=5: W+=15, mean 7.5, var 13.75, z²=4.09 > 2.7055 → rejects).
	all := []decimal.Decimal{d("1"), d("2"), d("3"), d("4"), d("5")}
	if w := wilcoxon(all); !w.Rejects {
		t.Fatalf("all-positive n=5 not rejected: %+v", w)
	}
	// n = 4 all positive: z² = 3.6 > 2.7055 as well; n = 3: W+=6, mean 3,
	// var 3.5, z² = 2.571 < 2.7055 → cannot reject (too few samples).
	three := []decimal.Decimal{d("1"), d("2"), d("3")}
	if w := wilcoxon(three); w.Rejects {
		t.Fatalf("n=3 rejected: %+v", w)
	}
}

func TestSqrtDec(t *testing.T) {
	for _, c := range []struct{ in, want string }{{"4", "2"}, {"2", "1.4142135624"}, {"0.25", "0.5"}, {"0", "0"}} {
		got := sqrtDec(d(c.in)).Round(10)
		if !got.Equal(d(c.want)) {
			t.Fatalf("sqrt(%s) = %s, want %s", c.in, got, c.want)
		}
	}
}

// TestComputeOpenPositionExposure (audit X6): the report carries the
// open side — count, the executor's unrealised exit marks summed,
// funding accrued, the newest mark's age, and unmarked positions
// counted as unknown rather than implied zero — while realised PnL
// stays realised-only.
func TestComputeOpenPositionExposure(t *testing.T) {
	mark := d("1.25")
	older := d("-0.5")
	ageMark, ageOlder := int64(4200), int64(9100)
	positions := []paperexec.Position{
		{ID: "o1", RuleID: "c", Strategy: screener.StrategyCarry, Base: "BTC", Quote: "USDT", Qty: d("0.1"),
			OpenedAt: t0, Status: paperexec.StatusOpen, FundingQuote: d("0.3"),
			MarkPnLQuote: &mark, MarkAgeMs: &ageMark, OpenPayload: map[string]any{"spot_open": "50000"}},
		{ID: "o2", RuleID: "c", Strategy: screener.StrategyCarry, Base: "ETH", Quote: "USDT", Qty: d("1"),
			OpenedAt: t0, Status: paperexec.StatusOpen, FundingQuote: d("-0.1"),
			MarkPnLQuote: &older, MarkAgeMs: &ageOlder, OpenPayload: map[string]any{"spot_open": "2500"}},
		{ID: "o3", RuleID: "c", Strategy: screener.StrategyCarry, Base: "SOL", Quote: "USDT", Qty: d("10"),
			OpenedAt: t0, Status: paperexec.StatusOpen, OpenPayload: map[string]any{"spot_open": "100"}},
	}
	st := Compute(Inputs{Strategy: screener.StrategyCarry, Positions: positions,
		Window: Window{Label: PeriodCumulative, Start: t0, End: t0.Add(100 * time.Hour)}, Now: t0.Add(100 * time.Hour)})
	if st.OpenPositions != 3 {
		t.Fatalf("open_positions = %d", st.OpenPositions)
	}
	if st.UnmarkedOpenPositions != 1 {
		t.Fatalf("unmarked = %d, want 1 (o3 has no live mark)", st.UnmarkedOpenPositions)
	}
	eq(t, "unrealised_mark", st.UnrealisedMarkQuote, "0.75")
	eq(t, "funding_accrued_open", st.FundingAccruedOpen, "0.2")
	if st.OpenMarkAgeMsMax == nil || *st.OpenMarkAgeMsMax != 9100 {
		t.Fatalf("mark age max = %v, want 9100", st.OpenMarkAgeMsMax)
	}
	if !st.NetPnLQuote.IsZero() {
		t.Fatalf("realised net = %s with no closed positions", st.NetPnLQuote)
	}
}
