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
}

var (
	ErrQtyBelowMin      = errors.New("quantity below minimum")
	ErrQtyAboveMax      = errors.New("quantity above maximum")
	ErrNotionalBelowMin = errors.New("notional below minimum")
	ErrNotionalAboveMax = errors.New("notional above maximum")
	ErrRulesUnset       = errors.New("instrument rules unset")
	ErrNegativeAmount   = errors.New("negative amount")
)

// Usable reports whether the rules carry enough information to quantize.
func (r InstrumentRules) Usable() bool {
	qtyOK := (r.QtyMode == PrecisionStep && r.QtyStep.IsPositive()) ||
		(r.QtyMode == PrecisionDecimals && r.QtyDecimals >= 0)
	priceOK := (r.PriceMode == PrecisionStep && r.PriceTick.IsPositive()) ||
		(r.PriceMode == PrecisionDecimals && r.PriceDecimals >= 0)
	return qtyOK && priceOK
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
// execution price (VWAP for depth-aware sizing).
func (r InstrumentRules) ValidateOrder(price, qty decimal.Decimal) error {
	if price.IsNegative() || qty.IsNegative() {
		return ErrNegativeAmount
	}
	if r.MinQty.IsPositive() && qty.LessThan(r.MinQty) {
		return fmt.Errorf("%w: %s < %s", ErrQtyBelowMin, qty, r.MinQty)
	}
	if r.MaxQty.IsPositive() && qty.GreaterThan(r.MaxQty) {
		return fmt.Errorf("%w: %s > %s", ErrQtyAboveMax, qty, r.MaxQty)
	}
	notional := price.Mul(qty)
	if r.MinNotional.IsPositive() && notional.LessThan(r.MinNotional) {
		return fmt.Errorf("%w: %s < %s", ErrNotionalBelowMin, notional, r.MinNotional)
	}
	if r.MaxNotional.IsPositive() && notional.GreaterThan(r.MaxNotional) {
		return fmt.Errorf("%w: %s > %s", ErrNotionalAboveMax, notional, r.MaxNotional)
	}
	return nil
}
