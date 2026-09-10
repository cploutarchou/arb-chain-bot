package alerts

import (
	"context"
	"testing"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/screener"
)

// TestRecoverClosesOpenEventsAsRestart (audit X5): rows still open from
// a previous process are closed with the restart reason — they can
// never be closed honestly (this process holds none of their lane
// state) and must not stay open forever. Already-closed rows keep their
// recorded reason and lifetime.
func TestRecoverClosesOpenEventsAsRestart(t *testing.T) {
	svc := newSvc(t)
	events := svc.Events.(*screener.MemoryEventStore)
	ctx := context.Background()

	openOld := screener.Event{ID: "evt-open-old", RuleID: "r1", Kind: screener.RuleKindSpread, OpenedAt: time.Now().Add(-time.Hour)}
	openRecent := screener.Event{ID: "evt-open-new", RuleID: "r2", Kind: screener.RuleKindSpread, OpenedAt: time.Now().Add(-time.Minute)}
	closed := screener.Event{ID: "evt-closed", RuleID: "r3", Kind: screener.RuleKindSpread, OpenedAt: time.Now().Add(-2 * time.Hour)}
	for _, ev := range []screener.Event{openOld, openRecent, closed} {
		if err := events.InsertEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}
	if err := events.CloseEventWithReason(ctx, closed.ID, time.Now(), 7200, "10", "below_min_spread"); err != nil {
		t.Fatal(err)
	}

	e := New(svc, nil, nil)
	e.Recover(ctx)

	rows, err := events.ListEvents(ctx, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]screener.Event{}
	for _, r := range rows {
		byID[r.ID] = r
	}
	for _, id := range []string{"evt-open-old", "evt-open-new"} {
		ev := byID[id]
		if ev.ClosedAt == nil {
			t.Fatalf("%s still open after recover", id)
		}
		if ev.CloseReason != screener.EventCloseReasonRestart {
			t.Fatalf("%s close reason = %q", id, ev.CloseReason)
		}
	}
	ev := byID["evt-closed"]
	if ev.CloseReason != "below_min_spread" {
		t.Fatalf("already-closed row rewritten: %+v", ev)
	}
}

// Recover without an event store or with a store that cannot sweep is a
// no-op, not a panic.
func TestRecoverWithoutSweepableStore(t *testing.T) {
	svc := newSvc(t)
	svc.Events = nil
	New(svc, nil, nil).Recover(context.Background())
}
