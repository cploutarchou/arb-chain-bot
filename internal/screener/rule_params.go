package screener

import (
	"fmt"

	"github.com/shopspring/decimal"
)

// Strategy names the paper-execution model a rule runs under
// (docs/design/strategy-models.md §2, §3, §5). The default follows the
// rule kind — spread → cross_venue_spot, carry → carry, basis →
// funding_harvest — and RuleParams.Strategy may override it for the two
// perp-based kinds (a "basis" rule may run the §3 carry model and vice
// versa; a spread rule is always cross-venue spot).
type Strategy string

const (
	StrategyCrossVenueSpot Strategy = "cross_venue_spot"
	StrategyCarry          Strategy = "carry"
	StrategyFundingHarvest Strategy = "funding_harvest"
)

// RuleParams are the per-rule model inputs of strategy-models §1–§5.
// Every field is optional on the wire; nil means "use the documented
// default" (see Defaults* helpers below), so a rule saved before this
// task keeps validating and behaving exactly as its defaults describe.
// Values are decimal strings on the wire, never floats.
type RuleParams struct {
	Strategy Strategy `json:"strategy,omitempty"`

	// §1.3 slip allowance per leg (bps; default 2) and depth haircut
	// (fraction of top-of-book qty deemed fillable; default 1, stress
	// 0.5). PartialAllowed lets a smaller-than-requested fill proceed.
	SlipBps        *decimal.Decimal `json:"slip_bps,omitempty"`
	DepthHaircut   *decimal.Decimal `json:"depth_haircut,omitempty"`
	PartialAllowed bool             `json:"partial_allowed,omitempty"`
	// §2.1 buffer_bps (default 5). StepSize is the quantity step both
	// venues are truncated to (default 0.00001); MinNotionalQuote skips
	// executions below a venue minimum (default 0 = not enforced —
	// instrument rules are not fetched by the screener).
	BufferBps        *decimal.Decimal `json:"buffer_bps,omitempty"`
	StepSize         *decimal.Decimal `json:"step_size,omitempty"`
	MinNotionalQuote *decimal.Decimal `json:"min_notional_quote,omitempty"`
	// §2.4 drift cap in quote (default 3 × paper_size_quote).
	MaxDriftQuote *decimal.Decimal `json:"max_drift_quote,omitempty"`

	// §3.1/§3.2 carry: entry edge (default 10 bps), close basis
	// (default 0 bps), exit when predicted funding ≤ ExitFunding
	// (fraction per interval, default 0) for ExitK intervals (default 2),
	// max hold hours (default 720 = 30 d).
	MinEdgeBps  *decimal.Decimal `json:"min_edge_bps,omitempty"`
	CloseBps    *decimal.Decimal `json:"close_bps,omitempty"`
	ExitFunding *decimal.Decimal `json:"exit_funding,omitempty"`
	ExitK       int              `json:"exit_k,omitempty"`
	MaxHoldH    int              `json:"max_hold_h,omitempty"`
	// §3.4 stops: margin stop fraction (default 0.50), basis blow-out
	// (default 100 bps for BTC/ETH, 300 otherwise), maintenance margin
	// rate as a fraction (nil = unknown → MMR_UNKNOWN skip; the screener
	// never infers it from another venue).
	MarginStopFrac *decimal.Decimal `json:"margin_stop_frac,omitempty"`
	BasisStopBps   *decimal.Decimal `json:"basis_stop_bps,omitempty"`
	MMR            *decimal.Decimal `json:"mmr,omitempty"`

	// §5.1/§5.2 funding harvest: entry floor per interval (default 3
	// bps), breakeven cap (default 12 intervals), max negative basis
	// (default 10 bps), exit floor (default 1 bps).
	MinFundingBps         *decimal.Decimal `json:"min_funding_bps,omitempty"`
	MaxBreakevenIntervals int              `json:"max_breakeven_intervals,omitempty"`
	MaxNegativeBasisBps   *decimal.Decimal `json:"max_negative_basis_bps,omitempty"`
	ExitFundingBps        *decimal.Decimal `json:"exit_funding_bps,omitempty"`
}

var (
	defaultSlipBps       = decimal.NewFromInt(2)
	defaultDepthHaircut  = decimal.NewFromInt(1)
	defaultBufferBps     = decimal.NewFromInt(5)
	defaultStepSize      = decimal.RequireFromString("0.00001")
	defaultMinEdgeBps    = decimal.NewFromInt(10)
	defaultMarginStop    = decimal.RequireFromString("0.5")
	defaultBasisStopMaj  = decimal.NewFromInt(100)
	defaultBasisStopOth  = decimal.NewFromInt(300)
	defaultMinFundingBps = decimal.NewFromInt(3)
	defaultMaxNegBasis   = decimal.NewFromInt(10)
	defaultExitFunding   = decimal.NewFromInt(1)
	defaultDriftMultiple = decimal.NewFromInt(3)
)

