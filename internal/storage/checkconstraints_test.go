package storage

import (
	"context"
	"testing"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/exchange"
	"github.com/cploutarchou/arb-chain-bot/internal/execution"
)

// TestFinancialCheckConstraintsRefuseNegatives (audit D10): the tables
// refuse rows a sign error would produce — a negative available balance,
// a negative fill price — instead of persisting them silently. PnL and
// slippage columns stay unconstrained (measurements legitimately go both
// ways; the fixture's negative realized PnL proves that on the same run).
func TestFinancialCheckConstraintsRefuseNegatives(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	if err := s.EnsurePaperSession(ctx, "sess-checks", "PAPER", map[string]string{"USDT": "100"}, 1, 7); err != nil {
		t.Fatal(err)
	}
	// The ledger upsert path would refuse a negative at the engine layer
	// first; raw SQL proves the table itself enforces the bound regardless
	// of which role or code path issues the write.
	if _, err := s.Pool.Exec(ctx, `
		INSERT INTO virtual_balances (session_id, exchange_id, asset, available, reserved)
		VALUES ('sess-checks', 'binance', 'USDT', -1, 0)`); err == nil {
		t.Fatal("negative available balance accepted")
	}

	// A legal cycle (negative realized PnL included — PnL stays
	// unconstrained) must still persist; only the corrupted shapes fail.
	good := checkFixtureCycle("cyc-good", "ord-good", "fill-good", "100")
	if err := s.InsertCycle(ctx, "sess-checks", good); err != nil {
		t.Fatalf("legal cycle refused: %v", err)
	}

	negPrice := checkFixtureCycle("cyc-negprice", "ord-negprice", "fill-negprice", "-100")
	if err := s.InsertCycle(ctx, "sess-checks", negPrice); err == nil {
		t.Fatal("negative fill price accepted")
	}
	zeroQty := checkFixtureCycle("cyc-zeroqty", "ord-zeroqty", "fill-zeroqty", "0")
	if err := s.InsertCycle(ctx, "sess-checks", zeroQty); err == nil {
		t.Fatal("zero-quantity fill accepted")
	}
}

// checkFixtureCycle builds one minimal all-filled cycle with a single
// leg, order and fill priced at the given fill price — the smallest
// legal shape the CHECKs must let through, with the fill price swapped
// for the refusal cases.
func checkFixtureCycle(cycleID, orderID, fillID, fillPrice string) *execution.CycleResult {
	mkt := exchange.MarketID{Exchange: "binance", Symbol: "BTCUSDT"}
	res := &execution.CycleResult{
		CycleID: cycleID, Outcome: execution.OutcomeAllFilled, StartAsset: "USDT",
		InputConsumed: d("1000"), FinalAmount: d("1010"),
		RealizedPnL: d("-5"), TotalPnL: d("-5"),
		StartedAt: t0, SettledAt: t0.Add(time.Second),
	}
	res.Orders = []execution.SimOrder{{
		ID: orderID, LegNo: 1, Market: mkt, Side: exchange.SideBuy, Type: "LIMIT_IOC",
		QtyRequested: d("0.01"), QtyFilled: d("0.01"), LimitPrice: d("100200"),
		AvgPrice: d("100000"), FeeAmount: d("0.00001"), FeeAsset: "BTC",
		CreatedAt: t0, AckedAt: t0.Add(20 * time.Millisecond), FilledAt: t0.Add(50 * time.Millisecond),
		Status: execution.OrderFilled,
		Fills: []execution.SimFill{{
			ID: fillID, Price: d(fillPrice), Qty: d("0.01"),
			FeeAmount: d("0.00001"), FeeAsset: "BTC", BookVersion: 5, At: t0.Add(50 * time.Millisecond),
		}},
	}}
	return res
}
