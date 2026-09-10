// Package pricing computes exact, depth-aware executable economics for
// triangle legs and full cycles: VWAP walks over L2 levels, fee placement,
// precision truncation, and leg chaining (SKILL.md §15–§17,
// resources/triangular-math.md). All arithmetic is decimal; every number a
// caller sees is reproducible from the cited book versions.
package pricing

import (
	"errors"
	"fmt"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/exchange"
	"github.com/cploutarchou/arb-chain-bot/internal/fees"
	"github.com/cploutarchou/arb-chain-bot/internal/graph"
	"github.com/cploutarchou/arb-chain-bot/internal/orderbook"
)

var (
	ErrNoDepth       = errors.New("pricing: no depth on required book side")
	ErrZeroInput     = errors.New("pricing: non-positive input")
	ErrDustInput     = errors.New("pricing: input quantizes to zero")
	ErrRuleViolation = errors.New("pricing: instrument rule violation")
)

var (
	one  = decimal.NewFromInt(1)
	tenK = decimal.NewFromInt(10_000)
)

// LegQuote is the exact executable economics of one leg.
type LegQuote struct {
	Market exchange.MarketID
	Side   exchange.Side
	From   exchange.Asset
	To     exchange.Asset

	InputConsumed decimal.Decimal // From-asset actually consumed (walk cost + input-side fee)
	OrderQty      decimal.Decimal // quantized base order quantity
	AvgPrice      decimal.Decimal // VWAP over consumed levels
	GrossOut      decimal.Decimal // To-asset before output-side fee
	NetOut        decimal.Decimal // To-asset after fee — the next leg's input
	Dust          decimal.Decimal // From-asset stranded by quantization/depth (legs 2-3: lost to the cycle)

	FeeAmount decimal.Decimal
	FeeAsset  exchange.Asset
	FeeRate   decimal.Decimal
	FeeSource string

	LevelsConsumed int
	PriceImpactBps decimal.Decimal // VWAP vs best level, in bps (0 when single level)
	BookVersion    uint64
	DepthExhausted bool // the walk consumed the entire visible side
}

// CycleQuote is the exact executable economics of a full triangle at one
// input size.
type CycleQuote struct {
	Triangle string
	Start    exchange.Asset

	InputConsumed decimal.Decimal // leg-1 From actually deployed
	FinalAmount   decimal.Decimal // Start-asset received from leg 3
	GrossProfit   decimal.Decimal // FinalAmount - InputConsumed (fees already inside)
	ReturnBps     decimal.Decimal

	Legs             [3]LegQuote
	LiquidityLimited bool
}

// MarketData is the per-market context pricing needs. Views must come from
// the same evaluation pass (the caller pins versions).
type MarketData struct {
	View  orderbook.View
	Rules exchange.InstrumentRules
}

