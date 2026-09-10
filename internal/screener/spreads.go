package screener

import (
	"sort"
	"sync"
	"time"

	"github.com/shopspring/decimal"
)

var (
	decOne  = decimal.NewFromInt(1)
	decTenK = decimal.NewFromInt(10000)
)

// SpreadKey identifies one tracked cross-venue spread lane, independent
// of any single request's filters.
type SpreadKey struct {
	Base      string
	Quote     string
	BuyVenue  Venue
	SellVenue Venue
}

// SpreadRow is one row of GET /screener/spreads (design §7), decimal
// end to end.
type SpreadRow struct {
	Base      string `json:"base"`
	Quote     string `json:"quote"`
	BuyVenue  Venue  `json:"buy_venue"`
	SellVenue Venue  `json:"sell_venue"`

	BuyAsk     decimal.Decimal `json:"buy_ask"`
	BuyAskQty  decimal.Decimal `json:"buy_ask_qty"`
	SellBid    decimal.Decimal `json:"sell_bid"`
	SellBidQty decimal.Decimal `json:"sell_bid_qty"`

	SpreadBpsGross decimal.Decimal `json:"spread_bps_gross"`
	SpreadBpsNet   decimal.Decimal `json:"spread_bps_net"`
	// LiquidityQuote is min(ask notional, bid notional) in quote, or
	// null when either side's collector publishes no top-of-book size
	// (LiquidityUnknown=true; Gate's bulk ticker) — never a zero that
	// looks like a measured empty book.
	LiquidityQuote   *decimal.Decimal `json:"liquidity_quote"`
	LiquidityUnknown bool             `json:"liquidity_unknown"`
	// Suspect marks a lane the asset-identity guard (guard.go) refuses:
	// the two venues' prices cannot be the same asset. Suspect rows are
	// excluded unless the request asks for them (include_suspect=1) and
	// are never alerted on or executed.
	Suspect       bool   `json:"suspect"`
	SuspectReason string `json:"suspect_reason,omitempty"`

	LifetimeS   int64      `json:"lifetime_s"`
	FirstSeenAt *time.Time `json:"first_seen_at,omitempty"`

	BuyAgeMs  int64 `json:"buy_age_ms"`
	SellAgeMs int64 `json:"sell_age_ms"`

	BuyFeeBps  decimal.Decimal `json:"buy_fee_bps"`
	SellFeeBps decimal.Decimal `json:"sell_fee_bps"`

	Networks SpreadNetworks `json:"networks"`

	// stale is set when SpreadFilters.MaxLegAge is given and the two
	// legs fail DataAgeOK against it; it only steers the ranking (a
	// stale row never outranks a fresh one) and is not a wire field —
	// the ages above are, and the evaluator applies the gate itself.
	stale bool
}

// SpreadNetworks is the per-row deposit/withdraw status pair (design §7:
// "networks: { buy_withdraw, sell_deposit, reason? }").
type SpreadNetworks struct {
	BuyWithdraw NetworkStatusValue `json:"buy_withdraw"`
	SellDeposit NetworkStatusValue `json:"sell_deposit"`
	Reason      string             `json:"reason,omitempty"`
}