// Validate rejects negative or out-of-range parameters. Pure.
func (p *RuleParams) Validate(kind RuleKind) error {
	if p == nil {
		return nil
	}
	switch p.Strategy {
	case "":
	case StrategyCrossVenueSpot:
		if kind != RuleKindSpread {
			return fmt.Errorf("%w: params.strategy=cross_venue_spot requires kind=spread", ErrInvalid)
		}
	case StrategyCarry, StrategyFundingHarvest:
		if kind == RuleKindSpread {
			return fmt.Errorf("%w: params.strategy=%s is not valid for kind=spread", ErrInvalid, p.Strategy)
		}
	default:
		return fmt.Errorf("%w: params.strategy must be one of cross_venue_spot|carry|funding_harvest", ErrInvalid)
	}
	nonNeg := map[string]*decimal.Decimal{
		"slip_bps": p.SlipBps, "buffer_bps": p.BufferBps, "step_size": p.StepSize,
		"min_notional_quote": p.MinNotionalQuote, "max_drift_quote": p.MaxDriftQuote,
		"min_edge_bps": p.MinEdgeBps, "basis_stop_bps": p.BasisStopBps,
		"min_funding_bps": p.MinFundingBps, "max_negative_basis_bps": p.MaxNegativeBasisBps,
		"exit_funding_bps": p.ExitFundingBps,
	}
	for name, v := range nonNeg {
		if v != nil && v.IsNegative() {
			return fmt.Errorf("%w: params.%s must be >= 0", ErrInvalid, name)
		}
	}
	if p.DepthHaircut != nil && (!p.DepthHaircut.IsPositive() || p.DepthHaircut.GreaterThan(decOne)) {
		return fmt.Errorf("%w: params.depth_haircut must be in (0, 1]", ErrInvalid)
	}
	if p.MarginStopFrac != nil && (!p.MarginStopFrac.IsPositive() || p.MarginStopFrac.GreaterThan(decOne)) {
		return fmt.Errorf("%w: params.margin_stop_frac must be in (0, 1]", ErrInvalid)
	}
	if p.MMR != nil && (!p.MMR.IsPositive() || p.MMR.GreaterThan(decOne)) {
		return fmt.Errorf("%w: params.mmr must be in (0, 1]", ErrInvalid)
	}
	if p.ExitK < 0 || p.MaxHoldH < 0 || p.MaxBreakevenIntervals < 0 {
		return fmt.Errorf("%w: params.exit_k, max_hold_h, max_breakeven_intervals must be >= 0", ErrInvalid)
	}
	return nil
}

func orDefault(v *decimal.Decimal, def decimal.Decimal) decimal.Decimal {
	if v == nil {
		return def
	}
	return *v
}

// EffectiveStrategy resolves the strategy a rule executes under.
func (r Rule) EffectiveStrategy() Strategy {
	if r.Params != nil && r.Params.Strategy != "" {
		return r.Params.Strategy
	}
	switch r.Kind {
	case RuleKindCarry:
		return StrategyCarry
	case RuleKindBasis:
		return StrategyFundingHarvest
	default:
		return StrategyCrossVenueSpot
	}
}

// Resolved model inputs with defaults applied (strategy-models §1–§5).
func (r Rule) SlipBps() decimal.Decimal { return orDefault(r.params().SlipBps, defaultSlipBps) }
func (r Rule) DepthHaircut() decimal.Decimal {
	return orDefault(r.params().DepthHaircut, defaultDepthHaircut)
}
func (r Rule) BufferBps() decimal.Decimal { return orDefault(r.params().BufferBps, defaultBufferBps) }
func (r Rule) StepSize() decimal.Decimal  { return orDefault(r.params().StepSize, defaultStepSize) }
func (r Rule) MinNotionalQuote() decimal.Decimal {
	return orDefault(r.params().MinNotionalQuote, decimal.Zero)
}
func (r Rule) PartialAllowed() bool { return r.params().PartialAllowed }
func (r Rule) MaxDriftQuote() decimal.Decimal {
	return orDefault(r.params().MaxDriftQuote, r.PaperSizeQuote.Mul(defaultDriftMultiple))
}
func (r Rule) MinEdgeBps() decimal.Decimal {
	return orDefault(r.params().MinEdgeBps, defaultMinEdgeBps)
}
func (r Rule) CloseBps() decimal.Decimal    { return orDefault(r.params().CloseBps, decimal.Zero) }
func (r Rule) ExitFunding() decimal.Decimal { return orDefault(r.params().ExitFunding, decimal.Zero) }
func (r Rule) ExitK() int {
	if k := r.params().ExitK; k > 0 {
		return k
	}
	return 2
}
func (r Rule) MaxHoldH() int {
	if h := r.params().MaxHoldH; h > 0 {
		return h
	}
	return 24 * 30
}
func (r Rule) MarginStopFrac() decimal.Decimal {
	return orDefault(r.params().MarginStopFrac, defaultMarginStop)
}

// BasisStopBps defaults per §3.4: 100 bps for BTC/ETH, 300 otherwise.
func (r Rule) BasisStopBps(base string) decimal.Decimal {
	if v := r.params().BasisStopBps; v != nil {
		return *v
	}
	if base == "BTC" || base == "ETH" {
		return defaultBasisStopMaj
	}
	return defaultBasisStopOth
}

// MMR returns the maintenance margin rate when the operator entered
// one; ok=false means unknown (§3.4: skip entry with MMR_UNKNOWN).
func (r Rule) MMR() (decimal.Decimal, bool) {
	if v := r.params().MMR; v != nil {
		return *v, true
	}
	return decimal.Decimal{}, false
}
func (r Rule) MinFundingBps() decimal.Decimal {
	return orDefault(r.params().MinFundingBps, defaultMinFundingBps)
}
func (r Rule) MaxBreakevenIntervals() int {
	if n := r.params().MaxBreakevenIntervals; n > 0 {
		return n
	}
	return 12
}
func (r Rule) MaxNegativeBasisBps() decimal.Decimal {
	return orDefault(r.params().MaxNegativeBasisBps, defaultMaxNegBasis)
}
func (r Rule) ExitFundingBps() decimal.Decimal {
	return orDefault(r.params().ExitFundingBps, defaultExitFunding)
}

func (r Rule) params() RuleParams {
	if r.Params == nil {
		return RuleParams{}
	}
	return *r.Params
}
