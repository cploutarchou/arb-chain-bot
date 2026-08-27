package alerts

import (
	"context"
	"testing"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/screener"
)

func setQuote(book *screener.Book, venue screener.Venue, base, bid, bidQty, ask, askQty string, unknown bool, at time.Time) {
	book.SetQuote(screener.Quote{Venue: venue, Base: base, Quote: "USDT", Bid: d(bid), BidQty: d(bidQty),
		Ask: d(ask), AskQty: d(askQty), At: at, LiquidityUnknown: unknown})
}

// TestEvaluatorNeverOpensOnGuardedLanes: the VON (suspect) and TROLL
// (unknown liquidity) live cases produce inactive signals with the
// shared reasons, no event and no Telegram — while the clean BTC lane
// in the same book still opens.
func TestEvaluatorNeverOpensOnGuardedLanes(t *testing.T) {
	svc := newSvc(t)
	book := svc.Book
	setQuote(book, screener.VenueGate, "VON", "0.0000000010", "0", "0.0000000011", "0", true, t0)
	setQuote(book, screener.VenueMEXC, "VON", "0.19", "1000", "0.195", "1000", false, t0)
	setQuote(book, screener.VenueGate, "TROLL", "0.00000180", "0", "0.00000182", "0", true, t0)
	setQuote(book, screener.VenueMEXC, "TROLL", "0.00000175", "50000000", "0.00000177", "50000000", false, t0)
	setSpread(book, "50250", t0)

	r := spreadRule()
	r.MinLifetimeS, r.CooldownS = 0, 0
	r.BuyVenues, r.SellVenues = nil, nil // every venue pair
	if _, err := svc.Rules.InsertRule(context.Background(), r, "t"); err != nil {
		t.Fatal(err)
	}
	fn := &fakeNotifier{}
	e := New(svc, fn.Notify, nil)
	e.Tick(context.Background(), t0)

	snap := e.Snapshot()
	von := snap["r1|VON/USDT|gate>mexc"]
	if von.Active || von.Reason != screener.SkipSuspectMismatch || !von.Suspect {
		t.Fatalf("VON signal = active %v reason %q suspect %v", von.Active, von.Reason, von.Suspect)
	}
	troll := snap["r1|TROLL/USDT|gate>mexc"]
	if troll.Active || troll.Reason != screener.SkipLiquidityUnknown || !troll.LiquidityUnknown || troll.Suspect {
		t.Fatalf("TROLL signal = active %v reason %q unknown %v suspect %v", troll.Active, troll.Reason, troll.LiquidityUnknown, troll.Suspect)
	}
	open := e.OpenEvents()
	if len(open) != 1 || open[0].Base != "BTC" {
		t.Fatalf("open events = %+v, want exactly the BTC lane", open)
	}
	if fn.count() != 1 {
		t.Fatalf("telegram sends = %d, want 1 (BTC only)", fn.count())
	}
}

// TestPerpSignalGuard: a perp bid 10× the spot ask is not a basis; the
// carry rule's signal is SUSPECT_MISMATCH, and a spot leg with no size
// is LIQUIDITY_UNKNOWN.
func TestPerpSignalGuard(t *testing.T) {
	svc := newSvc(t)
	apr := d("0")
	r := screener.Rule{ID: "c1", Name: "carry", Enabled: true, Kind: screener.RuleKindCarry, MinCarryAPR: &apr,
		Quotes: []string{"USDT"}}
	setQuote(svc.Book, screener.VenueBinance, "BTC", "49999", "1", "50000", "1", false, t0)
	svc.Book.SetPerp(screener.Perp{Venue: screener.VenueBinance, Base: "BTC", Quote: "USDT", Mark: d("500000"), Bid: d("500000"), Ask: d("500010"),
		FundingRate: d("0.0001"), IntervalH: 8, NextFundingAt: t0.Add(4 * time.Hour), At: t0})
	in := Inputs{Book: svc.Book, SpotFees: feeLookup(svc, false), PerpFees: feeLookup(svc, true), PollInterval: 5 * time.Second}
	sigs := ComputeSignals(context.Background(), in, r, t0)
	if len(sigs) != 1 || sigs[0].Active || sigs[0].Reason != screener.SkipSuspectMismatch {
		t.Fatalf("carry signal = %+v", sigs)
	}
	setQuote(svc.Book, screener.VenueBinance, "BTC", "49999", "0", "50000", "0", true, t0)
	svc.Book.SetPerp(screener.Perp{Venue: screener.VenueBinance, Base: "BTC", Quote: "USDT", Mark: d("50010"), Bid: d("50010"), Ask: d("50011"),
		FundingRate: d("0.0001"), IntervalH: 8, NextFundingAt: t0.Add(4 * time.Hour), At: t0})
	sigs = ComputeSignals(context.Background(), in, r, t0)
	if len(sigs) != 1 || sigs[0].Active || sigs[0].Reason != screener.SkipLiquidityUnknown {
		t.Fatalf("carry signal with unknown spot size = %+v", sigs)
	}
}
