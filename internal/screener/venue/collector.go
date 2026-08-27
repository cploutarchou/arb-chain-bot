// Package venue holds one read-only public-market-data collector per
// Scanner Suite venue (T-066, docs/design/scanner-suite.md §2) and the
// Poller that drives them into screener.Book.
//
// Non-negotiables (.claude/skills/scanner-suite/SKILL.md):
//   - public endpoints only — nothing here signs a request or reads the
//     secrets vault;
//   - every field name, limit and fee is cited to the venue's official
//     docs with URL + access date in a comment
//     (docs/research/screener-endpoints.md is the index), or is marked
//     UNVERIFIED and not relied on;
//   - decimal parsing end to end (shopspring/decimal unmarshals number
//     tokens from their exact JSON text — never through float64);
//   - symbol → base/quote comes from the venue's instrument list, never
//     from splitting the symbol string;
//   - each venue has its own rate gate honouring Retry-After on
//     429/418/403.
package venue

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/screener"
)

// InstrumentKind distinguishes a spot pair from a perpetual contract.
type InstrumentKind string

const (
	KindSpot InstrumentKind = "spot"
	KindPerp InstrumentKind = "perp"
)

// Instrument is one venue-native tradable symbol, normalised. Limits
// are decimal strings exactly as the venue publishes them (empty when
// the venue publishes no such field — callers must not treat "" as 0).
type Instrument struct {
	Venue    screener.Venue
	Kind     InstrumentKind
	Symbol   string // venue-native id, the key every ticker row uses
	Base     string
	Quote    string
	Tradable bool
	// TickSize is the price increment, StepSize the size increment,
	// MinNotional the minimum order value in Quote, as decimal strings.
	TickSize    string
	StepSize    string
	MinNotional string
	// IntervalH is the funding interval in hours for a perp, when the
	// instrument list publishes it (0 when it does not; the collector's
	// Perps() fills it from a funding endpoint instead).
	IntervalH int
	// CtVal is the contract value in Base units per contract for a perp
	// whose sizes are quoted in contracts ("" when 1:1 / not published).
	CtVal string
}

// Fees is one venue's regular-tier taker fee pair in basis points.
// Verified is true only when both numbers were read from the venue's
// official fee schedule with URL + access date (see each collector).
type Fees struct {
	SpotTakerBps decimal.Decimal
	PerpTakerBps decimal.Decimal
	Verified     bool
}

// Collector is the per-venue contract the Poller drives. Every method
// is safe for concurrent use and honours ctx.
type Collector interface {
	ID() screener.Venue
	// Instruments fetches (and caches) the venue's spot + perp list.
	Instruments(ctx context.Context) ([]Instrument, error)
	// Spot returns one Quote per tradable spot pair from ONE bulk call
	// (plus an instrument refresh when the cache is stale).
	Spot(ctx context.Context) ([]screener.Quote, error)
	// Perps returns one Perp per tradable USDT-margined perpetual.
	Perps(ctx context.Context) ([]screener.Perp, error)
	// Networks returns deposit/withdraw status per asset where the venue
	// publishes it publicly, else screener.NetworkUnknown with reason
	// "venue requires API key" for every known base asset.
	Networks(ctx context.Context) (map[string]screener.NetworkStatus, error)
	// Fees returns the regular-tier taker fees and whether they are
	// verified against the official fee page.
	Fees() Fees
	// RateLimited returns how many 429/418/403 responses this venue's
	// gate has observed since construction.
	RateLimited() int
}

