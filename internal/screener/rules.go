package screener

import (
	"fmt"

	"github.com/shopspring/decimal"
)

// RuleKind is the alert-rule discriminator (design §7).
type RuleKind string

const (
	RuleKindSpread RuleKind = "spread"
	RuleKindCarry  RuleKind = "carry"
	RuleKindBasis  RuleKind = "basis"
)

// Rule is one alert rule (design §7); evaluation/cooldown/dedup and the
// Telegram push land in T-070 — this task only owns the shape and its
// validation.
type Rule struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Enabled bool     `json:"enabled"`
	Kind    RuleKind `json:"kind"`

	MinSpreadBps *decimal.Decimal `json:"min_spread_bps,omitempty"`
	MinCarryAPR  *decimal.Decimal `json:"min_carry_apr,omitempty"`

	MinLiquidityQuote decimal.Decimal `json:"min_liquidity_quote"`
	MinLifetimeS      int64           `json:"min_lifetime_s"`

	BuyVenues  []Venue  `json:"buy_venues"`
	SellVenues []Venue  `json:"sell_venues"`
	Quotes     []string `json:"quotes"`
	BasesAllow []string `json:"bases_allow"`
	BasesDeny  []string `json:"bases_deny"`

	CooldownS int64 `json:"cooldown_s"`

	Telegram       bool            `json:"telegram"`
	AutoPaper      bool            `json:"auto_paper"`
	PaperSizeQuote decimal.Decimal `json:"paper_size_quote"`

	// Params are the optional strategy-model inputs (rule_params.go);
	// nil keeps every documented default.
	Params *RuleParams `json:"params,omitempty"`
}

const maxRuleNameLen = 100

// Validate rejects a structurally invalid rule. Pure, no I/O — it does
// not check that buy/sell venues actually have live collectors (T-066).
func (r Rule) Validate() error {
	if r.Name == "" || len(r.Name) > maxRuleNameLen {
		return fmt.Errorf("%w: name must be 1..%d chars", ErrInvalid, maxRuleNameLen)
	}
	switch r.Kind {
	case RuleKindSpread:
		if r.MinSpreadBps == nil {
			return fmt.Errorf("%w: kind=spread requires min_spread_bps", ErrInvalid)
		}
		if r.MinSpreadBps.IsNegative() {
			return fmt.Errorf("%w: min_spread_bps must be >= 0", ErrInvalid)
		}
	case RuleKindCarry, RuleKindBasis:
		if r.MinCarryAPR == nil {
			return fmt.Errorf("%w: kind=%s requires min_carry_apr", ErrInvalid, r.Kind)
		}
	default:
		return fmt.Errorf("%w: kind must be one of spread|carry|basis, got %q", ErrInvalid, r.Kind)
	}
	if r.MinLiquidityQuote.IsNegative() {
		return fmt.Errorf("%w: min_liquidity_quote must be >= 0", ErrInvalid)
	}
	if r.MinLifetimeS < 0 {
		return fmt.Errorf("%w: min_lifetime_s must be >= 0", ErrInvalid)
	}
	if r.CooldownS < 0 {
		return fmt.Errorf("%w: cooldown_s must be >= 0", ErrInvalid)
	}
	for _, v := range r.BuyVenues {
		if !KnownVenues[v] {
			return fmt.Errorf("%w: buy_venues has unknown venue %q", ErrInvalid, v)
		}
	}
	for _, v := range r.SellVenues {
		if !KnownVenues[v] {
			return fmt.Errorf("%w: sell_venues has unknown venue %q", ErrInvalid, v)
		}
	}
	if r.AutoPaper && !r.PaperSizeQuote.IsPositive() {
		return fmt.Errorf("%w: auto_paper requires paper_size_quote > 0", ErrInvalid)
	}
	return r.Params.Validate(r.Kind)
}
