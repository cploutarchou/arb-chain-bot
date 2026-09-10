package alerts

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/screener"
)

// bigBook fills 600 stale lanes whose net (≈ 150 bps before fees) beats
// the ONE genuine lane (BTC, 29.95 bps net, fresh) and one 6 s old leg
// each, so every artefact outranks the real lane on net bps alone.
func bigBook(book *screener.Book, now time.Time) {
	for i := 0; i < 600; i++ {
		base := fmt.Sprintf("B%03d", i)
		book.SetQuote(screener.Quote{Venue: screener.VenueBinance, Base: base, Quote: "USDT",
			Ask: d("100"), AskQty: d("100"), Bid: d("99.5"), BidQty: d("100"), At: now.Add(-6 * time.Second)})
		book.SetQuote(screener.Quote{Venue: screener.VenueOKX, Base: base, Quote: "USDT",
			Bid: d("101.5"), BidQty: d("100"), Ask: d("102"), AskQty: d("100"), At: now})
	}
	setSpread(book, "50250", now)
}

func applyAlerts(t *testing.T, svc *screener.Service, mutate func(*screener.Settings)) {
	t.Helper()
	doc := svc.Current().Settings
	mutate(&doc)
	if _, err := svc.ApplyExpect(context.Background(), "t", "test", doc, 0); err != nil {
		t.Fatal(err)
	}
}

// TestEvaluatorSeesLaneRankedPastOldCap: the genuine lane ranks 601st by
// net bps. With the universe capped at the 500 highest-net rows it was
// never evaluated and never opened; uncapped by default it opens on the
// first tick and the accounting says nothing was cut.
func TestEvaluatorSeesLaneRankedPastOldCap(t *testing.T) {
	svc := newSvc(t)
	r := spreadRule()
	r.MinLifetimeS, r.CooldownS = 0, 0
	if _, err := svc.Rules.InsertRule(context.Background(), r, "t"); err != nil {
		t.Fatal(err)
	}
	bigBook(svc.Book, t0)
	fn := &fakeNotifier{}
	ev := New(svc, fn.Notify, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ev.Tick(context.Background(), t0)

	open := ev.OpenEvents()
	if len(open) != 1 || open[0].Base != "BTC" {
		t.Fatalf("open events = %+v, want exactly the BTC lane ranked 601st by net", open)
	}
	st := ev.LaneStats()
	if st.Rules != 1 || st.Universe != 601 || st.Lanes != 601 || st.Truncated != 0 || st.MaxLanesPerRule != 0 {
		t.Fatalf("lane stats = %+v, want 601 lanes evaluated, none truncated", st)
	}
	for i := 0; i < 600; i++ {
		s, ok := ev.Snapshot()[fmt.Sprintf("r1|B%03d/USDT|binance>okx", i)]
		if !ok || s.Active || s.Reason != ReasonDataAge {
			t.Fatalf("stale lane %d: present=%v active=%v reason=%q", i, ok, s.Active, s.Reason)
		}
	}
}

// TestEvaluatorCapRanksQualityFirstAndCountsTruncation: an operator cap
// of 10 keeps the genuine lane (clean and fresh lanes rank first) and
// reports the 591 lanes it hid, on the evaluator, in the status
// diagnostics and in the cumulative counter.
func TestEvaluatorCapRanksQualityFirstAndCountsTruncation(t *testing.T) {
	svc := newSvc(t)
	applyAlerts(t, svc, func(s *screener.Settings) { s.Alerts.MaxLanesPerRule = 10 })
	r := spreadRule()
	r.MinLifetimeS, r.CooldownS = 0, 0
	if _, err := svc.Rules.InsertRule(context.Background(), r, "t"); err != nil {
		t.Fatal(err)
	}
	bigBook(svc.Book, t0)
	ev := New(svc, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ev.Tick(context.Background(), t0)
	if open := ev.OpenEvents(); len(open) != 1 || open[0].Base != "BTC" {
		t.Fatalf("open events = %+v, want the BTC lane kept by the quality ranking", open)
	}
	st := ev.LaneStats()
	if st.Universe != 601 || st.Lanes != 10 || st.Truncated != 591 || st.TruncatedTotal != 591 || st.MaxLanesPerRule != 10 {
		t.Fatalf("lane stats = %+v, want universe 601, evaluated 10, truncated 591", st)
	}
	if len(ev.Snapshot()) != 10 {
		t.Fatalf("signals kept = %d, want 10", len(ev.Snapshot()))
	}
	diag := svc.Diagnostics()["alerts"]
	if diag["truncated"] != 591 || diag["universe"] != 601 || diag["lanes"] != 10 || diag["max_lanes_per_rule"] != 10 {
		t.Fatalf("status diagnostics = %v", diag)
	}
	bigBook(svc.Book, t0.Add(5*time.Second))
	ev.Tick(context.Background(), t0.Add(5*time.Second))
	if st := ev.LaneStats(); st.Truncated != 591 || st.TruncatedTotal != 1182 {
		t.Fatalf("second tick stats = %+v, want truncated 591 and total 1182", st)
	}
	// Lifting the cap on the live document is honoured on the next tick.
	applyAlerts(t, svc, func(s *screener.Settings) { s.Alerts.MaxLanesPerRule = 0 })
	bigBook(svc.Book, t0.Add(10*time.Second))
	ev.Tick(context.Background(), t0.Add(10*time.Second))
	if st := ev.LaneStats(); st.Lanes != 601 || st.Truncated != 0 || st.TruncatedTotal != 1182 {
		t.Fatalf("uncapped stats = %+v", st)
	}
}
