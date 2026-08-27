// Package alerts is the T-070 alert evaluator (docs/design/scanner-suite.md
// §3/§7, docs/design/strategy-models.md §2.1, §3.1, §5.1): on every poll
// it derives one Signal per (rule, lane) from the screener Book, tracks
// lifetime, opens/closes screener_events rows, and hands measurement-only
// text to the notification router. Nothing here places an order; the
// executor (internal/screener/paperexec) subscribes to opened events.
package alerts

import (
	"context"
	"sort"
	"time"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/screener"
)

var (
	decTenK = decimal.NewFromInt(10_000)
	decTwo  = decimal.NewFromInt(2)
	decFour = decimal.NewFromInt(4)
)

// Lane identifies one evaluated pair of legs. For cross-venue spot
// VenueA is the ask side and VenueB the bid side; for carry / funding
// harvest both are the same venue (spot + perp on one exchange).
type Lane struct {
	Base   string         `json:"base"`
	Quote  string         `json:"quote"`
	VenueA screener.Venue `json:"venue_a"`
	VenueB screener.Venue `json:"venue_b"`
}

// Signal is one lane's measurement at one poll: the strategy-model
// figures the rule compares against its thresholds, plus the inputs
// the executor and the alert text need. Every price/qty/rate is
// decimal; every leg's age is carried.
type Signal struct {
	Rule     screener.Rule
	Strategy screener.Strategy
	Lane     Lane
	At       time.Time

	// Active is true when every threshold of the rule is met at this
	// poll (before lifetime/cooldown gating).
	Active bool
	// Reason explains a false Active for diagnostics ("" when active).
	Reason string
	// DataAgeOK reports the §1.1 gate; a failed gate never activates.
	DataAgeOK bool

	// Cross-venue spot (§2.1).
	GrossBps decimal.Decimal
	NetBps   decimal.Decimal
	ExecBps  decimal.Decimal
	// Carry (§3.1) / funding harvest (§5.1).
	BasisEntryBps decimal.Decimal
	FeesRTBps     decimal.Decimal
	FundingExpBps decimal.Decimal
	EdgeBps       decimal.Decimal
	PredictedBps  decimal.Decimal // F̂₁ × 10 000, labelled "predicted"
	MeanSettled   decimal.Decimal // per-interval fraction over the look-back
	FHatBps       decimal.Decimal // min(predicted, mean settled) for §5.1
	BreakevenN    int
	CarryAPR      decimal.Decimal // predicted rate annualised (display)

	LiquidityQuote decimal.Decimal
	// LiquidityUnknown: a leg's collector publishes no top-of-book size;
	// LiquidityQuote is zero and the signal is never active
	// (LIQUIDITY_UNKNOWN). Suspect: the shared asset-identity guard
	// (screener.GuardLane) refused the lane (SUSPECT_MISMATCH).
	LiquidityUnknown bool
	Suspect          bool
	SuspectReason    string
	AgeAMs           int64
	AgeBMs           int64
	FundingAgeMs     int64

	// Inputs at signal time (the executor re-reads the book after
	// latency; these are what the alert text and event peak report).
	SpotA screener.Quote
	SpotB screener.Quote
	Perp  screener.Perp
	FeeA  decimal.Decimal // bps, taker
	FeeB  decimal.Decimal // bps, taker (perp taker for carry/harvest)
}

// Threshold figure the lifetime tracker and event peak use.
func (s Signal) Score() decimal.Decimal {
	switch s.Strategy {
	case screener.StrategyCrossVenueSpot:
		return s.ExecBps
	case screener.StrategyFundingHarvest:
		return s.FHatBps
	default:
		return s.EdgeBps
	}
}

// FeeLookup mirrors screener.VenueFeeLookup; a venue that is disabled
// or unconfigured returns ok=false and the lane is skipped.
type FeeLookup = screener.VenueFeeLookup

// FundingLookback is how many settled intervals the evaluator reads for
// the carry mean (§3.1: 30) and the harvest mean (§5.1: 6).
const (
	carryLookback   = 30
	harvestLookback = 6
)

// Inputs bundles what a signal computation reads; FundingHistory may be
// nil (no settled history → predicted rate is used and MeanSettled
// equals it, which the text labels).
type Inputs struct {
	Book           *screener.Book
	SpotFees       FeeLookup
	PerpFees       FeeLookup
	FundingHistory screener.FundingStore
	PollInterval   time.Duration
	// MaxPlausibleSpreadBps is settings.max_plausible_spread_bps for the
	// guard; zero means screener.DefaultMaxPlausibleSpreadBps.
	MaxPlausibleSpreadBps decimal.Decimal
}

