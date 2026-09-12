package screener

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/strategy"
)

// Settings is the Scanner Suite's versioned configuration document
// (design §2/§7): poll cadence, per-venue enablement/fees, and paper
// balances used by the (not-yet-wired, T-071) auto-paper executor.
type Settings struct {
	PollIntervalS int `json:"poll_interval_s"`
	// FundingCallsPerPoll bounds how many per-instrument funding
	// requests a collector issues per poll on venues whose funding rate
	// / next-funding time is not in the bulk ticker (T-066: OKX, Bitget,
	// MEXC), round-robin over the contract list; last-known values are
	// carried for the rest. 0 means the collector default (10).
	FundingCallsPerPoll int             `json:"funding_calls_per_poll"`
	MinLiquidityQuote   decimal.Decimal `json:"min_liquidity_quote"`
	// MaxPlausibleSpreadBps is the asset-identity guard (guard.go): a
	// cross-venue lane whose two mids differ by more than this is
	// flagged suspect_mismatch (same ticker, different asset) and is
	// excluded from the spreads table, the alert evaluator and the
	// paper executor. Default 2000 (20 %); 100..100000; hot. Zero in a
	// stored document (predating the field) means the default — Load
	// normalises it so the API always shows the effective value.
	MaxPlausibleSpreadBps decimal.Decimal         `json:"max_plausible_spread_bps"`
	Venues                map[Venue]VenueSettings `json:"venues"`
	Paper                 PaperSettings           `json:"paper"`
	// PerpQuotePreference orders the quote/margin assets the poller
	// prefers when ONE venue lists several perpetuals on the same base
	// (Binance USDⓈ-M: AAVEUSDT and AAVEUSDC). The suite tracks one
	// contract per (venue, base) because funding_history is keyed
	// (venue, base, at) (migration 000010) and the carry/harvest models
	// read it by (venue, base); the first listed asset the venue offers
	// wins, an asset not listed here ranks last, and a base with a
	// single contract is always kept whatever its quote. Default
	// ["USDT"] (the suite's documented perp scope); hot. Empty in a
	// stored document (predating the field) means the default.
	PerpQuotePreference []string `json:"perp_quote_preference"`
	// Alerts tunes the alert evaluator (internal/screener/alerts); hot.
	Alerts AlertSettings `json:"alerts"`
}

// AlertSettings is the evaluator section of the settings document.
type AlertSettings struct {
	// MaxLanesPerRule caps how many cross-venue spot lanes one rule
	// evaluates per tick, taken from the quality-ranked universe (lanes
	// passing the guard and the data-age gate first, then by net bps).
	// 0 = unbounded (default): the evaluator sees every lane its filters
	// admit; the cap exists for operators who must bound CPU on a very
	// large book, and every lane it cuts is counted in GET
	// /screener/status (automation.alerts.truncated). 0..100000.
	MaxLanesPerRule int `json:"max_lanes_per_rule"`
	// StaleHoldS is how long a lane whose legs fail the data-age gate is
	// HELD (lifetime preserved, open event kept open) before the
	// evaluator gives up and closes the event with reason HOLD_TIMEOUT.
	// A poll that lands a few seconds late on one venue is not a market
	// event: at a 5 s poll the slowest enabled venues in the 2026-08-28
	// soak averaged 10.2 s per poll (max 25.1 s), so their quotes fail a
	// 5 s gate on most ticks. Default 30 s (one missed poll of the slowest
	// venue); 1..600; 0 in a stored document means the default.
	StaleHoldS int `json:"stale_hold_s"`
}

// DefaultStaleHoldS is AlertSettings.StaleHoldS when unset.
const DefaultStaleHoldS = 30

const (
	maxLanesPerRuleCeiling = 100000
	maxStaleHoldS          = 600
	maxPerpQuotePreference = 8
)

// EffectiveStaleHold returns stale_hold_s as a duration, or the default
// when the document has none.
func (a AlertSettings) EffectiveStaleHold() time.Duration {
	if a.StaleHoldS <= 0 {
		return DefaultStaleHoldS * time.Second
	}
	return time.Duration(a.StaleHoldS) * time.Second
}

// EffectivePerpQuotePreference returns perp_quote_preference, or the
// default ["USDT"] when the document has none.
func (s Settings) EffectivePerpQuotePreference() []string {
	if len(s.PerpQuotePreference) == 0 {
		return []string{"USDT"}
	}
	return append([]string(nil), s.PerpQuotePreference...)
}

