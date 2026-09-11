package exchange

import (
	"errors"
	"fmt"

	"github.com/shopspring/decimal"
)

// PrecisionMode selects how a venue expresses quantization: a step/tick
// size (Binance, OKX, Bybit, Kraken) or a number of decimals (Bitget,
// Gate). Both are normalized here so downstream math never branches on
// venue names (docs/research/fees.md §5).
type PrecisionMode uint8

const (
	PrecisionUnset    PrecisionMode = iota
	PrecisionStep                   // quantize to multiples of Step
	PrecisionDecimals               // truncate to N decimal places
)

// InstrumentRules are the normalized trading constraints of one market.
// Zero-valued decimal fields mean "no constraint" ONLY where documented;
// quantization modes must be set for a market to be usable.
type InstrumentRules struct {
	QtyMode     PrecisionMode
	QtyStep     decimal.Decimal // PrecisionStep: base-quantity step
	QtyDecimals int32           // PrecisionDecimals: base-quantity decimals

	PriceMode     PrecisionMode
	PriceTick     decimal.Decimal
	PriceDecimals int32

	MinQty      decimal.Decimal // minimum base quantity (0 = none)
	MaxQty      decimal.Decimal // maximum base quantity (0 = none)
	MinNotional decimal.Decimal // minimum order value in quote (0 = none)
	MaxNotional decimal.Decimal // maximum order value in quote (0 = none)
	// NotionalMarketBasis: the venue evaluates a MARKET order's
	// notional against an average price over AvgPriceMins minutes when
	// ApplyMinToMarket is set (Binance NOTIONAL semantics, audit T12).
	// The simulator approximates that average with the depth walk's own
	// VWAP — the exact figure the model can see — rather than ignoring
	// the market-order basis entirely.
	ApplyMinToMarket bool
	AvgPriceMins     int32

	// MARKET orders carry their own quantity filter on Binance
	// (MARKET_LOT_SIZE, volume-derived and usually far tighter than
	// LOT_SIZE). Zero fields fall back to the LOT_SIZE values; see
	// ForMarketOrders.
	MarketQtyStep decimal.Decimal
	MarketMinQty  decimal.Decimal
	MarketMaxQty  decimal.Decimal
}

// ForMarketOrders returns the rules a MARKET order is validated against:
// the market-order quantity filter where the venue publishes one, the
// limit-order filter otherwise. Callers pricing MARKET fills use these so
// the model cannot "fill" a size the venue would reject (audit T4).
func (r InstrumentRules) ForMarketOrders() InstrumentRules {
	out := r
	if r.MarketQtyStep.IsPositive() {
		out.QtyMode, out.QtyStep = PrecisionStep, r.MarketQtyStep
	}
	if r.MarketMinQty.IsPositive() {
		out.MinQty = r.MarketMinQty
	}
	if r.MarketMaxQty.IsPositive() {
		out.MaxQty = r.MarketMaxQty
	}
	return out
}

var (
	ErrQtyBelowMin      = errors.New("quantity below minimum")
	ErrQtyAboveMax      = errors.New("quantity above maximum")
	ErrNotionalBelowMin = errors.New("notional below minimum")
	ErrNotionalAboveMax = errors.New("notional above maximum")
	ErrRulesUnset       = errors.New("instrument rules unset")
	ErrNegativeAmount   = errors.New("negative amount")
)

// Usable reports whether the rules carry enough information to quantize
// AND to verify the notional floor. A market with no NOTIONAL filter
// publishes no minimum order value: any sized order could be refused by
// the venue, so the rules are unusable — rejected on uncertainty rather
// than guessed at (triangular-constraints §"reject on uncertainty",
// audit T12).
func (r InstrumentRules) Usable() bool {
	qtyOK := (r.QtyMode == PrecisionStep && r.QtyStep.IsPositive()) ||
		(r.QtyMode == PrecisionDecimals && r.QtyDecimals >= 0)
	priceOK := (r.PriceMode == PrecisionStep && r.PriceTick.IsPositive()) ||
		(r.PriceMode == PrecisionDecimals && r.PriceDecimals >= 0)
	return qtyOK && priceOK && r.MinNotional.IsPositive()
}

