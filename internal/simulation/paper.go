// Package simulation implements the simulated executors: realistic
// three-leg paper execution with seeded latency, fill-time book reads,
// limit-IOC semantics, partial fills, and explicit intermediate exposure
// (SKILL.md §22–§23, resources/execution-simulation.md).
//
// One engine, four configurations (SKILL.md §3):
//   - NewPaper: wall clock + real waits against live books
//   - NewReplay: virtual clock over recorded books (deterministic)
//   - NewSimulation: virtual clock over synthetic books (tests, studies)
//   - NewShadow: paper configuration whose results are marked shadow-only
//     (observed, never applied to the portfolio)
package simulation

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"math/rand"
	"sync"
	"time"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/exchange"
	"github.com/cploutarchou/arb-chain-bot/internal/execution"
	"github.com/cploutarchou/arb-chain-bot/internal/fees"
	"github.com/cploutarchou/arb-chain-bot/internal/graph"
	"github.com/cploutarchou/arb-chain-bot/internal/orderbook"
	"github.com/cploutarchou/arb-chain-bot/internal/pricing"
)

// BookSource provides fill-time book views. Live paper uses the orderbook
// registry; replay uses recorded books.
type BookSource interface {
	View(id exchange.MarketID, depth int) (orderbook.View, bool)
}

// RulesSource provides instrument rules per market.
type RulesSource interface {
	Rules(id exchange.MarketID) (exchange.InstrumentRules, bool)
}

// Clock abstracts time; replay drives a virtual clock (SKILL.md §64).
type Clock interface {
	Now() time.Time
}

// Waiter models elapsing latency. RealWaiter sleeps (bounded by ctx);
// VirtualWaiter advances a virtual clock instantly.
type Waiter interface {
	Wait(ctx context.Context, d time.Duration) error
}

// Marker values a stranded asset in the start asset (portfolio provides
// book-based marks). ok=false when unmarkable — the exposure then carries
// zero mark and stays visible as raw quantity.
type Marker interface {
	Mark(asset exchange.Asset, amount decimal.Decimal, in exchange.Asset) (decimal.Decimal, bool)
}

// LatencyModel is a seeded jitter model per simulated step.
type LatencyModel struct {
	SubmitBase, SubmitJitter time.Duration
	FillBase, FillJitter     time.Duration
}

func (l LatencyModel) submit(rng *rand.Rand) time.Duration {
	return jitter(rng, l.SubmitBase, l.SubmitJitter)
}
func (l LatencyModel) fill(rng *rand.Rand) time.Duration {
	return jitter(rng, l.FillBase, l.FillJitter)
}

func jitter(rng *rand.Rand, base, j time.Duration) time.Duration {
	if j <= 0 {
		return base
	}
	return base + time.Duration(rng.Int63n(int64(j)))
}

// Config tunes the engine.
type Config struct {
	Latency           LatencyModel
	LimitToleranceBps decimal.Decimal // limit price = planned VWAP ± tolerance
	MarketOrders      bool            // true: ignore limit, eat available depth
	Depth             int             // book view depth (0 = full)
	Seed              int64           // determinism root; per-cycle RNG derives from it
	// MaxBookAge, when positive, fails a leg whose fill-time book is older
	// than this (a HEALTHY state is always required). Live paper sets it
	// to the scanner's age budget; replay and backtests leave it zero and
	// rely on the state alone.
	MaxBookAge time.Duration
}

// Engine is the simulated executor core.
type Engine struct {
	books  BookSource
	rules  RulesSource
	sched  *fees.Schedule
	clock  Clock
	wait   Waiter
	marker Marker
	cfg    Config
	shadow bool
	idGen  func() string

	// progress, when set, receives one event per leg-stage transition
	// (audit F6: the console's live-cycle monitor). Fire-and-forget by
	// contract: the paper engine's registry update is a short mutex'd
	// write, and a slow callback would tax the very latency model this
	// engine exists to model. Never consulted on any execution decision.
	progress   func(execution.CycleProgress)
	progressMu sync.Mutex
}

// SetProgressHook installs the leg-stage observer. The setter exists so
// the paper engine (constructed after the executor it owns) can wire its
// registry without the constructor needing a forward reference.
func (e *Engine) SetProgressHook(fn func(execution.CycleProgress)) {
	e.progressMu.Lock()
	e.progress = fn
	e.progressMu.Unlock()
}

