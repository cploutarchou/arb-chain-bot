package risk

import (
	"fmt"
	"time"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/opportunity"
	"github.com/cploutarchou/arb-chain-bot/internal/orderbook"
)

// Reason codes are the machine-readable rejection vocabulary; they flow to
// opportunity records, metrics, and the Risk Center.
const (
	ReasonTriangleDisabled = "RISK_TRIANGLE_DISABLED"
	ReasonBreakerOpen      = "RISK_BREAKER_OPEN"
	ReasonClockUnsafe      = "RISK_CLOCK_UNSAFE"
	ReasonBookState        = "RISK_BOOK_STATE"
	ReasonBookAge          = "RISK_BOOK_AGE"
	ReasonBookAgeSpread    = "RISK_BOOK_AGE_SPREAD"
	ReasonDataQuality      = "RISK_DATA_QUALITY"
	ReasonMinEdge          = "RISK_MIN_EDGE"
	ReasonMinProfit        = "RISK_MIN_PROFIT"
	ReasonMaxTradeSize     = "RISK_MAX_TRADE_SIZE"
	ReasonTriangleCapital  = "RISK_TRIANGLE_CAPITAL"
	ReasonUtilization      = "RISK_UTILIZATION"
	ReasonConcurrency      = "RISK_CONCURRENCY"
	ReasonPriceImpact      = "RISK_PRICE_IMPACT"
	ReasonDailyLoss        = "RISK_DAILY_LOSS"
	ReasonDrawdown         = "RISK_DRAWDOWN"
	ReasonExpired          = "RISK_TTL_EXPIRED"
	// ReasonRevalidation: the pre-execution re-quote found no executable
	// cycle at the qualified size (depth gone, venue minimums no longer
	// cleared) — the scanner's Revalidate emits it before the gate runs.
	ReasonRevalidation = "RISK_REVALIDATION"
)

// Context is the risk-relevant world snapshot at evaluation time. The
// caller (scanner/paper engine) assembles it; Evaluate never reaches out.
type Context struct {
	Now           time.Time
	ConfigVersion int64

	BookStates [3]orderbook.State
	BookAges   [3]time.Duration

	DataQuality  decimal.Decimal // 0..1, min across legs
	ClockHealthy bool

	CapitalAvailable decimal.Decimal // start asset, session scope
	CapitalReserved  decimal.Decimal
	TriangleReserved decimal.Decimal // already reserved for this triangle

	ConcurrentSimulations int

	DailyLoss decimal.Decimal // loss magnitude so far today (>= 0)
	Drawdown  decimal.Decimal // fraction 0..1

	BreakerOpen bool // any relevant breaker OPEN (registry consulted upstream)
}

// Check is one evaluated control, kept for explainability (§36: every
// opportunity must explain why it was qualified or rejected).
type Check struct {
	Name      string
	Observed  string
	Threshold string
	Passed    bool
}

// Decision is the deterministic verdict.
type Decision struct {
	Allowed       bool
	ReasonCode    string // first failing check; empty when allowed
	Checks        []Check
	ConfigVersion int64
	Effective     Limits
}