// QuantizeQty truncates a base quantity DOWN to the instrument's
// quantization (never up: rounding up could exceed available funds or
// depth). Returns an error for negative input or unusable rules.
func (r InstrumentRules) QuantizeQty(q decimal.Decimal) (decimal.Decimal, error) {
	if q.IsNegative() {
		return decimal.Zero, ErrNegativeAmount
	}
	switch r.QtyMode {
	case PrecisionStep:
		if !r.QtyStep.IsPositive() {
			return decimal.Zero, fmt.Errorf("%w: qty step", ErrRulesUnset)
		}
		steps, _ := q.QuoRem(r.QtyStep, 0) // integer number of whole steps
		return steps.Mul(r.QtyStep), nil
	case PrecisionDecimals:
		return q.Truncate(r.QtyDecimals), nil
	default:
		return decimal.Zero, fmt.Errorf("%w: qty mode", ErrRulesUnset)
	}
}

// QuantizePriceDown / QuantizePriceUp truncate a price to the tick grid in
// the stated direction. Passive-safe direction is the caller's decision
// (bids down, asks up when placing; simulation mostly reads book prices,
// which are already on-grid).
func (r InstrumentRules) QuantizePriceDown(p decimal.Decimal) (decimal.Decimal, error) {
	return r.quantizePrice(p, false)
}

func (r InstrumentRules) QuantizePriceUp(p decimal.Decimal) (decimal.Decimal, error) {
	return r.quantizePrice(p, true)
}

func (r InstrumentRules) quantizePrice(p decimal.Decimal, up bool) (decimal.Decimal, error) {
	if p.IsNegative() {
		return decimal.Zero, ErrNegativeAmount
	}
	switch r.PriceMode {
	case PrecisionStep:
		if !r.PriceTick.IsPositive() {
			return decimal.Zero, fmt.Errorf("%w: price tick", ErrRulesUnset)
		}
		ticks, rem := p.QuoRem(r.PriceTick, 0)
		res := ticks.Mul(r.PriceTick)
		if up && !rem.IsZero() {
			res = res.Add(r.PriceTick)
		}
		return res, nil
	case PrecisionDecimals:
		t := p.Truncate(r.PriceDecimals)
		if up && !t.Equal(p) {
			t = t.Add(decimal.New(1, -r.PriceDecimals))
		}
		return t, nil
	default:
		return decimal.Zero, fmt.Errorf("%w: price mode", ErrRulesUnset)
	}
}

// ValidateOrder checks an already-quantized (price, qty) pair against min/
// max quantity and notional constraints. price is the expected average
// execution price (VWAP for depth-aware sizing); callers holding the
// walk's EXACT cost or proceeds should prefer ValidateQty + ValidateNotional
// — VWAP × qty rounds against the exact figure the venue will see (audit
// T12).
func (r InstrumentRules) ValidateOrder(price, qty decimal.Decimal) error {
	if err := r.ValidateQty(qty); err != nil {
		return err
	}
	return r.ValidateNotional(price.Mul(qty))
}

// ValidateQty checks an already-quantized quantity against the min/max
// quantity constraints alone.
func (r InstrumentRules) ValidateQty(qty decimal.Decimal) error {
	if qty.IsNegative() {
		return ErrNegativeAmount
	}
	if r.MinQty.IsPositive() && qty.LessThan(r.MinQty) {
		return fmt.Errorf("%w: %s < %s", ErrQtyBelowMin, qty, r.MinQty)
	}
	if r.MaxQty.IsPositive() && qty.GreaterThan(r.MaxQty) {
		return fmt.Errorf("%w: %s > %s", ErrQtyAboveMax, qty, r.MaxQty)
	}
	return nil
}

// ValidateNotional checks an exact order value — the depth walk's cost
// or proceeds — against the notional bounds. The venue evaluates the
// notional of the value that actually moves; a VWAP×qty reconstruction
// of it can round below a floor the exact figure clears, or above a
// cap it does not (audit T12).
func (r InstrumentRules) ValidateNotional(notional decimal.Decimal) error {
	if notional.IsNegative() {
		return ErrNegativeAmount
	}
	if r.MinNotional.IsPositive() && notional.LessThan(r.MinNotional) {
		return fmt.Errorf("%w: %s < %s", ErrNotionalBelowMin, notional, r.MinNotional)
	}
	if r.MaxNotional.IsPositive() && notional.GreaterThan(r.MaxNotional) {
		return fmt.Errorf("%w: %s > %s", ErrNotionalAboveMax, notional, r.MaxNotional)
	}
	return nil
}
