package screener

import (
	"fmt"
	"sort"

	"github.com/shopspring/decimal"
)

// Suspect reasons a LaneGuard reports. They are wire values
// (SpreadRow.suspect_reason) and evaluator/executor skip details, so
// they never change without a docs/design/scanner-suite.md §7 update.
const (
	// SuspectSpreadImplausible: the two sides' mids differ by more than
	// settings.max_plausible_spread_bps — on a real, fungible asset a
	// 20 %+ cross-venue gap does not survive a poll; the far likelier
	// explanation is that the same ticker names two different assets
	// (the live VON gate→mexc case: 1 922 997 909 779 bps).
	SuspectSpreadImplausible = "spread_exceeds_max_plausible"
	// SuspectMedianOutlier: with ≥ 3 venues quoting the pair, one side's
	// mid is more than 50 % away from the cross-venue median mid.
	SuspectMedianOutlier = "price_deviates_from_median"
)

// Skip reasons shared by the alert evaluator (Signal.Reason) and the
// paper executor (Position.SkippedReason) when a lane fails the guard.
// Defined here so both apply the SAME words for the SAME function.
const (
	SkipSuspectMismatch  = "SUSPECT_MISMATCH"
	SkipLiquidityUnknown = "LIQUIDITY_UNKNOWN"
)

// DefaultMaxPlausibleSpreadBps is settings.max_plausible_spread_bps
// when the stored document predates the field (20 %).
var DefaultMaxPlausibleSpreadBps = decimal.NewFromInt(2000)

// medianOutlierFrac is the fixed 50 % band around the cross-venue
// median mid; medianMinVenues the venue count from which the median is
// meaningful (a 2-venue "median" would just re-test the pair itself).
var (
	medianOutlierFrac = decimal.RequireFromString("0.5")
	medianMinVenues   = 3
)

// LaneGuard is the asset-identity / liquidity verdict for one
// (base, quote, venue A, venue B) lane. Nothing is persisted; it is
// recomputed from the book on every request, tick and execution.
type LaneGuard struct {
	Suspect       bool
	SuspectReason string
	// Detail is the human-readable measurement behind Suspect
	// (executor skip detail, logs); empty when not suspect.
	Detail string
	// LiquidityUnknown is true when either side's collector publishes no
	// top-of-book size (Quote.LiquidityUnknown) — the lane's notional
	// cannot be compared with any minimum and must not be acted on.
	LiquidityUnknown bool
}

// GuardLane is THE shared guard (task 1d): GET /screener/spreads, the
// alert evaluator and the paper executor all call it, so a lane that
// is excluded from the table can never be alerted on or executed.
//
// a and b are the two sides' quotes; peers are every venue's quote for
// the same (base, quote) — the median test only runs when at least
// medianMinVenues of them carry a positive bid and ask. maxPlausibleBps
// ≤ 0 means DefaultMaxPlausibleSpreadBps.
func GuardLane(a, b Quote, peers map[Venue]Quote, maxPlausibleBps decimal.Decimal) LaneGuard {
	g := LaneGuard{LiquidityUnknown: a.LiquidityUnknown || b.LiquidityUnknown}
	if !maxPlausibleBps.IsPositive() {
		maxPlausibleBps = DefaultMaxPlausibleSpreadBps
	}
	midA, okA := mid(a)
	midB, okB := mid(b)
	if !okA || !okB {
		return g
	}
	// (1) pairwise plausibility: |mid_A − mid_B| / min(mid) in bps.
	lo := midA
	if midB.LessThan(lo) {
		lo = midB
	}
	gapBps := midA.Sub(midB).Abs().Div(lo).Mul(decTenK)
	if gapBps.GreaterThan(maxPlausibleBps) {
		g.Suspect, g.SuspectReason = true, SuspectSpreadImplausible
		g.Detail = fmt.Sprintf("mid %s@%s vs %s@%s differ by %s bps > max_plausible_spread_bps %s",
			midA.String(), a.Venue, midB.String(), b.Venue, gapBps.StringFixed(2), maxPlausibleBps.String())
		return g
	}
	// (2) cross-venue median: with ≥ 3 venues, either side > 50 % off.
	mids := make([]decimal.Decimal, 0, len(peers))
	for _, q := range peers {
		if m, ok := mid(q); ok {
			mids = append(mids, m)
		}
	}
	if len(mids) < medianMinVenues {
		return g
	}
	med := medianDec(mids)
	if !med.IsPositive() {
		return g
	}
	for _, side := range []struct {
		v Venue
		m decimal.Decimal
	}{{a.Venue, midA}, {b.Venue, midB}} {
		dev := side.m.Sub(med).Abs().Div(med)
		if dev.GreaterThan(medianOutlierFrac) {
			g.Suspect, g.SuspectReason = true, SuspectMedianOutlier
			g.Detail = fmt.Sprintf("mid %s@%s deviates %s%% from the %d-venue median %s",
				side.m.String(), side.v, dev.Mul(decimal.NewFromInt(100)).StringFixed(1), len(mids), med.String())
			return g
		}
	}
	return g
}

// GuardSpotPerp applies the pairwise plausibility test to a same-venue
// spot/perp pair (carry, funding harvest): a perp bid more than
// maxPlausibleBps away from the spot ask is not a basis, it is a
// different contract (or a broken feed). Liquidity is the spot leg's
// only (Perp carries no top-of-book size, see alerts.perpSignals).
func GuardSpotPerp(spot Quote, perp Perp, maxPlausibleBps decimal.Decimal) LaneGuard {
	g := LaneGuard{LiquidityUnknown: spot.LiquidityUnknown}
	if !maxPlausibleBps.IsPositive() {
		maxPlausibleBps = DefaultMaxPlausibleSpreadBps
	}
	if !spot.Ask.IsPositive() || !perp.Bid.IsPositive() {
		return g
	}
	gapBps := perp.Bid.Sub(spot.Ask).Abs().Div(spot.Ask).Mul(decTenK)
	if gapBps.GreaterThan(maxPlausibleBps) {
		g.Suspect, g.SuspectReason = true, SuspectSpreadImplausible
		g.Detail = fmt.Sprintf("perp bid %s vs spot ask %s on %s differ by %s bps > max_plausible_spread_bps %s",
			perp.Bid.String(), spot.Ask.String(), spot.Venue, gapBps.StringFixed(2), maxPlausibleBps.String())
	}
	return g
}

// SkipReason maps a guard verdict to the shared skip reason ("" when the
// lane passes). Identity comes first: a lane whose two prices cannot be
// the same asset is skipped as SUSPECT_MISMATCH even when one side also
// has no size (the VON case has both); LIQUIDITY_UNKNOWN is the verdict
// for a plausible price with no top-of-book size. The tests pin this
// order.
func (g LaneGuard) SkipReason() string {
	switch {
	case g.Suspect:
		return SkipSuspectMismatch
	case g.LiquidityUnknown:
		return SkipLiquidityUnknown
	}
	return ""
}

func mid(q Quote) (decimal.Decimal, bool) {
	if !q.Bid.IsPositive() || !q.Ask.IsPositive() {
		return decimal.Decimal{}, false
	}
	return q.Bid.Add(q.Ask).Div(decimal.NewFromInt(2)), true
}

func medianDec(xs []decimal.Decimal) decimal.Decimal {
	sorted := append([]decimal.Decimal(nil), xs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].LessThan(sorted[j]) })
	n := len(sorted)
	if n%2 == 1 {
		return sorted[n/2]
	}
	return sorted[n/2-1].Add(sorted[n/2]).Div(decimal.NewFromInt(2))
}
