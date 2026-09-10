package app

import (
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/exchange"
	"github.com/cploutarchou/arb-chain-bot/internal/execution"
	"github.com/cploutarchou/arb-chain-bot/internal/portfolio"
	"github.com/cploutarchou/arb-chain-bot/internal/reservation"
	"github.com/cploutarchou/arb-chain-bot/internal/storage"
)

func TestSameStartingBalances(t *testing.T) {
	configured := map[exchange.Asset]decimal.Decimal{"USDT": dec("10000"), "BTC": dec("0.5")}
	for _, tc := range []struct {
		name    string
		session map[string]string
		want    bool
	}{
		{"identical", map[string]string{"USDT": "10000", "BTC": "0.5"}, true},
		{"same value, different text", map[string]string{"USDT": "10000.0", "BTC": "0.50"}, true},
		{"changed amount", map[string]string{"USDT": "20000", "BTC": "0.5"}, false},
		{"missing asset", map[string]string{"USDT": "10000"}, false},
		{"extra asset", map[string]string{"USDT": "10000", "BTC": "0.5", "ETH": "1"}, false},
		{"unparsable", map[string]string{"USDT": "ten", "BTC": "0.5"}, false},
	} {
		if got := sameStartingBalances(tc.session, configured); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

// A snapshot restores the cash baseline, folds capital the dead process
// still had reserved back into available, and carries every portfolio
// figure across; one unparsable amount rejects the whole snapshot.
func TestPlanResumeRestoresLedgerAndFoldsReserved(t *testing.T) {
	snap := storage.LedgerSnapshot{
		SessionID: "sess-1", At: policyT0,
		Balances: map[string]storage.LedgerBalance{"USDT": {Available: "9800", Reserved: "150"}},
		Realized: map[string]string{"USDT": "-50"},
		Peak:     map[string]string{"USDT": "10000"},
		Drawdown: map[string]string{"USDT": "0.005"},
		Fees:     map[string]string{"BTC": "0.0001"},
		Exposure: map[string]string{"BTC": "0.002"},
		Cycles:   3, Completed: 2, Failed: 1,
	}
	r, err := planResume(snap)
	if err != nil {
		t.Fatal(err)
	}
	if r.SessionID != "sess-1" || !r.Balances["USDT"].Equal(dec("9950")) || !r.FoldedReserved["USDT"].Equal(dec("150")) {
		t.Fatalf("resume = %+v", r)
	}
	p := r.Portfolio
	if !p.Realized["USDT"].Equal(dec("-50")) || !p.Peak["USDT"].Equal(dec("10000")) ||
		!p.Drawdown["USDT"].Equal(dec("0.005")) || !p.Fees["BTC"].Equal(dec("0.0001")) ||
		!p.Exposure["BTC"].Equal(dec("0.002")) || p.Cycles != 3 || p.Completed != 2 || p.Failed != 1 {
		t.Fatalf("portfolio state = %+v", p)
	}

	bad := snap
	bad.Realized = map[string]string{"USDT": "minus fifty"}
	if _, err := planResume(bad); err == nil {
		t.Fatal("unparsable realized accepted")
	}
	empty := snap
	empty.Balances = nil
	if _, err := planResume(empty); err == nil {
		t.Fatal("snapshot without balances accepted")
	}
	negative := snap
	negative.Balances = map[string]storage.LedgerBalance{"USDT": {Available: "-1", Reserved: "0"}}
	if _, err := planResume(negative); err == nil {
		t.Fatal("negative balance accepted")
	}
}

// A snapshot built after a settlement round-trips through planResume
// into a portfolio that reports the same figures, and the restored
// reservation ledger still satisfies its invariants.
func TestLedgerSnapshotRoundTripRestoresPortfolio(t *testing.T) {
	var seq atomic.Int64
	ids := func() string { return fmt.Sprintf("r-%d", seq.Add(1)) }
	now := func() time.Time { return policyT0 }
	resv := reservation.New(map[exchange.Asset]decimal.Decimal{"USDT": dec("10000")}, ids, now)
	port := portfolio.New(resv, map[exchange.Asset]decimal.Decimal{"USDT": dec("10000")})
	starts := []exchange.Asset{"USDT"}

	// One losing cycle: 1000 deployed, 0.005 BTC stranded, marked at 90.
	r, err := resv.Reserve("op-1", "USDT", dec("1000"), "tri", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := resv.Settle(r.ID, dec("1000")); err != nil {
		t.Fatal(err)
	}
	if err := port.ApplyCycle(execution.CycleResult{
		CycleID: "c1", Outcome: execution.OutcomeLeg1FilledLeg2Failed, StartAsset: "USDT",
		InputConsumed: dec("1000"), RealizedPnL: dec("-1000"), TotalPnL: dec("-910"),
		Exposure: map[exchange.Asset]decimal.Decimal{"BTC": dec("0.005")},
		Fees:     map[exchange.Asset]decimal.Decimal{"BTC": dec("0.000005")},
	}, false); err != nil {
		t.Fatal(err)
	}
	marker := fixedMarker{"BTC": dec("18000")}
	port.TakeSnapshot(policyT0, marker) // advances the high-water mark / drawdown

	snap := buildLedgerSnapshot("sess-1", "binance", policyT0, resv, port, marker, starts)
	if snap.Balances["USDT"].Available != "9000" || snap.Realized["USDT"] != "-1000" ||
		snap.Exposure["BTC"] != "0.005" || snap.MarkValues["USDT"] != "90" || snap.Cycles != 1 || snap.Failed != 1 {
		t.Fatalf("snapshot = %+v", snap)
	}

	resume, err := planResume(snap)
	if err != nil {
		t.Fatal(err)
	}
	resv2 := reservation.New(resume.Balances, ids, now)
	port2 := portfolio.New(resv2, map[exchange.Asset]decimal.Decimal{"USDT": dec("10000")})
	port2.Restore(resume.Portfolio)
	if err := resv2.CheckInvariants(); err != nil {
		t.Fatal(err)
	}
	if avail, _ := resv2.Balance("USDT"); !avail.Equal(dec("9000")) {
		t.Fatalf("restored cash = %s", avail)
	}
	if !port2.Realized("USDT").Equal(port.Realized("USDT")) || !port2.FeesPaid("BTC").Equal(port.FeesPaid("BTC")) ||
		!port2.CurrentDrawdown("USDT").Equal(port.CurrentDrawdown("USDT")) {
		t.Fatalf("restored portfolio differs: realized %s/%s drawdown %s/%s",
			port2.Realized("USDT"), port.Realized("USDT"), port2.CurrentDrawdown("USDT"), port.CurrentDrawdown("USDT"))
	}
	net1, _ := port.NetPnL("USDT", marker)
	net2, _ := port2.NetPnL("USDT", marker)
	if !net1.Equal(net2) || !net2.Equal(dec("-910")) {
		t.Fatalf("net pnl %s vs %s", net1, net2)
	}
	// The same snapshot taken again from the restored state is identical.
	again := buildLedgerSnapshot("sess-1", "binance", policyT0, resv2, port2, marker, starts)
	if again.Balances["USDT"] != snap.Balances["USDT"] || again.Realized["USDT"] != snap.Realized["USDT"] ||
		again.Peak["USDT"] != snap.Peak["USDT"] || again.Drawdown["USDT"] != snap.Drawdown["USDT"] {
		t.Fatalf("second snapshot differs: %+v vs %+v", again, snap)
	}
}

type fixedMarker map[exchange.Asset]decimal.Decimal

func (m fixedMarker) Mark(asset exchange.Asset, amount decimal.Decimal, _ exchange.Asset) (decimal.Decimal, bool) {
	rate, ok := m[asset]
	if !ok {
		return decimal.Zero, false
	}
	return amount.Mul(rate), true
}