// Options configures a collector. Zero values pick the venue's public
// production hosts and the documented defaults; tests point the bases
// at an httptest server serving testdata/.
type Options struct {
	// SpotBase / PerpBase override the REST hosts (no trailing slash).
	SpotBase string
	PerpBase string
	// FundingCallsPerPoll bounds per-instrument funding requests per
	// Perps() call on venues that need them (0 → 10).
	FundingCallsPerPoll int
	// BooksPerPoll bounds per-product order-book requests per Spot()
	// call on a venue with no bulk bid/ask ticker (T-075: Coinbase
	// Advanced Trade); the rest carry their last-known quote (0 → 40).
	BooksPerPoll int
	// InstrumentTTL is how long the instrument cache is trusted before
	// Spot()/Perps() refresh it (0 → 10 min).
	InstrumentTTL time.Duration
	// Now is injectable for tests (0 → time.Now).
	Now func() time.Time
}

func (o Options) fundingCalls() int {
	if o.FundingCallsPerPoll <= 0 {
		return 10
	}
	return o.FundingCallsPerPoll
}

func (o Options) booksPerPoll() int {
	if o.BooksPerPoll <= 0 {
		return 40
	}
	return o.BooksPerPoll
}

func (o Options) ttl() time.Duration {
	if o.InstrumentTTL <= 0 {
		return 10 * time.Minute
	}
	return o.InstrumentTTL
}

func (o Options) now() func() time.Time {
	if o.Now == nil {
		return time.Now
	}
	return o.Now
}

// KeyGatedReason is the verbatim reason every key-gated venue returns
// from Networks (design §2: shown as "unknown (venue requires API key)"
// and never inferred from another source).
const KeyGatedReason = "venue requires API key"

// New constructs the collector for id, or an error for an unknown venue.
func New(id screener.Venue, opts Options) (Collector, error) {
	switch id {
	case screener.VenueBinance:
		return newBinance(opts), nil
	case screener.VenueOKX:
		return newOKX(opts), nil
	case screener.VenueBybit:
		return newBybit(opts), nil
	case screener.VenueBitget:
		return newBitget(opts), nil
	case screener.VenueGate:
		return newGateIO(opts), nil
	case screener.VenueMEXC:
		return newMEXC(opts), nil
	case screener.VenueKuCoin:
		return newKuCoin(opts), nil
	case screener.VenueHTX:
		return newHTX(opts), nil
	case screener.VenueKraken:
		return newKraken(opts), nil
	case screener.VenueCoinbase:
		return newCoinbase(opts), nil
	}
	return nil, fmt.Errorf("venue: unknown venue %q", id)
}

// Registry lists every collector this build ships with and whether its
// endpoint facts AND fee numbers are verified against official docs
// (SKILL.md step 4: the console shows unverified venues as such). A
// venue with Verified=false still runs — its fees are what the settings
// document says, flagged unverified.
type RegistryEntry struct {
	ID       screener.Venue
	Verified bool
	Fees     Fees
}

// Registry returns the venue registry in screener.OrderedVenues order.
func Registry() []RegistryEntry {
	out := make([]RegistryEntry, 0, len(screener.OrderedVenues))
	for _, id := range screener.OrderedVenues {
		c, err := New(id, Options{})
		if err != nil {
			continue
		}
		f := c.Fees()
		out = append(out, RegistryEntry{ID: id, Verified: f.Verified, Fees: f})
	}
	return out
}

// instrumentCache is the shared symbol → Instrument map every collector
// embeds: refreshed by the collector's own fetch func when older than
// ttl, read under a RWMutex by Spot()/Perps().
type instrumentCache struct {
	mu        sync.RWMutex
	bySymbol  map[InstrumentKind]map[string]Instrument
	fetchedAt time.Time
	ttl       time.Duration
	now       func() time.Time
	fetch     func(ctx context.Context) ([]Instrument, error)
}

func newInstrumentCache(opts Options, fetch func(context.Context) ([]Instrument, error)) *instrumentCache {
	return &instrumentCache{ttl: opts.ttl(), now: opts.now(), fetch: fetch}
}