// SpreadFilters narrows the rows ComputeSpreads returns; the zero value
// applies no filter beyond "buy venue != sell venue". Filters are purely
// a view over the book/tracker state — they never mutate lifetime
// tracking (see LifetimeTracker's doc comment for why that matters).
type SpreadFilters struct {
	MinSpreadBpsNet   *decimal.Decimal
	MinLiquidityQuote *decimal.Decimal
	MinLifetimeS      int64
	BuyVenues         map[Venue]bool // nil/empty = no restriction
	SellVenues        map[Venue]bool // nil/empty = no restriction
	Quote             string         // "" = no restriction (single quote; see Quotes)
	// Quotes restricts to a set of quote assets (?quote=USDT,USDC).
	// USDT / USDC / FDUSD / USD are distinct quotes and are NEVER
	// merged: a row's quote is exactly the venue's quote asset.
	Quotes     map[string]bool
	BasesAllow map[string]bool
	BasesDeny  map[string]bool
	Limit      int // <=0 = default (200), capped at 500
	// NoLimit returns every filtered row regardless of Limit. The page
	// cap is a table page size; the alert evaluator sets NoLimit because
	// an evaluation universe bounded by a page size hides every lane
	// ranked past it (the evaluator applies its own, configurable cap
	// after the ranking below).
	NoLimit bool
	// MaxLegAge, when > 0, marks rows whose legs fail DataAgeOK against
	// it and ranks them after every fresh row. Rows are always ranked
	// clean-before-suspect/unknown-liquidity, then by net bps; with a
	// MaxLegAge the stale rows join the back of the queue, so a cap
	// (Limit or the evaluator's) cuts artefacts before genuine lanes.
	MaxLegAge time.Duration

	// IncludeSuspect keeps rows the asset-identity guard flagged
	// (default false: excluded, counted in SpreadsResult.ExcludedSuspect).
	IncludeSuspect bool
	// IncludeUnknownLiquidity keeps rows whose liquidity is unknown
	// (default false: excluded regardless of MinLiquidityQuote, counted
	// in SpreadsResult.ExcludedLiquidityUnknown). A kept row still has
	// liquidity_quote null — it is never compared with MinLiquidityQuote.
	IncludeUnknownLiquidity bool
	// MaxPlausibleSpreadBps is settings.max_plausible_spread_bps; zero
	// means DefaultMaxPlausibleSpreadBps.
	MaxPlausibleSpreadBps decimal.Decimal
}

const (
	defaultSpreadsLimit = 200
	maxSpreadsLimit     = 500
)

// SpreadsResult is ComputeSpreads' return: the filtered, sorted,
// limited rows plus the total BEFORE limit (design §7: "total" counts
// past filters, not past the page).
type SpreadsResult struct {
	Rows  []SpreadRow
	Total int
	// Excluded* count lanes the guards removed before the filters
	// (0 when the matching Include* flag is set) so the caller can say
	// "N lanes hidden" instead of silently showing fewer rows.
	ExcludedSuspect          int
	ExcludedLiquidityUnknown int
}

// LifetimeTracker tracks, per (base, quote, buy_venue, sell_venue) lane,
// how long the NET spread has stayed at or above ONE fixed threshold
// (design §3: "seconds since the spread first exceeded ... without
// dropping below it"). The threshold is fixed at construction and
// shared by every caller/request — it is deliberately NOT the
// min_spread_bps query parameter a particular GET /screener/spreads
// request happens to pass: if it were, one viewer's filter would reset
// or extend "first seen" for every OTHER viewer watching the same lane,
// which is a shared, request-independent fact and must not be
// observer-dependent. A request's own min_spread_bps/min_lifetime_s
// stay pure row filters (SpreadFilters) applied on top of whatever this
// tracker already recorded.
type LifetimeTracker struct {
	threshold decimal.Decimal
	mu        sync.Mutex
	firstSeen map[SpreadKey]time.Time
}

// NewLifetimeTracker returns a tracker keyed off one fixed net-bps
// threshold (commonly 0: "spread lifetime" measured from when a lane
// first turns net-positive).
func NewLifetimeTracker(thresholdBps decimal.Decimal) *LifetimeTracker {
	return &LifetimeTracker{threshold: thresholdBps, firstSeen: make(map[SpreadKey]time.Time)}
}

// Observe records one lane's current net spread at time now and returns
// the lane's first-seen time and lifetime in whole seconds. netBps below
// the tracker's threshold resets (deletes) the lane's tracking entry and
// reports a zero lifetime.
func (t *LifetimeTracker) Observe(key SpreadKey, netBps decimal.Decimal, now time.Time) (firstSeen time.Time, lifetimeS int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if netBps.LessThan(t.threshold) {
		delete(t.firstSeen, key)
		return time.Time{}, 0
	}
	fs, ok := t.firstSeen[key]
	if !ok {
		t.firstSeen[key] = now
		return now, 0
	}
	if now.Before(fs) {
		// Clock oddity (e.g. test clocks going backward): treat as a
		// fresh sighting rather than reporting a negative lifetime.
		t.firstSeen[key] = now
		return now, 0
	}
	return fs, int64(now.Sub(fs) / time.Second)
}

