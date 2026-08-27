package storage

import (
	"context"
	"testing"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/exchange"
	"github.com/cploutarchou/arb-chain-bot/internal/execution"
	"github.com/cploutarchou/arb-chain-bot/internal/opportunity"
	"github.com/cploutarchou/arb-chain-bot/internal/pricing"
	"github.com/cploutarchou/arb-chain-bot/internal/risk"
)

// seedCycle inserts one opportunity + cycle with three orders (one fill
// each), all timestamped at the SAME instant `at` — exactly the shape a
// real cycle produces (three legs written in one transaction) and
// exactly the case that breaks second-granularity cursors.
func seedCycle(t *testing.T, s *Store, oppID, cycleID, triangleID string, at time.Time, status execution.OrderStatus) {
	t.Helper()
	ctx := context.Background()
	q := pricing.CycleQuote{Triangle: triangleID, Start: "USDT", InputConsumed: d("1000"), FinalAmount: d("1010")}
	op := opportunity.Build(oppID, "binance", q, opportunity.Buffers{}, time.Second, at, 1)
	dec := risk.Decision{Allowed: true}
	if err := s.InsertOpportunity(ctx, &op, &dec); err != nil {
		t.Fatal(err)
	}
	syms := []exchange.Symbol{"BTCUSDT", "ETHBTC", "ETHUSDT"}
	var orders []execution.SimOrder
	for i, sym := range syms {
		legNo := i + 1
		orderID := cycleID + "-ord" + string(rune('0'+i))
		orders = append(orders, execution.SimOrder{
			ID: orderID, LegNo: legNo, Market: exchange.MarketID{Exchange: "binance", Symbol: sym},
			Side: exchange.SideBuy, Type: "LIMIT_IOC",
			QtyRequested: d("1"), QtyFilled: d("1"), LimitPrice: d("100"), AvgPrice: d("100"),
			CreatedAt: at, AckedAt: at, FilledAt: at, Status: status,
			Fills: []execution.SimFill{{
				ID: orderID + "-fill", Price: d("100"), Qty: d("1"), BookVersion: uint64(i + 1), At: at,
			}},
		})
	}
	res := execution.CycleResult{
		CycleID: cycleID, OpportunityID: oppID, Outcome: execution.OutcomeAllFilled, StartAsset: "USDT",
		InputConsumed: d("1000"), FinalAmount: d("1010"), RealizedPnL: d("10"), TotalPnL: d("10"),
		Orders: orders, StartedAt: at, SettledAt: at,
	}
	if err := s.InsertCycle(ctx, "sess-of", &res); err != nil {
		t.Fatal(err)
	}
}

func TestOrdersFillsGlobalListFiltersAndCrossLinks(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if err := s.EnsurePaperSession(ctx, "sess-of", "PAPER", map[string]string{"USDT": "10000"}, 1, 1); err != nil {
		t.Fatal(err)
	}
	seedCycle(t, s, "op-a", "cyc-a", "tri-a", t0, execution.OrderFilled)
	seedCycle(t, s, "op-b", "cyc-b", "tri-b", t0.Add(time.Minute), execution.OrderFilled)

	// Unfiltered: 6 orders across both cycles.
	page, err := s.ListOrdersGlobal(ctx, ListFilter{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Rows) != 6 {
		t.Fatalf("orders = %d, want 6", len(page.Rows))
	}
	for _, r := range page.Rows {
		if r.TriangleID == nil || r.OpportunityID == nil || r.Symbol == nil {
			t.Fatalf("cross-link ids missing: %+v", r)
		}
	}

	// Filter by triangle.
	byTri, err := s.ListOrdersGlobal(ctx, ListFilter{Triangle: "tri-a", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(byTri.Rows) != 3 {
		t.Fatalf("triangle filter = %d, want 3", len(byTri.Rows))
	}
	for _, r := range byTri.Rows {
		if *r.TriangleID != "tri-a" {
			t.Fatalf("leaked row from another triangle: %+v", r)
		}
	}

	// Filter by symbol.
	bySym, err := s.ListOrdersGlobal(ctx, ListFilter{Symbol: "ETHBTC", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(bySym.Rows) != 2 {
		t.Fatalf("symbol filter = %d, want 2", len(bySym.Rows))
	}

	// Filter by cycle.
	byCycle, err := s.ListOrdersGlobal(ctx, ListFilter{Cycle: "cyc-a", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(byCycle.Rows) != 3 {
		t.Fatalf("cycle filter = %d, want 3", len(byCycle.Rows))
	}

	// Filter by from/to window: only cyc-b (started a minute later).
	byWindow, err := s.ListOrdersGlobal(ctx, ListFilter{From: t0.Add(30 * time.Second), Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(byWindow.Rows) != 3 {
		t.Fatalf("from-window filter = %d, want 3", len(byWindow.Rows))
	}

	// Fills mirror the same cross-link/filter behavior.
	fillPage, err := s.ListFillsGlobal(ctx, ListFilter{Triangle: "tri-b", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(fillPage.Rows) != 3 {
		t.Fatalf("fills triangle filter = %d, want 3", len(fillPage.Rows))
	}
	for _, r := range fillPage.Rows {
		if r.OpportunityID == nil || *r.OpportunityID != "op-b" {
			t.Fatalf("fill cross-link wrong: %+v", r)
		}
	}
}

// TestListCyclesByTriangle covers BL-26's "recent cycles" panel.
func TestListCyclesByTriangle(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if err := s.EnsurePaperSession(ctx, "sess-of", "PAPER", map[string]string{"USDT": "10000"}, 1, 1); err != nil {
		t.Fatal(err)
	}
	seedCycle(t, s, "op-a", "cyc-a", "tri-a", t0, execution.OrderFilled)
	seedCycle(t, s, "op-b", "cyc-b", "tri-b", t0.Add(time.Minute), execution.OrderFilled)

	rows, err := s.ListCyclesByTriangle(ctx, "tri-a", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ID != "cyc-a" {
		t.Fatalf("rows = %+v", rows)
	}

	empty, err := s.ListCyclesByTriangle(ctx, "tri-nonexistent", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(empty) != 0 {
		t.Fatalf("empty = %+v", empty)
	}
}

// TestOrdersGlobalCursorPaginatesRowsSharingATimestamp is the precision
// regression the advisor flagged: three orders in cyc-a share the exact
// same created_at. A second-granularity cursor would skip or repeat
// rows at this boundary; RFC3339Nano must not.
func TestOrdersGlobalCursorPaginatesRowsSharingATimestamp(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if err := s.EnsurePaperSession(ctx, "sess-of", "PAPER", map[string]string{"USDT": "10000"}, 1, 1); err != nil {
		t.Fatal(err)
	}
	seedCycle(t, s, "op-a", "cyc-a", "tri-a", t0, execution.OrderFilled)

	seen := map[string]bool{}
	cursor := ""
	pages := 0
	for {
		pages++
		if pages > 10 {
			t.Fatal("pagination did not terminate")
		}
		page, err := s.ListOrdersGlobal(ctx, ListFilter{Limit: 1, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range page.Rows {
			if seen[r.ID] {
				t.Fatalf("row %s repeated across pages", r.ID)
			}
			seen[r.ID] = true
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if len(seen) != 3 {
		t.Fatalf("paginated through %d distinct rows, want 3: %v", len(seen), seen)
	}
}
