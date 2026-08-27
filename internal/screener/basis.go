package screener

import (
	"sort"
	"time"

	"github.com/shopspring/decimal"
)

// HoldDaysAssumed is the holding period §3's carry_apr_net amortises the
// four round-trip taker fees over. It is a modelling assumption, not a
// measured hold time (T-071's auto-paper Carry strategy has its own real
// close-on-convergence/max-hold logic; this is only the number shown on
// the funding monitor row before any position exists) — documented here
// AND echoed on every row (HoldDaysAssumed) so the console never has to
// hardcode it to explain the figure.
const HoldDaysAssumed = 30

var (
	decTwo       = decimal.NewFromInt(2)
	decDaysAYear = decimal.NewFromInt(365)
	decHoldDays  = decimal.NewFromInt(HoldDaysAssumed)
	decHoursADay = decimal.NewFromInt(24)
)

// PerpRow is one row of GET /screener/perpetuals (design §7).
type PerpRow struct {
	Venue Venue  `json:"venue"`
	Base  string `json:"base"`
	Quote string `json:"quote"`

	SpotMid   decimal.Decimal `json:"spot_mid"`
	PerpMark  decimal.Decimal `json:"perp_mark"`
	PerpIndex decimal.Decimal `json:"perp_index"`

	BasisBps decimal.Decimal `json:"basis_bps"`

	FundingRate          decimal.Decimal `json:"funding_rate"`
	PredictedFundingRate decimal.Decimal `json:"predicted_funding_rate"`
	FundingIntervalH     int             `json:"funding_interval_h"`
	NextFundingAt        time.Time       `json:"next_funding_at"`

	// CarryAPRGross/Net are fractions (0.1095 = 10.95%/yr), not
	// percentages, matching the min_carry_apr filter's unit (design §7).
	CarryAPRGross   decimal.Decimal `json:"carry_apr_gross"`
	CarryAPRNet     decimal.Decimal `json:"carry_apr_net"`
	HoldDaysAssumed int             `json:"hold_days_assumed"`

	SpotFeeBps decimal.Decimal `json:"spot_fee_bps"`
	PerpFeeBps decimal.Decimal `json:"perp_fee_bps"`
	AgeMs      int64           `json:"age_ms"`
}

// PerpFilters narrows GET /screener/perpetuals rows.
type PerpFilters struct {
	Venue       Venue // "" = no restriction
	Base        string
	MinCarryAPR *decimal.Decimal // fraction, e.g. 0.10
	Limit       int
}

const (
	defaultPerpsLimit = 200
	maxPerpsLimit     = 500
)

// SpotMidLookup resolves the spot mid ((bid+ask)/2) for (venue, base,
// quote); ok=false when that venue has no spot quote for the pair
// (basis needs THAT VENUE's own spot book, never another venue's — a
// borrowed mid would silently misprice basis on venues screened only
// for perps).
type SpotMidLookup func(venue Venue, base, quote string) (mid decimal.Decimal, ok bool)

// ComputePerps is the golden-tested core of GET /screener/perpetuals.
func ComputePerps(book *Book, spotMid SpotMidLookup, spotFees, perpFees VenueFeeLookup, now time.Time, f PerpFilters) []PerpRow {
	var out []PerpRow
	for _, p := range book.Perps() {
		if f.Venue != "" && p.Venue != f.Venue {
			continue
		}
		if f.Base != "" && p.Base != f.Base {
			continue
		}
		if p.IntervalH <= 0 || !p.Mark.IsPositive() {
			continue // guard: a zero/negative interval or mark cannot be annualised safely
		}
		mid, ok := spotMid(p.Venue, p.Base, p.Quote)
		if !ok || !mid.IsPositive() {
			continue
		}
		spotFeeBps, ok := spotFees(p.Venue)
		if !ok {
			continue
		}
		perpFeeBps, ok := perpFees(p.Venue)
		if !ok {
			continue
		}
		row := buildPerpRow(p, mid, spotFeeBps, perpFeeBps, now)
		if f.MinCarryAPR != nil && row.CarryAPRNet.LessThan(*f.MinCarryAPR) {
			continue
		}
		out = append(out, row)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].CarryAPRNet.GreaterThan(out[j].CarryAPRNet)
	})
	limit := f.Limit
	if limit <= 0 {
		limit = defaultPerpsLimit
	}
	if limit > maxPerpsLimit {
		limit = maxPerpsLimit
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func buildPerpRow(p Perp, spotMid, spotFeeBps, perpFeeBps decimal.Decimal, now time.Time) PerpRow {
	basisBps := p.Mark.Sub(spotMid).Div(spotMid).Mul(decTenK)

	intervalH := decimal.NewFromInt(int64(p.IntervalH))
	// Order: multiply before dividing (design D6-style precision note)
	// so 0.0001 * 24 / 8 * 365 = 0.1095 exactly rather than routing
	// through a lossier 24/8 first.
	carryGross := p.FundingRate.Mul(decHoursADay).Div(intervalH).Mul(decDaysAYear)

	spotFeeFrac := spotFeeBps.Div(decTenK)
	perpFeeFrac := perpFeeBps.Div(decTenK)
	roundTripFeeFrac := spotFeeFrac.Mul(decTwo).Add(perpFeeFrac.Mul(decTwo))
	feeAPR := roundTripFeeFrac.Mul(decDaysAYear).Div(decHoldDays)
	carryNet := carryGross.Sub(feeAPR)

	return PerpRow{
		Venue: p.Venue, Base: p.Base, Quote: p.Quote,
		SpotMid: spotMid, PerpMark: p.Mark, PerpIndex: p.Index,
		BasisBps:    basisBps,
		FundingRate: p.FundingRate, PredictedFundingRate: p.PredictedFundingRate,
		FundingIntervalH: p.IntervalH, NextFundingAt: p.NextFundingAt,
		CarryAPRGross: carryGross, CarryAPRNet: carryNet, HoldDaysAssumed: HoldDaysAssumed,
		SpotFeeBps: spotFeeBps, PerpFeeBps: perpFeeBps,
		AgeMs: ageMs(p.At, now),
	}
}
