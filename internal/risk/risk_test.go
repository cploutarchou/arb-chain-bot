package risk

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/exchange"
	"github.com/cploutarchou/arb-chain-bot/internal/opportunity"
	"github.com/cploutarchou/arb-chain-bot/internal/orderbook"
	"github.com/cploutarchou/arb-chain-bot/internal/pricing"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func dp(s string) *decimal.Decimal { v := d(s); return &v }

var t0 = time.Unix(1_700_000_000, 0)

func opFixture(netBps, netProfit, input string) *opportunity.Opportunity {
	q := pricing.CycleQuote{
		Triangle: "binance|USDT|A>B>C", Start: "USDT",
		InputConsumed: d(input),
	}
	q.FinalAmount = q.InputConsumed.Add(d(netProfit))
	o := opportunity.Build("op", "binance", q, opportunity.Buffers{}, time.Second, t0, 7)
	// Overwrite derived economics for direct boundary control.
	o.NetReturnBps = d(netBps)
	o.NetProfit = d(netProfit)
	return &o
}

func healthyCtx() Context {
	return Context{
		Now:              t0.Add(100 * time.Millisecond),
		ConfigVersion:    7,
		BookStates:       [3]orderbook.State{orderbook.StateHealthy, orderbook.StateHealthy, orderbook.StateHealthy},
		BookAges:         [3]time.Duration{50 * time.Millisecond, 60 * time.Millisecond, 70 * time.Millisecond},
		DataQuality:      d("0.99"),
		ClockHealthy:     true,
		CapitalAvailable: d("10000"),
		CapitalReserved:  d("0"),
	}
}

func baseLimits() Limits {
	return Limits{
		MinNetEdgeBps:            d("5"),
		MinExpectedProfit:        d("1"),
		MaxTradeSize:             d("5000"),
		MaxCapitalPerTriangle:    d("2000"),
		MaxCapitalUtilization:    d("0.5"),
		MaxConcurrentSimulations: 3,
		MaxBookAge:               500 * time.Millisecond,
		MaxBookAgeSpread:         200 * time.Millisecond,
		MaxPriceImpactBps:        d("50"),
		MaxDailyLoss:             d("100"),
		MaxDrawdown:              d("0.1"),
		MinDataQuality:           d("0.9"),
	}
}

func TestAllowsAtExactBoundaries(t *testing.T) {
	// Edge exactly at threshold, profit exactly at threshold, size exactly
	// at cap: all inclusive-pass.
	op := opFixture("5", "1", "2000")
	dec := Evaluate(op, healthyCtx(), baseLimits(), false)
	if !dec.Allowed {
		t.Fatalf("boundary rejected: %s %+v", dec.ReasonCode, dec.Checks)
	}
	if dec.ConfigVersion != 7 {
		t.Fatalf("config version = %d", dec.ConfigVersion)
	}
}

