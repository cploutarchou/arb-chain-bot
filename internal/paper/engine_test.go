package paper

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/exchange"
	"github.com/cploutarchou/arb-chain-bot/internal/execution"
	"github.com/cploutarchou/arb-chain-bot/internal/graph"
	"github.com/cploutarchou/arb-chain-bot/internal/opportunity"
	"github.com/cploutarchou/arb-chain-bot/internal/portfolio"
	"github.com/cploutarchou/arb-chain-bot/internal/pricing"
	"github.com/cploutarchou/arb-chain-bot/internal/reservation"
	"github.com/cploutarchou/arb-chain-bot/internal/risk"
	"github.com/cploutarchou/arb-chain-bot/internal/scanner"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

var t0 = time.Unix(1_700_000_000, 0)

const triID = "binance|USDT|BTCUSDT>ETHBTC>ETHUSDT"

func triangle() graph.Triangle {
	mk := func(sym string) exchange.MarketID {
		return exchange.MarketID{Exchange: "binance", Symbol: exchange.Symbol(sym)}
	}
	return graph.Triangle{
		ID: triID, Exchange: "binance", Start: "USDT",
		Legs: [3]graph.Leg{
			{Market: mk("BTCUSDT"), From: "USDT", To: "BTC", Side: exchange.SideBuy},
			{Market: mk("ETHBTC"), From: "BTC", To: "ETH", Side: exchange.SideBuy},
			{Market: mk("ETHUSDT"), From: "ETH", To: "USDT", Side: exchange.SideSell},
		},
	}
}

func qualifiedEvent(id, input string) scanner.Event {
	q := pricing.CycleQuote{Triangle: triID, Start: "USDT", InputConsumed: d(input)}
	q.FinalAmount = q.InputConsumed.Add(d("5"))
	op := opportunity.Build(id, "binance", q, opportunity.Buffers{}, time.Minute, t0, 1)
	_ = op.Transition(opportunity.StatusCalculating, "")
	_ = op.Transition(opportunity.StatusQualified, "")
	return scanner.Event{Opportunity: op, Decision: risk.Decision{Allowed: true}}
}

// scriptedExecutor returns canned results and records concurrency.
type scriptedExecutor struct {
	result   func(plan execution.CyclePlan) execution.CycleResult
	inFlight atomic.Int32
	peak     atomic.Int32
	block    chan struct{} // when non-nil, cycles wait here (concurrency test)
}

func (s *scriptedExecutor) ExecuteCycle(ctx context.Context, plan execution.CyclePlan) (execution.CycleResult, error) {
	cur := s.inFlight.Add(1)
	for {
		p := s.peak.Load()
		if cur <= p || s.peak.CompareAndSwap(p, cur) {
			break
		}
	}
	defer s.inFlight.Add(-1)
	if s.block != nil {
		select {
		case <-s.block:
		case <-ctx.Done():
			return execution.CycleResult{}, ctx.Err()
		}
	}
	return s.result(plan), nil
}

func harness(t *testing.T, exec execution.Executor) (*Engine, chan scanner.Event, *reservation.Manager, *portfolio.Portfolio) {
	t.Helper()
	var seq atomic.Int64
	resv := reservation.New(
		map[exchange.Asset]decimal.Decimal{"USDT": d("10000")},
		func() string { return fmt.Sprintf("r-%d", seq.Add(1)) },
		func() time.Time { return t0 },
	)
	port := portfolio.New(resv, map[exchange.Asset]decimal.Decimal{"USDT": d("10000")})
	in := make(chan scanner.Event, 32)
	e := &Engine{
		Executor:  exec,
		Resv:      resv,
		Portfolio: port,
		Triangles: map[string]graph.Triangle{triID: triangle()},
		In:        in,
		Clock:     func() time.Time { return t0.Add(time.Millisecond) },
		IDGen:     func() string { return fmt.Sprintf("cyc-%d", seq.Add(1)) },
	}
	e.Resume()
	return e, in, resv, port
}

