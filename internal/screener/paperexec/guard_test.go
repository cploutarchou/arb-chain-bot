package paperexec

import (
	"context"
	"testing"

	"github.com/cploutarchou/arb-chain-bot/internal/screener"
)

// TestSkipSuspectMismatch / TestSkipLiquidityUnknown: even if a signal
// reached the executor (a stale rule, a hook called directly), the
// executor re-runs screener.GuardLane and books SKIPPED with the shared
// reason; nothing is reserved, filled or executed.
func TestSkipSuspectMismatch(t *testing.T) {
	h := newHarness(t, nil)
	h.balance(screener.VenueBinance, "USDT", "100000")
	h.balance(screener.VenueOKX, "BTC", "1")
	r := h.rule(spreadRule("5000"))
	// XTER-style: OKX bid 6× Binance ask (50 000 vs 300 000).
	setSpot(h.svc.Book, screener.VenueBinance, "49999", "1", "50000", "1", t0)
	setSpot(h.svc.Book, screener.VenueOKX, "300000", "1", "300100", "1", t0)
	s := h.signal(r, t0)
	if s.Active || s.Reason != SkipSuspectMismatch {
		t.Fatalf("signal = active %v reason %q", s.Active, s.Reason)
	}
	s.Active = true // force the hook path: the executor must still refuse
	h.x.OnOpen(context.Background(), s, screener.Event{ID: "e1", RuleID: r.ID})
	if got := skippedReason(t, h, r.ID); got != SkipSuspectMismatch {
		t.Fatalf("reason = %s, want SUSPECT_MISMATCH", got)
	}
	if len(h.execs(r.ID)) != 0 {
		t.Fatal("execution booked on a suspect lane")
	}
	bal, _ := h.ledger.ListBalances(context.Background())
	for _, b := range bal {
		if (b.Asset == "USDT" && !b.Balance.Equal(d("100000"))) || (b.Asset == "BTC" && !b.Balance.Equal(d("1"))) {
			t.Fatalf("balance moved on a skipped lane: %+v", b)
		}
	}
}

func TestSkipLiquidityUnknown(t *testing.T) {
	h := newHarness(t, nil)
	h.balance(screener.VenueBinance, "USDT", "100000")
	h.balance(screener.VenueOKX, "BTC", "1")
	r := h.rule(spreadRule("5000"))
	setSpot(h.svc.Book, screener.VenueBinance, "49999", "1", "50000", "0.35", t0)
	// Plausible price on OKX but no sizes published (Gate-style ticker).
	h.svc.Book.SetQuote(screener.Quote{Venue: screener.VenueOKX, Base: "BTC", Quote: "USDT", Bid: d("50250"), Ask: d("50255"), At: t0, LiquidityUnknown: true})
	s := h.signal(r, t0)
	if s.Active || s.Reason != SkipLiquidityUnknown {
		t.Fatalf("signal = active %v reason %q", s.Active, s.Reason)
	}
	s.Active = true
	h.x.OnOpen(context.Background(), s, screener.Event{ID: "e1", RuleID: r.ID})
	if got := skippedReason(t, h, r.ID); got != SkipLiquidityUnknown {
		t.Fatalf("reason = %s, want LIQUIDITY_UNKNOWN", got)
	}
	if len(h.execs(r.ID)) != 0 {
		t.Fatal("execution booked on an unknown-liquidity lane")
	}
}