// refresh re-fetches unconditionally and returns the sorted list.
func (c *instrumentCache) refresh(ctx context.Context) ([]Instrument, error) {
	list, err := c.fetch(ctx)
	if err != nil {
		return nil, err
	}
	m := map[InstrumentKind]map[string]Instrument{KindSpot: {}, KindPerp: {}}
	for _, in := range list {
		m[in.Kind][in.Symbol] = in
	}
	c.mu.Lock()
	c.bySymbol = m
	c.fetchedAt = c.now()
	c.mu.Unlock()
	sort.Slice(list, func(i, j int) bool {
		if list[i].Kind != list[j].Kind {
			return list[i].Kind < list[j].Kind
		}
		return list[i].Symbol < list[j].Symbol
	})
	return list, nil
}

// ensure refreshes when stale, then returns the map for kind (a shared
// read-only view; callers must not mutate it).
func (c *instrumentCache) ensure(ctx context.Context, kind InstrumentKind) (map[string]Instrument, error) {
	c.mu.RLock()
	stale := c.bySymbol == nil || c.now().Sub(c.fetchedAt) > c.ttl
	m := c.bySymbol
	c.mu.RUnlock()
	if stale {
		if _, err := c.refresh(ctx); err != nil {
			// A stale cache beats no cache: keep serving the old map if
			// we have one, so a transient instruments failure does not
			// blank a venue's quotes.
			if m == nil {
				return nil, err
			}
			return m[kind], nil
		}
		c.mu.RLock()
		m = c.bySymbol
		c.mu.RUnlock()
	}
	return m[kind], nil
}

// keyGatedNetworks builds the "unknown (venue requires API key)" map for
// every base asset in the cache.
func (c *instrumentCache) keyGatedNetworks(ctx context.Context) (map[string]screener.NetworkStatus, error) {
	spot, err := c.ensure(ctx, KindSpot)
	if err != nil {
		return nil, err
	}
	out := make(map[string]screener.NetworkStatus)
	for _, in := range spot {
		out[in.Base] = screener.UnknownNetworkStatus(KeyGatedReason)
	}
	return out, nil
}

// fundingRR is the round-robin state for venues whose funding rate /
// next-funding time needs one request per contract: at most n symbols
// are refreshed per Perps() call; the rest carry their last-known
// values.
type fundingRR struct {
	mu    sync.Mutex
	n     int
	pos   int
	known map[string]fundingInfo
}

type fundingInfo struct {
	Rate, Predicted decimal.Decimal
	IntervalH       int
	NextAt          time.Time
	At              time.Time
}

func newFundingRR(n int) *fundingRR { return &fundingRR{n: n, known: map[string]fundingInfo{}} }

// pick returns the next ≤ n symbols (sorted order, wrapping around).
func (r *fundingRR) pick(symbols []string) []string {
	if len(symbols) == 0 {
		return nil
	}
	sort.Strings(symbols)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.pos >= len(symbols) {
		r.pos = 0
	}
	n := r.n
	if n > len(symbols) {
		n = len(symbols)
	}
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, symbols[(r.pos+i)%len(symbols)])
	}
	r.pos = (r.pos + n) % len(symbols)
	return out
}

func (r *fundingRR) set(symbol string, fi fundingInfo) {
	r.mu.Lock()
	r.known[symbol] = fi
	r.mu.Unlock()
}

func (r *fundingRR) get(symbol string) (fundingInfo, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	fi, ok := r.known[symbol]
	return fi, ok
}

// hoursBetween returns the whole-hour funding interval implied by two
// consecutive funding timestamps, 0 when they are not both set or the
// difference is not a whole positive hour.
func hoursBetween(from, to time.Time) int {
	if from.IsZero() || to.IsZero() || !to.After(from) {
		return 0
	}
	d := to.Sub(from)
	if d%time.Hour != 0 {
		return 0
	}
	return int(d / time.Hour)
}

// bps converts a fee expressed as a decimal fraction string ("0.001")
// to basis points exactly.
func bps(fraction string) decimal.Decimal {
	return decimal.RequireFromString(fraction).Mul(decimal.NewFromInt(10000))
}