func TestRejectionsPerLimit(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(op *opportunity.Opportunity, ctx *Context, l *Limits)
		want   string
	}{
		{"edge below min", func(op *opportunity.Opportunity, _ *Context, _ *Limits) {
			op.NetReturnBps = d("4.999")
		}, ReasonMinEdge},
		{"profit below min", func(op *opportunity.Opportunity, _ *Context, _ *Limits) {
			op.NetProfit = d("0.999")
		}, ReasonMinProfit},
		{"size above cap", func(op *opportunity.Opportunity, _ *Context, _ *Limits) {
			op.Quote.InputConsumed = d("5000.01")
		}, ReasonMaxTradeSize},
		{"triangle capital", func(_ *opportunity.Opportunity, ctx *Context, _ *Limits) {
			ctx.TriangleReserved = d("1500") // 1500+1000 > 2000
		}, ReasonTriangleCapital},
		{"utilization", func(_ *opportunity.Opportunity, ctx *Context, _ *Limits) {
			ctx.CapitalReserved = d("4500")
			ctx.CapitalAvailable = d("5500") // (4500+1000)/10000 = 0.55 > 0.5
		}, ReasonUtilization},
		{"concurrency", func(_ *opportunity.Opportunity, ctx *Context, _ *Limits) {
			ctx.ConcurrentSimulations = 3
		}, ReasonConcurrency},
		{"book state", func(_ *opportunity.Opportunity, ctx *Context, _ *Limits) {
			ctx.BookStates[1] = orderbook.StateStale
		}, ReasonBookState},
		{"book age", func(_ *opportunity.Opportunity, ctx *Context, _ *Limits) {
			ctx.BookAges[2] = 600 * time.Millisecond
		}, ReasonBookAge},
		{"age spread", func(_ *opportunity.Opportunity, ctx *Context, l *Limits) {
			ctx.BookAges = [3]time.Duration{10 * time.Millisecond, 20 * time.Millisecond, 300 * time.Millisecond}
			l.MaxBookAge = time.Second // isolate the spread check
		}, ReasonBookAgeSpread},
		{"data quality", func(_ *opportunity.Opportunity, ctx *Context, _ *Limits) {
			ctx.DataQuality = d("0.85")
		}, ReasonDataQuality},
		{"clock", func(_ *opportunity.Opportunity, ctx *Context, _ *Limits) {
			ctx.ClockHealthy = false
		}, ReasonClockUnsafe},
		{"breaker", func(_ *opportunity.Opportunity, ctx *Context, _ *Limits) {
			ctx.BreakerOpen = true
		}, ReasonBreakerOpen},
		{"price impact", func(op *opportunity.Opportunity, _ *Context, _ *Limits) {
			op.Quote.Legs[1].PriceImpactBps = d("51")
		}, ReasonPriceImpact},
		{"daily loss", func(_ *opportunity.Opportunity, ctx *Context, _ *Limits) {
			ctx.DailyLoss = d("100")
		}, ReasonDailyLoss},
		{"drawdown", func(_ *opportunity.Opportunity, ctx *Context, _ *Limits) {
			ctx.Drawdown = d("0.1")
		}, ReasonDrawdown},
		{"ttl", func(_ *opportunity.Opportunity, ctx *Context, _ *Limits) {
			ctx.Now = t0.Add(2 * time.Second)
		}, ReasonExpired},
	}
	for _, c := range cases {
		op := opFixture("10", "5", "1000")
		ctx := healthyCtx()
		l := baseLimits()
		c.mutate(op, &ctx, &l)
		dec := Evaluate(op, ctx, l, false)
		if dec.Allowed {
			t.Fatalf("%s: allowed", c.name)
		}
		if dec.ReasonCode != c.want {
			t.Fatalf("%s: reason = %s, want %s", c.name, dec.ReasonCode, c.want)
		}
	}
}

func TestAllChecksRecordedEvenAfterFailure(t *testing.T) {
	op := opFixture("0", "0", "1000") // fails edge AND profit
	dec := Evaluate(op, healthyCtx(), baseLimits(), false)
	if dec.Allowed {
		t.Fatal("allowed")
	}
	var failed int
	for _, c := range dec.Checks {
		if !c.Passed {
			failed++
		}
	}
	if failed < 2 {
		t.Fatalf("failed checks recorded = %d, want >= 2 (full evaluation)", failed)
	}
	if dec.ReasonCode != ReasonMinEdge {
		t.Fatalf("first reason = %s", dec.ReasonCode)
	}
}

func TestDisabledScopeRejects(t *testing.T) {
	op := opFixture("10", "5", "1000")
	dec := Evaluate(op, healthyCtx(), baseLimits(), true)
	if dec.Allowed || dec.ReasonCode != ReasonTriangleDisabled {
		t.Fatalf("disabled scope: %+v", dec.ReasonCode)
	}
}