// QuoteLeg prices one leg for a given input amount of leg.From.
func QuoteLeg(leg graph.Leg, md MarketData, sched *fees.Schedule, input decimal.Decimal) (LegQuote, error) {
	if !input.IsPositive() {
		return LegQuote{}, ErrZeroInput
	}
	eff := sched.Taker(leg.Market)
	placement, err := fees.PlacementFor(sched.Convention, leg.Side)
	if err != nil {
		return LegQuote{}, err
	}
	usable, _ := fees.UsableInput(placement, input, eff.Rate)

	q := LegQuote{
		Market: leg.Market, Side: leg.Side, From: leg.From, To: leg.To,
		FeeRate: eff.Rate, FeeSource: eff.Source, BookVersion: md.View.Version,
	}

	switch leg.Side {
	case exchange.SideBuy: // spend quote budget on asks
		if len(md.View.Asks) == 0 {
			return LegQuote{}, fmt.Errorf("%w: %s asks", ErrNoDepth, leg.Market)
		}
		rawQty, _, exhausted := walkBuyBudget(md.View.Asks, usable)
		orderQty, qerr := md.Rules.QuantizeQty(rawQty)
		if qerr != nil {
			return LegQuote{}, qerr
		}
		if !orderQty.IsPositive() {
			return LegQuote{}, fmt.Errorf("%w: buy on %s", ErrDustInput, leg.Market)
		}
		cost, levels, ok := walkBuyQty(md.View.Asks, orderQty)
		if !ok {
			return LegQuote{}, fmt.Errorf("%w: %s asks for qty %s", ErrNoDepth, leg.Market, orderQty)
		}
		vwap := cost.Div(orderQty)
		if err := md.Rules.ValidateOrder(vwap, orderQty); err != nil {
			return LegQuote{}, fmt.Errorf("%w: %s: %s", ErrRuleViolation, leg.Market, err)
		}
		q.OrderQty = orderQty
		q.AvgPrice = vwap
		q.LevelsConsumed = levels
		q.DepthExhausted = exhausted
		q.InputConsumed = cost
		q.Dust = usable.Sub(cost)
		if placement == fees.FeeOnInput {
			// The input fee is proportional to what was deployed, not to the
			// original budget: consumed input = cost * (1 + rate).
			propFee := cost.Mul(eff.Rate)
			q.InputConsumed = cost.Add(propFee)
			q.Dust = input.Sub(q.InputConsumed)
			q.FeeAmount, q.FeeAsset = propFee, leg.From
		}
		q.GrossOut = orderQty
		net, outFee := fees.NetOutput(placement, q.GrossOut, eff.Rate)
		q.NetOut = net
		if placement == fees.FeeOnOutput {
			q.FeeAmount, q.FeeAsset = outFee, leg.To
		}
		q.PriceImpactBps = impactBps(md.View.Asks[0].Price, vwap, true)

	case exchange.SideSell: // sell base into bids
		if len(md.View.Bids) == 0 {
			return LegQuote{}, fmt.Errorf("%w: %s bids", ErrNoDepth, leg.Market)
		}
		orderQty, qerr := md.Rules.QuantizeQty(usable)
		if qerr != nil {
			return LegQuote{}, qerr
		}
		if !orderQty.IsPositive() {
			return LegQuote{}, fmt.Errorf("%w: sell on %s", ErrDustInput, leg.Market)
		}
		proceeds, soldQty, levels, exhausted := walkSellQty(md.View.Bids, orderQty)
		if !soldQty.IsPositive() {
			return LegQuote{}, fmt.Errorf("%w: %s bids", ErrNoDepth, leg.Market)
		}
		vwap := proceeds.Div(soldQty)
		// The venue validates the order as submitted, not as filled: a
		// depth-limited fill is a partial (DepthExhausted, dust stranded),
		// never a rule violation (audit T6).
		if err := md.Rules.ValidateOrder(vwap, orderQty); err != nil {
			return LegQuote{}, fmt.Errorf("%w: %s: %s", ErrRuleViolation, leg.Market, err)
		}
		q.OrderQty = soldQty
		q.AvgPrice = vwap
		q.LevelsConsumed = levels
		q.DepthExhausted = exhausted
		q.InputConsumed = soldQty
		q.Dust = usable.Sub(soldQty)
		if placement == fees.FeeOnInput {
			propFee := soldQty.Mul(eff.Rate)
			q.InputConsumed = soldQty.Add(propFee)
			q.Dust = input.Sub(q.InputConsumed)
			q.FeeAmount, q.FeeAsset = propFee, leg.From
		}
		q.GrossOut = proceeds
		net, outFee := fees.NetOutput(placement, q.GrossOut, eff.Rate)
		q.NetOut = net
		if placement == fees.FeeOnOutput {
			q.FeeAmount, q.FeeAsset = outFee, leg.To
		}
		q.PriceImpactBps = impactBps(md.View.Bids[0].Price, vwap, false)

	default:
		return LegQuote{}, fmt.Errorf("pricing: unknown side %v", leg.Side)
	}
	return q, nil
}

