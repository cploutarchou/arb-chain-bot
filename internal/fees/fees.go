// Package fees resolves effective fee rates (defaults, per-market
// overrides, conditional token discounts) and defines the exact placement
// semantics of spot fees per venue convention. Every fee assumption an
// opportunity carries originates here (SKILL.md §18).
package fees

import (
	"errors"
	"fmt"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/exchange"
)

var one = decimal.NewFromInt(1)

// Rate holds fractional fee rates (0.001 = 10 bps).
type Rate struct {
	Maker decimal.Decimal
	Taker decimal.Decimal
}

// Discount is a venue token-payment discount (BNB/BGB/GT style).
// AppliesToAPI matters: Bybit's MNT discount excludes API-executed trades,
// so a bot must not model it (docs/research/fees.md).
type Discount struct {
	Enabled      bool
	Rate         decimal.Decimal // 0.25 = 25% off
	PayAsset     exchange.Asset  // asset the fee is debited in when active
	AppliesToAPI bool
}

// Schedule is one venue's resolved fee configuration. PerMarket overrides
// express promo/zero-fee/fiat pairs; the default must be positive — "never
// assume zero fees" (SKILL.md §18) — while an explicit override MAY be zero
// (verified promo pairs).
type Schedule struct {
	Exchange   exchange.ExchangeID
	Convention exchange.FeeConvention
	Default    Rate
	PerMarket  map[exchange.MarketID]Rate
	Discount   Discount
}

var (
	ErrNonPositiveDefault = errors.New("fees: default rates must be positive")
	ErrNoConvention       = errors.New("fees: fee convention unset")
)

// NewSchedule validates the safety rules at construction.
func NewSchedule(ex exchange.ExchangeID, conv exchange.FeeConvention, def Rate) (*Schedule, error) {
	if conv == "" {
		return nil, ErrNoConvention
	}
	if !def.Maker.IsPositive() || !def.Taker.IsPositive() {
		return nil, fmt.Errorf("%w (maker=%s taker=%s)", ErrNonPositiveDefault, def.Maker, def.Taker)
	}
	return &Schedule{
		Exchange:   ex,
		Convention: conv,
		Default:    def,
		PerMarket:  make(map[exchange.MarketID]Rate),
	}, nil
}

// SetOverride records a verified per-market rate (promo pairs may be zero,
// never negative).
func (s *Schedule) SetOverride(id exchange.MarketID, r Rate) error {
	if r.Maker.IsNegative() || r.Taker.IsNegative() {
		return fmt.Errorf("fees: negative override for %s", id)
	}
	s.PerMarket[id] = r
	return nil
}

// Effective is a fully resolved fee for one order.
type Effective struct {
	Rate      decimal.Decimal
	Source    string         // "default" | "override", "+discount" suffix when applied
	PayAsset  exchange.Asset // non-empty when a token discount pays the fee
	TokenPaid bool           // fee debited in PayAsset (value-equivalent approximation in pricing)
}

// Taker resolves the effective taker rate for a market.
func (s *Schedule) Taker(id exchange.MarketID) Effective {
	r, src := s.Default.Taker, "default"
	if o, ok := s.PerMarket[id]; ok {
		r, src = o.Taker, "override"
	}
	if s.Discount.Enabled && s.Discount.AppliesToAPI && s.Discount.Rate.IsPositive() && r.IsPositive() {
		r = r.Mul(one.Sub(s.Discount.Rate))
		return Effective{Rate: r, Source: src + "+discount", PayAsset: s.Discount.PayAsset, TokenPaid: true}
	}
	return Effective{Rate: r, Source: src}
}

// Placement says which side of a conversion the fee is taken from.
type Placement uint8

const (
	// FeeOnOutput: fee deducted from the received amount; the next leg's
	// input shrinks (RECEIVED always; QUOTE on sells).
	FeeOnOutput Placement = iota + 1
	// FeeOnInput: fee consumes part of the spendable input; the walkable
	// amount is input/(1+rate) (SPENT always; QUOTE on buys).
	FeeOnInput
)