var (
	minPlausibleSpreadBps = decimal.NewFromInt(100)
	maxPlausibleSpreadBps = decimal.NewFromInt(100000)
)

// EffectiveMaxPlausibleSpreadBps returns max_plausible_spread_bps, or
// the default when the document has none (see the field comment).
func (s Settings) EffectiveMaxPlausibleSpreadBps() decimal.Decimal {
	if !s.MaxPlausibleSpreadBps.IsPositive() {
		return DefaultMaxPlausibleSpreadBps
	}
	return s.MaxPlausibleSpreadBps
}

// Normalised fills defaults for fields a stored document predates so
// the active snapshot never exposes a zero that means "default".
func (s Settings) Normalised() Settings {
	c := s.Clone()
	c.MaxPlausibleSpreadBps = s.EffectiveMaxPlausibleSpreadBps()
	c.PerpQuotePreference = s.EffectivePerpQuotePreference()
	c.Alerts.StaleHoldS = int(s.Alerts.EffectiveStaleHold() / time.Second)
	return c
}

// VenueSettings is one venue's screener configuration. Fees are the
// venue's regular-tier taker rate (bps) used by spreads.go/basis.go; a
// per-symbol override table, like platform.FeeSettings.Overrides, is
// deliberately not modeled yet — nothing in this task needs it.
type VenueSettings struct {
	Enabled      bool            `json:"enabled"`
	PerpsEnabled bool            `json:"perps_enabled"`
	SpotTakerBps decimal.Decimal `json:"spot_taker_bps"`
	PerpTakerBps decimal.Decimal `json:"perp_taker_bps"`
}

// PaperSettings holds per-venue virtual balances for the (future)
// auto-paper executor (T-071); empty/zero balances are legal — a venue
// with no paper inventory simply cannot auto-execute on it yet.
type PaperSettings struct {
	Balances map[Venue]map[string]decimal.Decimal `json:"balances"`
}

var (
	maxFeeBps = decimal.NewFromInt(200)
)

// Clone returns a deep copy; no caller can mutate a stored version
// through the value it was handed (mirrors platform.Settings.Clone).
func (s Settings) Clone() Settings {
	c := s
	if s.Venues != nil {
		c.Venues = make(map[Venue]VenueSettings, len(s.Venues))
		for id, v := range s.Venues {
			c.Venues[id] = v
		}
	}
	if s.Paper.Balances != nil {
		c.Paper.Balances = make(map[Venue]map[string]decimal.Decimal, len(s.Paper.Balances))
		for venue, bal := range s.Paper.Balances {
			cp := make(map[string]decimal.Decimal, len(bal))
			for asset, amt := range bal {
				cp[asset] = amt
			}
			c.Paper.Balances[venue] = cp
		}
	}
	if s.PerpQuotePreference != nil {
		c.PerpQuotePreference = append([]string(nil), s.PerpQuotePreference...)
	}
	return c
}