// Evaluate runs every control and returns the full check list. All checks
// always run — partial evaluation would hide compound problems from the
// rejection explorer.
func Evaluate(op *opportunity.Opportunity, ctx Context, eff Limits, disabled bool) Decision {
	d := Decision{ConfigVersion: ctx.ConfigVersion, Effective: eff}
	add := func(name string, passed bool, observed, threshold string) {
		d.Checks = append(d.Checks, Check{Name: name, Observed: observed, Threshold: threshold, Passed: passed})
		if !passed && d.ReasonCode == "" {
			d.ReasonCode = name
		}
	}

	add(ReasonTriangleDisabled, !disabled, fmt.Sprintf("disabled=%v", disabled), "enabled")
	add(ReasonBreakerOpen, !ctx.BreakerOpen, fmt.Sprintf("open=%v", ctx.BreakerOpen), "closed")
	add(ReasonClockUnsafe, ctx.ClockHealthy, fmt.Sprintf("healthy=%v", ctx.ClockHealthy), "healthy")

	healthy := true
	for _, s := range ctx.BookStates {
		if s != orderbook.StateHealthy {
			healthy = false
		}
	}
	add(ReasonBookState, healthy, fmt.Sprintf("%v/%v/%v", ctx.BookStates[0], ctx.BookStates[1], ctx.BookStates[2]), "HEALTHY x3")

	if eff.MaxBookAge > 0 {
		worst := maxDur(ctx.BookAges)
		add(ReasonBookAge, worst <= eff.MaxBookAge, worst.String(), eff.MaxBookAge.String())
	}
	if eff.MaxBookAgeSpread > 0 {
		spread := maxDur(ctx.BookAges) - minDur(ctx.BookAges)
		add(ReasonBookAgeSpread, spread <= eff.MaxBookAgeSpread, spread.String(), eff.MaxBookAgeSpread.String())
	}
	if eff.MinDataQuality.IsPositive() {
		add(ReasonDataQuality, ctx.DataQuality.GreaterThanOrEqual(eff.MinDataQuality),
			ctx.DataQuality.String(), eff.MinDataQuality.String())
	}

	add(ReasonMinEdge, op.NetReturnBps.GreaterThanOrEqual(eff.MinNetEdgeBps),
		op.NetReturnBps.String()+"bps", eff.MinNetEdgeBps.String()+"bps")
	add(ReasonMinProfit, op.NetProfit.GreaterThanOrEqual(eff.MinExpectedProfit),
		op.NetProfit.String(), eff.MinExpectedProfit.String())

	input := op.Quote.InputConsumed
	if eff.MaxTradeSize.IsPositive() {
		add(ReasonMaxTradeSize, input.LessThanOrEqual(eff.MaxTradeSize), input.String(), eff.MaxTradeSize.String())
	}
	if eff.MaxCapitalPerTriangle.IsPositive() {
		total := ctx.TriangleReserved.Add(input)
		add(ReasonTriangleCapital, total.LessThanOrEqual(eff.MaxCapitalPerTriangle),
			total.String(), eff.MaxCapitalPerTriangle.String())
	}
	if eff.MaxCapitalUtilization.IsPositive() {
		capital := ctx.CapitalAvailable.Add(ctx.CapitalReserved)
		if capital.IsPositive() {
			util := ctx.CapitalReserved.Add(input).Div(capital)
			add(ReasonUtilization, util.LessThanOrEqual(eff.MaxCapitalUtilization),
				util.StringFixed(4), eff.MaxCapitalUtilization.String())
		} else {
			add(ReasonUtilization, false, "no capital", eff.MaxCapitalUtilization.String())
		}
	}
	if eff.MaxConcurrentSimulations > 0 {
		add(ReasonConcurrency, ctx.ConcurrentSimulations < eff.MaxConcurrentSimulations,
			fmt.Sprintf("%d", ctx.ConcurrentSimulations), fmt.Sprintf("<%d", eff.MaxConcurrentSimulations))
	}
	if eff.MaxPriceImpactBps.IsPositive() {
		worst := decimal.Zero
		for _, l := range op.Quote.Legs {
			if l.PriceImpactBps.GreaterThan(worst) {
				worst = l.PriceImpactBps
			}
		}
		add(ReasonPriceImpact, worst.LessThanOrEqual(eff.MaxPriceImpactBps),
			worst.StringFixed(2)+"bps", eff.MaxPriceImpactBps.String()+"bps")
	}
	if eff.MaxDailyLoss.IsPositive() {
		add(ReasonDailyLoss, ctx.DailyLoss.LessThan(eff.MaxDailyLoss),
			ctx.DailyLoss.String(), eff.MaxDailyLoss.String())
	}
	if eff.MaxDrawdown.IsPositive() {
		add(ReasonDrawdown, ctx.Drawdown.LessThan(eff.MaxDrawdown),
			ctx.Drawdown.StringFixed(4), eff.MaxDrawdown.String())
	}

	add(ReasonExpired, !op.Expired(ctx.Now),
		fmt.Sprintf("expires=%s", op.ExpiresAt.Format(time.RFC3339Nano)), "alive")

	d.Allowed = d.ReasonCode == ""
	return d
}

func maxDur(ds [3]time.Duration) time.Duration {
	m := ds[0]
	for _, d := range ds[1:] {
		if d > m {
			m = d
		}
	}
	return m
}

func minDur(ds [3]time.Duration) time.Duration {
	m := ds[0]
	for _, d := range ds[1:] {
		if d < m {
			m = d
		}
	}
	return m
}