// PlacementFor maps (convention, side) to fee placement:
//
//	RECEIVED: buy→fee in base (output), sell→fee in quote (output)
//	QUOTE:    buy→fee in quote (input), sell→fee in quote (output)
//	SPENT:    buy→fee in quote (input), sell→fee in base (input)
func PlacementFor(conv exchange.FeeConvention, side exchange.Side) (Placement, error) {
	switch conv {
	case exchange.FeeInReceived:
		return FeeOnOutput, nil
	case exchange.FeeInQuote:
		if side == exchange.SideBuy {
			return FeeOnInput, nil
		}
		return FeeOnOutput, nil
	case exchange.FeeInSpent:
		return FeeOnInput, nil
	default:
		return 0, fmt.Errorf("%w: %q", ErrNoConvention, conv)
	}
}

// UsableInput returns how much of the input amount may reach the book when
// the fee is charged on the input side, plus the fee amount (in the input
// asset). For output-side fees it returns the input unchanged.
func UsableInput(p Placement, in, rate decimal.Decimal) (usable, fee decimal.Decimal) {
	if p != FeeOnInput || !rate.IsPositive() {
		return in, decimal.Zero
	}
	usable = in.Div(one.Add(rate))
	return usable, in.Sub(usable)
}

// NetOutput applies an output-side fee to the gross received amount,
// returning the net amount and the fee (in the received asset). For
// input-side fees it returns the gross unchanged.
//
// Token-paid fees (TokenPaid) are NOT modeled as a discount against the
// received asset today: Taker() computes the discounted rate, and
// Effective carries PayAsset/TokenPaid so a caller COULD account for the
// pay-asset debit, but nothing does — no pay-asset balance is ever
// reserved, spent, or tracked by the paper engine or portfolio (P1-2,
// docs/MASTER_PLAN.md T-057). Concretely, applying the discounted rate
// here still shrinks the fee taken from gross by the full discount
// amount with no offsetting debit anywhere, which is exactly why
// platform.FeeSettings.validate refuses token_discount:true at the
// settings layer until a real pay-asset ledger lands: this function
// alone cannot be relied on to keep P&L honest for a token-paid fee.
func NetOutput(p Placement, gross, rate decimal.Decimal) (net, fee decimal.Decimal) {
	if p != FeeOnOutput || !rate.IsPositive() {
		return gross, decimal.Zero
	}
	fee = gross.Mul(rate)
	return gross.Sub(fee), fee
}

// venueDiscounts is the compiled-in per-venue token-discount constant
// table (docs/research/fees.md): rate, pay asset and API eligibility are
// never operator input (T-057 design §1.2) — an operator can only toggle
// Enabled. Zero value (not present) means the venue offers no token
// discount at all.
var venueDiscounts = map[exchange.ExchangeID]Discount{
	// Binance: 25% off when paid in BNB, verified to apply to
	// API-executed trades (re-verified 2026-08-26, docs/research/fees.md).
	"binance": {Rate: decimal.RequireFromString("0.25"), PayAsset: "BNB", AppliesToAPI: true},
	// Bybit: MNT fee-payment discount excludes API-executed trades
	// (re-verified 2026-08-26, docs/research/fees.md). Data only — Bybit
	// has no compiled-in connector yet (T-050); the entry exists so a
	// venue whose discount excludes API trades validates as an error
	// (design §1.2) rather than a silent no-op the day one lands.
	"bybit": {Rate: decimal.RequireFromString("0.25"), PayAsset: "MNT", AppliesToAPI: false},
}

// VenueDiscount returns the compiled-in discount profile for a venue
// (Enabled always false in the returned value — the caller/operator
// toggle decides that) and whether the venue has one at all.
func VenueDiscount(ex exchange.ExchangeID) (Discount, bool) {
	d, ok := venueDiscounts[ex]
	return d, ok
}
