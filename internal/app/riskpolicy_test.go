package app

import (
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/exchange"
	"github.com/cploutarchou/arb-chain-bot/internal/execution"
	"github.com/cploutarchou/arb-chain-bot/internal/orderbook"
	"github.com/cploutarchou/arb-chain-bot/internal/portfolio"
	"github.com/cploutarchou/arb-chain-bot/internal/reservation"
	"github.com/cploutarchou/arb-chain-bot/internal/risk"
)

var policyT0 = time.Unix(1_700_000_000, 0)

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

// One transport loss marks every book at once and counts as one event;
// repeated faults inside the window open the exchange breaker; the
// policy probes it, reopens on a fault during the probe, and closes it
// after a quiet probe window.
func TestFeedFaultsOpenProbeAndCloseTheBreaker(t *testing.T) {
	reg := risk.NewRegistry(nil)
	scope := "exchange:binance"
	reg.Register(breakerFeed, scope, feedProbeAfter)
	faults := newFaultWindow(feedFaultWindow)
	now := policyT0
	obs := feedFaultObserver(reg, faults, func() time.Time { return now })
	pol := &feedPolicy{reg: reg, faults: faults, scope: scope}
	id := exchange.MarketID{Exchange: "binance", Symbol: "BTCUSDT"}

	for i := 0; i < 20; i++ {
		obs(id, orderbook.StateHealthy, orderbook.StateDisconnected, "transport lost")
	}
	if reg.AnyOpen(scope) {
		t.Fatal("a single disconnect must not open the breaker")
	}
	if faults.count(now) != 1 {
		t.Fatalf("coalesced faults = %d, want 1", faults.count(now))
	}
	// STALE and HEALTHY transitions are not faults.
	obs(id, orderbook.StateHealthy, orderbook.StateStale, "age exceeded")
	obs(id, orderbook.StateSyncing, orderbook.StateHealthy, "snapshot")
	if faults.count(now) != 1 {
		t.Fatalf("non-fault transitions counted: %d", faults.count(now))
	}

	for i := 1; i < feedFaultThreshold; i++ {
		now = policyT0.Add(time.Duration(i) * 5 * time.Second)
		obs(id, orderbook.StateHealthy, orderbook.StateCorrupted, "sequence gap")
	}
	if !reg.AnyOpen(scope) {
		t.Fatalf("%d faults inside the window did not open the breaker", feedFaultThreshold)
	}
	openedAt := now

	// Not yet eligible to probe.
	now = openedAt.Add(feedProbeAfter / 2)
	pol.tick(now)
	if !reg.AnyOpen(scope) {
		t.Fatal("probed before feedProbeAfter")
	}
	// Eligible: HALF_OPEN does not gate.
	now = openedAt.Add(feedProbeAfter)
	pol.tick(now)
	if reg.AnyOpen(scope) {
		t.Fatal("HALF_OPEN must not gate qualification")
	}
	if st, _ := breakerState(reg, breakerFeed, scope); st != risk.BreakerHalfOpen {
		t.Fatalf("state = %s", st)
	}
	// A fault during the probe reopens it.
	now = now.Add(time.Second)
	obs(id, orderbook.StateHealthy, orderbook.StateCorrupted, "sequence gap")
	pol.tick(now)
	if !reg.AnyOpen(scope) {
		t.Fatal("fault during the probe did not reopen the breaker")
	}
	reopenedAt := now

	// Quiet: probe again, then close after a quiet probe window. The
	// earlier faults are still inside the 60 s window but predate the
	// probe, so they must not count against it.
	now = reopenedAt.Add(feedProbeAfter)
	pol.tick(now)
	if st, _ := breakerState(reg, breakerFeed, scope); st != risk.BreakerHalfOpen {
		t.Fatalf("second probe: state = %s", st)
	}
	now = now.Add(feedProbeAfter)
	pol.tick(now)
	if st, _ := breakerState(reg, breakerFeed, scope); st != risk.BreakerClosed {
		t.Fatalf("quiet probe window did not close the breaker: %s", st)
	}
}