// QuoteCycle chains three legs: leg N's NetOut is leg N+1's input. Dust
// stranded on legs 2–3 is lost to the cycle (it sits in a non-start
// asset); leg-1 dust never left the start asset and is simply not
// deployed.
func QuoteCycle(tri graph.Triangle, data [3]MarketData, sched *fees.Schedule, input decimal.Decimal) (CycleQuote, error) {
	cq := CycleQuote{Triangle: tri.ID, Start: tri.Start}
	in := input
	for i, leg := range tri.Legs {
		lq, err := QuoteLeg(leg, data[i], sched, in)
		if err != nil {
			return CycleQuote{}, fmt.Errorf("leg %d: %w", i+1, err)
		}
		cq.Legs[i] = lq
		cq.LiquidityLimited = cq.LiquidityLimited || lq.DepthExhausted
		in = lq.NetOut
	}
	cq.InputConsumed = cq.Legs[0].InputConsumed
	cq.FinalAmount = cq.Legs[2].NetOut
	cq.GrossProfit = cq.FinalAmount.Sub(cq.InputConsumed)
	if cq.InputConsumed.IsPositive() {
		cq.ReturnBps = cq.FinalAmount.Div(cq.InputConsumed).Sub(one).Mul(tenK)
	}
	return cq, nil
}

// walkBuyBudget spends up to budget across asks, returning the raw base
// quantity obtainable and whether the whole side was consumed.
func walkBuyBudget(asks []orderbook.Level, budget decimal.Decimal) (qty, cost decimal.Decimal, exhausted bool) {
	remaining := budget
	for _, lv := range asks {
		levelCost := lv.Price.Mul(lv.Qty)
		if remaining.GreaterThanOrEqual(levelCost) {
			qty = qty.Add(lv.Qty)
			cost = cost.Add(levelCost)
			remaining = remaining.Sub(levelCost)
			continue
		}
		partial := remaining.Div(lv.Price)
		qty = qty.Add(partial)
		cost = cost.Add(remaining)
		return qty, cost, false
	}
	return qty, cost, true
}

// walkBuyQty prices buying exactly qty across asks; ok=false when depth is
// insufficient for the full quantity.
func walkBuyQty(asks []orderbook.Level, qty decimal.Decimal) (cost decimal.Decimal, levels int, ok bool) {
	remaining := qty
	for _, lv := range asks {
		levels++
		if remaining.GreaterThan(lv.Qty) {
			cost = cost.Add(lv.Price.Mul(lv.Qty))
			remaining = remaining.Sub(lv.Qty)
			continue
		}
		cost = cost.Add(lv.Price.Mul(remaining))
		return cost, levels, true
	}
	return cost, levels, false
}

// walkSellQty sells up to qty into bids, returning proceeds and the
// quantity actually absorbed (may be less when depth runs out).
func walkSellQty(bids []orderbook.Level, qty decimal.Decimal) (proceeds, sold decimal.Decimal, levels int, exhausted bool) {
	remaining := qty
	for _, lv := range bids {
		levels++
		if remaining.GreaterThan(lv.Qty) {
			proceeds = proceeds.Add(lv.Price.Mul(lv.Qty))
			sold = sold.Add(lv.Qty)
			remaining = remaining.Sub(lv.Qty)
			continue
		}
		proceeds = proceeds.Add(lv.Price.Mul(remaining))
		sold = sold.Add(remaining)
		return proceeds, sold, levels, false
	}
	return proceeds, sold, levels, true
}

// impactBps measures VWAP deterioration vs the best level. Buys worsen
// upward, sells downward; both yield non-negative bps.
func impactBps(best, vwap decimal.Decimal, buy bool) decimal.Decimal {
	if !best.IsPositive() {
		return decimal.Zero
	}
	var rel decimal.Decimal
	if buy {
		rel = vwap.Div(best).Sub(one)
	} else {
		rel = one.Sub(vwap.Div(best))
	}
	if rel.IsNegative() {
		rel = decimal.Zero
	}
	return rel.Mul(tenK)
}
