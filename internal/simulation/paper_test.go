package simulation

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/exchange"
	"github.com/cploutarchou/arb-chain-bot/internal/execution"
	"github.com/cploutarchou/arb-chain-bot/internal/fees"
	"github.com/cploutarchou/arb-chain-bot/internal/graph"
	"github.com/cploutarchou/arb-chain-bot/internal/opportunity"
	"github.com/cploutarchou/arb-chain-bot/internal/orderbook"
	"github.com/cploutarchou/arb-chain-bot/internal/pricing"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func lv(p, q string) orderbook.Level { return orderbook.Level{Price: d(p), Qty: d(q)} }

var t0 = time.Unix(1_700_000_000, 0)

// --- fakes -----------------------------------------------------------------

type fakeBooks map[exchange.MarketID]orderbook.View

func (f fakeBooks) View(id exchange.MarketID, _ int) (orderbook.View, bool) {
	v, ok := f[id]
	return v, ok
}

type fakeRules map[exchange.MarketID]exchange.InstrumentRules

func (f fakeRules) Rules(id exchange.MarketID) (exchange.InstrumentRules, bool) {
	r, ok := f[id]
	return r, ok
}

type fakeMarker map[exchange.Asset]decimal.Decimal // asset -> rate in start asset

func (f fakeMarker) Mark(asset exchange.Asset, amount decimal.Decimal, _ exchange.Asset) (decimal.Decimal, bool) {
	rate, ok := f[asset]
	if !ok {
		return decimal.Zero, false
	}
	return amount.Mul(rate), true
}

// --- fixture ---------------------------------------------------------------

var (
	mBTCUSDT = exchange.MarketID{Exchange: "binance", Symbol: "BTCUSDT"}
	mETHBTC  = exchange.MarketID{Exchange: "binance", Symbol: "ETHBTC"}
	mETHUSDT = exchange.MarketID{Exchange: "binance", Symbol: "ETHUSDT"}
)

func stepRules() exchange.InstrumentRules {
	return exchange.InstrumentRules{
		QtyMode: exchange.PrecisionStep, QtyStep: d("0.001"),
		PriceMode: exchange.PrecisionStep, PriceTick: d("0.00000001"),
	}
}

func triangle() graph.Triangle {
	return graph.Triangle{
		ID: "binance|USDT|BTCUSDT>ETHBTC>ETHUSDT", Exchange: "binance", Start: "USDT",
		Legs: [3]graph.Leg{
			{Market: mBTCUSDT, Base: "BTC", Quote: "USDT", From: "USDT", To: "BTC", Side: exchange.SideBuy},
			{Market: mETHBTC, Base: "ETH", Quote: "BTC", From: "BTC", To: "ETH", Side: exchange.SideBuy},
			{Market: mETHUSDT, Base: "ETH", Quote: "USDT", From: "ETH", To: "USDT", Side: exchange.SideSell},
		},
	}
}

// planBooks: the profitable fixture from the pricing tests
// (1000 USDT -> 1016.94204 at 10 bps fees).
func planBooks() fakeBooks {
	return fakeBooks{
		mBTCUSDT: {Market: mBTCUSDT, Version: 1, State: orderbook.StateHealthy,
			Asks: []orderbook.Level{lv("100", "10"), lv("101", "5")}},
		mETHBTC: {Market: mETHBTC, Version: 2, State: orderbook.StateHealthy,
			Asks: []orderbook.Level{lv("0.1", "100"), lv("0.11", "100")}},
		mETHUSDT: {Market: mETHUSDT, Version: 3, State: orderbook.StateHealthy,
			Bids: []orderbook.Level{lv("10.2", "1000")}},
	}
}

func rules() fakeRules {
	return fakeRules{mBTCUSDT: stepRules(), mETHBTC: stepRules(), mETHUSDT: stepRules()}
}

