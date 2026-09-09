package scanner

import (
	"testing"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/exchange"
	"github.com/cploutarchou/arb-chain-bot/internal/opportunity"
	"github.com/cploutarchou/arb-chain-bot/internal/orderbook"
	"github.com/cploutarchou/arb-chain-bot/internal/risk"
)

var btcusdt = exchange.MarketID{Exchange: "binance", Symbol: "BTCUSDT"}

// qualify runs one evaluation round and returns the qualified opportunity.
func qualify(t *testing.T, s *Scanner) opportunity.Opportunity {
	t.Helper()
	s.EvaluateMarket(btcusdt)
	for _, ev := range drain(s) {
		if ev.Opportunity.Status == opportunity.StatusQualified {
			return ev.Opportunity
		}
	}
	t.Fatal("no qualified opportunity")
	return opportunity.Opportunity{}
}

func resnapshot(t *testing.T, books *orderbook.Set, sym string, bids, asks []orderbook.Level, ver int64) {
	t.Helper()
	id := exchange.MarketID{Exchange: "binance", Symbol: exchange.Symbol(sym)}
	b, ok := books.Get(id)
	if !ok {
		t.Fatalf("no book %s", sym)
	}
	b.ApplySnapshot(orderbook.DepthEvent{
		Market: id, IsSnapshot: true, FinalUpdateID: ver, Bids: bids, Asks: asks, ReceiveTime: t0,
	})
}

// Unmoved books: no re-quote, the economics are byte-identical, and the
// gate still runs (counted once).
func TestRevalidateUnchangedBooksKeepsTheQuote(t *testing.T) {
	s, _ := harness(t)
	op := qualify(t, s)

	fresh, dec, ok := s.Revalidate(op)
	if !ok || !dec.Allowed {
		t.Fatalf("ok=%v decision=%+v", ok, dec)
	}
	if !fresh.Quote.FinalAmount.Equal(op.Quote.FinalAmount) || !fresh.NetReturnBps.Equal(op.NetReturnBps) {
		t.Fatalf("economics changed without a book move: %s vs %s", fresh.Quote.FinalAmount, op.Quote.FinalAmount)
	}
	if fresh.ID != op.ID || !fresh.ExpiresAt.Equal(op.ExpiresAt) {
		t.Fatalf("identity or TTL changed: %+v", fresh)
	}
	if s.Stats.Revalidations.Load() != 1 || s.Stats.RevalidationRejects.Load() != 0 {
		t.Fatalf("stats = %d/%d", s.Stats.Revalidations.Load(), s.Stats.RevalidationRejects.Load())
	}
}

// A book that moved against the plan is re-priced at the qualified size
// and the gate refuses the cycle the scanner qualified moments earlier.
func TestRevalidateRepricesMovedBooks(t *testing.T) {
	s, books := harness(t)
	op := qualify(t, s)

	// ETHUSDT bid collapses: the last leg returns far less than planned.
	resnapshot(t, books, "ETHUSDT", []orderbook.Level{lv("9.5", "1000")}, []orderbook.Level{lv("10.3", "1000")}, 9)

	fresh, dec, ok := s.Revalidate(op)
	if !ok {
		t.Fatal("revalidation unavailable")
	}
	if dec.Allowed {
		t.Fatalf("stale plan passed revalidation: %+v", dec)
	}
	if dec.ReasonCode != risk.ReasonMinEdge {
		t.Fatalf("reason = %s, want %s", dec.ReasonCode, risk.ReasonMinEdge)
	}
	if !fresh.NetReturnBps.LessThan(op.NetReturnBps) || !fresh.Quote.InputConsumed.Equal(op.Quote.InputConsumed) {
		t.Fatalf("fresh economics not re-priced at the qualified size: %s bps at %s (was %s bps at %s)",
			fresh.NetReturnBps, fresh.Quote.InputConsumed, op.NetReturnBps, op.Quote.InputConsumed)
	}
	if s.Stats.RevalidationRejects.Load() != 1 {
		t.Fatalf("rejects = %d", s.Stats.RevalidationRejects.Load())
	}
}

// A book that moved in the plan's favour passes with the fresh, better
// economics (the executor then fills against the real book anyway).
func TestRevalidateAcceptsAFavourableMove(t *testing.T) {
	s, books := harness(t)
	op := qualify(t, s)
	resnapshot(t, books, "ETHUSDT", []orderbook.Level{lv("10.5", "1000")}, []orderbook.Level{lv("10.6", "1000")}, 9)

	fresh, dec, ok := s.Revalidate(op)
	if !ok || !dec.Allowed {
		t.Fatalf("ok=%v decision=%+v", ok, dec)
	}
	if !fresh.NetReturnBps.GreaterThan(op.NetReturnBps) {
		t.Fatalf("fresh edge %s not above %s", fresh.NetReturnBps, op.NetReturnBps)
	}
}

