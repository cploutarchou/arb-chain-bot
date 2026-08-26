// Package exchange defines the normalized vocabulary shared by every
// subsystem: identities, markets, instrument rules, capabilities, and
// balances. All money and quantity values are decimal.Decimal — float64 on
// a financial path is a defect (SKILL.md §15). shopspring/decimal marshals
// as a quoted JSON string by default, which is exactly the wire contract
// (docs/architecture.md §7); nothing may flip MarshalJSONWithoutQuotes.
package exchange

import (
	"fmt"

	"github.com/shopspring/decimal"
)

// ExchangeID identifies a venue ("binance", "okx").
type ExchangeID string

// Asset is a normalized upper-case asset code ("BTC", "USDT").
type Asset string

// Symbol is the venue-native market symbol ("BTCUSDT").
type Symbol string

// MarketID uniquely identifies one market on one venue. It is a value
// type usable as a map key.
type MarketID struct {
	Exchange ExchangeID
	Symbol   Symbol
}

func (m MarketID) String() string { return string(m.Exchange) + ":" + string(m.Symbol) }

// Side is the taker side of a conversion on a market.
type Side uint8

const (
	SideBuy  Side = iota + 1 // buy base with quote — consumes ASKS
	SideSell                 // sell base for quote — consumes BIDS
)

func (s Side) String() string {
	switch s {
	case SideBuy:
		return "BUY"
	case SideSell:
		return "SELL"
	default:
		return fmt.Sprintf("Side(%d)", uint8(s))
	}
}

// MarketStatus is the venue-reported trading status, normalized.
type MarketStatus string

const (
	MarketTrading  MarketStatus = "TRADING"
	MarketHalted   MarketStatus = "HALTED"
	MarketDelisted MarketStatus = "DELISTED"
	MarketUnknown  MarketStatus = "UNKNOWN"
)

// Market is a normalized spot market.
type Market struct {
	ID      MarketID
	Base    Asset
	Quote   Asset
	Status  MarketStatus
	Enabled bool // platform-level enablement (config), independent of Status
	Rules   InstrumentRules
}

// Tradeable reports whether the market may participate in triangles.
func (m Market) Tradeable() bool { return m.Enabled && m.Status == MarketTrading }

// Balance is a venue asset balance (virtual in paper mode).
type Balance struct {
	Asset     Asset
	Available decimal.Decimal
	Reserved  decimal.Decimal
}

// Total returns Available + Reserved.
func (b Balance) Total() decimal.Decimal { return b.Available.Add(b.Reserved) }
