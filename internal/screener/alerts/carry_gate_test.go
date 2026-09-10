package alerts

import (
	"context"
	"testing"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/screener"
)

// carryRule mirrors the worked-example rule of evaluator_test.go.
func carryRule(maxHoldH int) screener.Rule {
	apr := d("0")
	return screener.Rule{ID: "c1", Name: "carry", Enabled: true, Kind: screener.RuleKindCarry,
		MinCarryAPR: &apr, BuyVenues: []screener.Venue{screener.VenueBinance},
		Params: &screener.RuleParams{MaxHoldH: maxHoldH}}
}

func carrySignal(t *testing.T, svc *screener.Service, r screener.Rule, at time.Time) Signal {
	t.Helper()
	in := Inputs{Book: svc.Book, SpotFees: feeLookup(svc, false), PerpFees: feeLookup(svc, true),
		FundingHistory: svc.Funding, PollInterval: 5 * time.Second}
	sigs := ComputeSignals(context.Background(), in, r, at)
	if len(sigs) != 1 {
		t.Fatalf("signals = %d, want 1", len(sigs))
	}
	return sigs[0]
}

// A carry whose round trip cannot be paid for within the rule's hold
// window is refused: at 1 bps per 8 h it needs 43 settlements, so a hold
// of 10 intervals (80 h) can never break even. The §3.5 worked example
// (30-day hold, 90 intervals) must still qualify — the gate is about the
// hold, not a fixed cap (T-097).
func TestCarryRefusedWhenBreakevenExceedsHold(t *testing.T) {
	svc := newSvc(t)
	setCarry(svc.Book, t0)
	for i := 1; i <= 30; i++ {
		_ = svc.Funding.UpsertFunding(context.Background(), screener.VenueBinance, "BTC", t0.Add(-time.Duration(i*8)*time.Hour), "0.00012")
	}

	// Raise the entry basis so the edge check passes on a short hold and
	// the breakeven gate is the one under test (a low basis would be
	// refused by below_min_edge first, which is also correct but proves
	// nothing about breakeven).
	svc.Book.SetPerp(screener.Perp{Venue: screener.VenueBinance, Base: "BTC", Quote: "USDT",
		Mark: d("50050"), Index: d("49999"), Bid: d("50300"), Ask: d("50302"), BidQty: d("2"), AskQty: d("2"),
		FundingRate: d("0.0001"), PredictedFundingRate: d("0.0001"), IntervalH: 8,
		NextFundingAt: t0.Add(4 * time.Hour), At: t0})

	short := carrySignal(t, svc, carryRule(80), t0) // 10 intervals
	if short.Active {
		t.Fatalf("carry active with a hold too short to break even (breakeven=%d hold=%d)", short.BreakevenN, short.HoldIntervals)
	}
	if short.Reason != "breakeven_exceeds_hold" {
		t.Fatalf("reason = %q, want breakeven_exceeds_hold", short.Reason)
	}

	long := carrySignal(t, svc, carryRule(720), t0) // 90 intervals
	if !long.Active {
		t.Fatalf("worked-example carry refused: %s (breakeven=%d hold=%d)", long.Reason, long.BreakevenN, long.HoldIntervals)
	}
}

// The exit compares perp ask against spot bid, so a book whose round-trip
// spread swallows the entry basis would close on the next poll, before any
// funding settles. Such an entry must be refused rather than opened and
// immediately closed for the spread plus four taker fees (T-097).
func TestCarryRefusedWhenItWouldCloseImmediately(t *testing.T) {
	svc := newSvc(t)
	setCarry(svc.Book, t0)
	for i := 1; i <= 30; i++ {
		_ = svc.Funding.UpsertFunding(context.Background(), screener.VenueBinance, "BTC", t0.Add(-time.Duration(i*8)*time.Hour), "0.00012")
	}
	// Widen the perp spread so the exit-side basis sits below close_bps
	// while the entry-side basis still looks attractive.
	svc.Book.SetPerp(screener.Perp{Venue: screener.VenueBinance, Base: "BTC", Quote: "USDT",
		Mark: d("50050"), Index: d("49999"), Bid: d("50100"), Ask: d("49990"), BidQty: d("2"), AskQty: d("2"),
		FundingRate: d("0.0001"), PredictedFundingRate: d("0.0001"), IntervalH: 8,
		NextFundingAt: t0.Add(4 * time.Hour), At: t0})

	s := carrySignal(t, svc, carryRule(720), t0)
	if s.BasisEntryBps.IsNegative() {
		t.Fatalf("fixture no longer has a positive entry basis: %s", s.BasisEntryBps)
	}
	if s.Active {
		t.Fatalf("carry active though it would close immediately (exit basis %s)", s.BasisExitBps)
	}
	if s.Reason != "closes_immediately" {
		t.Fatalf("reason = %q, want closes_immediately", s.Reason)
	}
}