// report is the nil-safe progress emit; a missing hook costs one lock
// read, an installed one must not block execution.
func (e *Engine) report(cycleID, opportunityID string, legNo int, stage execution.LegStage, at time.Time) {
	e.progressMu.Lock()
	fn := e.progress
	e.progressMu.Unlock()
	if fn != nil {
		fn(execution.CycleProgress{CycleID: cycleID, OpportunityID: opportunityID, LegNo: legNo, Stage: stage, At: at})
	}
}

// Compile-time boundary checks.
var (
	_ execution.Executor = (*Engine)(nil)
	_ execution.Executor = execution.LiveExecutor{}
)

func newEngine(books BookSource, rules RulesSource, sched *fees.Schedule, clock Clock, wait Waiter, marker Marker, cfg Config, idGen func() string, shadow bool) *Engine {
	if cfg.Depth == 0 {
		cfg.Depth = 200
	}
	return &Engine{books: books, rules: rules, sched: sched, clock: clock, wait: wait, marker: marker, cfg: cfg, idGen: idGen, shadow: shadow}
}

// NewPaper builds the live-books paper executor.
func NewPaper(books BookSource, rules RulesSource, sched *fees.Schedule, clock Clock, wait Waiter, marker Marker, cfg Config, idGen func() string) *Engine {
	return newEngine(books, rules, sched, clock, wait, marker, cfg, idGen, false)
}

// NewReplay builds the deterministic replay executor (virtual clock/waiter
// + recorded books supplied by the caller).
func NewReplay(books BookSource, rules RulesSource, sched *fees.Schedule, clock Clock, wait Waiter, marker Marker, cfg Config, idGen func() string) *Engine {
	return newEngine(books, rules, sched, clock, wait, marker, cfg, idGen, false)
}

// NewSimulation builds the synthetic-books executor.
func NewSimulation(books BookSource, rules RulesSource, sched *fees.Schedule, clock Clock, wait Waiter, marker Marker, cfg Config, idGen func() string) *Engine {
	return newEngine(books, rules, sched, clock, wait, marker, cfg, idGen, false)
}

// NewShadow builds a paper executor whose results are shadow-only.
func NewShadow(books BookSource, rules RulesSource, sched *fees.Schedule, clock Clock, wait Waiter, marker Marker, cfg Config, idGen func() string) *Engine {
	return newEngine(books, rules, sched, clock, wait, marker, cfg, idGen, true)
}

// Shadow reports whether results must not touch the portfolio.
func (e *Engine) Shadow() bool { return e.shadow }

// cycleRNG derives a schedule-independent RNG per (seed, cycle, leg):
// concurrency cannot change latency draws, so replays reproduce exactly.
func cycleRNG(seed int64, cycleID string, leg int) *rand.Rand {
	h := fnv.New64a()
	_, _ = h.Write([]byte(cycleID))
	_, _ = h.Write([]byte{byte(leg)})                        //nolint:gosec // leg is always 0..2
	return rand.New(rand.NewSource(seed ^ int64(h.Sum64()))) //nolint:gosec // simulation jitter, not crypto
}

