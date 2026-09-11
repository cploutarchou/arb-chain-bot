package screener

import "github.com/cploutarchou/arb-chain-bot/internal/exchange"

// VenueFeeConvention is the ONE compiled-in per-venue fee-placement
// table (docs/research/fees.md, per-exchange findings; audit T10's fee
// half): which side of a conversion the taker fee is taken from. The
// paper executor routes every spot leg through it so its fills obey
// the same placement semantics the engine's exact simulator does
// (fees.PlacementFor), instead of assuming quote-side fees everywhere.
//
// Venues without a verified statement in the research default to
// quote-side fees — the shape the screener always assumed, and the
// conservative equal-cost choice: the bps are identical either way,
// only the asset differs.
var venueFeeConventions = map[Venue]exchange.FeeConvention{
	VenueBinance:  exchange.FeeInReceived, // fees.md §Binance: received asset (re-verified 2026-08-26)
	VenueOKX:      exchange.FeeInReceived, // fees.md §OKX: deducted from the received currency
	VenueBybit:    exchange.FeeInReceived, // fees.md §Bybit: charged in the asset received
	VenueBitget:   exchange.FeeInReceived, // fees.md §Bitget: deducted from the received asset (BGB off)
	VenueKraken:   exchange.FeeInSpent,    // fees.md §Kraken: default is the currency spent
	VenueCoinbase: exchange.FeeInQuote,    // fees.md §Coinbase: charged in the quote asset
	VenueGate:     exchange.FeeInQuote,    // fees.md §Gate: sell→quote observed; buys unverified
	// MEXC, KuCoin, HTX, Crypto.com, Bitfinex, BingX, WhiteBIT, BitMart:
	// no verified placement statement — quote assumed.
}

// VenueFeeConvention returns the venue's taker-fee placement, defaulting
// to quote-side. Linear USDT-margined PERPETUALS are always quote-side
// (the margin asset) regardless of venue — callers pass FeeInQuote for
// perp legs explicitly.
func VenueFeeConvention(v Venue) exchange.FeeConvention {
	if c, ok := venueFeeConventions[v]; ok {
		return c
	}
	return exchange.FeeInQuote
}
