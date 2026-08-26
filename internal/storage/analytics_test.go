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

// seedAnalyticsCycle inserts one opportunity + one-order cycle with
// caller-controlled economics, for BL-19 breakdown/series/distribution
// fixtures.
func seedAnalyticsCycle(t *testing.T, s *Store, oppID, cycleID, triangleID string, configVersion int64,
	at time.Time, pnl, slippageBps, netReturnBps string, latencyMs int64) {
	t.Helper()
	ctx := context.Background()
	q := pricing.CycleQuote{Triangle: triangleID, Start: "USDT", InputConsumed: d("1000"), FinalAmount: d("1010")}
	q.ReturnBps = d(netReturnBps)
	op := opportunity.Build(oppID, "binance", q, opportunity.Buffers{}, time.Hour, at, configVersion)
	op.NetReturnBps = d(netReturnBps)
	op.Status = opportunity.StatusQualified
	dec := risk.Decision{Allowed: true}
	if err := s.InsertOpportunity(ctx, &op, &dec); err != nil {
		t.Fatal(err)
	}
	filledAt := at.Add(time.Duration(latencyMs) * time.Millisecond)
	res := execution.CycleResult{
		CycleID: cycleID, OpportunityID: oppID, Outcome: execution.OutcomeAllFilled, StartAsset: "USDT",
		InputConsumed: d("1000"), FinalAmount: d("1000").Add(d(pnl)), RealizedPnL: d(pnl), TotalPnL: d(pnl),
		SlippageBps: d(slippageBps),
		Orders: []execution.SimOrder{{
			ID: cycleID + "-ord", LegNo: 1, Market: exchange.MarketID{Exchange: "binance", Symbol: "BTCUSDT"},
			Side: exchange.SideBuy, Type: "LIMIT_IOC",
			QtyRequested: d("1"), QtyFilled: d("1"), LimitPrice: d("100"), AvgPrice: d("100"),
			CreatedAt: at, AckedAt: at, FilledAt: filledAt, Status: execution.OrderFilled,
			Fills: []execution.SimFill{{ID: cycleID + "-fill", Price: d("100"), Qty: d("1"), At: filledAt}},
		}},
		StartedAt: at, SettledAt: at,
	}
	if err := s.InsertCycle(ctx, "sess-an", &res); err != nil {
		t.Fatal(err)
	}
}

func seedUnattributedCycle(t *testing.T, s *Store, cycleID string, at time.Time, pnl string) {
	t.Helper()
	res := execution.CycleResult{
		CycleID: cycleID, Outcome: execution.OutcomeExpired, StartAsset: "USDT",
		TotalPnL: d(pnl), StartedAt: at, SettledAt: at,
	}
	if err := s.InsertCycle(context.Background(), "sess-an", &res); err != nil {
		t.Fatal(err)
	}
}

func TestPnLBreakdownDimensions(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if err := s.EnsurePaperSession(ctx, "sess-an", "PAPER", map[string]string{"USDT": "10000"}, 1, 1); err != nil {
		t.Fatal(err)
	}
	seedAnalyticsCycle(t, s, "op-1", "cyc-1", "tri-a", 1, t0, "10", "1.5", "17", 20)
	seedAnalyticsCycle(t, s, "op-2", "cyc-2", "tri-b", 2, t0.Add(time.Minute), "-5", "2.5", "12", 30)
	seedUnattributedCycle(t, s, "cyc-3", t0.Add(2*time.Minute), "3")

	from, to := t0.Add(-time.Hour), t0.Add(time.Hour)

	byTri, err := s.PnLBreakdown(ctx, "triangle", from, to)
	if err != nil {
		t.Fatal(err)
	}
	if byTri.N != 2 || byTri.Unattributed != 1 {
		t.Fatalf("triangle breakdown n=%d unattributed=%d, want 2/1: %+v", byTri.N, byTri.Unattributed, byTri)
	}
	if len(byTri.Rows) != 2 {
		t.Fatalf("triangle rows = %+v", byTri.Rows)
	}

	byAsset, err := s.PnLBreakdown(ctx, "asset", from, to)
	if err != nil {
		t.Fatal(err)
	}
	// asset has no join, so the unattributed cycle IS counted (pnl_asset
	// is always populated directly on paper_cycles).
	if byAsset.N != 3 || byAsset.Unattributed != 0 {
		t.Fatalf("asset breakdown = %+v", byAsset)
	}
	if len(byAsset.Rows) != 1 || byAsset.Rows[0].Key != "USDT" {
		t.Fatalf("asset rows = %+v", byAsset.Rows)
	}
	if *byAsset.Rows[0].NetPnL != "8" { // 10 - 5 + 3
		t.Fatalf("asset net_pnl = %s, want 8", *byAsset.Rows[0].NetPnL)
	}

	byConfig, err := s.PnLBreakdown(ctx, "config_version", from, to)
	if err != nil {
		t.Fatal(err)
	}
	if byConfig.N != 2 || len(byConfig.Rows) != 2 {
		t.Fatalf("config_version breakdown = %+v", byConfig)
	}

	byMarket, err := s.PnLBreakdown(ctx, "market", from, to)
	if err != nil {
		t.Fatal(err)
	}
	if len(byMarket.Rows) != 1 || byMarket.Rows[0].Key != "BTCUSDT" || byMarket.Rows[0].N != 2 {
		t.Fatalf("market breakdown = %+v", byMarket)
	}
	if byMarket.Rows[0].NetPnL != nil {
		t.Fatalf("market breakdown must never carry net_pnl: %+v", byMarket.Rows[0])
	}
	if len(byMarket.Notes) == 0 {
		t.Fatal("market breakdown must explain why it has no net_pnl")
	}

	if _, err := s.PnLBreakdown(ctx, "bogus", from, to); err != ErrBadBreakdown {
		t.Fatalf("bad dimension error = %v, want ErrBadBreakdown", err)
	}
}