func completed(plan execution.CyclePlan) execution.CycleResult {
	in := plan.Opportunity.Quote.InputConsumed
	res := execution.CycleResult{
		CycleID: plan.CycleID, Outcome: execution.OutcomeAllFilled,
		StartAsset: "USDT", InputConsumed: in, FinalAmount: in.Add(d("4")),
		Exposure: map[exchange.Asset]decimal.Decimal{},
		Fees:     map[exchange.Asset]decimal.Decimal{"USDT": d("0.3")},
	}
	res.RealizedPnL = res.FinalAmount.Sub(res.InputConsumed)
	res.TotalPnL = res.RealizedPnL
	return res
}

func runEngine(t *testing.T, e *Engine) (cancel func(), wait func()) {
	t.Helper()
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- e.Run(ctx) }()
	return stop, func() { <-done }
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for !cond() {
		select {
		case <-deadline:
			t.Fatal("condition not reached")
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func TestCompletedCycleSettlesLedgerAndPortfolio(t *testing.T) {
	exec := &scriptedExecutor{result: completed}
	e, in, resv, port := harness(t, exec)
	var results atomic.Int32
	e.OnResult = func(execution.CycleResult) { results.Add(1) }
	cancel, wait := runEngine(t, e)
	defer wait()
	defer cancel()

	in <- qualifiedEvent("op-1", "1000")
	waitFor(t, func() bool { return results.Load() == 1 })

	if err := resv.CheckInvariants(); err != nil {
		t.Fatal(err)
	}
	avail, reserved := resv.Balance("USDT")
	// 10000 - 1000 + 1004 = 10004; nothing left reserved.
	if !avail.Equal(d("10004")) || !reserved.IsZero() {
		t.Fatalf("ledger = %s/%s", avail, reserved)
	}
	snap := port.TakeSnapshot(t0, nil)
	if snap.Completed != 1 || !snap.Realized["USDT"].Equal(d("4")) {
		t.Fatalf("portfolio = %+v", snap)
	}
	st := e.Snapshot()
	if st.Completed != 1 || st.Failed != 0 {
		t.Fatalf("stats = %+v", st)
	}
}

func TestDuplicateOpportunityRunsOnce(t *testing.T) {
	exec := &scriptedExecutor{result: completed}
	e, in, resv, _ := harness(t, exec)
	var results atomic.Int32
	e.OnResult = func(execution.CycleResult) { results.Add(1) }
	cancel, wait := runEngine(t, e)
	defer wait()
	defer cancel()

	in <- qualifiedEvent("op-dup", "500")
	waitFor(t, func() bool { return results.Load() == 1 })
	in <- qualifiedEvent("op-dup", "500") // same idempotency key
	waitFor(t, func() bool { return e.Snapshot().Skipped >= 1 })

	if results.Load() != 1 {
		t.Fatalf("duplicate executed: %d results", results.Load())
	}
	avail, _ := resv.Balance("USDT")
	if !avail.Equal(d("10004")) { // only one cycle's effect
		t.Fatalf("ledger after duplicate = %s", avail)
	}
}

func TestPausedEngineSkips(t *testing.T) {
	exec := &scriptedExecutor{result: completed}
	e, in, resv, _ := harness(t, exec)
	e.Pause()
	cancel, wait := runEngine(t, e)
	defer wait()
	defer cancel()

	in <- qualifiedEvent("op-p", "100")
	waitFor(t, func() bool { return e.Snapshot().Skipped == 1 })
	avail, reserved := resv.Balance("USDT")
	if !avail.Equal(d("10000")) || !reserved.IsZero() {
		t.Fatalf("paused engine touched ledger: %s/%s", avail, reserved)
	}
}

func TestFailedCycleAppliesExposure(t *testing.T) {
	exec := &scriptedExecutor{result: func(plan execution.CyclePlan) execution.CycleResult {
		in := plan.Opportunity.Quote.InputConsumed
		return execution.CycleResult{
			CycleID: plan.CycleID, Outcome: execution.OutcomeLeg1FilledLeg2Failed,
			StartAsset: "USDT", InputConsumed: in, FinalAmount: d("0"),
			RealizedPnL: in.Neg(),
			Exposure:    map[exchange.Asset]decimal.Decimal{"BTC": d("0.01")},
			Fees:        map[exchange.Asset]decimal.Decimal{},
		}
	}}
	e, in, resv, port := harness(t, exec)
	var results atomic.Int32
	e.OnResult = func(execution.CycleResult) { results.Add(1) }
	cancel, wait := runEngine(t, e)
	defer wait()
	defer cancel()

	in <- qualifiedEvent("op-f", "300")
	waitFor(t, func() bool { return results.Load() == 1 })

	avail, _ := resv.Balance("USDT")
	if !avail.Equal(d("9700")) {
		t.Fatalf("ledger = %s", avail)
	}
	snap := port.TakeSnapshot(t0, nil)
	if snap.Failed != 1 || !snap.Exposure["BTC"].Equal(d("0.01")) {
		t.Fatalf("portfolio = %+v", snap)
	}
	if err := resv.CheckInvariants(); err != nil {
		t.Fatal(err)
	}
}

func TestRejectedSimulationReleasesReservation(t *testing.T) {
	exec := &scriptedExecutor{result: func(plan execution.CyclePlan) execution.CycleResult {
		return execution.CycleResult{
			CycleID: plan.CycleID, Outcome: execution.OutcomeRejected,
			StartAsset: "USDT",
			Exposure:   map[exchange.Asset]decimal.Decimal{},
			Fees:       map[exchange.Asset]decimal.Decimal{},
		}
	}}
	e, in, resv, _ := harness(t, exec)
	cancel, wait := runEngine(t, e)
	defer wait()
	defer cancel()

	in <- qualifiedEvent("op-r", "250")
	waitFor(t, func() bool { return e.Snapshot().Skipped == 1 })
	avail, reserved := resv.Balance("USDT")
	if !avail.Equal(d("10000")) || !reserved.IsZero() {
		t.Fatalf("rejected cycle leaked capital: %s/%s", avail, reserved)
	}
}

// Distinct triangles (no shared conflict keys) must run concurrently but
// never beyond the cap.
func TestConcurrencyBounded(t *testing.T) {
	exec := &scriptedExecutor{result: completed, block: make(chan struct{})}
	e, in, _, _ := harness(t, exec)
	e.MaxConcurrent = 2
	for i := 0; i < 6; i++ {
		tri := triangle()
		tri.ID = fmt.Sprintf("tri-%d", i)
		for l := range tri.Legs {
			tri.Legs[l].Market.Symbol = exchange.Symbol(fmt.Sprintf("%s%d", tri.Legs[l].Market.Symbol, i))
		}
		e.Triangles[tri.ID] = tri
	}
	var results atomic.Int32
	e.OnResult = func(execution.CycleResult) { results.Add(1) }
	cancel, wait := runEngine(t, e)
	defer wait()
	defer cancel()

	for i := 0; i < 6; i++ {
		ev := qualifiedEvent(fmt.Sprintf("op-c%d", i), "100")
		ev.Opportunity.TriangleID = fmt.Sprintf("tri-%d", i)
		in <- ev
	}
	waitFor(t, func() bool { return exec.inFlight.Load() == 2 })
	close(exec.block)
	waitFor(t, func() bool { return results.Load() == 6 })
	if exec.peak.Load() > 2 {
		t.Fatalf("concurrency peak = %d, cap 2", exec.peak.Load())
	}
	if e.Active() != 0 {
		t.Fatalf("active = %d after drain", e.Active())
	}
}

// The same triangle's opportunities serialize through conflict keys: with
// one blocked in flight, the second is skipped, never double-consuming
// the same displayed depth.
func TestSameTriangleSerializedByConflictKeys(t *testing.T) {
	exec := &scriptedExecutor{result: completed, block: make(chan struct{})}
	e, in, _, _ := harness(t, exec)
	cancel, wait := runEngine(t, e)
	defer wait()
	defer cancel()

	in <- qualifiedEvent("op-s1", "100")
	waitFor(t, func() bool { return exec.inFlight.Load() == 1 })
	in <- qualifiedEvent("op-s2", "100")
	waitFor(t, func() bool { return e.Snapshot().Skipped == 1 })
	close(exec.block)
	waitFor(t, func() bool { return e.Snapshot().Completed == 1 })
}

// Acceptance (BL-10): Reset refuses while running, refuses with a
// simulation still in flight even when paused, and otherwise rebuilds
// the ledger/portfolio and zeroes the session counters.
func TestResetRequiresIdleEngine(t *testing.T) {
	exec := &scriptedExecutor{result: completed, block: make(chan struct{})}
	e, in, resv, port := harness(t, exec)
	cancel, wait := runEngine(t, e)
	defer wait()
	defer cancel()

	if err := e.Reset(map[exchange.Asset]decimal.Decimal{"USDT": d("5000")}); !errors.Is(err, ErrActive) {
		t.Fatalf("reset while running = %v, want ErrActive", err)
	}

	in <- qualifiedEvent("op-r1", "100")
	waitFor(t, func() bool { return exec.inFlight.Load() == 1 })
	e.Pause()
	if err := e.Reset(map[exchange.Asset]decimal.Decimal{"USDT": d("5000")}); !errors.Is(err, ErrActive) {
		t.Fatalf("reset with a simulation in flight = %v, want ErrActive", err)
	}
	close(exec.block)
	waitFor(t, func() bool { return e.Snapshot().Completed == 1 })

	if err := e.Reset(map[exchange.Asset]decimal.Decimal{"USDT": d("5000")}); err != nil {
		t.Fatalf("reset when idle: %v", err)
	}
	if err := resv.CheckInvariants(); err != nil {
		t.Fatalf("invariants after reset: %v", err)
	}
	avail, reserved := resv.Balance("USDT")
	if !avail.Equal(d("5000")) || !reserved.IsZero() {
		t.Fatalf("post-reset balance = %s/%s", avail, reserved)
	}
	snap := port.TakeSnapshot(t0, nil)
	if snap.Cycles != 0 || snap.Completed != 0 {
		t.Fatalf("post-reset portfolio snapshot = %+v", snap)
	}
	st := e.Snapshot()
	if st != (Stats{}) {
		t.Fatalf("post-reset stats = %+v, want zero", st)
	}
}

// --- pre-execution revalidation and ledger invariants ------------------------

// A plan the revalidation refuses is skipped before any capital is
// reserved and never reaches the executor; the reject hook fires.
func TestRevalidationRejectSkipsWithoutReserving(t *testing.T) {
	exec := &scriptedExecutor{result: completed}
	e, in, resv, _ := harness(t, exec)
	var rejected atomic.Int32
	var reason atomic.Value
	e.Revalidate = func(op opportunity.Opportunity) (opportunity.Opportunity, risk.Decision, bool) {
		return op, risk.Decision{ReasonCode: risk.ReasonMinEdge}, true
	}
	e.OnRevalidationReject = func(_ opportunity.Opportunity, dec risk.Decision) {
		rejected.Add(1)
		reason.Store(dec.ReasonCode)
	}
	cancel, wait := runEngine(t, e)
	in <- qualifiedEvent("op-1", "1000")
	waitFor(t, func() bool { return e.Snapshot().RevalidationRejected == 1 })
	cancel()
	wait()

	st := e.Snapshot()
	if st.Started != 0 || st.Skipped != 1 || st.Revalidated != 1 {
		t.Fatalf("stats = %+v", st)
	}
	if avail, reserved := resv.Balance("USDT"); !avail.Equal(d("10000")) || !reserved.IsZero() {
		t.Fatalf("ledger touched: available=%s reserved=%s", avail, reserved)
	}
	if exec.peak.Load() != 0 {
		t.Fatal("executor ran a refused plan")
	}
	if rejected.Load() != 1 || reason.Load() != risk.ReasonMinEdge {
		t.Fatalf("reject hook: calls=%d reason=%v", rejected.Load(), reason.Load())
	}
}

// The executor receives the re-priced plan, and the ledger settles on
// the re-priced input.
func TestRevalidationRefreshesThePlan(t *testing.T) {
	var seen atomic.Value
	exec := &scriptedExecutor{result: func(plan execution.CyclePlan) execution.CycleResult {
		seen.Store(plan.Opportunity.Quote.InputConsumed.String())
		return completed(plan)
	}}
	e, in, resv, _ := harness(t, exec)
	e.Revalidate = func(op opportunity.Opportunity) (opportunity.Opportunity, risk.Decision, bool) {
		fresh := op
		fresh.Quote.InputConsumed = d("900")
		fresh.Quote.FinalAmount = d("904")
		return fresh, risk.Decision{Allowed: true}, true
	}
	cancel, wait := runEngine(t, e)
	in <- qualifiedEvent("op-1", "1000")
	waitFor(t, func() bool { return e.Snapshot().Completed == 1 })
	cancel()
	wait()

	if seen.Load() != "900" {
		t.Fatalf("executor saw input %v, want the re-priced 900", seen.Load())
	}
	// completed() returns input + 4: 10000 - 900 + 904.
	if avail, _ := resv.Balance("USDT"); !avail.Equal(d("10004")) {
		t.Fatalf("available = %s, want 10004", avail)
	}
}

// A revalidation that cannot run (unknown triangle, missing book) is a
// rejection, never a pass.
func TestRevalidationUnavailableIsARejection(t *testing.T) {
	exec := &scriptedExecutor{result: completed}
	e, in, _, _ := harness(t, exec)
	var reason atomic.Value
	e.Revalidate = func(op opportunity.Opportunity) (opportunity.Opportunity, risk.Decision, bool) {
		return op, risk.Decision{}, false
	}
	e.OnRevalidationReject = func(_ opportunity.Opportunity, dec risk.Decision) { reason.Store(dec.ReasonCode) }
	cancel, wait := runEngine(t, e)
	in <- qualifiedEvent("op-1", "1000")
	waitFor(t, func() bool { return e.Snapshot().RevalidationRejected == 1 })
	cancel()
	wait()
	if exec.peak.Load() != 0 || reason.Load() != risk.ReasonRevalidation {
		t.Fatalf("executor ran=%v reason=%v", exec.peak.Load() != 0, reason.Load())
	}
}

// A ledger invariant failure after a settlement pauses the engine before
// another cycle can start and reports through the hook.
func TestInvariantViolationPausesEngine(t *testing.T) {
	exec := &scriptedExecutor{result: completed}
	e, in, _, _ := harness(t, exec)
	var got atomic.Value
	e.Invariants = func() error { return errors.New("conservation broken") }
	e.OnInvariantViolation = func(err error) { got.Store(err.Error()) }
	cancel, wait := runEngine(t, e)
	in <- qualifiedEvent("op-1", "1000")
	waitFor(t, func() bool { return e.Snapshot().InvariantViolations == 1 })
	if e.Running() {
		t.Fatal("engine still running after an invariant violation")
	}
	in <- qualifiedEvent("op-2", "1000")
	waitFor(t, func() bool { return e.Snapshot().Received == 2 })
	cancel()
	wait()

	st := e.Snapshot()
	if st.Started != 1 || st.Skipped != 1 {
		t.Fatalf("second cycle ran on a broken ledger: %+v", st)
	}
	if got.Load() != "conservation broken" {
		t.Fatalf("hook = %v", got.Load())
	}
}

// The default check is the ledger's own; a healthy settlement passes it.
func TestDefaultInvariantCheckPassesOnHealthySettlement(t *testing.T) {
	exec := &scriptedExecutor{result: completed}
	e, in, _, _ := harness(t, exec)
	cancel, wait := runEngine(t, e)
	in <- qualifiedEvent("op-1", "1000")
	waitFor(t, func() bool { return e.Snapshot().Completed == 1 })
	cancel()
	wait()
	if st := e.Snapshot(); st.InvariantViolations != 0 || !e.Running() {
		t.Fatalf("healthy settlement flagged: %+v running=%v", st, e.Running())
	}
}
