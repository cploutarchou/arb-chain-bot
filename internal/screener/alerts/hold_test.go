package alerts

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/screener"
)

// staleLeg re-stamps the Binance leg 6 s before at: one late poll on one
// venue, the market unchanged.
func staleLeg(book *screener.Book, at time.Time) {
	setSpread(book, "50260", at)
	q := book.QuotesFor("BTC", "USDT")[screener.VenueBinance]
	q.At = at.Add(-6 * time.Second)
	book.SetQuote(q)
}

func openAt10(t *testing.T, svc *screener.Service, fn *fakeNotifier) *Evaluator {
	t.Helper()
	r := spreadRule()
	if _, err := svc.Rules.InsertRule(context.Background(), r, "t"); err != nil {
		t.Fatal(err)
	}
	ev := New(svc, fn.Notify, slog.New(slog.NewTextHandler(io.Discard, nil)))
	for _, s := range []int{0, 5, 10} {
		at := t0.Add(time.Duration(s) * time.Second)
		setSpread(svc.Book, "50260", at)
		ev.Tick(context.Background(), at)
	}
	if len(ev.OpenEvents()) != 1 || fn.count() != 1 {
		t.Fatalf("setup: open=%d notified=%d", len(ev.OpenEvents()), fn.count())
	}
	return ev
}

// TestSingleLatePollHoldsInsteadOfClosing: one stale tick used to close
// the event (a "signal ended" notification, a cooldown, lifetime reset).
// It now HOLDS: the event stays open carrying hold_reason, no close text
// goes out, and when fresh quotes return the lifetime continues from the
// original first-seen tick. A genuine market drop still closes at once,
// and the close reason is stored.
func TestSingleLatePollHoldsInsteadOfClosing(t *testing.T) {
	svc := newSvc(t)
	fn := &fakeNotifier{}
	ev := openAt10(t, svc, fn)
	ctx := context.Background()

	staleLeg(svc.Book, t0.Add(15*time.Second))
	ev.Tick(ctx, t0.Add(15*time.Second))
	open := ev.OpenEvents()
	if len(open) != 1 || open[0].HoldReason != ReasonDataAge {
		t.Fatalf("after one stale tick: open=%+v, want the event open with hold_reason DATA_AGE", open)
	}
	if fn.count() != 1 {
		t.Fatalf("a close notification went out on a stale tick (%d)", fn.count())
	}
	if st := ev.LaneStats(); st.Holding != 1 {
		t.Fatalf("holding = %d, want 1", st.Holding)
	}
	if sig := ev.Snapshot()["r1|BTC/USDT|binance>okx"]; sig.Active || sig.Reason != ReasonDataAge {
		t.Fatalf("held lane signal = active %v reason %q", sig.Active, sig.Reason)
	}

	setSpread(svc.Book, "50260", t0.Add(20*time.Second))
	ev.Tick(ctx, t0.Add(20*time.Second))
	open = ev.OpenEvents()
	if len(open) != 1 || open[0].HoldReason != "" || fn.count() != 1 {
		t.Fatalf("after recovery: open=%+v notified=%d", open, fn.count())
	}
	if st := ev.LaneStats(); st.Holding != 0 || st.HoldTimeoutCloses != 0 {
		t.Fatalf("stats after recovery = %+v", st)
	}

	setSpread(svc.Book, "50150", t0.Add(25*time.Second)) // the spread really ends
	ev.Tick(ctx, t0.Add(25*time.Second))
	if len(ev.OpenEvents()) != 0 || fn.count() != 2 {
		t.Fatalf("market close: open=%d notified=%d", len(ev.OpenEvents()), fn.count())
	}
	events, _ := svc.Events.ListEvents(ctx, "r1", 10)
	if len(events) != 1 || events[0].ClosedAt == nil || events[0].CloseReason != "below_min_spread" || events[0].HoldReason != "" {
		t.Fatalf("stored event = %+v, want closed with close_reason below_min_spread", events)
	}
	if events[0].LifetimeS != 25 {
		t.Errorf("lifetime_s = %d, want 25 (first seen t0, held 15..20, closed t0+25)", events[0].LifetimeS)
	}
	if !strings.Contains(fn.events[1].Body, "reason below_min_spread") {
		t.Errorf("close text lacks the reason: %s", fn.events[1].Body)
	}
}