// VenueFeeLookup resolves the spot taker fee (bps) for a venue; ADMIN
// configures these in Settings.Venues[venue].SpotTakerBps.
type VenueFeeLookup func(v Venue) (bps decimal.Decimal, ok bool)

// NetworkLookup resolves one asset's withdraw/deposit status on a venue.
// nil means "no lookup wired" — every row falls back to
// UnknownNetworkStatus (T-066 collectors, and Gate's public endpoint,
// wire a real one later; nothing before that infers a status).
type NetworkLookup func(v Venue, asset string) NetworkStatus

// ComputeSpreads is the golden-tested core of GET /screener/spreads
// (design §3/§7). now is passed explicitly so callers and tests get a
// deterministic clock; tracker may be nil (lifetime always reports 0,
// untracked) for a pure, stateless computation.
func ComputeSpreads(book *Book, fees VenueFeeLookup, tracker *LifetimeTracker, netLookup NetworkLookup, now time.Time, f SpreadFilters) SpreadsResult {
	var all []SpreadRow
	var excludedSuspect, excludedUnknown int
	for _, pair := range book.Pairs() {
		if f.Quote != "" && pair.Quote != f.Quote {
			continue
		}
		if len(f.Quotes) > 0 && !f.Quotes[pair.Quote] {
			continue
		}
		if len(f.BasesAllow) > 0 && !f.BasesAllow[pair.Base] {
			continue
		}
		if f.BasesDeny[pair.Base] {
			continue
		}
		byVenue := book.QuotesFor(pair.Base, pair.Quote)
		venues := make([]Venue, 0, len(byVenue))
		for v := range byVenue {
			venues = append(venues, v)
		}
		sort.Slice(venues, func(i, j int) bool { return venues[i] < venues[j] })

		for _, buyVenue := range venues {
			if len(f.BuyVenues) > 0 && !f.BuyVenues[buyVenue] {
				continue
			}
			buyQ := byVenue[buyVenue]
			if !buyQ.Ask.IsPositive() {
				continue
			}
			buyFeeBps, ok := fees(buyVenue)
			if !ok {
				continue
			}
			for _, sellVenue := range venues {
				if sellVenue == buyVenue {
					continue
				}
				if len(f.SellVenues) > 0 && !f.SellVenues[sellVenue] {
					continue
				}
				sellQ := byVenue[sellVenue]
				if !sellQ.Bid.IsPositive() {
					continue
				}
				sellFeeBps, ok := fees(sellVenue)
				if !ok {
					continue
				}
				row := buildSpreadRow(pair, buyVenue, sellVenue, buyQ, sellQ, buyFeeBps, sellFeeBps, now)
				// Task 1a/1b: the shared guard decides suspect / unknown
				// liquidity BEFORE any filter or sort, so a mismatched
				// ticker can never reach the top of the table.
				g := GuardLane(buyQ, sellQ, byVenue, f.MaxPlausibleSpreadBps)
				row.Suspect, row.SuspectReason = g.Suspect, g.SuspectReason
				if g.LiquidityUnknown {
					row.LiquidityUnknown, row.LiquidityQuote = true, nil
				}
				if f.MaxLegAge > 0 {
					row.stale = !DataAgeOK(now.Sub(buyQ.At), now.Sub(sellQ.At), f.MaxLegAge)
				}
				if row.Suspect && !f.IncludeSuspect {
					excludedSuspect++
					continue
				}
				if row.LiquidityUnknown && !f.IncludeUnknownLiquidity {
					excludedUnknown++
					continue
				}
				// A suspect lane's lifetime is not tracked: "seconds since
				// the spread first exceeded the threshold" is meaningless
				// for a spread that is not between the same asset.
				if tracker != nil && !row.Suspect {
					key := SpreadKey{Base: pair.Base, Quote: pair.Quote, BuyVenue: buyVenue, SellVenue: sellVenue}
					fs, life := tracker.Observe(key, row.SpreadBpsNet, now)
					row.LifetimeS = life
					if !fs.IsZero() {
						t := fs
						row.FirstSeenAt = &t
					}
				}
				if netLookup != nil {
					row.Networks = SpreadNetworks{
						BuyWithdraw: netLookup(buyVenue, pair.Base).Status,
						SellDeposit: netLookup(sellVenue, pair.Base).Status,
					}
				} else {
					row.Networks = SpreadNetworks{BuyWithdraw: NetworkUnknown, SellDeposit: NetworkUnknown, Reason: "collectors not started"}
				}
				all = append(all, row)
			}
		}
	}

	filtered := all[:0:0]
	for _, row := range all {
		if f.MinSpreadBpsNet != nil && row.SpreadBpsNet.LessThan(*f.MinSpreadBpsNet) {
			continue
		}
		if f.MinLiquidityQuote != nil && row.LiquidityQuote != nil && row.LiquidityQuote.LessThan(*f.MinLiquidityQuote) {
			continue
		}
		if f.MinLifetimeS > 0 && row.LifetimeS < f.MinLifetimeS {
			continue
		}
		filtered = append(filtered, row)
	}

	// Quality before size: a row the guard flagged, whose liquidity is
	// unknown, or whose legs are stale (MaxLegAge) can carry the largest
	// net bps in the book precisely BECAUSE it is an artefact (a
	// mismatched ticker, a fresh leg against a stale one). Ranking such
	// rows first meant a cap kept the artefacts and cut the real lanes;
	// they now sort after every clean row, and by net bps within each
	// group.
	sort.SliceStable(filtered, func(i, j int) bool {
		ri, rj := spreadRank(filtered[i]), spreadRank(filtered[j])
		if ri != rj {
			return ri < rj
		}
		return filtered[i].SpreadBpsNet.GreaterThan(filtered[j].SpreadBpsNet)
	})

	total := len(filtered)
	if !f.NoLimit {
		limit := f.Limit
		if limit <= 0 {
			limit = defaultSpreadsLimit
		}
		if limit > maxSpreadsLimit {
			limit = maxSpreadsLimit
		}
		if len(filtered) > limit {
			filtered = filtered[:limit]
		}
	}
	return SpreadsResult{Rows: filtered, Total: total, ExcludedSuspect: excludedSuspect, ExcludedLiquidityUnknown: excludedUnknown}
}