func sched(t testing.TB) *fees.Schedule {
	t.Helper()
	s, err := fees.NewSchedule("binance", exchange.FeeInReceived,
		fees.Rate{Maker: d("0.001"), Taker: d("0.001")})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func plan(t testing.TB, books fakeBooks, ttl time.Duration) execution.CyclePlan {
	t.Helper()
	tri := triangle()
	data := [3]pricing.MarketData{}
	for i, leg := range tri.Legs {
		v, _ := books.View(leg.Market, 0)
		data[i] = pricing.MarketData{View: v, Rules: stepRules()}
	}
	cq, err := pricing.QuoteCycle(tri, data, sched(t), d("1000"))
	if err != nil {
		t.Fatal(err)
	}
	op := opportunity.Build("op-1", "binance", cq,
		opportunity.Buffers{LatencyBps: d("2"), RiskBps: d("3")}, ttl, t0, 1)
	return execution.CyclePlan{CycleID: "cycle-1", SessionID: "sess-1", Opportunity: &op, Triangle: tri}
}

func engine(t testing.TB, books fakeBooks, clock *VirtualClock, cfg Config) *Engine {
	t.Helper()
	var seq atomic.Int64
	if cfg.LimitToleranceBps.IsZero() {
		cfg.LimitToleranceBps = d("20")
	}
	cfg.Latency = LatencyModel{SubmitBase: 5 * time.Millisecond, SubmitJitter: 5 * time.Millisecond,
		FillBase: 10 * time.Millisecond, FillJitter: 10 * time.Millisecond}
	return NewSimulation(books, rules(), sched(t), clock, VirtualWaiter{Clock: clock}, fakeMarker{},
		cfg, func() string { return fmt.Sprintf("id-%d", seq.Add(1)) })
}

// --- tests -----------------------------------------------------------------

// Unchanged books: the simulation must reproduce the plan's economics
// exactly and settle ALL_FILLED. Realized slippage against the un-buffered
// plan is exactly zero — the buffers are a risk allowance, never part of
// the measurement.
func TestAllFilledReproducesPlan(t *testing.T) {
	books := planBooks()
	clock := NewVirtualClock(t0)
	e := engine(t, books, clock, Config{Seed: 42})
	p := plan(t, books, time.Minute)

	res, err := e.ExecuteCycle(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != execution.OutcomeAllFilled {
		t.Fatalf("outcome = %s (%s)", res.Outcome, res.Reason)
	}
	// The correlation chain starts here: the result carries the plan's
	// opportunity so persistence never needs caller-side re-attachment.
	if res.OpportunityID != "op-1" {
		t.Fatalf("opportunity linkage = %q", res.OpportunityID)
	}
	if !res.InputConsumed.Equal(d("1000")) || !res.FinalAmount.Equal(d("1016.94204")) {
		t.Fatalf("in=%s final=%s", res.InputConsumed, res.FinalAmount)
	}
	if !res.RealizedPnL.Equal(d("16.94204")) || !res.TotalPnL.Equal(res.RealizedPnL) {
		t.Fatalf("pnl=%s total=%s", res.RealizedPnL, res.TotalPnL)
	}
	if !res.Fees["BTC"].Equal(d("0.01")) || !res.Fees["ETH"].Equal(d("0.0999")) || !res.Fees["USDT"].Equal(d("1.01796")) {
		t.Fatalf("fees=%v", res.Fees)
	}
	// Leg-3 quantization dust (0.0001 ETH) is explicit exposure.
	if !res.Exposure["ETH"].Equal(d("0.0001")) {
		t.Fatalf("exposure=%v", res.Exposure)
	}
	if len(res.Orders) != 3 || res.Orders[2].Status != execution.OrderFilled {
		t.Fatalf("orders=%+v", res.Orders)
	}
	// Identical books ⇒ the realized return equals the plan's return and
	// slippage is zero, whatever the configured buffers (5 bps here).
	if !res.SlippageBps.IsZero() {
		t.Fatalf("slippage = %s bps, want 0", res.SlippageBps)
	}
	if !res.PlannedReturnBps.Equal(d("169.4204")) || !res.ActualReturnBps.Equal(res.PlannedReturnBps) {
		t.Fatalf("planned = %s actual = %s bps", res.PlannedReturnBps, res.ActualReturnBps)
	}
	if !res.SettledAt.After(res.StartedAt) {
		t.Fatal("virtual time did not advance")
	}
}

// Same seed ⇒ byte-identical results including timestamps; different seed
// ⇒ different latency draws (SKILL.md §64).
func TestDeterminismBySeed(t *testing.T) {
	run := func(seed int64) execution.CycleResult {
		books := planBooks()
		clock := NewVirtualClock(t0)
		e := engine(t, books, clock, Config{Seed: seed})
		res, err := e.ExecuteCycle(context.Background(), plan(t, books, time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	a, b := run(42), run(42)
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("same seed diverged:\n%+v\n%+v", a, b)
	}
	c := run(43)
	if a.SettledAt.Equal(c.SettledAt) {
		t.Fatal("different seed produced identical latency schedule")
	}
}

// Books moved against us within tolerance: filled, positive leg slippage.
func TestAdverseMoveWithinToleranceFills(t *testing.T) {
	books := planBooks()
	p := plan(t, books, time.Minute) // plan on original books
	// Leg-1 asks worsen by ~10 bps (100 -> 100.1), inside the 20 bps limit.
	books[mBTCUSDT] = orderbook.View{Market: mBTCUSDT, Version: 9, State: orderbook.StateHealthy,
		Asks: []orderbook.Level{lv("100.1", "10"), lv("101", "5")}}
	clock := NewVirtualClock(t0)
	e := engine(t, books, clock, Config{Seed: 1})

	res, err := e.ExecuteCycle(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != execution.OutcomeAllFilled {
		t.Fatalf("outcome = %s (%s)", res.Outcome, res.Reason)
	}
	if !res.Orders[0].SlippageBps.IsPositive() {
		t.Fatalf("leg1 slippage = %s", res.Orders[0].SlippageBps)
	}
	if res.RealizedPnL.GreaterThanOrEqual(d("16.94204")) {
		t.Fatalf("pnl did not degrade: %s", res.RealizedPnL)
	}
	// The adverse move is the only case in this file that should register
	// as cycle slippage: planned 169.4204 bps, actual below it.
	if !res.SlippageBps.IsPositive() || !res.ActualReturnBps.LessThan(res.PlannedReturnBps) {
		t.Fatalf("cycle slippage = %s (planned %s, actual %s)", res.SlippageBps, res.PlannedReturnBps, res.ActualReturnBps)
	}
}

// Books moved beyond the limit tolerance on leg 1: nothing fillable,
// cycle REJECTED, nothing consumed, nothing exposed.
func TestMoveBeyondToleranceRejects(t *testing.T) {
	books := planBooks()
	p := plan(t, books, time.Minute)
	books[mBTCUSDT] = orderbook.View{Market: mBTCUSDT, Version: 9, State: orderbook.StateHealthy,
		Asks: []orderbook.Level{lv("101", "10")}} // +100 bps > 20 bps limit
	clock := NewVirtualClock(t0)
	e := engine(t, books, clock, Config{Seed: 1})

	res, err := e.ExecuteCycle(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != execution.OutcomeRejected {
		t.Fatalf("outcome = %s", res.Outcome)
	}
	if !res.InputConsumed.IsZero() || len(res.Exposure) != 0 || !res.TotalPnL.IsZero() {
		t.Fatalf("rejected cycle has effects: %+v", res)
	}
}

// Leg 2 book vanishes after leg 1 fills: LEG1_FILLED_LEG2_FAILED with the
// BTC holding as explicit exposure and mark-to-market P&L.
func TestLeg2FailureCreatesExposure(t *testing.T) {
	books := planBooks()
	p := plan(t, books, time.Minute)
	books[mETHBTC] = orderbook.View{Market: mETHBTC, Version: 9, State: orderbook.StateHealthy} // empty
	clock := NewVirtualClock(t0)
	e := engine(t, books, clock, Config{Seed: 1})
	e.marker = fakeMarker{"BTC": d("99.9")} // mark BTC at 99.9 USDT

	res, err := e.ExecuteCycle(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != execution.OutcomeLeg1FilledLeg2Failed {
		t.Fatalf("outcome = %s", res.Outcome)
	}
	if !res.Exposure["BTC"].Equal(d("9.99")) {
		t.Fatalf("exposure = %v", res.Exposure)
	}
	// Consumed 1000, hold 9.99 BTC marked at 99.9 → 998.001; total ≈ -1.999.
	if !res.ExposureMark.Equal(d("998.001")) {
		t.Fatalf("mark = %s", res.ExposureMark)
	}
	if !res.TotalPnL.Equal(d("-1.999")) {
		t.Fatalf("total pnl = %s", res.TotalPnL)
	}
	if res.Orders[1].Status != execution.OrderRejected {
		t.Fatalf("leg2 order = %+v", res.Orders[1])
	}
}

// Leg 3 failure strands ETH.
func TestLeg3FailureCreatesExposure(t *testing.T) {
	books := planBooks()
	p := plan(t, books, time.Minute)
	books[mETHUSDT] = orderbook.View{Market: mETHUSDT, Version: 9, State: orderbook.StateHealthy} // empty
	clock := NewVirtualClock(t0)
	e := engine(t, books, clock, Config{Seed: 1})

	res, err := e.ExecuteCycle(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != execution.OutcomeLeg12FilledLeg3Failed {
		t.Fatalf("outcome = %s", res.Outcome)
	}
	if !res.Exposure["ETH"].Equal(d("99.8001")) {
		t.Fatalf("exposure = %v", res.Exposure)
	}
	if !res.RealizedPnL.Equal(d("-1000")) { // deployed, nothing returned yet
		t.Fatalf("realized = %s", res.RealizedPnL)
	}
}

// Leg-1 depth (within limit) covers only part of the planned quantity:
// the cycle completes smaller → LEG1_PARTIAL.
func TestLeg1PartialFill(t *testing.T) {
	books := planBooks()
	p := plan(t, books, time.Minute)
	books[mBTCUSDT] = orderbook.View{Market: mBTCUSDT, Version: 9, State: orderbook.StateHealthy,
		Asks: []orderbook.Level{lv("100", "4")}} // only 4 BTC vs planned 10
	clock := NewVirtualClock(t0)
	e := engine(t, books, clock, Config{Seed: 1})

	res, err := e.ExecuteCycle(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != execution.OutcomeLeg1Partial {
		t.Fatalf("outcome = %s", res.Outcome)
	}
	if res.Orders[0].Status != execution.OrderPartiallyFilled {
		t.Fatalf("leg1 status = %s", res.Orders[0].Status)
	}
	if !res.InputConsumed.Equal(d("400")) {
		t.Fatalf("consumed = %s", res.InputConsumed)
	}
	if !res.Outcome.Complete() || !res.FinalAmount.IsPositive() {
		t.Fatalf("partial cycle did not complete: %+v", res)
	}
	// Prices are byte-identical to the plan; only the size shrank. The
	// return per unit deployed is therefore the plan's return and the
	// slippage is zero — a size mismatch must never masquerade as
	// slippage (the previous formula reported +15 229 bps here).
	if !res.SlippageBps.IsZero() || !res.ActualReturnBps.Equal(res.PlannedReturnBps) {
		t.Fatalf("slippage = %s (planned %s, actual %s)", res.SlippageBps, res.PlannedReturnBps, res.ActualReturnBps)
	}
}

func TestExpiredPlanShortCircuits(t *testing.T) {
	books := planBooks()
	clock := NewVirtualClock(t0.Add(time.Hour)) // far past TTL
	e := engine(t, books, clock, Config{Seed: 1})
	res, err := e.ExecuteCycle(context.Background(), plan(t, books, time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != execution.OutcomeExpired || len(res.Orders) != 0 {
		t.Fatalf("res = %+v", res)
	}
}

// cancelAfterWaiter cancels the context after n waits (mid-cycle timeout).
type cancelAfterWaiter struct {
	inner  Waiter
	cancel context.CancelFunc
	left   int
}

func (w *cancelAfterWaiter) Wait(ctx context.Context, dur time.Duration) error {
	if err := w.inner.Wait(ctx, dur); err != nil {
		return err
	}
	w.left--
	if w.left == 0 {
		w.cancel()
	}
	return nil
}

func TestTimeoutMidCycleTracksExposure(t *testing.T) {
	books := planBooks()
	clock := NewVirtualClock(t0)
	e := engine(t, books, clock, Config{Seed: 1})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Leg1 = waits 1(submit)+2(fill); cancel fires entering leg2's submit wait.
	e.wait = &cancelAfterWaiter{inner: VirtualWaiter{Clock: clock}, cancel: cancel, left: 3}

	res, err := e.ExecuteCycle(ctx, plan(t, books, time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != execution.OutcomeTimeout {
		t.Fatalf("outcome = %s (%s)", res.Outcome, res.Reason)
	}
	if !res.Exposure["BTC"].Equal(d("9.99")) {
		t.Fatalf("exposure = %v", res.Exposure)
	}
}

// The execution boundary: LiveExecutor always refuses.
func TestLiveExecutorDisabled(t *testing.T) {
	var live execution.LiveExecutor
	_, err := live.ExecuteCycle(context.Background(), execution.CyclePlan{})
	if !errors.Is(err, execution.ErrLiveTradingDisabled) {
		t.Fatalf("live executor returned %v", err)
	}
}

// Depth simulation (§73): one full three-leg cycle under the virtual
// clock (latency waits advance instantly; the cost measured is fill
// construction, filtering, re-pricing, and settlement math).
func BenchmarkExecuteCycle(b *testing.B) {
	books := planBooks()
	p := plan(b, books, time.Minute)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		clock := NewVirtualClock(t0)
		e := engine(b, books, clock, Config{Seed: 42})
		p2 := p
		p2.CycleID = fmt.Sprintf("cycle-%d", i)
		if _, err := e.ExecuteCycle(context.Background(), p2); err != nil {
			b.Fatal(err)
		}
	}
}
