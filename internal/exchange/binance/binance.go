// Package binance implements the Binance spot market-data connector:
// decimal-preserving decoding of depth-diff streams and REST snapshots,
// the official U/u splice and continuity rules, instrument metadata
// mapping, and transport constants. Protocol facts come from
// docs/research/exchanges.md (official docs, accessed 2026-08-26) and are
// re-verified against current docs before live use (T-047).
package binance

import (
	"github.com/cploutarchou/arb-chain-bot/internal/exchange"
)

// ID is the platform exchange identifier.
const ID = exchange.ExchangeID("binance")

// Hosts. Public market data needs no credentials; Demo Mode is the
// development target (live-equivalent filters, realistic data).
const (
	MarketDataWSHost   = "wss://data-stream.binance.vision"
	MarketDataRESTHost = "https://data-api.binance.vision"
	DemoRESTHost       = "https://demo-api.binance.com"
	DemoWSHost         = "wss://demo-stream.binance.com"
)

// Capabilities per docs/research/exchanges.md.
var Capabilities = exchange.Capabilities{
	BookInit:               exchange.BookInitREST,
	Integrity:              exchange.IntegrityUpdateChain,
	RequiresRESTDriftCheck: false,
	FeeConvention:          exchange.FeeInReceived,
	HasSpotTestEnv:         true,
	ForcedDisconnect:       true, // 24h — transport reconnects pre-emptively
}

// Operational limits the transport must respect (docs/research §Binance).
const (
	MaxStreamsPerConn     = 1024
	ConnAttemptsPer5Min   = 300
	ForcedDisconnectHours = 24
	// 1000 levels cost 50 weight (5000 would cost 250): a 60-symbol
	// universe primes within one minute instead of tripping an IP ban.
	SnapshotDepthLimit     = 1000
	RESTWeightBudgetPerMin = 6000
)
