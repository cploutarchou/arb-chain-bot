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
	"fmt"
	"hash/fnv"
	"math/rand"
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
	_, _ = h.Write([]byte{byte(leg)})
	return rand.New(rand.NewSource(seed ^ int64(h.Sum64()))) //nolint:gosec // simulation jitter, not crypto
}

// ExecuteCycle runs the plan's three legs sequentially against fill-time
// books. It returns an error only for programmer/config faults; market
// outcomes (failures, partials, timeouts) are results, not errors.
func (e *Engine) ExecuteCycle(ctx context.Context, plan execution.CyclePlan) (execution.CycleResult, error) {
	op := plan.Opportunity
	res := execution.CycleResult{
		CycleID:    plan.CycleID,
		StartAsset: op.Start,
		Exposure:   make(map[exchange.Asset]decimal.Decimal),
		Fees:       make(map[exchange.Asset]decimal.Decimal),
		StartedAt:  e.clock.Now(),
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

		if err := e.wait.Wait(ctx, e.cfg.Latency.submit(rng)); err != nil {
			return e.timeout(res, order, cur, leg, i, "submit wait: "+err.Error()), nil
		}
		order.AckedAt = e.clock.Now()
		order.Status = execution.OrderAcked

		lq, ferr := e.fillLeg(leg, planned, cur, &order)
		if err := e.wait.Wait(ctx, e.cfg.Latency.fill(rng)); err != nil {
			return e.timeout(res, order, cur, leg, i, "fill wait: "+err.Error()), nil
		}
		now := e.clock.Now()

		if ferr != nil {
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
	if op.EstimatedFinal.IsPositive() && res.InputConsumed.IsPositive() {
		res.SlippageBps = op.EstimatedFinal.Sub(res.FinalAmount).
			Div(res.InputConsumed).Mul(decimal.NewFromInt(10_000))
	}
	return res, nil
}

// fillLeg reads the fill-time book, applies limit-IOC filtering, and
// prices the achievable fill with the exact pricing engine.
func (e *Engine) fillLeg(leg graph.Leg, planned pricing.LegQuote, input decimal.Decimal, order *execution.SimOrder) (pricing.LegQuote, error) {
	view, ok := e.books.View(leg.Market, e.cfg.Depth)
	if !ok {
		return pricing.LegQuote{}, fmt.Errorf("no book for %s", leg.Market)
	}
	rules, ok := e.rules.Rules(leg.Market)
	if !ok {
		return pricing.LegQuote{}, fmt.Errorf("no instrument rules for %s", leg.Market)
	}
	if !e.cfg.MarketOrders {
		limit := limitPrice(planned.AvgPrice, e.cfg.LimitToleranceBps, leg.Side)
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

func (e *Engine) timeout(res execution.CycleResult, order execution.SimOrder, held decimal.Decimal, leg graph.Leg, legIdx int, reason string) execution.CycleResult {
	order.Status = execution.OrderExpired
	order.Reason = reason
	res.Orders = append(res.Orders, order)
	res.Outcome = execution.OutcomeTimeout
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
