package storage

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/exchange"
	"github.com/cploutarchou/arb-chain-bot/internal/execution"
	"github.com/cploutarchou/arb-chain-bot/internal/opportunity"
	"github.com/cploutarchou/arb-chain-bot/internal/pricing"
	"github.com/cploutarchou/arb-chain-bot/internal/risk"
)

func TestGetOpportunityDetailWithDecisionAndSimulation(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	q := pricing.CycleQuote{
		Triangle: "binance|USDT|A>B>C", Start: "USDT",
		InputConsumed: d("1000"), FinalAmount: d("1010"),
		Legs: [3]pricing.LegQuote{
			{BookVersion: 11}, {BookVersion: 22}, {BookVersion: 33},
		},
	}
	op := opportunity.Build("op-detail", "binance", q, opportunity.Buffers{}, time.Hour, t0, 1)
	op.Status = opportunity.StatusRejected
	op.Reason = "RISK_MIN_EDGE"
	dec := risk.Decision{
		Allowed: false, ReasonCode: "RISK_MIN_EDGE", ConfigVersion: 1,
		Checks: []risk.Check{{Name: "RISK_MIN_EDGE", Observed: "1", Threshold: "5", Passed: false}},
	}
	if err := s.InsertOpportunity(ctx, &op, &dec); err != nil {
		t.Fatal(err)
	}

	detail, err := s.GetOpportunity(ctx, "op-detail")
	if err != nil {
		t.Fatal(err)
	}
	if detail.Decision == nil || detail.Decision.Legacy {
		t.Fatalf("decision = %+v, want non-legacy", detail.Decision)
	}
	if detail.Decision.ReasonCode != "RISK_MIN_EDGE" || detail.Decision.Allowed {
		t.Fatalf("decision = %+v", detail.Decision)
	}
	var checks []risk.Check
	if err := json.Unmarshal(detail.Decision.Checks, &checks); err != nil || len(checks) != 1 {
		t.Fatalf("checks = %s err=%v", detail.Decision.Checks, err)
	}
	if len(detail.BookVersions) != 3 || detail.BookVersions[0] != 11 || detail.BookVersions[2] != 33 {
		t.Fatalf("book_versions = %v", detail.BookVersions)
	}
	if detail.Simulation != nil {
		t.Fatalf("no cycle recorded yet, simulation should be nil: %+v", detail.Simulation)
	}

	// Now record a settled cycle for it and confirm it shows up.
	if err := s.EnsurePaperSession(ctx, "sess-od", "PAPER", map[string]string{"USDT": "10000"}, 1, 1); err != nil {
		t.Fatal(err)
	}
	res := execution.CycleResult{
		CycleID: "cyc-detail", OpportunityID: "op-detail", Outcome: execution.OutcomeAllFilled,
		StartAsset: "USDT", InputConsumed: d("1000"), FinalAmount: d("1010"),
		RealizedPnL: d("10"), TotalPnL: d("10"), SlippageBps: d("2"),
		Orders: []execution.SimOrder{{
			ID: "ord-detail", LegNo: 1, Market: exchange.MarketID{Exchange: "binance", Symbol: "BTCUSDT"},
			Side: exchange.SideBuy, Type: "LIMIT_IOC",
			QtyRequested: d("1"), QtyFilled: d("1"), AvgPrice: d("100"),
			CreatedAt: t0, Status: execution.OrderFilled,
		}},
		StartedAt: t0, SettledAt: t0.Add(time.Second),
	}
	if err := s.InsertCycle(ctx, "sess-od", &res); err != nil {
		t.Fatal(err)
	}
	detail2, err := s.GetOpportunity(ctx, "op-detail")
	if err != nil {
		t.Fatal(err)
	}
	if detail2.Simulation == nil || detail2.Simulation.CycleID != "cyc-detail" || detail2.Simulation.Outcome != "ALL_FILLED" {
		t.Fatalf("simulation = %+v", detail2.Simulation)
	}
	if detail2.Simulation.PnLAmount == nil || *detail2.Simulation.PnLAmount != "10" {
		t.Fatalf("simulation pnl = %+v", detail2.Simulation.PnLAmount)
	}

	if _, err := s.GetOpportunity(ctx, "does-not-exist"); !errors.Is(err, ErrOpportunityNotFound) {
		t.Fatalf("unknown id error = %v, want ErrOpportunityNotFound", err)
	}
}
