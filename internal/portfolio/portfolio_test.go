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
	"github.com/cploutarchou/arb-chain-bot/internal/fees"
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
	if !p.DailyLoss("USDT", nil).IsZero() {
		t.Fatalf("profit counted as loss: %s", p.DailyLoss("USDT", nil))
	}
	loss := completedCycle("c2", "100", "70")
	_ = p.ApplyCycle(loss, false)
	// Net realized = +10 - 30 = -20 → loss magnitude 20.
	if !p.DailyLoss("USDT", nil).Equal(d("20")) {
		t.Fatalf("daily loss = %s", p.DailyLoss("USDT", nil))
	}
}

// A mid-cycle failure strands the deployed input in an intermediate
// asset. Cash-basis realized books the whole input as a loss; the loss a
// risk limit and the console should see is the marked one — the input
// minus what the stranded asset is worth. Unmarkable exposure stays at
// zero, which is the conservative side.
func TestDailyLossNetsMarkedExposure(t *testing.T) {
	p := New(newResv("1000"), map[exchange.Asset]decimal.Decimal{"USDT": d("1000")})
	failed := execution.CycleResult{
		CycleID: "f1", Outcome: execution.OutcomeLeg1FilledLeg2Failed, StartAsset: "USDT",
		InputConsumed: d("100"), FinalAmount: decimal.Zero,
		Exposure: map[exchange.Asset]decimal.Decimal{"BTC": d("0.001")},
		Fees:     map[exchange.Asset]decimal.Decimal{},
	}
	failed.RealizedPnL = failed.FinalAmount.Sub(failed.InputConsumed)
	_ = p.ApplyCycle(failed, false)

	// Cash basis: the full 100 left the start asset.
	if !p.Realized("USDT").Equal(d("-100")) || !p.DailyLoss("USDT", nil).Equal(d("100")) {
		t.Fatalf("cash basis: realized %s loss %s", p.Realized("USDT"), p.DailyLoss("USDT", nil))
	}
	// Marked: 0.001 BTC is worth 99 USDT, so the economic loss is 1.
	marker := fakeMarker{"BTC": d("99000")}
	mark, unmarked := p.ExposureMark("USDT", marker)
	if !mark.Equal(d("99")) || len(unmarked) != 0 {
		t.Fatalf("mark = %s unmarked = %v", mark, unmarked)
	}
	net, _ := p.NetPnL("USDT", marker)
	if !net.Equal(d("-1")) || !p.DailyLoss("USDT", marker).Equal(d("1")) {
		t.Fatalf("net = %s loss = %s", net, p.DailyLoss("USDT", marker))
	}
	// A marker that cannot value BTC reports it and falls back to the
	// cash-basis loss.
	mark, unmarked = p.ExposureMark("USDT", fakeMarker{})
	if !mark.IsZero() || len(unmarked) != 1 || unmarked[0] != "BTC" {
		t.Fatalf("unmarkable: mark = %s unmarked = %v", mark, unmarked)
	}
	if !p.DailyLoss("USDT", fakeMarker{}).Equal(d("100")) {
		t.Fatalf("unmarkable loss = %s", p.DailyLoss("USDT", fakeMarker{}))
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

// fakeMarker values an asset in the start asset at a fixed rate; assets
// absent from the map are unmarkable.
type fakeMarker map[exchange.Asset]decimal.Decimal

func (f fakeMarker) Mark(asset exchange.Asset, amount decimal.Decimal, _ exchange.Asset) (decimal.Decimal, bool) {
	rate, ok := f[asset]
	if !ok {
		return decimal.Zero, false
	}
	return amount.Mul(rate), true
}

// Restore reproduces a session's accounting state exactly; a start asset
// without a persisted peak keeps its initial balance as the mark.
func TestStateRestoreRoundTrip(t *testing.T) {
	cash := newResv("10000")
	if r, err := cash.Reserve("op-1", "USDT", d("1000"), "tri", nil); err != nil {
		t.Fatal(err)
	} else if err := cash.Settle(r.ID, d("1000")); err != nil {
		t.Fatal(err)
	}
	p := New(cash, map[exchange.Asset]decimal.Decimal{"USDT": d("10000")})
	if err := p.ApplyCycle(execution.CycleResult{
		Outcome: execution.OutcomeLeg1FilledLeg2Failed, StartAsset: "USDT",
		InputConsumed: d("1000"), RealizedPnL: d("-1000"),
		Exposure: map[exchange.Asset]decimal.Decimal{"BTC": d("0.01")},
		Fees:     map[exchange.Asset]decimal.Decimal{"BTC": d("0.00001")},
	}, false); err != nil {
		t.Fatal(err)
	}
	p.TakeSnapshot(time.Now(), nil)
	st := p.State()
	if !st.Realized["USDT"].Equal(d("-1000")) || !st.Exposure["BTC"].Equal(d("0.01")) ||
		!st.Peak["USDT"].Equal(d("10000")) || !st.Drawdown["USDT"].Equal(d("0.1")) || st.Cycles != 1 || st.Failed != 1 {
		t.Fatalf("state = %+v", st)
	}

	q := New(cash, map[exchange.Asset]decimal.Decimal{"USDT": d("10000"), "BTC": d("1")})
	q.Restore(st)
	if got := q.State(); !got.Realized["USDT"].Equal(st.Realized["USDT"]) || !got.Exposure["BTC"].Equal(st.Exposure["BTC"]) ||
		!got.Fees["BTC"].Equal(st.Fees["BTC"]) || !got.Peak["USDT"].Equal(d("10000")) || !got.Peak["BTC"].Equal(d("1")) ||
		!got.Drawdown["USDT"].Equal(d("0.1")) || got.Cycles != 1 || got.Failed != 1 {
		t.Fatalf("restored = %+v", got)
	}
	// Mutating the returned state must not touch the portfolio.
	st.Realized["USDT"] = d("0")
	if !q.Realized("USDT").Equal(d("-1000")) {
		t.Fatal("State returned a shared map")
	}
}

// With a fee schedule the mark is a liquidation value: the position is
// walked through the depth net of the taker fee, and depth the book
// does not show counts for nothing (audit F16).
func TestBookMarkerLiquidationValue(t *testing.T) {
	books := orderbook.NewSet()
	id := exchange.MarketID{Exchange: "binance", Symbol: "ETHUSDT"}
	b := orderbook.New(id, 0)
	b.ApplySnapshot(orderbook.DepthEvent{Market: id, IsSnapshot: true, FinalUpdateID: 1,
		Bids: []orderbook.Level{{Price: d("100"), Qty: d("1")}, {Price: d("99"), Qty: d("1")}},
		Asks: []orderbook.Level{{Price: d("101"), Qty: d("1")}}})
	books.Add(b)
	sched, err := fees.NewSchedule("binance", exchange.FeeInReceived, fees.Rate{Maker: d("0.001"), Taker: d("0.001")})
	if err != nil {
		t.Fatal(err)
	}
	rules := exchange.InstrumentRules{QtyMode: exchange.PrecisionStep, QtyStep: d("0.001"),
		PriceMode: exchange.PrecisionStep, PriceTick: d("0.01"), MinNotional: d("5")}
	markets := []exchange.Market{{ID: id, Base: "ETH", Quote: "USDT", Rules: rules}}

	top := BookMarker{Books: books, Markets: markets}
	if v, ok := top.Mark("ETH", d("2"), "USDT"); !ok || !v.Equal(d("200")) {
		t.Fatalf("top-of-book mark = %s %v", v, ok)
	}
	liq := BookMarker{Books: books, Markets: markets, Fees: sched}
	// 1 ETH at 100 + 1 ETH at 99 = 199 USDT, minus the 0.1% taker fee.
	if v, ok := liq.Mark("ETH", d("2"), "USDT"); !ok || !v.Equal(d("198.801")) {
		t.Fatalf("liquidation mark = %s %v, want 198.801", v, ok)
	}
	// Beyond the visible depth only the fillable part is valued.
	if v, ok := liq.Mark("ETH", d("5"), "USDT"); !ok || !v.Equal(d("198.801")) {
		t.Fatalf("mark past depth = %s %v, want 198.801", v, ok)
	}
	// Inverse pair: 202 USDT buys 2 ETH at 101, fee in ETH.
	if v, ok := liq.Mark("USDT", d("101"), "ETH"); !ok || !v.Equal(d("0.999")) {
		t.Fatalf("inverse liquidation mark = %s %v, want 0.999", v, ok)
	}
	// Dust the quantity step cannot express falls back to the top level.
	if v, ok := liq.Mark("ETH", d("0.0001"), "USDT"); !ok || !v.Equal(d("0.01")) {
		t.Fatalf("dust mark = %s %v", v, ok)
	}
}

// Fees charged in intermediate assets are valued in the start asset;
// the raw map stays visible and unmarkable fee assets are listed.
func TestFeesMarkValuesEveryFeeAsset(t *testing.T) {
	p := New(newResv("10000"), map[exchange.Asset]decimal.Decimal{"USDT": d("10000")})
	res := completedCycle("c1", "1000", "1002")
	res.Fees = map[exchange.Asset]decimal.Decimal{"BTC": d("0.00001"), "ETH": d("0.001"), "USDT": d("1.02"), "DOGE": d("5")}
	if err := p.ApplyCycle(res, false); err != nil {
		t.Fatal(err)
	}
	total, byAsset, unmarked := p.FeesMark("USDT", fakeMarker{"BTC": d("100000"), "ETH": d("2000")})
	// 0.00001 BTC = 1, 0.001 ETH = 2, USDT 1.02 itself; DOGE unmarkable.
	if !total.Equal(d("4.02")) {
		t.Fatalf("fees marked = %s, want 4.02", total)
	}
	if !byAsset["DOGE"].Equal(d("5")) || len(byAsset) != 4 {
		t.Fatalf("raw fee map = %v", byAsset)
	}
	if len(unmarked) != 1 || unmarked[0] != "DOGE" {
		t.Fatalf("unmarked = %v", unmarked)
	}
	// Without a marker only the start asset's own slice is valued.
	if total, _, unmarked := p.FeesMark("USDT", nil); !total.Equal(d("1.02")) || len(unmarked) != 3 {
		t.Fatalf("nil marker: total=%s unmarked=%v", total, unmarked)
	}
}