// dataAgeOK applies §1.1: each leg's age ≤ poll interval and the legs'
// ages within half a poll interval of each other.
func dataAgeOK(ageA, ageB, poll time.Duration) bool {
	if ageA < 0 || ageB < 0 {
		return false
	}
	if ageA > poll || ageB > poll {
		return false
	}
	diff := ageA - ageB
	if diff < 0 {
		diff = -diff
	}
	return diff <= poll/2
}

func fundingAgeOK(p screener.Perp, now time.Time, poll time.Duration) bool {
	age := now.Sub(p.At)
	return age >= 0 && age <= 2*poll && p.NextFundingAt.After(now)
}

func ruleQuotes(r screener.Rule) []string {
	if len(r.Quotes) == 0 {
		return []string{"USDT"}
	}
	return r.Quotes
}

func ruleAllowsBase(r screener.Rule, base string) bool {
	for _, d := range r.BasesDeny {
		if d == base {
			return false
		}
	}
	if len(r.BasesAllow) == 0 {
		return true
	}
	for _, a := range r.BasesAllow {
		if a == base {
			return true
		}
	}
	return false
}

func venueSet(vs []screener.Venue) map[screener.Venue]bool {
	if len(vs) == 0 {
		return nil
	}
	out := map[screener.Venue]bool{}
	for _, v := range vs {
		out[v] = true
	}
	return out
}

// ComputeSignals evaluates one rule over the book at now and returns
// one Signal per lane the rule's filters admit, sorted by lane for
// deterministic iteration. Pure apart from the funding-history read.
func ComputeSignals(ctx context.Context, in Inputs, r screener.Rule, now time.Time) []Signal {
	var out []Signal
	switch r.EffectiveStrategy() {
	case screener.StrategyCrossVenueSpot:
		out = spotSignals(in, r, now)
	default:
		out = perpSignals(ctx, in, r, now)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i].Lane, out[j].Lane
		if a.Base != b.Base {
			return a.Base < b.Base
		}
		if a.Quote != b.Quote {
			return a.Quote < b.Quote
		}
		if a.VenueA != b.VenueA {
			return a.VenueA < b.VenueA
		}
		return a.VenueB < b.VenueB
	})
	return out
}

func spotSignals(in Inputs, r screener.Rule, now time.Time) []Signal {
	var out []Signal
	buy, sell := venueSet(r.BuyVenues), venueSet(r.SellVenues)
	slip, buffer := r.SlipBps(), r.BufferBps()
	for _, quote := range ruleQuotes(r) {
		f := screener.SpreadFilters{
			BuyVenues: buy, SellVenues: sell, Quote: quote,
			BasesAllow: toSet(r.BasesAllow), BasesDeny: toSet(r.BasesDeny),
			Limit: 500,
			// Include the guarded lanes so the signal carries the verdict
			// and its reason (diagnostics, tests); spotActive refuses them
			// — the guard is ONE function (screener.GuardLane) for the
			// table, this evaluator and the executor.
			IncludeSuspect: true, IncludeUnknownLiquidity: true,
			MaxPlausibleSpreadBps: in.MaxPlausibleSpreadBps,
		}
		// tracker=nil: this computation must not touch the shared
		// GET /screener/spreads LifetimeTracker (spreads.go doc comment);
		// per-rule lifetime is tracked by the Evaluator itself.
		res := screener.ComputeSpreads(in.Book, in.SpotFees, nil, nil, now, f)
		for _, row := range res.Rows {
			quotes := in.Book.QuotesFor(row.Base, row.Quote)
			qa, qb := quotes[row.BuyVenue], quotes[row.SellVenue]
			s := Signal{
				Rule: r, Strategy: screener.StrategyCrossVenueSpot, At: now,
				Lane:     Lane{Base: row.Base, Quote: row.Quote, VenueA: row.BuyVenue, VenueB: row.SellVenue},
				GrossBps: row.SpreadBpsGross, NetBps: row.SpreadBpsNet,
				ExecBps:          row.SpreadBpsNet.Sub(slip).Sub(slip).Sub(buffer),
				LiquidityUnknown: row.LiquidityUnknown,
				Suspect:          row.Suspect, SuspectReason: row.SuspectReason,
				AgeAMs: row.BuyAgeMs, AgeBMs: row.SellAgeMs,
				SpotA: qa, SpotB: qb, FeeA: row.BuyFeeBps, FeeB: row.SellFeeBps,
			}
			if row.LiquidityQuote != nil {
				s.LiquidityQuote = *row.LiquidityQuote
			}
			s.DataAgeOK = dataAgeOK(now.Sub(qa.At), now.Sub(qb.At), in.PollInterval)
			s.Active, s.Reason = spotActive(r, s)
			out = append(out, s)
		}
	}
	return out
}