// Validate rejects a structurally invalid document. Pure, no I/O.
func (s Settings) Validate() error {
	if s.PollIntervalS < 2 || s.PollIntervalS > 60 {
		return fmt.Errorf("%w: poll_interval_s must be 2..60, got %d", ErrInvalid, s.PollIntervalS)
	}
	if s.FundingCallsPerPoll < 0 || s.FundingCallsPerPoll > 50 {
		return fmt.Errorf("%w: funding_calls_per_poll must be 0..50, got %d", ErrInvalid, s.FundingCallsPerPoll)
	}
	if s.MinLiquidityQuote.IsNegative() {
		return fmt.Errorf("%w: min_liquidity_quote must be >= 0", ErrInvalid)
	}
	if v := s.MaxPlausibleSpreadBps; !v.IsZero() && (v.LessThan(minPlausibleSpreadBps) || v.GreaterThan(maxPlausibleSpreadBps)) {
		return fmt.Errorf("%w: max_plausible_spread_bps must be 100..100000, got %s", ErrInvalid, v)
	}
	if len(s.PerpQuotePreference) > maxPerpQuotePreference {
		return fmt.Errorf("%w: perp_quote_preference must list at most %d assets", ErrInvalid, maxPerpQuotePreference)
	}
	seenQuote := map[string]bool{}
	for _, q := range s.PerpQuotePreference {
		if q == "" || q != strings.TrimSpace(q) || q != strings.ToUpper(q) {
			return fmt.Errorf("%w: perp_quote_preference entries must be upper-case asset codes, got %q", ErrInvalid, q)
		}
		if seenQuote[q] {
			return fmt.Errorf("%w: perp_quote_preference lists %q twice", ErrInvalid, q)
		}
		seenQuote[q] = true
	}
	if s.Alerts.MaxLanesPerRule < 0 || s.Alerts.MaxLanesPerRule > maxLanesPerRuleCeiling {
		return fmt.Errorf("%w: alerts.max_lanes_per_rule must be 0..%d, got %d", ErrInvalid, maxLanesPerRuleCeiling, s.Alerts.MaxLanesPerRule)
	}
	if s.Alerts.StaleHoldS < 0 || s.Alerts.StaleHoldS > maxStaleHoldS {
		return fmt.Errorf("%w: alerts.stale_hold_s must be 0..%d, got %d", ErrInvalid, maxStaleHoldS, s.Alerts.StaleHoldS)
	}
	if len(s.Venues) == 0 {
		return fmt.Errorf("%w: at least one venue must be configured", ErrInvalid)
	}
	for id, v := range s.Venues {
		if !KnownVenues[id] {
			return fmt.Errorf("%w: venues.%s is not a known screener venue", ErrInvalid, id)
		}
		if v.SpotTakerBps.IsNegative() || v.SpotTakerBps.GreaterThan(maxFeeBps) {
			return fmt.Errorf("%w: venues.%s.spot_taker_bps must be 0..100", ErrInvalid, id)
		}
		if v.PerpTakerBps.IsNegative() || v.PerpTakerBps.GreaterThan(maxFeeBps) {
			return fmt.Errorf("%w: venues.%s.perp_taker_bps must be 0..100", ErrInvalid, id)
		}
	}
	for venue, bal := range s.Paper.Balances {
		if !KnownVenues[venue] {
			return fmt.Errorf("%w: paper.balances has unknown venue %q", ErrInvalid, venue)
		}
		for asset, amt := range bal {
			if asset == "" {
				return fmt.Errorf("%w: paper.balances.%s has an empty asset key", ErrInvalid, venue)
			}
			if amt.IsNegative() {
				return fmt.Errorf("%w: paper.balances.%s.%s must be >= 0", ErrInvalid, venue, asset)
			}
		}
	}
	return nil
}

// defaultVenueFee is one venue's regular-tier taker fee placeholder
// (spot, perp) in bps, as decimal strings so a fractional rate (Bybit's
// perp taker) is exact. Sourced provisionally from public fee pages seen
// during design; T-065 (docs/research/screener-endpoints.md) verifies
// each with a URL + access date and this table is updated to match —
// until then these are placeholders, not verified numbers, and every
// consumer of Defaults() must treat them as such.
type defaultVenueFee struct{ spotBps, perpBps string }