func TestFaultWindowPrunes(t *testing.T) {
	w := newFaultWindow(time.Minute)
	for i := 0; i < 3; i++ {
		w.add(policyT0.Add(time.Duration(i) * 10 * time.Second))
	}
	if w.count(policyT0.Add(25*time.Second)) != 3 {
		t.Fatalf("count = %d", w.count(policyT0.Add(25*time.Second)))
	}
	if w.count(policyT0.Add(75*time.Second)) != 1 {
		t.Fatalf("after pruning count = %d, want 1", w.count(policyT0.Add(75*time.Second)))
	}
	if w.count(policyT0.Add(2*time.Minute)) != 0 {
		t.Fatal("window not emptied")
	}
}

// The session ledger reports the marked loss and drawdown the risk gate
// reads, and opens the loss breaker at the limit.
func TestSessionLedgerOpensLossBreakerAtLimit(t *testing.T) {
	var seq atomic.Int64
	resv := reservation.New(map[exchange.Asset]decimal.Decimal{"USDT": dec("1000")},
		func() string { return fmt.Sprintf("r-%d", seq.Add(1)) }, func() time.Time { return policyT0 })
	port := portfolio.New(resv, map[exchange.Asset]decimal.Decimal{"USDT": dec("1000")})

	// A mid-cycle failure: 100 deployed, nothing back, nothing markable.
	r, err := resv.Reserve("op-1", "USDT", dec("100"), "tri", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := resv.Settle(r.ID, dec("100")); err != nil {
		t.Fatal(err)
	}
	res := execution.CycleResult{
		CycleID: "c1", Outcome: execution.OutcomeLeg1FilledLeg2Failed, StartAsset: "USDT",
		InputConsumed: dec("100"), RealizedPnL: dec("-100"), TotalPnL: dec("-100"),
		Exposure: map[exchange.Asset]decimal.Decimal{}, Fees: map[exchange.Asset]decimal.Decimal{},
	}
	if err := port.ApplyCycle(res, false); err != nil {
		t.Fatal(err)
	}

	reg := risk.NewRegistry(nil)
	ledger := newSessionLedger()
	starts := []exchange.Asset{"USDT"}

	// Below the limit: figures reported, nothing opens.
	ledger.refresh(policyT0, port, nil, starts, risk.Limits{MaxDailyLoss: dec("150"), MaxDrawdown: dec("0.5")}, reg)
	if !ledger.DailyLoss("USDT").Equal(dec("100")) {
		t.Fatalf("loss = %s", ledger.DailyLoss("USDT"))
	}
	if !ledger.Drawdown("USDT").Equal(dec("0.1")) {
		t.Fatalf("drawdown = %s, want 0.1 (equity 900 from a 1000 peak)", ledger.Drawdown("USDT"))
	}
	if reg.AnyOpen() {
		t.Fatal("breaker opened below the limit")
	}

	// At the loss limit: the global breaker opens; drawdown stays closed.
	ledger.refresh(policyT0.Add(time.Second), port, nil, starts, risk.Limits{MaxDailyLoss: dec("100"), MaxDrawdown: dec("0.5")}, reg)
	if st, _ := breakerState(reg, breakerDailyLoss, ""); st != risk.BreakerOpen {
		t.Fatalf("daily_loss = %s", st)
	}
	if st, _ := breakerState(reg, breakerDrawdown, ""); st != risk.BreakerClosed {
		t.Fatalf("drawdown = %s", st)
	}
	if !reg.AnyOpen("exchange:binance", "triangle:x") {
		t.Fatal("global loss breaker does not gate")
	}

	// At the drawdown limit as well.
	ledger.refresh(policyT0.Add(2*time.Second), port, nil, starts, risk.Limits{MaxDrawdown: dec("0.1")}, reg)
	if st, _ := breakerState(reg, breakerDrawdown, ""); st != risk.BreakerOpen {
		t.Fatalf("drawdown = %s", st)
	}
	// A nil portfolio (profile without paper) is a no-op.
	ledger.refresh(policyT0, nil, nil, starts, risk.Limits{}, reg)
}