func spotActive(r screener.Rule, s Signal) (bool, string) {
	minSpread := decimal.Zero
	if r.MinSpreadBps != nil {
		minSpread = *r.MinSpreadBps
	}
	switch {
	case !s.DataAgeOK:
		return false, "DATA_AGE"
	case s.Suspect:
		return false, screener.SkipSuspectMismatch
	case s.LiquidityUnknown:
		return false, screener.SkipLiquidityUnknown
	case s.ExecBps.LessThan(minSpread):
		return false, "below_min_spread"
	case s.LiquidityQuote.LessThan(r.MinLiquidityQuote):
		return false, "below_min_liquidity"
	}
	return true, ""
}

func toSet(xs []string) map[string]bool {
	if len(xs) == 0 {
		return nil
	}
	out := map[string]bool{}
	for _, x := range xs {
		out[x] = true
	}
	return out
}

func perpSignals(ctx context.Context, in Inputs, r screener.Rule, now time.Time) []Signal {
	var out []Signal
	venues := venueSet(append(append([]screener.Venue{}, r.BuyVenues...), r.SellVenues...))
	strategy := r.EffectiveStrategy()
	for _, p := range in.Book.Perps() {
		if venues != nil && !venues[p.Venue] {
			continue
		}
		if !ruleAllowsBase(r, p.Base) || p.IntervalH <= 0 {
			continue
		}
		quote := p.Quote
		if quote == "" {
			quote = "USDT"
		}
		if !containsStr(ruleQuotes(r), quote) {
			continue
		}
		spot, ok := in.Book.QuotesFor(p.Base, quote)[p.Venue]
		if !ok || !spot.Ask.IsPositive() || !spot.Bid.IsPositive() || !p.Bid.IsPositive() {
			continue
		}
		fSpot, ok := in.SpotFees(p.Venue)
		if !ok {
			continue
		}
		fPerp, ok := in.PerpFees(p.Venue)
		if !ok {
			continue
		}
		s := Signal{
			Rule: r, Strategy: strategy, At: now,
			Lane:  Lane{Base: p.Base, Quote: quote, VenueA: p.Venue, VenueB: p.Venue},
			SpotA: spot, Perp: p, FeeA: fSpot, FeeB: fPerp,
			AgeAMs: ageMs(spot.At, now), AgeBMs: ageMs(p.At, now), FundingAgeMs: ageMs(p.At, now),
		}
		s.DataAgeOK = dataAgeOK(now.Sub(spot.At), now.Sub(p.At), in.PollInterval) && fundingAgeOK(p, now, in.PollInterval)
		s.BasisEntryBps = p.Bid.Sub(spot.Ask).Div(spot.Ask).Mul(decTenK)
		s.FeesRTBps = fSpot.Mul(decTwo).Add(fPerp.Mul(decTwo))
		// screener.Perp (the collector contract, types.go) carries no
		// top-of-book quantity, so liquidity is the spot leg's ask
		// notional only; the perp leg's depth is unknown and reported
		// as such (the executor cannot apply the §1.3 haircut to it).
		s.LiquidityQuote = spot.Ask.Mul(spot.AskQty)
		g := screener.GuardSpotPerp(spot, p, in.MaxPlausibleSpreadBps)
		s.LiquidityUnknown, s.Suspect, s.SuspectReason = g.LiquidityUnknown, g.Suspect, g.SuspectReason

		predicted := p.PredictedFundingRate
		if predicted.IsZero() {
			// Venue publishes no prediction (T-066 per-venue note): the
			// current rate stands in and the text says "predicted" for
			// the model input either way.
			predicted = p.FundingRate
		}
		s.PredictedBps = predicted.Mul(decTenK)
		s.CarryAPR = predicted.Mul(decimal.NewFromInt(24)).Div(decimal.NewFromInt(int64(p.IntervalH))).Mul(decimal.NewFromInt(365))

		lookback := carryLookback
		if strategy == screener.StrategyFundingHarvest {
			lookback = harvestLookback
		}
		mean, n := meanSettled(ctx, in.FundingHistory, p, lookback, now)
		if n == 0 {
			mean = predicted
		}
		s.MeanSettled = mean

		slip, buffer := r.SlipBps(), r.BufferBps()
		switch strategy {
		case screener.StrategyFundingHarvest:
			s.FHatBps = minDec(s.PredictedBps, mean.Mul(decTenK))
			costs := s.FeesRTBps.Add(slip.Mul(decFour)).Add(buffer)
			if s.FHatBps.IsPositive() {
				s.BreakevenN = int(costs.Div(s.FHatBps).Ceil().IntPart())
			}
			s.Active, s.Reason = harvestActive(r, s)
		default:
			nHold := int64(r.MaxHoldH() / p.IntervalH)
			if nHold < 1 {
				nHold = 1
			}
			s.FundingExpBps = s.PredictedBps.Add(mean.Mul(decTenK).Mul(decimal.NewFromInt(nHold - 1)))
			// §3.1 prints "2 × slip_bps" but §3.3 and the §3.5 worked
			// example charge slippage on all four legs (2 bps × 4 = 8);
			// the worked example is the golden figure, so four legs it is.
			s.EdgeBps = s.BasisEntryBps.Add(s.FundingExpBps).Sub(s.FeesRTBps).Sub(slip.Mul(decFour)).Sub(buffer)
			s.Active, s.Reason = carryActive(r, s)
		}
		out = append(out, s)
	}
	return out
}