func TestOverridePrecedence(t *testing.T) {
	off := false
	r := Resolver{
		Global:     baseLimits(),
		ByExchange: map[exchange.ExchangeID]Override{"binance": {MinNetEdgeBps: dp("8")}},
		ByAsset:    map[exchange.Asset]Override{"USDT": {MinNetEdgeBps: dp("12")}},
		ByTriangle: map[string]Override{
			"tri-1": {MinNetEdgeBps: dp("20")},
			"tri-2": {Enabled: &off},
		},
	}
	// Most specific wins.
	if l, dis := r.Effective("binance", "USDT", "tri-1"); dis || !l.MinNetEdgeBps.Equal(d("20")) {
		t.Fatalf("tri-1: %s dis=%v", l.MinNetEdgeBps, dis)
	}
	// Asset-level wins when triangle has no override.
	if l, _ := r.Effective("binance", "USDT", "tri-9"); !l.MinNetEdgeBps.Equal(d("12")) {
		t.Fatalf("asset scope: %s", l.MinNetEdgeBps)
	}
	// Exchange-level when asset absent.
	if l, _ := r.Effective("binance", "BTC", "tri-9"); !l.MinNetEdgeBps.Equal(d("8")) {
		t.Fatalf("exchange scope: %s", l.MinNetEdgeBps)
	}
	// Unknown everything: global.
	if l, _ := r.Effective("okx", "BTC", "tri-9"); !l.MinNetEdgeBps.Equal(d("5")) {
		t.Fatalf("global scope: %s", l.MinNetEdgeBps)
	}
	// Disabled at triangle scope.
	if _, dis := r.Effective("binance", "USDT", "tri-2"); !dis {
		t.Fatal("tri-2 not disabled")
	}
}

// Monotonicity: worsening one input can never flip REJECTED → ALLOWED.
func TestMonotonicity(t *testing.T) {
	base := opFixture("6", "2", "1000")
	ctx := healthyCtx()
	l := baseLimits()
	allowed := Evaluate(base, ctx, l, false).Allowed
	if !allowed {
		t.Fatal("baseline must pass")
	}
	worsenings := []func(*opportunity.Opportunity, *Context){
		func(o *opportunity.Opportunity, _ *Context) { o.NetReturnBps = o.NetReturnBps.Sub(d("2")) },
		func(o *opportunity.Opportunity, _ *Context) { o.NetProfit = o.NetProfit.Sub(d("1.5")) },
		func(_ *opportunity.Opportunity, c *Context) { c.BookAges[0] += time.Second },
		func(_ *opportunity.Opportunity, c *Context) { c.DataQuality = c.DataQuality.Sub(d("0.2")) },
		func(_ *opportunity.Opportunity, c *Context) { c.Drawdown = d("0.5") },
	}
	for i, w := range worsenings {
		op := opFixture("6", "2", "1000")
		wctx := healthyCtx()
		w(op, &wctx)
		first := Evaluate(op, wctx, l, false)
		w(op, &wctx) // worsen further
		second := Evaluate(op, wctx, l, false)
		if !first.Allowed && second.Allowed {
			t.Fatalf("worsening %d flipped reject->allow", i)
		}
	}
}

func TestBreakerLifecycle(t *testing.T) {
	var events []Transition
	reg := NewRegistry(func(tr Transition) { events = append(events, tr) })
	reg.Register("ws_unstable", "exchange:binance", 100*time.Millisecond)

	if reg.AnyOpen("exchange:binance") {
		t.Fatal("closed breaker reads open")
	}
	reg.Trip("ws_unstable", "exchange:binance", "reconnect storm", t0)
	if !reg.AnyOpen("exchange:binance") {
		t.Fatal("tripped breaker not open")
	}
	// Scope isolation: a different exchange is unaffected.
	if reg.AnyOpen("exchange:okx") {
		t.Fatal("okx gated by binance breaker")
	}
	// Global breakers gate every scope.
	reg.Trip("db_degraded", "", "outbox overflow", t0)
	if !reg.AnyOpen("exchange:okx") {
		t.Fatal("global breaker must gate all scopes")
	}
	reg.Close("db_degraded", "", t0)

	// Probe eligibility honors the delay.
	if reg.Probe("ws_unstable", "exchange:binance", t0.Add(50*time.Millisecond)) {
		t.Fatal("probe before delay")
	}
	if !reg.Probe("ws_unstable", "exchange:binance", t0.Add(150*time.Millisecond)) {
		t.Fatal("probe after delay refused")
	}
	// HALF_OPEN does not gate.
	if reg.AnyOpen("exchange:binance") {
		t.Fatal("half-open gates")
	}
	reg.Close("ws_unstable", "exchange:binance", t0.Add(200*time.Millisecond))

	if len(events) != 5 { // open, open, close, half-open, close
		t.Fatalf("events = %d: %+v", len(events), events)
	}
}