// spreadRank is the ranking group of a row: 0 for a clean, fresh lane,
// 1 for anything the guard or the data-age gate would refuse.
func spreadRank(row SpreadRow) int {
	if row.Suspect || row.LiquidityUnknown || row.stale {
		return 1
	}
	return 0
}

func buildSpreadRow(pair PairKey, buyVenue, sellVenue Venue, buyQ, sellQ Quote, buyFeeBps, sellFeeBps decimal.Decimal, now time.Time) SpreadRow {
	grossSpread := sellQ.Bid.Sub(buyQ.Ask).Div(buyQ.Ask)
	grossBps := grossSpread.Mul(decTenK)

	buyFeeFrac := buyFeeBps.Div(decTenK)
	sellFeeFrac := sellFeeBps.Div(decTenK)
	sellNetProceeds := sellQ.Bid.Mul(decOne.Sub(sellFeeFrac))
	buyNetCost := buyQ.Ask.Mul(decOne.Add(buyFeeFrac))
	netSpread := sellNetProceeds.Sub(buyNetCost).Div(buyQ.Ask)
	netBps := netSpread.Mul(decTenK)

	buyNotional := buyQ.Ask.Mul(buyQ.AskQty)
	sellNotional := sellQ.Bid.Mul(sellQ.BidQty)
	liquidity := buyNotional
	if sellNotional.LessThan(liquidity) {
		liquidity = sellNotional
	}

	return SpreadRow{
		Base: pair.Base, Quote: pair.Quote,
		BuyVenue: buyVenue, SellVenue: sellVenue,
		BuyAsk: buyQ.Ask, BuyAskQty: buyQ.AskQty,
		SellBid: sellQ.Bid, SellBidQty: sellQ.BidQty,
		SpreadBpsGross: grossBps, SpreadBpsNet: netBps,
		LiquidityQuote: &liquidity,
		BuyAgeMs:       ageMs(buyQ.At, now),
		SellAgeMs:      ageMs(sellQ.At, now),
		BuyFeeBps:      buyFeeBps, SellFeeBps: sellFeeBps,
	}
}

func ageMs(t, now time.Time) int64 {
	d := now.Sub(t)
	if d < 0 {
		return 0
	}
	return d.Milliseconds()
}
