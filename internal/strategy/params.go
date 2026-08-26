// Package strategy is the versioned dynamic-configuration service
// (docs/architecture.md §13): typed parameters, validation, immutable
// version rows with diffs and actor, hot swap via atomic pointer, and
// rollback-as-new-version. Static bootstrap config (ports, DSNs, secret
// refs) stays in internal/config; nothing here is a secret.
package strategy

import (
	"fmt"
	"time"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/opportunity"
	"github.com/cploutarchou/arb-chain-bot/internal/pricing"
	"github.com/cploutarchou/arb-chain-bot/internal/risk"
	"github.com/cploutarchou/arb-chain-bot/internal/scanner"
)

// Params is the complete dynamic strategy payload. Decimals marshal as
// quoted strings (shopspring default); durations are explicit-unit
// integer fields so the JSON never carries ambiguous bare numbers.
type Params struct {
	Scanner       ScannerParams      `json:"scanner"`
	Risk          RiskParams         `json:"risk"`
	Notifications NotificationParams `json:"notifications"`
}

// ScannerParams govern opportunity detection and sizing.
type ScannerParams struct {
	LatencyBufferBps decimal.Decimal `json:"latency_buffer_bps"`
	RiskBufferBps    decimal.Decimal `json:"risk_buffer_bps"`
	TTLMs            int64           `json:"ttl_ms"`
	MinInput         decimal.Decimal `json:"min_input"`
	Depth            int             `json:"depth"`
	GridPoints       int             `json:"grid_points"`
	RefineIters      int             `json:"refine_iters"`
	MaxBookAgeMs     int64           `json:"max_book_age_ms"`
	// Workers applies at component start, not on hot swap.
	Workers int `json:"workers"`
}

// RiskParams mirror risk.Limits (global scope) in JSON-safe form. The
// risk engine stays the authority; these are only its inputs.
type RiskParams struct {
	MinNetEdgeBps            decimal.Decimal `json:"min_net_edge_bps"`
	MinExpectedProfit        decimal.Decimal `json:"min_expected_profit"`
	MaxTradeSize             decimal.Decimal `json:"max_trade_size"`
	MaxCapitalPerTriangle    decimal.Decimal `json:"max_capital_per_triangle"`
	MaxCapitalUtilization    decimal.Decimal `json:"max_capital_utilization"`
	MaxConcurrentSimulations int             `json:"max_concurrent_simulations"`
	MaxBookAgeMs             int64           `json:"max_book_age_ms"`
	MaxBookAgeSpreadMs       int64           `json:"max_book_age_spread_ms"`
	MaxSlippageBps           decimal.Decimal `json:"max_slippage_bps"`
	MaxPriceImpactBps        decimal.Decimal `json:"max_price_impact_bps"`
	MaxDailyLoss             decimal.Decimal `json:"max_daily_loss"`
	MaxDrawdown              decimal.Decimal `json:"max_drawdown"`
	MinDataQuality           decimal.Decimal `json:"min_data_quality"`
}

// NotificationParams route alerts (consumed by the notification service).
type NotificationParams struct {
	CooldownSeconds int `json:"cooldown_seconds"`
	// Routes maps severity (INFO/WARNING/CRITICAL) to channel names
	// (web, telegram). Missing severity falls back to web only.
	Routes map[string][]string `json:"routes,omitempty"`
}

// Clone returns a deep copy. Routes is the only reference type in the
// tree; without the copy a caller could mutate an immutable version's
// routing in place (audit P3).
func (p Params) Clone() Params {
	c := p
	if p.Notifications.Routes != nil {
		c.Notifications.Routes = make(map[string][]string, len(p.Notifications.Routes))
		for k, v := range p.Notifications.Routes {
			c.Notifications.Routes[k] = append([]string(nil), v...)
		}
	}
	return c
}