// ExecuteCycle runs the plan's three legs sequentially against fill-time
// books. It returns an error only for programmer/config faults; market
// outcomes (failures, partials, timeouts) are results, not errors.
func (e *Engine) ExecuteCycle(ctx context.Context, plan execution.CyclePlan) (execution.CycleResult, error) {
	op := plan.Opportunity
	res := execution.CycleResult{
		CycleID:       plan.CycleID,
		OpportunityID: op.ID,
		StartAsset:    op.Start,
		Exposure:      make(map[exchange.Asset]decimal.Decimal),
		Fees:          make(map[exchange.Asset]decimal.Decimal),
		StartedAt:     e.clock.Now(),
	}

	if op.Expired(res.StartedAt) {
		res.Outcome = execution.OutcomeExpired
		res.Reason = "expired before simulation start"
		res.SettledAt = res.StartedAt
		return res, nil
	}

	cur := op.Quote.InputConsumed // deployed start-asset budget
	for i, leg := range plan.Triangle.Legs {
		rng := cycleRNG(e.cfg.Seed, plan.CycleID, i)
		planned := op.Quote.Legs[i]

		order := execution.SimOrder{
			ID:           e.idGen(),
			LegNo:        i + 1,
			Market:       leg.Market,
			Side:         leg.Side,
			Type:         "LIMIT_IOC",
			QtyRequested: planned.OrderQty,
			PlannedAvg:   planned.AvgPrice,
			CreatedAt:    e.clock.Now(),
			Status:       execution.OrderNew,
		}
		if e.cfg.MarketOrders {
			order.Type = "MARKET"
		}

		e.report(plan.CycleID, op.ID, i+1, execution.LegStageSubmitted, e.clock.Now())

		if err := e.wait.Wait(ctx, e.cfg.Latency.submit(rng)); err != nil {
			return e.interrupted(res, order, cur, leg, i, "submit wait", err), nil
		}
		order.AckedAt = e.clock.Now()
		order.Status = execution.OrderAcked

		lq, ferr := e.fillLeg(leg, planned, cur, &order)
		if err := e.wait.Wait(ctx, e.cfg.Latency.fill(rng)); err != nil {
			return e.interrupted(res, order, cur, leg, i, "fill wait", err), nil
		}
		now := e.clock.Now()

		if ferr != nil {
			e.report(plan.CycleID, op.ID, i+1, execution.LegStageFailed, now)
			order.Status = execution.OrderRejected
			order.Reason = ferr.Error()
			res.Orders = append(res.Orders, order)
			return e.legFailure(res, cur, leg, i, ferr.Error()), nil
		}

		order.FilledAt = now
		order.QtyFilled = lq.OrderQty
		order.AvgPrice = lq.AvgPrice
		order.FeeAmount = lq.FeeAmount
		order.FeeAsset = lq.FeeAsset
		order.SlippageBps = slippageBps(planned.AvgPrice, lq.AvgPrice, leg.Side)
		order.Status = execution.OrderFilled
		// Partial = the depth within the limit ran out while budget/holdings
		// remained. Comparing against plan-time quantities would misclassify
		// every chain that runs slightly smaller than planned (adverse price
		// moves shrink legs 2-3 proportionally without any depth shortfall).
		if lq.DepthExhausted && lq.Dust.IsPositive() {
			order.Status = execution.OrderPartiallyFilled
		}
		order.Fills = append(order.Fills, execution.SimFill{
			ID: e.idGen(), Price: lq.AvgPrice, Qty: lq.OrderQty,
			FeeAmount: lq.FeeAmount, FeeAsset: lq.FeeAsset,
			BookVersion: lq.BookVersion, At: now,
		})
		res.Orders = append(res.Orders, order)
		e.report(plan.CycleID, op.ID, i+1, execution.LegStageFilled, now)

		if lq.FeeAmount.IsPositive() {
			res.Fees[lq.FeeAsset] = res.Fees[lq.FeeAsset].Add(lq.FeeAmount)
		}
		if i == 0 {
			res.InputConsumed = lq.InputConsumed
		} else if lq.Dust.IsPositive() {
			// Stranded intermediate-asset dust is exposure, never hidden.
			res.Exposure[leg.From] = res.Exposure[leg.From].Add(lq.Dust)
		}
		cur = lq.NetOut
	}

	res.FinalAmount = cur
	res.Outcome = cycleOutcome(res.Orders)
	e.settle(&res)
	res.PlannedReturnBps, res.ActualReturnBps, res.SlippageBps = slippageVsPlan(op.Quote, res.InputConsumed, res.FinalAmount)
	return res, nil
}

