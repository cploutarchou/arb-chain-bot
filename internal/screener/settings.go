package screener

import (
	"fmt"
	"sort"

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
	maxFeeBps = decimal.NewFromInt(100)
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
}

// Defaults returns the first-boot document: all six target venues
// enabled with placeholder regular-tier taker fees (spot/perp) "to be
// confirmed by T-065" (docs/research/screener-endpoints.md verifies each
// against the venue's current official fee schedule).
func Defaults() Settings {
	venues := make(map[Venue]VenueSettings, len(OrderedVenues))
	for _, id := range OrderedVenues {
		f := defaultVenueFees[id]
		venues[id] = VenueSettings{
			Enabled: true, PerpsEnabled: true,
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
