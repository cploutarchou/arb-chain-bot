// Package execution defines the execution boundary (SKILL.md §3): the
// Executor interface, cycle plans/results, and the permanently disabled
// LiveExecutor. Simulated executors live in internal/simulation; nothing
// in this platform places real orders.
package execution

import (
	"context"
	"errors"
	"time"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/exchange"
	"github.com/cploutarchou/arb-chain-bot/internal/graph"
	"github.com/cploutarchou/arb-chain-bot/internal/opportunity"
)

// ErrLiveTradingDisabled is the entire live-trading feature.
var ErrLiveTradingDisabled = errors.New("execution: live trading is permanently disabled by design")

// Outcome classifies a simulated cycle (SKILL.md §22). Every adverse
// outcome carries its P&L consequences in the result.
type Outcome string

const (
	OutcomeAllFilled             Outcome = "ALL_FILLED"
	OutcomeLeg1Partial           Outcome = "LEG1_PARTIAL"
	OutcomeLeg1FilledLeg2Failed  Outcome = "LEG1_FILLED_LEG2_FAILED"
	OutcomeLeg12FilledLeg3Failed Outcome = "LEG1_LEG2_FILLED_LEG3_FAILED"
	OutcomePartialCycle          Outcome = "PARTIAL_CYCLE"
	OutcomeTimeout               Outcome = "TIMEOUT"
	OutcomeExpired               Outcome = "EXPIRED"
	OutcomeRejected              Outcome = "REJECTED"
)

// Complete reports whether the cycle returned to its start asset.
func (o Outcome) Complete() bool {
	return o == OutcomeAllFilled || o == OutcomeLeg1Partial || o == OutcomePartialCycle
}

// OrderStatus for simulated leg orders.
type OrderStatus string

const (
	OrderNew             OrderStatus = "NEW"
	OrderAcked           OrderStatus = "ACKED"
	OrderFilled          OrderStatus = "FILLED"
	OrderPartiallyFilled OrderStatus = "PARTIALLY_FILLED"
	OrderRejected        OrderStatus = "REJECTED"
	OrderExpired         OrderStatus = "EXPIRED"
)

// SimFill is one simulated fill against a cited book version.
type SimFill struct {
	ID          string
	Price       decimal.Decimal
	Qty         decimal.Decimal
	FeeAmount   decimal.Decimal
	FeeAsset    exchange.Asset
	BookVersion uint64
	At          time.Time
}

// SimOrder is one simulated leg order with its event timeline.
type SimOrder struct {
	ID     string
	LegNo  int
	Market exchange.MarketID
	Side   exchange.Side
	Type   string // "LIMIT_IOC" | "MARKET"

	QtyRequested decimal.Decimal // plan-reference quantity (leg orders are budget-denominated)
	QtyFilled    decimal.Decimal
	LimitPrice   decimal.Decimal // zero for market
	PlannedAvg   decimal.Decimal // plan-time expected VWAP
	AvgPrice     decimal.Decimal
	SlippageBps  decimal.Decimal // realized vs planned VWAP (positive = worse)
	FeeAmount    decimal.Decimal
	FeeAsset     exchange.Asset

	CreatedAt time.Time
	AckedAt   time.Time
	FilledAt  time.Time
	Status    OrderStatus
	Fills     []SimFill
	Reason    string // failure detail for REJECTED/EXPIRED
}

// CyclePlan is a reserved, revalidated opportunity ready for simulation.
type CyclePlan struct {
	CycleID     string
	SessionID   string
	Opportunity *opportunity.Opportunity
	Triangle    graph.Triangle
}

// CycleResult is the settled simulation of one plan.
type CycleResult struct {
	CycleID string
	// OpportunityID carries the plan's opportunity through to persistence
	// so the cycle→opportunity correlation chain never depends on callers
	// re-attaching it (empty only for results not born from a plan).
	OpportunityID string
	Outcome       Outcome

	StartAsset    exchange.Asset
	InputConsumed decimal.Decimal // start asset deployed on leg 1
	FinalAmount   decimal.Decimal // start asset returned by leg 3 (zero on mid-cycle failure)

	RealizedPnL  decimal.Decimal                    // FinalAmount - InputConsumed
	Exposure     map[exchange.Asset]decimal.Decimal // stranded non-start assets
	ExposureMark decimal.Decimal                    // exposure valued in start asset (0 when unmarkable)
	TotalPnL     decimal.Decimal                    // RealizedPnL + ExposureMark

	Fees map[exchange.Asset]decimal.Decimal

	// PlannedReturnBps is the un-buffered plan's return (Quote.FinalAmount
	// over Quote.InputConsumed); ActualReturnBps is the realized return
	// over the input actually deployed; SlippageBps = planned − actual
	// (positive = worse than plan). All three are zero when the cycle did
	// not return to the start asset; persistence gates on the outcome.
	PlannedReturnBps decimal.Decimal
	ActualReturnBps  decimal.Decimal
	SlippageBps      decimal.Decimal

	Orders    []SimOrder
	StartedAt time.Time
	SettledAt time.Time
	Reason    string
}

// Executor runs one cycle plan to a settled result. Implementations:
// paper/replay/simulation/shadow in internal/simulation — and
// LiveExecutor below, which refuses.
type Executor interface {
	ExecuteCycle(ctx context.Context, plan CyclePlan) (CycleResult, error)
}

// LiveExecutor exists so the boundary is explicit and testable. Every
// invocation fails with ErrLiveTradingDisabled. There is no configuration,
// build tag, or environment variable that changes this; enabling live
// trading is a deliberate human code change outside this project's scope
// (docs/security.md §2).
type LiveExecutor struct{}

func (LiveExecutor) ExecuteCycle(context.Context, CyclePlan) (CycleResult, error) {
	return CycleResult{}, ErrLiveTradingDisabled
}