var defaultVenueFees = map[Venue]defaultVenueFee{
	VenueBinance: {spotBps: "10", perpBps: "5"},
	VenueOKX:     {spotBps: "10", perpBps: "5"},
	VenueBybit:   {spotBps: "10", perpBps: "5.5"},
	VenueBitget:  {spotBps: "10", perpBps: "6"},
	VenueGate:    {spotBps: "20", perpBps: "5"},
	VenueMEXC:    {spotBps: "5", perpBps: "2"},
	// T-075 Tier-2 (docs/research/venues/<venue>.md §fees): KuCoin and
	// Kraken verified from primary; HTX and Coinbase UNVERIFIED placeholders.
	// Coinbase's UNVERIFIED regular-tier taker is 1.20 % (120 bps); the fee
	// cap is 200 bps to admit it.
	VenueKuCoin:   {spotBps: "10", perpBps: "6"},
	VenueHTX:      {spotBps: "20", perpBps: "6"},
	VenueKraken:   {spotBps: "80", perpBps: "5"},
	VenueCoinbase: {spotBps: "120", perpBps: "5"},
	// T-078 Tier-3 (docs/research/venues/<venue>.md §6): BingX perp taker
	// is VERIFIED (5 bps); every other number below is an UNVERIFIED
	// placeholder — each venue's Registry entry stays Verified=false.
	VenueCryptoCom: {spotBps: "50", perpBps: "7"},
	VenueBitfinex:  {spotBps: "20", perpBps: "6.5"},
	VenueBingX:     {spotBps: "10", perpBps: "5"},
	VenueWhiteBIT:  {spotBps: "10", perpBps: "5.5"},
	VenueBitMart:   {spotBps: "25", perpBps: "6"},
	// Tier-4 (T-075 remainder, docs/research/venues/<venue>.md §6):
	// Bithumb's standard-tier taker is VERIFIED 0.25 % on the official
	// fee page (the 0.04 % maker is an opt-in program, NOT applied);
	// Phemex's spot 0.10 % is VERIFIED from the API itself
	// (defaultTakerFee "0.001") and its USDT-perp taker 0.06 % from the
	// official help centre — but the Registry entry stays Verified only
	// when both numbers are venue-verified, and Bithumb's opt-in state
	// keeps its flag false.
	VenueBithumb: {spotBps: "25", perpBps: "25"},
	VenuePhemex:  {spotBps: "10", perpBps: "6"},
	// Upbit's third-party-corroborated fees differ per lane (KRW 0.05 %,
	// BTC/USDT 0.25 %) with no official page reachable — the
	// conservative 0.25 % covers every lane (research §6). LBank's fee
	// page answers 403; 0.10 % is the common base-tier placeholder.
	VenueUpbit: {spotBps: "25", perpBps: "25"},
	VenueLBank: {spotBps: "10", perpBps: "10"},
}

// Tier-3 venues (T-078: Crypto.com Exchange, Bitfinex, BingX, WhiteBIT,
// BitMart) shipped opt-in and are enabled by default since the 30-minute
// live soak of 2026-08-28 (venue.TestSoakLive, poll 5 s, all fifteen
// venues in ONE process and sharing the IP with the running paper stack,
// so the per-IP budget was stricter than production): cryptocom 118
// polls 576 spot/366 perps avg 10243 ms max 25108; bitfinex 347 polls
// 197/75 avg 192 max 3588; bingx 283 polls 669/881 avg 1370 max 2573;
// whitebit 343 polls 798/305 avg 248 max 641; bitmart 167 polls 27/354
// avg 5790 max 12539 — 0 x HTTP 429/418/403, 0 in-band rate limits and
// 0 failed polls each (SKILL.md step 5). Full table: docs/MASTER_PLAN.md
// T-075. BitMart's 27 spot quotes are 100 % of the rows its bulk ticker
// returns (it lists only pairs with 24 h volume > 0); cryptocom's perps
// count is the round-robin mark fill saturating, not a dip.

// Tier-4 venues (T-075 remainder: Bithumb, Phemex, Upbit, LBank) were
// opt-in until their 30-min live soak of 2026-09-13 (TestSoakLive, poll
// 5 s, all nineteen venues in one process, perps force-enabled so
// Phemex's USDT-M polls were exercised): bithumb 305 polls 495 spot
// 614-905-2500 ms; phemex 324 polls 255 spot + 106 perps 106-560-1847
// ms; upbit 155 polls 848 spot 6601-6640-8075 ms (self-paces above the
// 5 s interval, like cryptocom); lbank 106 polls, spot 39..1028 (the
// round-robin carry filling the full book and holding it), 11487-11990-
// 15551 ms — 0 × HTTP 429/418/403, 0 in-band rate limits and 0 failed
// polls each, and zero for every other venue in the same run
// (SKILL.md step 5). They now start enabled like every soaked tier.

// Tier-2 venues (T-075: KuCoin, HTX, Kraken, Coinbase) were opt-in until
// a 30-min live soak showed zero 429/418/403/510 and zero errors. Soak of
// 2026-08-27 (TestSoakLive, poll 5 s, all ten venues): kucoin 295 polls
// 1006 spot/664 perps avg 1104 ms; htx 197 polls 600/301 avg 4145 ms;
// kraken 248 polls 1382/276 avg 2249 ms; coinbase 142 polls 921/0 avg
// 7701 ms — 0 rate-limit hits, 0 errors each. They now start enabled
// like Tier-1. Coinbase has no retail perps
// (docs/research/venues/coinbase.md §3) so PerpsEnabled stays false.

