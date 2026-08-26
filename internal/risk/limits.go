// Package risk is the deterministic gate in front of qualification and
// execution: limit evaluation with scoped overrides, machine-readable
// rejection reasons, and circuit breakers (docs/risk.md). Same inputs +
// same config version ⇒ same decision. Nothing overrides this engine.
package risk

import (
	"time"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/exchange"
)

// Limits are the effective controls for one evaluation. Values are
// decimals/durations, not floats; zero means "unset" ONLY where the field
// comment says so.
type Limits struct {
	MinNetEdgeBps     decimal.Decimal // required net return after buffers
	MinExpectedProfit decimal.Decimal // in start asset

	MaxTradeSize          decimal.Decimal // per-cycle input cap (start asset)
	MaxCapitalPerTriangle decimal.Decimal // reserved+new per triangle
	MaxCapitalUtilization decimal.Decimal // 0..1 fraction of session capital

	MaxConcurrentSimulations int

	MaxBookAge       time.Duration
	MaxBookAgeSpread time.Duration

	MaxSlippageBps    decimal.Decimal // modeled leg slippage cap
	MaxPriceImpactBps decimal.Decimal // worst-leg VWAP impact cap

	MaxDailyLoss decimal.Decimal // loss magnitude in start asset
	MaxDrawdown  decimal.Decimal // 0..1 fraction

	MinDataQuality decimal.Decimal // 0..1

	OpportunityTTL time.Duration
	LatencyBufferBps decimal.Decimal
	RiskBufferBps    decimal.Decimal
}

// Override carries the per-scope adjustable subset (docs/risk.md §2):
// nil fields inherit. Book-age overrides exist per exchange; edge/size/
// impact/quality overrides exist at every scope. Enabled=false disables
// the scope entirely.
type Override struct {
	Enabled               *bool
	MinNetEdgeBps         *decimal.Decimal
	MinExpectedProfit     *decimal.Decimal
	MaxTradeSize          *decimal.Decimal
	MaxCapitalPerTriangle *decimal.Decimal
	MaxSlippageBps        *decimal.Decimal
	MaxPriceImpactBps     *decimal.Decimal
	MinDataQuality        *decimal.Decimal
	MaxBookAge            *time.Duration
}

func (o Override) applyTo(l *Limits) {
	if o.MinNetEdgeBps != nil {
		l.MinNetEdgeBps = *o.MinNetEdgeBps
	}
	if o.MinExpectedProfit != nil {
		l.MinExpectedProfit = *o.MinExpectedProfit
	}
	if o.MaxTradeSize != nil {
		l.MaxTradeSize = *o.MaxTradeSize
	}
	if o.MaxCapitalPerTriangle != nil {
		l.MaxCapitalPerTriangle = *o.MaxCapitalPerTriangle
	}
	if o.MaxSlippageBps != nil {
		l.MaxSlippageBps = *o.MaxSlippageBps
	}
	if o.MaxPriceImpactBps != nil {
		l.MaxPriceImpactBps = *o.MaxPriceImpactBps
	}
	if o.MinDataQuality != nil {
		l.MinDataQuality = *o.MinDataQuality
	}
	if o.MaxBookAge != nil {
		l.MaxBookAge = *o.MaxBookAge
	}
}

// Resolver merges Global ← exchange ← starting-asset ← triangle overrides
// (most specific wins per field).
type Resolver struct {
	Global     Limits
	ByExchange map[exchange.ExchangeID]Override
	ByAsset    map[exchange.Asset]Override
	ByTriangle map[string]Override
}

// Effective resolves the limits for one opportunity's scope and reports
// whether any scope disabled it.
func (r Resolver) Effective(ex exchange.ExchangeID, asset exchange.Asset, triangleID string) (Limits, bool) {
	l := r.Global
	disabled := false
	for _, o := range []Override{r.ByExchange[ex], r.ByAsset[asset], r.ByTriangle[triangleID]} {
		if o.Enabled != nil && !*o.Enabled {
			disabled = true
		}
		o.applyTo(&l)
	}
	return l, disabled
}