// Depth gone entirely: nothing executable remains at the qualified size,
// reported under its own reason code rather than as a limit breach.
func TestRevalidateDepthGoneIsARevalidationReason(t *testing.T) {
	s, books := harness(t)
	op := qualify(t, s)
	resnapshot(t, books, "BTCUSDT", []orderbook.Level{lv("99.9", "10")}, nil, 9)

	_, dec, ok := s.Revalidate(op)
	if !ok || dec.Allowed || dec.ReasonCode != risk.ReasonRevalidation {
		t.Fatalf("ok=%v decision=%+v", ok, dec)
	}
}

// Breakers opened after qualification gate the cycle — including the
// per-market scope the feed policy uses.
func TestRevalidateSeesBreakersOpenedSinceQualification(t *testing.T) {
	s, _ := harness(t)
	op := qualify(t, s)
	tri, ok := s.triangle(op.TriangleID)
	if !ok {
		t.Fatal("triangle lookup")
	}
	for _, scope := range []string{"exchange:binance", "triangle:" + tri.ID, "market:" + tri.Legs[1].Market.String()} {
		s.Breakers.Trip("test", scope, "test", t0)
		_, dec, ok := s.Revalidate(op)
		if !ok || dec.Allowed || dec.ReasonCode != risk.ReasonBreakerOpen {
			t.Fatalf("scope %s: ok=%v decision=%+v", scope, ok, dec)
		}
		s.Breakers.Close("test", scope, t0)
	}
	if _, dec, _ := s.Revalidate(op); !dec.Allowed {
		t.Fatalf("closed breakers still gate: %+v", dec)
	}
}

// The revalidating cycle is itself one of the active simulations: at the
// concurrency limit the check counts the others only.
func TestRevalidateCountsOtherSimulationsOnly(t *testing.T) {
	s, _ := harness(t)
	op := qualify(t, s)
	s.Sims = func() int { return 4 } // MaxConcurrentSimulations is 4 in the harness

	if _, dec, _ := s.Revalidate(op); !dec.Allowed {
		t.Fatalf("self counted against the concurrency limit: %+v", dec)
	}
	s.Sims = func() int { return 5 }
	if _, dec, _ := s.Revalidate(op); dec.Allowed || dec.ReasonCode != risk.ReasonConcurrency {
		t.Fatalf("limit not applied to the others: %+v", dec)
	}
}

func TestRevalidateUnknownTriangleIsUnavailable(t *testing.T) {
	s, _ := harness(t)
	op := qualify(t, s)
	op.TriangleID = "binance|USDT|nope"
	if _, _, ok := s.Revalidate(op); ok {
		t.Fatal("unknown triangle revalidated")
	}
}

type staticLedger struct{ loss, dd decimal.Decimal }

func (l staticLedger) DailyLoss(exchange.Asset) decimal.Decimal { return l.loss }
func (l staticLedger) Drawdown(exchange.Asset) decimal.Decimal  { return l.dd }

// Session loss and drawdown reach the gate: at the limit nothing
// qualifies, with the matching reason on the profitable direction.
func TestLedgerLossAndDrawdownGateQualification(t *testing.T) {
	s, _ := harness(t)
	s.Resolver.Global.MaxDailyLoss = d("100")
	s.Resolver.Global.MaxDrawdown = d("0.05")

	// Below both limits: the profitable direction still qualifies.
	s.Ledger = staticLedger{loss: d("99.99"), dd: d("0.0499")}
	qualify(t, s)

	for _, tc := range []struct {
		name   string
		ledger staticLedger
		reason string
	}{
		{"loss at the limit", staticLedger{loss: d("100")}, risk.ReasonDailyLoss},
		{"drawdown at the limit", staticLedger{dd: d("0.05")}, risk.ReasonDrawdown},
	} {
		s.Ledger = tc.ledger
		s.EvaluateMarket(btcusdt)
		var reasons []string
		for _, ev := range drain(s) {
			if ev.Opportunity.Status == opportunity.StatusQualified {
				t.Fatalf("%s: qualified %s", tc.name, ev.Opportunity.ID)
			}
			reasons = append(reasons, ev.Decision.ReasonCode)
		}
		found := false
		for _, r := range reasons {
			if r == tc.reason {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s: reasons %v do not include %s", tc.name, reasons, tc.reason)
		}
	}
}