func TestPnLSeriesCumulativeAndDrawdown(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if err := s.EnsurePaperSession(ctx, "sess-an", "PAPER", map[string]string{"USDT": "10000"}, 1, 1); err != nil {
		t.Fatal(err)
	}
	seedAnalyticsCycle(t, s, "op-1", "cyc-1", "tri-a", 1, t0, "10", "1", "10", 10)
	seedAnalyticsCycle(t, s, "op-2", "cyc-2", "tri-a", 1, t0.Add(time.Minute), "-30", "1", "10", 10)
	seedAnalyticsCycle(t, s, "op-3", "cyc-3", "tri-a", 1, t0.Add(2*time.Minute), "5", "1", "10", 10)

	res, err := s.PnLSeries(ctx, t0.Add(-time.Hour), t0.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if res.N != 3 || len(res.Points) != 3 {
		t.Fatalf("series = %+v", res)
	}
	// 10, 10-30=-20, -20+5=-15; peak stays 10, so drawdown is 0, -30, -25.
	want := []struct{ cum, dd string }{{"10", "0"}, {"-20", "-30"}, {"-15", "-25"}}
	for i, w := range want {
		if res.Points[i].Cumulative != w.cum || res.Points[i].Drawdown != w.dd {
			t.Fatalf("point %d = %+v, want cum=%s dd=%s", i, res.Points[i], w.cum, w.dd)
		}
	}
}

func TestDistributionsReportSampleSizeAndPercentiles(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if err := s.EnsurePaperSession(ctx, "sess-an", "PAPER", map[string]string{"USDT": "10000"}, 1, 1); err != nil {
		t.Fatal(err)
	}
	seedAnalyticsCycle(t, s, "op-1", "cyc-1", "tri-a", 1, t0, "10", "1", "10", 10)
	seedAnalyticsCycle(t, s, "op-2", "cyc-2", "tri-a", 1, t0.Add(time.Minute), "5", "2", "20", 30)
	seedAnalyticsCycle(t, s, "op-3", "cyc-3", "tri-a", 1, t0.Add(2*time.Minute), "5", "3", "30", 50)

	res, err := s.Distributions(ctx, t0.Add(-time.Hour), t0.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if res.Edge.N != 3 || res.Edge.Min != "10" || res.Edge.Max != "30" {
		t.Fatalf("edge distribution = %+v", res.Edge)
	}
	if res.Slippage.N != 3 {
		t.Fatalf("slippage distribution = %+v", res.Slippage)
	}
	if res.Latency.N != 3 || res.Latency.Min != "10" || res.Latency.Max != "50" {
		t.Fatalf("latency distribution = %+v", res.Latency)
	}
	if len(res.Edge.Buckets) != histogramBuckets {
		t.Fatalf("edge buckets = %d, want %d", len(res.Edge.Buckets), histogramBuckets)
	}
	total := 0
	for _, b := range res.Edge.Buckets {
		total += b.Count
	}
	if total != res.Edge.N {
		t.Fatalf("bucket counts sum to %d, want %d", total, res.Edge.N)
	}

	// Empty window: honest zero, not an error.
	empty, err := s.Distributions(ctx, t0.Add(-48*time.Hour), t0.Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if empty.Edge.N != 0 || empty.Slippage.N != 0 || empty.Latency.N != 0 {
		t.Fatalf("empty window = %+v", empty)
	}
}