// TestMinLifetimeSurvivesStaleTick: a lane warming up toward
// min_lifetime_s keeps its first-seen time across a stale tick, so a
// 10 s minimum is reached at t0+10 despite t0+5 being stale.
func TestMinLifetimeSurvivesStaleTick(t *testing.T) {
	svc := newSvc(t)
	r := spreadRule()
	if _, err := svc.Rules.InsertRule(context.Background(), r, "t"); err != nil {
		t.Fatal(err)
	}
	ev := New(svc, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx := context.Background()
	setSpread(svc.Book, "50260", t0)
	ev.Tick(ctx, t0)
	staleLeg(svc.Book, t0.Add(5*time.Second))
	ev.Tick(ctx, t0.Add(5*time.Second))
	setSpread(svc.Book, "50260", t0.Add(10*time.Second))
	ev.Tick(ctx, t0.Add(10*time.Second))
	open := ev.OpenEvents()
	if len(open) != 1 || open[0].LifetimeS != 10 {
		t.Fatalf("open events = %+v, want one opened with lifetime 10", open)
	}
}

// TestHoldTimeoutClosesWithReason: the hold is bounded by
// settings.alerts.stale_hold_s (12 s here). Held from t0+15, the event
// is still open at t0+25 (10 s) and closes at t0+30 (15 s) with reason
// HOLD_TIMEOUT stored on the event, in the close text and counted.
func TestHoldTimeoutClosesWithReason(t *testing.T) {
	svc := newSvc(t)
	applyAlerts(t, svc, func(s *screener.Settings) { s.Alerts.StaleHoldS = 12 })
	fn := &fakeNotifier{}
	ev := openAt10(t, svc, fn)
	ctx := context.Background()
	for _, s := range []int{15, 20, 25} {
		at := t0.Add(time.Duration(s) * time.Second)
		staleLeg(svc.Book, at)
		ev.Tick(ctx, at)
		if len(ev.OpenEvents()) != 1 {
			t.Fatalf("t0+%ds: event closed inside the hold window", s)
		}
	}
	staleLeg(svc.Book, t0.Add(30*time.Second))
	ev.Tick(ctx, t0.Add(30*time.Second))
	if len(ev.OpenEvents()) != 0 || fn.count() != 2 {
		t.Fatalf("after the hold timeout: open=%d notified=%d", len(ev.OpenEvents()), fn.count())
	}
	events, _ := svc.Events.ListEvents(ctx, "r1", 10)
	if len(events) != 1 || events[0].CloseReason != CloseReasonHoldTimeout || events[0].ClosedAt == nil {
		t.Fatalf("stored event = %+v, want close_reason HOLD_TIMEOUT", events)
	}
	if events[0].LifetimeS != 30 {
		t.Errorf("lifetime_s = %d, want 30", events[0].LifetimeS)
	}
	if !strings.Contains(fn.events[1].Body, "reason "+CloseReasonHoldTimeout) {
		t.Errorf("close text lacks HOLD_TIMEOUT: %s", fn.events[1].Body)
	}
	if st := ev.LaneStats(); st.HoldTimeoutCloses != 1 || st.Holding != 0 {
		t.Fatalf("stats = %+v", st)
	}
	// The lifetime was reset by the timeout: fresh quotes start a new
	// lifetime rather than reopening at once.
	setSpread(svc.Book, "50260", t0.Add(35*time.Second))
	ev.Tick(ctx, t0.Add(35*time.Second))
	if len(ev.OpenEvents()) != 0 {
		t.Fatal("reopened immediately after a hold timeout: the lifetime was not reset")
	}
}

// TestLaneGoneClosesWithReason: configuration removing the lane (rule
// disabled) closes at once — no hold — with close_reason lane_gone.
func TestLaneGoneClosesWithReason(t *testing.T) {
	svc := newSvc(t)
	fn := &fakeNotifier{}
	ev := openAt10(t, svc, fn)
	ctx := context.Background()
	r, _ := svc.Rules.GetRule(ctx, "r1")
	r.Enabled = false
	if _, err := svc.Rules.UpdateRule(ctx, r, "t"); err != nil {
		t.Fatal(err)
	}
	setSpread(svc.Book, "50260", t0.Add(15*time.Second))
	ev.Tick(ctx, t0.Add(15*time.Second))
	events, _ := svc.Events.ListEvents(ctx, "r1", 10)
	if len(ev.OpenEvents()) != 0 || len(events) != 1 || events[0].CloseReason != CloseReasonLaneGone {
		t.Fatalf("open=%d events=%+v, want closed with lane_gone", len(ev.OpenEvents()), events)
	}
}

// TestReplayUnsynchronisedCadenceNoSpuriousCloses replays the audited
// phase problem: a venue polling every 9.1 s (5 s interval + a 4.1 s
// poll, HTX's soak average) against a 5 s evaluator tick, the spread
// constantly active. Every other tick sees a quote older than 5 s. With
// the lifetime reset on each such tick a 30 s min_lifetime_s could
// never accumulate; with the hold the event opens at t=30 s and over 200
// ticks (1 000 s) is never closed.
func TestReplayUnsynchronisedCadenceNoSpuriousCloses(t *testing.T) {
	svc := newSvc(t)
	r := spreadRule()
	r.MinLifetimeS, r.CooldownS = 30, 60
	if _, err := svc.Rules.InsertRule(context.Background(), r, "t"); err != nil {
		t.Fatal(err)
	}
	fn := &fakeNotifier{}
	ev := New(svc, fn.Notify, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx := context.Background()
	const cadence = 9100 * time.Millisecond
	staleTicks, openedAt := 0, time.Time{}
	for k := 0; k < 200; k++ {
		now := t0.Add(time.Duration(k) * 5 * time.Second)
		lastPoll := t0.Add(now.Sub(t0) / cadence * cadence) // most recent completed poll
		setSpread(svc.Book, "50260", lastPoll)
		ev.Tick(ctx, now)
		if sig := ev.Snapshot()["r1|BTC/USDT|binance>okx"]; sig.Reason == ReasonDataAge {
			staleTicks++
		}
		if len(ev.OpenEvents()) == 1 && openedAt.IsZero() {
			openedAt = now
		}
	}
	if staleTicks == 0 {
		t.Fatal("the replay never produced a stale tick; the cadence mismatch is not being exercised")
	}
	if openedAt.IsZero() || !openedAt.Equal(t0.Add(30*time.Second)) {
		t.Fatalf("opened at %v, want t0+30s (lifetime preserved across %d stale ticks)", openedAt, staleTicks)
	}
	if n := fn.count(); n != 1 {
		t.Fatalf("notifications = %d, want exactly the one open (no closes)", n)
	}
	if len(ev.OpenEvents()) != 1 || ev.LaneStats().HoldTimeoutCloses != 0 {
		t.Fatalf("open=%d stats=%+v", len(ev.OpenEvents()), ev.LaneStats())
	}
	t.Logf("replay: 200 ticks, %d stale ticks, 1 open at %s, 0 closes", staleTicks, openedAt.Sub(t0))
}