// DefaultParams are the conservative bootstrap values; they match the
// previously hard-coded engine defaults so a fresh install behaves
// identically to before the config service existed.
func DefaultParams() Params {
	return Params{
		Scanner: ScannerParams{
			LatencyBufferBps: decimal.NewFromInt(5),
			RiskBufferBps:    decimal.NewFromInt(5),
			TTLMs:            400,
			MinInput:         decimal.NewFromInt(50),
			Depth:            50,
			GridPoints:       pricing.DefaultSizeSearch.GridPoints,
			RefineIters:      pricing.DefaultSizeSearch.RefineIters,
			MaxBookAgeMs:     2000,
			Workers:          2,
		},
		Risk: RiskParams{
			MinNetEdgeBps:            decimal.NewFromInt(5),
			MinExpectedProfit:        decimal.NewFromInt(1),
			MaxTradeSize:             decimal.NewFromInt(1000),
			MaxCapitalPerTriangle:    decimal.NewFromInt(2000),
			MaxCapitalUtilization:    decimal.RequireFromString("0.5"),
			MaxConcurrentSimulations: 3,
			MaxBookAgeMs:             1500,
			MaxBookAgeSpreadMs:       750,
			MaxSlippageBps:           decimal.NewFromInt(50),
			MaxPriceImpactBps:        decimal.NewFromInt(30),
			MaxDailyLoss:             decimal.NewFromInt(200),
			MaxDrawdown:              decimal.RequireFromString("0.05"),
			MinDataQuality:           decimal.RequireFromString("0.5"),
		},
		Notifications: NotificationParams{
			CooldownSeconds: 60,
			Routes: map[string][]string{
				"INFO":     {"web"},
				"WARNING":  {"web", "telegram"},
				"CRITICAL": {"web", "telegram"},
			},
		},
	}
}

// Validate rejects out-of-bounds parameters. A payload that fails here
// never becomes a version.
func (p Params) Validate() error {
	s := p.Scanner
	switch {
	case s.LatencyBufferBps.IsNegative() || s.LatencyBufferBps.GreaterThan(decimal.NewFromInt(1000)):
		return fmt.Errorf("strategy: scanner.latency_buffer_bps out of [0,1000]")
	case s.RiskBufferBps.IsNegative() || s.RiskBufferBps.GreaterThan(decimal.NewFromInt(1000)):
		return fmt.Errorf("strategy: scanner.risk_buffer_bps out of [0,1000]")
	case s.TTLMs < 50 || s.TTLMs > 10_000:
		return fmt.Errorf("strategy: scanner.ttl_ms out of [50,10000]")
	case !s.MinInput.IsPositive():
		return fmt.Errorf("strategy: scanner.min_input must be positive")
	case s.Depth < 5 || s.Depth > 500:
		return fmt.Errorf("strategy: scanner.depth out of [5,500]")
	case s.GridPoints < 3 || s.GridPoints > 41:
		return fmt.Errorf("strategy: scanner.grid_points out of [3,41]")
	case s.RefineIters < 0 || s.RefineIters > 40:
		return fmt.Errorf("strategy: scanner.refine_iters out of [0,40]")
	case s.MaxBookAgeMs < 100 || s.MaxBookAgeMs > 60_000:
		return fmt.Errorf("strategy: scanner.max_book_age_ms out of [100,60000]")
	case s.Workers < 1 || s.Workers > 32:
		return fmt.Errorf("strategy: scanner.workers out of [1,32]")
	}

	r := p.Risk
	one := decimal.NewFromInt(1)
	switch {
	case r.MinNetEdgeBps.IsNegative():
		return fmt.Errorf("strategy: risk.min_net_edge_bps must be >= 0")
	case r.MinExpectedProfit.IsNegative():
		return fmt.Errorf("strategy: risk.min_expected_profit must be >= 0")
	case !r.MaxTradeSize.IsPositive():
		return fmt.Errorf("strategy: risk.max_trade_size must be positive")
	case !r.MaxCapitalPerTriangle.IsPositive():
		return fmt.Errorf("strategy: risk.max_capital_per_triangle must be positive")
	case !r.MaxCapitalUtilization.IsPositive() || r.MaxCapitalUtilization.GreaterThan(one):
		return fmt.Errorf("strategy: risk.max_capital_utilization out of (0,1]")
	case r.MaxConcurrentSimulations < 1 || r.MaxConcurrentSimulations > 64:
		return fmt.Errorf("strategy: risk.max_concurrent_simulations out of [1,64]")
	case r.MaxBookAgeMs < 100 || r.MaxBookAgeMs > 60_000:
		return fmt.Errorf("strategy: risk.max_book_age_ms out of [100,60000]")
	case r.MaxBookAgeSpreadMs < 50 || r.MaxBookAgeSpreadMs > 60_000:
		return fmt.Errorf("strategy: risk.max_book_age_spread_ms out of [50,60000]")
	case r.MaxSlippageBps.IsNegative():
		return fmt.Errorf("strategy: risk.max_slippage_bps must be >= 0")
	case r.MaxPriceImpactBps.IsNegative():
		return fmt.Errorf("strategy: risk.max_price_impact_bps must be >= 0")
	case !r.MaxDailyLoss.IsPositive():
		return fmt.Errorf("strategy: risk.max_daily_loss must be positive")
	case !r.MaxDrawdown.IsPositive() || r.MaxDrawdown.GreaterThanOrEqual(one):
		return fmt.Errorf("strategy: risk.max_drawdown out of (0,1)")
	case r.MinDataQuality.IsNegative() || r.MinDataQuality.GreaterThan(one):
		return fmt.Errorf("strategy: risk.min_data_quality out of [0,1]")
	}

	n := p.Notifications
	// Zero is rejected rather than silently coerced: the notification
	// service would substitute its 60s default, and a config that reads
	// 0 while behaving as 60 lies to the operator (audit CR-P2-8).
	if n.CooldownSeconds < 1 || n.CooldownSeconds > 3600 {
		return fmt.Errorf("strategy: notifications.cooldown_seconds out of [1,3600]")
	}
	for sev, chans := range n.Routes {
		switch sev {
		case "INFO", "WARNING", "CRITICAL":
		default:
			return fmt.Errorf("strategy: notifications.routes has unknown severity %q", sev)
		}
		for _, c := range chans {
			switch c {
			case "web", "telegram":
			default:
				return fmt.Errorf("strategy: notifications.routes[%s] has unknown channel %q", sev, c)
			}
		}
	}
	return nil
}

