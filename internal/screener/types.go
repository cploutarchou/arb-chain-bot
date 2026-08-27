// Package screener is the Scanner Suite backend core (T-067/T-068,
// docs/design/scanner-suite.md §2): normalised cross-venue quotes and
// perpetual snapshots, spread/basis/carry math NET of fees, a versioned
// settings document, and alert-rule shapes. Collectors (one per venue,
// docs/research/screener-endpoints.md) land separately; this package
// defines the ONLY contract they and the console math share (Quote/Perp)
// so a collector never needs to know how spreads or carry are computed.
//
// Public market data only (SKILL.md non-negotiables): nothing here signs
// a request or reads the secrets vault's exchange credential group.
// Money math is decimal end to end — never float64 in a price, size,
// fee or PnL path (internal/exchange/decimal.go raises shopspring's
// division precision for every package that imports it, including this
// one, before any division below runs).
package screener

import (
	"time"

	"github.com/shopspring/decimal"

	_ "github.com/cploutarchou/arb-chain-bot/internal/exchange" // raises decimal.DivisionPrecision (see package doc)
)

// Venue identifies one exchange this suite screens. It is a plain string
// (not an enum with methods) so a settings document round-trips through
// JSON without a custom (Un)MarshalJSON, matching platform.VenueSettings'
// map[string]... convention.
type Venue string

const (
	VenueBinance Venue = "binance"
	VenueOKX     Venue = "okx"
	VenueBybit   Venue = "bybit"
	VenueBitget  Venue = "bitget"
	VenueGate    Venue = "gate"
	VenueMEXC    Venue = "mexc"
)

// KnownVenues is the full target-venue set (docs/design/scanner-suite.md
// §0.1); a settings document or rule that names anything else is
// rejected. This is the "venue ids the suite knows how to screen" list —
// distinct from platform.CompiledVenues, which is "venue ids this build
// has a LIVE trading connector for". A screener venue collector (T-066)
// is a separate, read-only concern from platform's live/paper trading
// connectors, so the two sets are not unified.
var KnownVenues = map[Venue]bool{
	VenueBinance: true,
	VenueOKX:     true,
	VenueBybit:   true,
	VenueBitget:  true,
	VenueGate:    true,
	VenueMEXC:    true,
}

// OrderedVenues is KnownVenues in a stable, deterministic order for
// anything that must iterate venues reproducibly (Defaults(), table
// rendering, golden tests).
var OrderedVenues = []Venue{
	VenueBinance, VenueOKX, VenueBybit, VenueBitget, VenueGate, VenueMEXC,
}

// Quote is one venue's top-of-book for one spot pair, normalised from
// whatever shape that venue's public REST ticker returns (design §2: the
// ONLY contract between a venue collector and the spread/basis math).
type Quote struct {
	Venue  Venue
	Base   string
	Quote  string
	Bid    decimal.Decimal
	BidQty decimal.Decimal
	Ask    decimal.Decimal
	AskQty decimal.Decimal
	// At is when this quote was observed (the collector's poll
	// timestamp, not the venue's own timestamp when that field is
	// unreliable/absent) — every age shown to an operator is computed
	// against this.
	At time.Time
}

// Perp is one venue's snapshot for one USDT-margined (or venue-native)
// perpetual contract.
type Perp struct {
	Venue Venue
	Base  string
	Quote string
	Mark  decimal.Decimal
	Index decimal.Decimal
	Bid   decimal.Decimal
	Ask   decimal.Decimal
	// FundingRate is the current (last-settled or currently-accruing,
	// venue-dependent) per-interval rate; PredictedFundingRate is the
	// venue's forecast for the NEXT interval where published (zero value
	// when the venue does not publish one — callers must not confuse a
	// genuine zero prediction with "not published"; T-066 collectors
	// document per-venue availability).
	FundingRate          decimal.Decimal
	PredictedFundingRate decimal.Decimal
	// IntervalH is the funding interval in hours (commonly 8, but 1/4h
	// venues exist) — required to annualise FundingRate correctly
	// (design §3).
	IntervalH     int
	NextFundingAt time.Time
	At            time.Time
}

// NetworkStatusValue is the deposit/withdraw network status for one
// asset on one venue. "unknown" is the honest default: a venue that
// gates the status behind an API key is NEVER inferred from another
// source (SKILL.md non-negotiables) — the UI shows "unknown (venue
// requires API key)" verbatim, using Reason.
type NetworkStatusValue string

const (
	NetworkOpen    NetworkStatusValue = "open"
	NetworkClosed  NetworkStatusValue = "closed"
	NetworkUnknown NetworkStatusValue = "unknown"
)

// NetworkStatus is one side's (withdraw or deposit) network status plus
// why, when it is not a plain "open".
type NetworkStatus struct {
	Status NetworkStatusValue `json:"status"`
	Reason string             `json:"reason,omitempty"`
}

// UnknownNetworkStatus is the default for every venue until a collector
// wires a real public status endpoint (today: none does — T-066).
func UnknownNetworkStatus(reason string) NetworkStatus {
	if reason == "" {
		reason = "collectors not started"
	}
	return NetworkStatus{Status: NetworkUnknown, Reason: reason}
}