// Defaults returns the first-boot document: every known venue enabled
// (Tier-1; Tier-2 since the 2026-08-27 soak; Tier-3 since the 2026-08-28
// soak; Tier-4 since the 2026-09-13 soak, see the notes above) with
// regular-tier taker fees (spot/perp) "to be confirmed by T-065"
// (docs/research/screener-endpoints.md verifies each against the venue's
// current official fee schedule).
func Defaults() Settings {
	venues := make(map[Venue]VenueSettings, len(OrderedVenues))
	for _, id := range OrderedVenues {
		f := defaultVenueFees[id]
		venues[id] = VenueSettings{
			// Coinbase is the one venue OFF by default: the 30-minute
			// fifteen-venue soak on 2026-08-28 recorded 20 × HTTP 429 and
			// 20 failed polls of 61 attempts for it (MASTER_PLAN T-075),
			// failing the same zero-rate-limit rule that admitted it on
			// 2026-08-27. Its per-product book sweep costs one request per
			// product, so it is the first venue to feel a shared IP. An
			// operator can still enable it per deployment; re-enabling it
			// by default needs a clean soak after the gate is retuned.
			//
			// Tier-4 (Bithumb, Phemex, Upbit, LBank — T-075 remainder)
			// shipped 2026-09-13 opt-in and is enabled by default since
			// its 30-min live soak (see the Tier-4 note above Defaults).
			// Bithumb, Upbit and LBank list no perps on their public
			// market data, so PerpsEnabled stays false for them.
			Enabled:      id != VenueCoinbase,
			PerpsEnabled: id != VenueCoinbase && id != VenueBithumb && id != VenueUpbit && id != VenueLBank,
			SpotTakerBps: decimal.RequireFromString(f.spotBps),
			PerpTakerBps: decimal.RequireFromString(f.perpBps),
		}
	}
	return Settings{
		PollIntervalS:       5,
		FundingCallsPerPoll: 10,
		MinLiquidityQuote:   decimal.NewFromInt(500),
		// 20 %: see Settings.MaxPlausibleSpreadBps / guard.go.
		MaxPlausibleSpreadBps: DefaultMaxPlausibleSpreadBps,
		Venues:                venues,
		Paper:                 PaperSettings{Balances: map[Venue]map[string]decimal.Decimal{}},
		PerpQuotePreference:   []string{"USDT"},
		Alerts:                AlertSettings{MaxLanesPerRule: 0, StaleHoldS: DefaultStaleHoldS},
	}
}

// FieldTiming returns the field -> "hot"/"restart" map the API serves
// (design §7): every field is hot except venues.*.enabled, which is
// restart-scoped because a running poller loop is only started/stopped
// on a supervised restart (T-066 collectors; no live collector exists
// yet, but the timing contract is fixed now so the console never has to
// change once they land).
func FieldTiming(s Settings) map[string]string {
	out := map[string]string{
		"poll_interval_s":        "hot",
		"funding_calls_per_poll": "hot",
		"min_liquidity_quote":    "hot",
		// guard.go reads the active snapshot on every request/tick.
		"max_plausible_spread_bps": "hot",
		// venue.Poller re-reads the snapshot on every poll; the alert
		// evaluator on every tick.
		"perp_quote_preference":     "hot",
		"alerts.max_lanes_per_rule": "hot",
		"alerts.stale_hold_s":       "hot",
	}
	ids := make([]Venue, 0, len(s.Venues))
	for id := range s.Venues {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		prefix := "venues." + string(id) + "."
		out[prefix+"enabled"] = "restart"
		out[prefix+"perps_enabled"] = "hot"
		out[prefix+"spot_taker_bps"] = "hot"
		out[prefix+"perp_taker_bps"] = "hot"
	}
	for venue, bal := range s.Paper.Balances {
		for asset := range bal {
			out["paper.balances."+string(venue)+"."+asset] = "hot"
		}
	}
	return out
}

// Diff flattens both documents to dotted JSON paths and reports every
// leaf that differs, reusing strategy.DiffAny rather than duplicating
// the flatten/compare logic (mirrors internal/platform's design D6: one
// diff implementation for every versioned settings document in this
// codebase).
func Diff(oldS, newS Settings) (map[string]strategy.Change, error) {
	return strategy.DiffAny(oldS, newS)
}