// slippageVsPlan measures how far the cycle's realized return fell short of
// the plan's return, in bps: planned − actual, positive = worse.
//
// Two things are deliberately NOT in this number. The plan's return is the
// un-buffered quote (Quote.FinalAmount / Quote.InputConsumed): buffers are a
// risk allowance, not a prediction, and folding them in would report a
// cycle that filled exactly as planned as "−10 bps slippage". And both
// returns are ratios of their own deployed input: a partial leg-1 fill
// shrinks the cycle proportionally, and comparing the smaller final amount
// with the full-size estimate would report thousands of bps of "slippage"
// at byte-identical prices. Non-positive inputs (mid-cycle failures with
// nothing returned, rejected plans) yield zeros; persistence and reports
// additionally gate on the outcome.
func slippageVsPlan(plan pricing.CycleQuote, inputConsumed, finalAmount decimal.Decimal) (plannedBps, actualBps, slippage decimal.Decimal) {
	if !plan.InputConsumed.IsPositive() || !inputConsumed.IsPositive() || !finalAmount.IsPositive() {
		return decimal.Zero, decimal.Zero, decimal.Zero
	}
	tenK := decimal.NewFromInt(10_000)
	plannedBps = plan.FinalAmount.Div(plan.InputConsumed).Sub(decimal.NewFromInt(1)).Mul(tenK)
	actualBps = finalAmount.Div(inputConsumed).Sub(decimal.NewFromInt(1)).Mul(tenK)
	return plannedBps, actualBps, plannedBps.Sub(actualBps)
}

// fillLeg reads the fill-time book, applies limit-IOC filtering, and
// prices the achievable fill with the exact pricing engine.
func (e *Engine) fillLeg(leg graph.Leg, planned pricing.LegQuote, input decimal.Decimal, order *execution.SimOrder) (pricing.LegQuote, error) {
	view, ok := e.books.View(leg.Market, e.cfg.Depth)
	if !ok {
		return pricing.LegQuote{}, fmt.Errorf("no book for %s", leg.Market)
	}
	// Fill-time health: the plan was priced on a HEALTHY book, but the
	// fill happens tens of milliseconds later. A book that has since gone
	// STALE, CORRUPTED, DISCONNECTED or back to SYNCING carries no
	// knowable price, and filling against its last levels would invent an
	// execution. The leg fails instead — REJECTED before anything is
	// deployed, stranded exposure afterwards — which is the honest
	// outcome for a system that cannot see the market.
	if view.State != orderbook.StateHealthy {
		return pricing.LegQuote{}, fmt.Errorf("book %s is %s at fill time", leg.Market, view.State)
	}
	if e.cfg.MaxBookAge > 0 {
		if age := view.Age(e.clock.Now()); age > e.cfg.MaxBookAge {
			return pricing.LegQuote{}, fmt.Errorf("book %s is %s old at fill time (max %s)", leg.Market, age, e.cfg.MaxBookAge)
		}
	}
	rules, ok := e.rules.Rules(leg.Market)
	if !ok {
		return pricing.LegQuote{}, fmt.Errorf("no instrument rules for %s", leg.Market)
	}
	if e.cfg.MarketOrders {
		// MARKET orders are validated against the venue's market-order
		// quantity filter, which is volume-derived and usually far
		// tighter than the limit-order one (audit T4).
		rules = rules.ForMarketOrders()
	}
	if !e.cfg.MarketOrders {
		limit := limitPrice(planned.AvgPrice, e.cfg.LimitToleranceBps, leg.Side)
		// The venue accepts prices on its tick only; rounding toward the
		// planned price (down for a buy, up for a sell) keeps the limit
		// inside the tolerance rather than a fraction of a tick beyond it.
		if q, err := quantizeLimit(rules, limit, leg.Side); err == nil {
			limit = q
		}
		order.LimitPrice = limit
		view = filterByLimit(view, leg.Side, limit)
	}
	return pricing.QuoteLeg(leg, pricing.MarketData{View: view, Rules: rules}, e.sched, input)
}

func (e *Engine) legFailure(res execution.CycleResult, held decimal.Decimal, leg graph.Leg, legIdx int, reason string) execution.CycleResult {
	res.Reason = reason
	switch legIdx {
	case 0:
		res.Outcome = execution.OutcomeRejected // nothing deployed, nothing lost
	case 1:
		res.Outcome = execution.OutcomeLeg1FilledLeg2Failed
		res.Exposure[leg.From] = res.Exposure[leg.From].Add(held)
	default:
		res.Outcome = execution.OutcomeLeg12FilledLeg3Failed
		res.Exposure[leg.From] = res.Exposure[leg.From].Add(held)
	}
	e.settle(&res)
	return res
}