// ScannerConfig converts the payload into the scanner's config slice,
// citing the version so every opportunity records what produced it.
func (p Params) ScannerConfig(version int64) scanner.Config {
	return scanner.Config{
		ConfigVersion: version,
		Buffers: opportunity.Buffers{
			LatencyBps: p.Scanner.LatencyBufferBps,
			RiskBps:    p.Scanner.RiskBufferBps,
		},
		TTL:      time.Duration(p.Scanner.TTLMs) * time.Millisecond,
		MinInput: p.Scanner.MinInput,
		Depth:    p.Scanner.Depth,
		Workers:  p.Scanner.Workers,
		Search: pricing.SizeSearch{
			GridPoints:  p.Scanner.GridPoints,
			RefineIters: p.Scanner.RefineIters,
		},
		MaxBookAge: time.Duration(p.Scanner.MaxBookAgeMs) * time.Millisecond,
	}
}

// RiskResolver converts the payload into the risk engine's resolver
// (global scope; scoped overrides remain an operator-console feature).
func (p Params) RiskResolver() risk.Resolver {
	return risk.Resolver{Global: risk.Limits{
		MinNetEdgeBps:            p.Risk.MinNetEdgeBps,
		MinExpectedProfit:        p.Risk.MinExpectedProfit,
		MaxTradeSize:             p.Risk.MaxTradeSize,
		MaxCapitalPerTriangle:    p.Risk.MaxCapitalPerTriangle,
		MaxCapitalUtilization:    p.Risk.MaxCapitalUtilization,
		MaxConcurrentSimulations: p.Risk.MaxConcurrentSimulations,
		MaxBookAge:               time.Duration(p.Risk.MaxBookAgeMs) * time.Millisecond,
		MaxBookAgeSpread:         time.Duration(p.Risk.MaxBookAgeSpreadMs) * time.Millisecond,
		MaxSlippageBps:           p.Risk.MaxSlippageBps,
		MaxPriceImpactBps:        p.Risk.MaxPriceImpactBps,
		MaxDailyLoss:             p.Risk.MaxDailyLoss,
		MaxDrawdown:              p.Risk.MaxDrawdown,
		MinDataQuality:           p.Risk.MinDataQuality,
		OpportunityTTL:           time.Duration(p.Scanner.TTLMs) * time.Millisecond,
		LatencyBufferBps:         p.Scanner.LatencyBufferBps,
		RiskBufferBps:            p.Scanner.RiskBufferBps,
	}}
}
