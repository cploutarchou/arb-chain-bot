package portfolio

import (
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/exchange"
	"github.com/cploutarchou/arb-chain-bot/internal/execution"
	"github.com/cploutarchou/arb-chain-bot/internal/orderbook"
	"github.com/cploutarchou/arb-chain-bot/internal/reservation"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

var t0 = time.Unix(1_700_000_000, 0)

func newResv(usdt string) *reservation.Manager {
	var seq atomic.Int64
	return reservation.New(
		map[exchange.Asset]decimal.Decimal{"USDT": d(usdt)},
		func() string { return fmt.Sprintf("r-%d", seq.Add(1)) },
		func() time.Time { return t0 },
	)
}

func completedCycle(id string, consumed, final string) execution.CycleResult {
	res := execution.CycleResult{
		CycleID: id, Outcome: execution.OutcomeAllFilled, StartAsset: "USDT",
		InputConsumed: d(consumed), FinalAmount: d(final),
		Exposure: map[exchange.Asset]decimal.Decimal{},
		Fees:     map[exchange.Asset]decimal.Decimal{"USDT": d("0.5")},
	}
	res.RealizedPnL = res.FinalAmount.Sub(res.InputConsumed)
	res.TotalPnL = res.RealizedPnL
	return res
}

// The full settlement flow: reserve → settle+credit → apply. Cash and
// portfolio must reconcile exactly across a mixed history.
func TestReconciliationAcrossCycles(t *testing.T) {
	resv := newResv("10000")
	p := New(resv, map[exchange.Asset]decimal.Decimal{"USDT": d("10000")})

	settle := func(key string, res execution.CycleResult) {
		r, err := resv.Reserve(key, "USDT", res.InputConsumed.Add(d("1")), "tri", nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := resv.Settle(r.ID, res.InputConsumed); err != nil {
			t.Fatal(err)
		}
		if res.FinalAmount.IsPositive() {
			if err := resv.Credit("USDT", res.FinalAmount); err != nil {
				t.Fatal(err)
			}
		}
		if err := p.ApplyCycle(res, false); err != nil {
			t.Fatal(err)
		}
	}

	// Win +20, loss -10, failed leg2 (consumed 100, exposure 0.001 BTC).
	settle("c1", completedCycle("c1", "1000", "1020"))
	settle("c2", completedCycle("c2", "500", "490"))
	fail := execution.CycleResult{
		CycleID: "c3", Outcome: execution.OutcomeLeg1FilledLeg2Failed, StartAsset: "USDT",
		InputConsumed: d("100"), FinalAmount: d("0"),
		RealizedPnL: d("-100"),
		Exposure:    map[exchange.Asset]decimal.Decimal{"BTC": d("0.001")},
		Fees:        map[exchange.Asset]decimal.Decimal{"BTC": d("0.000001")},
	}
	fail.TotalPnL = fail.RealizedPnL
	settle("c3", fail)

	if err := resv.CheckInvariants(); err != nil {
		t.Fatal(err)
	}
	avail, reserved := resv.Balance("USDT")
	cash := avail.Add(reserved)
	// 10000 - 1000 + 1020 - 500 + 490 - 100 = 9910.
	if !cash.Equal(d("9910")) {
		t.Fatalf("cash = %s", cash)
	}
	// Portfolio realized: +20 -10 -100 = -90; initial + realized = cash + unreturned exposure value basis:
	// realized(-90) = cash(9910) - initial(10000) → exact reconciliation.
	snap := p.TakeSnapshot(t0, nil)
	if !snap.Realized["USDT"].Equal(d("-90")) {
		t.Fatalf("realized = %s", snap.Realized["USDT"])
	}
	if !cash.Sub(d("10000")).Equal(snap.Realized["USDT"]) {
		t.Fatalf("cash delta %s != realized %s", cash.Sub(d("10000")), snap.Realized["USDT"])
	}
	if !snap.Exposure["BTC"].Equal(d("0.001")) {
		t.Fatalf("exposure = %v", snap.Exposure)
	}
	if snap.Cycles != 3 || snap.Completed != 2 || snap.Failed != 1 {
		t.Fatalf("counts = %d/%d/%d", snap.Cycles, snap.Completed, snap.Failed)
	}
	if !snap.Fees["USDT"].Equal(d("1")) || !snap.Fees["BTC"].Equal(d("0.000001")) {
		t.Fatalf("fees = %v", snap.Fees)
	}
	// No marker: BTC exposure is listed as unmarked, never silently zeroed.
	if len(snap.Unmarked) != 1 || snap.Unmarked[0] != "BTC" {
		t.Fatalf("unmarked = %v", snap.Unmarked)
	}
}

func TestEquityAndDrawdownWithMarks(t *testing.T) {
	resv := newResv("1000")
	p := New(resv, map[exchange.Asset]decimal.Decimal{"USDT": d("1000")})

	// Set up a BTC/USDT book for marking: bid 100.
	books := orderbook.NewSet()
	id := exchange.MarketID{Exchange: "binance", Symbol: "BTCUSDT"}
	b := orderbook.New(id, 0)
	b.ApplySnapshot(orderbook.DepthEvent{Market: id, IsSnapshot: true, FinalUpdateID: 1,
		Bids: []orderbook.Level{{Price: d("100"), Qty: d("10")}},
		Asks: []orderbook.Level{{Price: d("101"), Qty: d("10")}}})
	books.Add(b)
	marker := BookMarker{Books: books, Markets: []exchange.Market{{
		ID: id, Base: "BTC", Quote: "USDT",
	}}}

	// A failed cycle: 200 USDT deployed, 1.5 BTC stranded.
	r, _ := resv.Reserve("c1", "USDT", d("200"), "tri", nil)
	_ = resv.Settle(r.ID, d("200"))
	res := execution.CycleResult{
		CycleID: "c1", Outcome: execution.OutcomeLeg1FilledLeg2Failed, StartAsset: "USDT",
		InputConsumed: d("200"), RealizedPnL: d("-200"),
		Exposure: map[exchange.Asset]decimal.Decimal{"BTC": d("1.5")},
		Fees:     map[exchange.Asset]decimal.Decimal{},
	}
	_ = p.ApplyCycle(res, false)

	snap := p.TakeSnapshot(t0, marker)
	// Equity = cash 800 + 1.5 BTC * 100 = 950. Peak was 1000 → dd 5%.
	if !snap.Equity["USDT"].Equal(d("950")) {
		t.Fatalf("equity = %s", snap.Equity["USDT"])
	}
	if !snap.Drawdown["USDT"].Equal(d("0.05")) {
		t.Fatalf("drawdown = %s", snap.Drawdown["USDT"])
	}
	// Unwind: sell the BTC at 100 → credit 150; exposure cleared.
	_ = resv.Credit("USDT", d("150"))
	p.ReduceExposure("BTC", d("1.5"))
	snap = p.TakeSnapshot(t0.Add(time.Minute), marker)
	if !snap.Equity["USDT"].Equal(d("950")) || len(snap.Exposure) != 0 {
		t.Fatalf("post-unwind equity=%s exposure=%v", snap.Equity["USDT"], snap.Exposure)
	}
	// Drawdown is a high-water ratchet: it never shrinks.
	if !snap.Drawdown["USDT"].Equal(d("0.05")) {
		t.Fatalf("drawdown after recovery = %s", snap.Drawdown["USDT"])
	}
}

func TestDailyLossOnlyCountsLosses(t *testing.T) {
	resv := newResv("1000")
	p := New(resv, map[exchange.Asset]decimal.Decimal{"USDT": d("1000")})
	win := completedCycle("c1", "100", "110")
	_ = p.ApplyCycle(win, false)
	if !p.DailyLoss("USDT").IsZero() {
		t.Fatalf("profit counted as loss: %s", p.DailyLoss("USDT"))
	}
	loss := completedCycle("c2", "100", "70")
	_ = p.ApplyCycle(loss, false)
	// Net realized = +10 - 30 = -20 → loss magnitude 20.
	if !p.DailyLoss("USDT").Equal(d("20")) {
		t.Fatalf("daily loss = %s", p.DailyLoss("USDT"))
	}
}

func TestShadowResultsRefused(t *testing.T) {
	p := New(newResv("1000"), map[exchange.Asset]decimal.Decimal{"USDT": d("1000")})
	if err := p.ApplyCycle(completedCycle("s1", "10", "11"), true); !errors.Is(err, ErrShadowResult) {
		t.Fatalf("shadow applied: %v", err)
	}
}

func TestBookMarkerInversePair(t *testing.T) {
	books := orderbook.NewSet()
	id := exchange.MarketID{Exchange: "binance", Symbol: "ETHBTC"}
	b := orderbook.New(id, 0)
	b.ApplySnapshot(orderbook.DepthEvent{Market: id, IsSnapshot: true, FinalUpdateID: 1,
		Bids: []orderbook.Level{{Price: d("0.05"), Qty: d("10")}},
		Asks: []orderbook.Level{{Price: d("0.05"), Qty: d("10")}}})
	books.Add(b)
	m := BookMarker{Books: books, Markets: []exchange.Market{{ID: id, Base: "ETH", Quote: "BTC"}}}

	// ETH → BTC: direct (bid): 2 ETH * 0.05 = 0.1 BTC.
	v, ok := m.Mark("ETH", d("2"), "BTC")
	if !ok || !v.Equal(d("0.1")) {
		t.Fatalf("direct mark = %s %v", v, ok)
	}
	// BTC → ETH: inverse (ask): 0.1 BTC / 0.05 = 2 ETH.
	v, ok = m.Mark("BTC", d("0.1"), "ETH")
	if !ok || !v.Equal(d("2")) {
		t.Fatalf("inverse mark = %s %v", v, ok)
	}
	// Identity and unmarkable.
	if v, ok := m.Mark("BTC", d("3"), "BTC"); !ok || !v.Equal(d("3")) {
		t.Fatalf("identity = %s %v", v, ok)
	}
	if _, ok := m.Mark("DOGE", d("1"), "BTC"); ok {
		t.Fatal("unmarkable asset marked")
	}
}

// Acceptance (BL-10): Reset clears realized P&L, exposure, fees, and
// cycle counters, and restarts the high-water mark at the new initial
// balances (not zero) so drawdown is not fabricated on the next snapshot.
func TestResetRestartsSession(t *testing.T) {
	resv := newResv("10000")
	p := New(resv, map[exchange.Asset]decimal.Decimal{"USDT": d("10000")})

	if err := p.ApplyCycle(completedCycle("c1", "1000", "1004"), false); err != nil {
		t.Fatal(err)
	}
	p.exposure["BTC"] = d("0.01") // simulate stranded exposure directly
	snap := p.TakeSnapshot(t0, nil)
	if snap.Cycles != 1 || !snap.Realized["USDT"].Equal(d("4")) {
		t.Fatalf("pre-reset snapshot = %+v", snap)
	}

	// A real paper reset resets the cash ledger and the portfolio
	// together (BL-10's ResetPaper does both); do the same here so
	// equity matches the new peak and drawdown is not fabricated purely
	// by the cash side lagging the portfolio side.
	resv.Reset(map[exchange.Asset]decimal.Decimal{"USDT": d("20000")})
	p.Reset(map[exchange.Asset]decimal.Decimal{"USDT": d("20000")})

	snap = p.TakeSnapshot(t0, nil)
	if snap.Cycles != 0 || snap.Completed != 0 || snap.Failed != 0 {
		t.Fatalf("post-reset counters = %+v", snap)
	}
	if !snap.Realized["USDT"].IsZero() {
		t.Fatalf("post-reset realized = %s", snap.Realized["USDT"])
	}
	if len(snap.Exposure) != 0 {
		t.Fatalf("post-reset exposure survived: %v", snap.Exposure)
	}
	if !snap.Equity["USDT"].Equal(d("20000")) {
		t.Fatalf("post-reset equity = %s, want 20000", snap.Equity["USDT"])
	}
	if dd := snap.Drawdown["USDT"]; !dd.IsZero() {
		t.Fatalf("fabricated drawdown after reset: %s", dd)
	}
}