// interrupted settles a cycle whose latency wait ended with an error.
// A deadline is a TIMEOUT; a cancellation is the engine stopping
// (shutdown, restart) and settles as ABORTED, so the ledger and the
// cycle history never read a controlled stop as an exchange timing
// failure. Either way whatever leg 1 deployed is exposure.
func (e *Engine) interrupted(res execution.CycleResult, order execution.SimOrder, held decimal.Decimal, leg graph.Leg, legIdx int, stage string, cause error) execution.CycleResult {
	reason := stage + ": " + cause.Error()
	e.report(res.CycleID, res.OpportunityID, legIdx+1, execution.LegStageFailed, e.clock.Now())
	order.Status = execution.OrderExpired
	order.Reason = reason
	res.Orders = append(res.Orders, order)
	res.Outcome = execution.OutcomeTimeout
	if errors.Is(cause, context.Canceled) {
		res.Outcome = execution.OutcomeAborted
	}
	res.Reason = reason
	if legIdx > 0 {
		res.Exposure[leg.From] = res.Exposure[leg.From].Add(held)
	}
	e.settle(&res)
	return res
}

// settle finalizes P&L: realized plus marked exposure. Unmarkable
// exposure keeps zero mark but remains visible as quantity.
func (e *Engine) settle(res *execution.CycleResult) {
	res.SettledAt = e.clock.Now()
	res.RealizedPnL = res.FinalAmount.Sub(res.InputConsumed)
	res.ExposureMark = decimal.Zero
	for asset, amt := range res.Exposure {
		if amt.IsZero() {
			delete(res.Exposure, asset)
			continue
		}
		if e.marker != nil {
			if v, ok := e.marker.Mark(asset, amt, res.StartAsset); ok {
				res.ExposureMark = res.ExposureMark.Add(v)
			}
		}
	}
	res.TotalPnL = res.RealizedPnL.Add(res.ExposureMark)
}

func cycleOutcome(orders []execution.SimOrder) execution.Outcome {
	partial := [3]bool{}
	for _, o := range orders {
		if o.Status == execution.OrderPartiallyFilled {
			partial[o.LegNo-1] = true
		}
	}
	switch {
	case !partial[0] && !partial[1] && !partial[2]:
		return execution.OutcomeAllFilled
	case partial[0] && !partial[1] && !partial[2]:
		return execution.OutcomeLeg1Partial
	default:
		return execution.OutcomePartialCycle
	}
}

func limitPrice(plannedVWAP, tolBps decimal.Decimal, side exchange.Side) decimal.Decimal {
	if !tolBps.IsPositive() {
		return plannedVWAP
	}
	frac := tolBps.Div(decimal.NewFromInt(10_000))
	if side == exchange.SideBuy {
		return plannedVWAP.Mul(decimal.NewFromInt(1).Add(frac))
	}
	return plannedVWAP.Mul(decimal.NewFromInt(1).Sub(frac))
}

// quantizeLimit snaps a limit price to the instrument's tick on the
// conservative side of the tolerance (audit T5).
func quantizeLimit(rules exchange.InstrumentRules, limit decimal.Decimal, side exchange.Side) (decimal.Decimal, error) {
	if side == exchange.SideBuy {
		return rules.QuantizePriceDown(limit)
	}
	return rules.QuantizePriceUp(limit)
}

// filterByLimit trims the consumable side to levels within the limit.
func filterByLimit(v orderbook.View, side exchange.Side, limit decimal.Decimal) orderbook.View {
	out := v
	if side == exchange.SideBuy {
		asks := v.Asks
		cut := len(asks)
		for i, l := range asks {
			if l.Price.GreaterThan(limit) {
				cut = i
				break
			}
		}
		out.Asks = asks[:cut]
	} else {
		bids := v.Bids
		cut := len(bids)
		for i, l := range bids {
			if l.Price.LessThan(limit) {
				cut = i
				break
			}
		}
		out.Bids = bids[:cut]
	}
	return out
}

func slippageBps(planned, actual decimal.Decimal, side exchange.Side) decimal.Decimal {
	if !planned.IsPositive() {
		return decimal.Zero
	}
	var rel decimal.Decimal
	if side == exchange.SideBuy {
		rel = actual.Div(planned).Sub(decimal.NewFromInt(1))
	} else {
		rel = decimal.NewFromInt(1).Sub(actual.Div(planned))
	}
	return rel.Mul(decimal.NewFromInt(10_000))
}
