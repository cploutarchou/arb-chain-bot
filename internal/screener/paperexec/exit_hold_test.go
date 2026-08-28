package paperexec

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/screener"
)

func dec(v string) decimal.Decimal { return decimal.RequireFromString(v) }

// A converged carry must not close before its first funding settlement,
// but a stop must still fire immediately (T-097).
func TestCarryConvergedCloseWaitsForFirstSettlement(t *testing.T) {
	x := &Executor{}
	now := time.Unix(1_700_000_000, 0).UTC()
	r := screener.Rule{ID: "c1", Kind: screener.RuleKindCarry,
		Params: &screener.RuleParams{MaxHoldH: 720, MMR: ptr(dec("0.004"))}}
	pos := Position{Strategy: screener.StrategyCarry, Base: "BTC", Quote: "USDT",
		Qty: dec("0.2"), OpenedAt: now.Add(-time.Minute), FundingQuote: decimal.Zero}
	po := perpOpen{SpotOpen: dec("50000"), PerpOpen: dec("50100"), BasisEntryBps: dec("20"),
		IntervalH: 8, IntervalHOpen: 8, NextFundingAt: now.Add(4 * time.Hour)}
	// Converged: exit-side basis below close_bps.
	q := screener.Quote{Bid: dec("50000"), Ask: dec("50002")}
	p := screener.Perp{Mark: dec("50000"), Bid: dec("49990"), Ask: dec("49995")}

	if reason := x.exitReason(r, pos, po, p, q, now); reason != "" {
		t.Fatalf("closed before the first settlement with reason %q", reason)
	}

	po.Settlements = 1
	if reason := x.exitReason(r, pos, po, p, q, now); reason != "converged" {
		t.Fatalf("reason after one settlement = %q, want converged", reason)
	}

	// A margin stop still fires with zero settlements.
	po.Settlements = 0
	pStop := screener.Perp{Mark: dec("80000"), Bid: dec("79990"), Ask: dec("79995")}
	if reason := x.exitReason(r, pos, po, pStop, q, now); reason != "margin_stop" {
		t.Fatalf("stop reason = %q, want margin_stop", reason)
	}
}

func ptr(d decimal.Decimal) *decimal.Decimal { return &d }