func carryActive(r screener.Rule, s Signal) (bool, string) {
	switch {
	case !s.DataAgeOK:
		return false, "DATA_AGE"
	case s.Suspect:
		return false, screener.SkipSuspectMismatch
	case s.LiquidityUnknown:
		return false, screener.SkipLiquidityUnknown
	case !s.PredictedBps.IsPositive():
		return false, "predicted_funding_not_positive"
	case s.EdgeBps.LessThan(r.MinEdgeBps()):
		return false, "below_min_edge"
	case r.MinCarryAPR != nil && s.CarryAPR.LessThan(*r.MinCarryAPR):
		return false, "below_min_carry_apr"
	case s.LiquidityQuote.LessThan(r.MinLiquidityQuote):
		return false, "below_min_liquidity"
	}
	return true, ""
}

func harvestActive(r screener.Rule, s Signal) (bool, string) {
	switch {
	case !s.DataAgeOK:
		return false, "DATA_AGE"
	case s.Suspect:
		return false, screener.SkipSuspectMismatch
	case s.LiquidityUnknown:
		return false, screener.SkipLiquidityUnknown
	case !s.FHatBps.IsPositive():
		return false, "funding_not_positive"
	case s.FHatBps.LessThan(r.MinFundingBps()):
		return false, "below_min_funding"
	case s.BreakevenN > r.MaxBreakevenIntervals():
		return false, "breakeven_too_long"
	case s.BasisEntryBps.LessThan(r.MaxNegativeBasisBps().Neg()):
		return false, "basis_too_negative"
	case r.MinCarryAPR != nil && s.CarryAPR.LessThan(*r.MinCarryAPR):
		return false, "below_min_carry_apr"
	case s.LiquidityQuote.LessThan(r.MinLiquidityQuote):
		return false, "below_min_liquidity"
	}
	return true, ""
}

// meanSettled returns the mean of the last n settled rates for the
// perp's (venue, base) and how many points were used.
func meanSettled(ctx context.Context, fs screener.FundingStore, p screener.Perp, n int, now time.Time) (decimal.Decimal, int) {
	if fs == nil || n <= 0 {
		return decimal.Zero, 0
	}
	since := now.Add(-time.Duration(n*p.IntervalH) * time.Hour)
	series, err := fs.ListFunding(ctx, p.Base, []screener.Venue{p.Venue}, since)
	if err != nil {
		return decimal.Zero, 0
	}
	var pts []screener.FundingPoint
	for _, s := range series {
		if s.Venue == p.Venue && s.Base == p.Base {
			pts = append(pts, s.Points...)
		}
	}
	if len(pts) == 0 {
		return decimal.Zero, 0
	}
	sort.Slice(pts, func(i, j int) bool { return pts[i].At.Before(pts[j].At) })
	if len(pts) > n {
		pts = pts[len(pts)-n:]
	}
	sum := decimal.Zero
	used := 0
	for _, pt := range pts {
		if pt.At.After(now) {
			continue // a predicted/future row is never a settled rate
		}
		v, err := decimal.NewFromString(pt.Rate)
		if err != nil {
			continue
		}
		sum = sum.Add(v)
		used++
	}
	if used == 0 {
		return decimal.Zero, 0
	}
	return sum.Div(decimal.NewFromInt(int64(used))), used
}

func minDec(a, b decimal.Decimal) decimal.Decimal {
	if b.LessThan(a) {
		return b
	}
	return a
}

func containsStr(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func ageMs(t, now time.Time) int64 {
	d := now.Sub(t)
	if d < 0 {
		return 0
	}
	return d.Milliseconds()
}
