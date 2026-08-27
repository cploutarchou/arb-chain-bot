package paperexec

import (
	"context"
	"hash/fnv"
	"math/rand"
	"time"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/screener"
	"github.com/cploutarchou/arb-chain-bot/internal/simulation"
)

var (
	decOne  = decimal.NewFromInt(1)
	decTwo  = decimal.NewFromInt(2)
	decTenK = decimal.NewFromInt(10_000)
)

// DefaultLatency is the simulation package's paper latency model
// (strategy-models §1.3: SubmitBase 20 ms + U(0,30 ms), FillBase 30 ms
// + U(0,50 ms)).
var DefaultLatency = simulation.LatencyModel{
	SubmitBase: 20 * time.Millisecond, SubmitJitter: 30 * time.Millisecond,
	FillBase: 30 * time.Millisecond, FillJitter: 50 * time.Millisecond,
}

// DefaultLimitToleranceBps is the limit-IOC tolerance (§1.3): a re-read
// price worse than the decision price by more than this REJECTS the leg.
var DefaultLimitToleranceBps = decimal.NewFromInt(20)

// legRNG mirrors simulation.cycleRNG: seed ^ fnv64(key, leg) so latency
// draws are reproducible per (seed, execution, leg) and independent of
// scheduling. simulation's helper is unexported, hence the copy.
func legRNG(seed int64, key string, leg int) *rand.Rand {
	h := fnv.New64a()
	_, _ = h.Write([]byte(key))
	_, _ = h.Write([]byte{byte(leg)})                        //nolint:gosec // leg is 1..3 by construction
	return rand.New(rand.NewSource(seed ^ int64(h.Sum64()))) //nolint:gosec // simulation jitter, not crypto
}

func jitter(rng *rand.Rand, base, j time.Duration) time.Duration {
	if j <= 0 {
		return base
	}
	return base + time.Duration(rng.Int63n(int64(j)))
}

// legReq describes one leg to simulate.
type legReq struct {
	Leg    int
	Venue  screener.Venue
	Market string // spot|perp
	Side   string // BUY|SELL
	Base   string
	Quote  string
	Qty    decimal.Decimal
	// Price is the decision-time top-of-book price (ask for BUY, bid
	// for SELL); Reread returns the same side's current top of book.
	Price  decimal.Decimal
	Reread func() (decimal.Decimal, bool)
	FeeBps decimal.Decimal
}

// simulateLeg applies §1.3: submit latency, limit-IOC re-read with
// tolerance, fill latency, fill at reread × (1 ± slip), taker fee in
// quote. It never returns an error for market outcomes — a REJECTED
// fill is a result.
func (x *Executor) simulateLeg(ctx context.Context, key string, r legReq, slipBps decimal.Decimal) Fill {
	f := Fill{Leg: r.Leg, Venue: r.Venue, Market: r.Market, Side: r.Side, Base: r.Base, Quote: r.Quote,
		QuotePrice: r.Price, Qty: r.Qty, Status: "REJECTED"}
	rng := legRNG(x.seed, key, r.Leg)
	submit := jitter(rng, x.latency.SubmitBase, x.latency.SubmitJitter)
	f.SubmitMs = submit.Milliseconds()
	if err := x.waiter.Wait(ctx, submit); err != nil {
		f.Reason = "submit wait: " + err.Error()
		return f
	}
	tol := x.tolBps.Div(decTenK)
	if r.Side == "BUY" {
		f.LimitPrice = r.Price.Mul(decOne.Add(tol))
	} else {
		f.LimitPrice = r.Price.Mul(decOne.Sub(tol))
	}
	reread, ok := r.Reread()
	if !ok || !reread.IsPositive() {
		f.Reason = "no quote at fill time"
		return f
	}
	f.RereadPrice = reread
	if (r.Side == "BUY" && reread.GreaterThan(f.LimitPrice)) || (r.Side == "SELL" && reread.LessThan(f.LimitPrice)) {
		f.Reason = "limit-IOC: re-read price outside tolerance"
		return f
	}
	fill := jitter(rng, x.latency.FillBase, x.latency.FillJitter)
	f.FillMs = fill.Milliseconds()
	if err := x.waiter.Wait(ctx, fill); err != nil {
		f.Reason = "fill wait: " + err.Error()
		return f
	}
	slip := slipBps.Div(decTenK)
	if r.Side == "BUY" {
		f.FillPrice = reread.Mul(decOne.Add(slip))
	} else {
		f.FillPrice = reread.Mul(decOne.Sub(slip))
	}
	f.FeeQuote = f.FillPrice.Mul(r.Qty).Mul(r.FeeBps.Div(decTenK))
	f.Status = "FILLED"
	return f
}

// truncStep truncates (never rounds) q to the step (§0 Rounding).
func truncStep(q, step decimal.Decimal) decimal.Decimal {
	if !step.IsPositive() {
		return q
	}
	return q.Div(step).Floor().Mul(step)
}

// realisedSlipBps is §1.3's after-the-fact measure: (next − quote)/quote
// × 10 000 on the traded side, signed so that positive = worse.
func realisedSlipBps(side string, quote, next decimal.Decimal) decimal.Decimal {
	if !quote.IsPositive() {
		return decimal.Zero
	}
	rel := next.Sub(quote).Div(quote).Mul(decTenK)
	if side == "SELL" {
		return rel.Neg()
	}
	return rel
}
