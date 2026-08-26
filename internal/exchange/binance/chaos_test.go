package binance

import (
	"fmt"
	"math/rand"
	"testing"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/exchange"
	"github.com/cploutarchou/arb-chain-bot/internal/orderbook"
)

// Chaos suite (SKILL.md §71): every injected fault must end in a SAFE
// state — the book is HEALTHY only when its contents are provably
// consistent, and any uncertainty degrades to SYNCING/CORRUPTED/STALE
// where the scanner will not touch it. "The platform must fail safely."

func chaosBookAndSyncer() (*orderbook.Book, *Syncer) {
	book := orderbook.New(exchange.MarketID{Exchange: ID, Symbol: "BTCUSDT"}, 0)
	return book, NewSyncer(book, 1000)
}

func mustHealthy(t *testing.T, book *orderbook.Book, wantLastID int64) {
	t.Helper()
	if st := book.View(0).State; st != orderbook.StateHealthy {
		t.Fatalf("state = %s, want HEALTHY", st)
	}
	if got := book.Meta().LastUpdateID; got != wantLastID {
		t.Fatalf("lastUpdateID = %d, want %d", got, wantLastID)
	}
}

// Message duplication flood: the same delta replayed many times mutates
// the book exactly once.
func TestChaosDuplicationFlood(t *testing.T) {
	book, s := chaosBookAndSyncer()
	if err := s.OnSnapshot(snap(100)); err != nil {
		t.Fatal(err)
	}
	ev := delta(101, 101, orderbook.Level{Price: d("101"), Qty: d("7")})
	for i := 0; i < 500; i++ {
		if _, err := s.OnDelta(ev); err != nil {
			t.Fatal(err)
		}
	}
	mustHealthy(t, book, 101)
	if v := book.View(0); len(v.Bids) != 2 || !v.Bids[0].Qty.Equal(d("7")) {
		t.Fatalf("book after flood: %+v", v.Bids)
	}
	// Exactly one applied mutation beyond the snapshot.
	if v := book.View(0); v.Version != 2 {
		t.Fatalf("version = %d, want 2", v.Version)
	}
}

// Message loss: a hole in the chain corrupts the book, the scanner-facing
// state is never HEALTHY until a fresh snapshot heals it.
func TestChaosMessageLoss(t *testing.T) {
	book, s := chaosBookAndSyncer()
	_ = s.OnSnapshot(snap(100))
	_, _ = s.OnDelta(delta(101, 102))
	// 103..110 lost.
	action, _ := s.OnDelta(delta(111, 112))
	if action != orderbook.ActionGap {
		t.Fatalf("action = %s", action)
	}
	if st := book.View(0).State; st != orderbook.StateSyncing {
		// MarkSyncing follows corruption inside the syncer's gap path.
		t.Fatalf("state after loss = %s", st)
	}
	// Deltas keep buffering; a newer snapshot heals.
	_, _ = s.OnDelta(delta(120, 121))
	if err := s.OnSnapshot(snap(119)); err != nil {
		t.Fatal(err)
	}
	mustHealthy(t, book, 121)
}

// Out-of-order delivery after init: late frames are dropped, never
// applied backwards.
func TestChaosOutOfOrderDelivery(t *testing.T) {
	book, s := chaosBookAndSyncer()
	_ = s.OnSnapshot(snap(100))
	_, _ = s.OnDelta(delta(101, 103, orderbook.Level{Price: d("101"), Qty: d("1")}))
	// A frame from the past arrives late.
	action, _ := s.OnDelta(delta(99, 100, orderbook.Level{Price: d("50"), Qty: d("9")}))
	if action != orderbook.ActionDrop {
		t.Fatalf("late frame action = %s", action)
	}
	if v := book.View(0); len(v.Bids) != 2 {
		t.Fatalf("late frame mutated book: %+v", v.Bids)
	}
	mustHealthy(t, book, 103)
}

// Snapshot delay: deltas buffer while REST is slow; splice succeeds when
// the snapshot finally lands mid-stream.
func TestChaosSnapshotDelay(t *testing.T) {
	book, s := chaosBookAndSyncer()
	for i := int64(0); i < 200; i++ {
		if _, err := s.OnDelta(delta(101+i*2, 102+i*2)); err != nil {
			t.Fatal(err)
		}
	}
	if s.Synced() {
		t.Fatal("synced without snapshot")
	}
	if err := s.OnSnapshot(snap(300)); err != nil {
		t.Fatal(err)
	}
	mustHealthy(t, book, 500)
}

// REST failure during resync: repeated stale snapshots never fake a sync;
// the book stays out of HEALTHY until a usable snapshot arrives.
func TestChaosRESTFailureDuringResync(t *testing.T) {
	book, s := chaosBookAndSyncer()
	_, _ = s.OnDelta(delta(500, 501))
	for i := 0; i < 5; i++ {
		if err := s.OnSnapshot(snap(100)); err != ErrSnapshotBehindBuffer {
			t.Fatalf("stale snapshot attempt %d: %v", i, err)
		}
		if s.Synced() || book.View(0).State == orderbook.StateHealthy {
			t.Fatal("stale snapshot produced a healthy book")
		}
	}
	if err := s.OnSnapshot(snap(499)); err != nil {
		t.Fatal(err)
	}
	mustHealthy(t, book, 501)
}

// WS freeze: no frames arriving degrades HEALTHY → STALE via the
// staleness sweep; a resumed feed restores HEALTHY.
func TestChaosWSFreeze(t *testing.T) {
	book, s := chaosBookAndSyncer()
	_ = s.OnSnapshot(snap(100))
	frozenAt := recvT
	if st := book.EvaluateStaleness(frozenAt.Add(5*time.Second), 2*time.Second); st != orderbook.StateStale {
		t.Fatalf("state after freeze = %s", st)
	}
	// Feed resumes.
	ev := delta(101, 101)
	ev.ReceiveTime = frozenAt.Add(6 * time.Second)
	if action, _ := s.OnDelta(ev); action != orderbook.ActionApply {
		t.Fatal("resume apply failed")
	}
	mustHealthy(t, book, 101)
}

// Clock skew: receive timestamps from a skewed clock (in the future) must
// not manufacture freshness for old data — age uses the platform's view
// of now, and a *negative* age (future receive time) is a data-quality
// problem surfaced by the risk gate, never a crash.
func TestChaosClockSkew(t *testing.T) {
	book, s := chaosBookAndSyncer()
	_ = s.OnSnapshot(snap(100))
	ev := delta(101, 101)
	ev.ReceiveTime = recvT.Add(1 * time.Hour) // skewed into the future
	_, _ = s.OnDelta(ev)
	v := book.View(0)
	if v.Age(recvT.Add(time.Second)) > 0 {
		t.Fatalf("future-skewed receive time reads positive age: %s", v.Age(recvT.Add(time.Second)))
	}
	// The zero-receive-time variant reads as infinitely old (never trusted).
	empty := orderbook.View{}
	if empty.Age(recvT) < time.Hour*24*365 {
		t.Fatal("zero receive time must read as effectively infinite age")
	}
}

// Reconnect storm: session churn (disconnect → syncing → snapshot) at
// high frequency always converges to a consistent HEALTHY book and never
// leaks a stale HEALTHY state mid-cycle.
func TestChaosReconnectStorm(t *testing.T) {
	book, s := chaosBookAndSyncer()
	last := int64(100)
	for cycle := 0; cycle < 50; cycle++ {
		_ = s.OnSnapshot(snap(last))
		mustHealthy(t, book, last)
		book.MarkDisconnected()
		if st := book.View(0).State; st != orderbook.StateDisconnected {
			t.Fatalf("cycle %d: %s", cycle, st)
		}
		book.MarkSyncing()
		s = NewSyncer(book, 1000) // fresh session, fresh syncer
		last += 10
	}
	_ = s.OnSnapshot(snap(last))
	mustHealthy(t, book, last)
}

// Message burst: a large randomized burst of valid chained deltas applies
// fully with the chain intact (coalescing/dirty pressure is the Set's
// concern, tested in orderbook; here the syncer must keep up exactly).
func TestChaosMessageBurst(t *testing.T) {
	book, s := chaosBookAndSyncer()
	_ = s.OnSnapshot(snap(0))
	rng := rand.New(rand.NewSource(7)) //nolint:gosec // deterministic chaos
	last := int64(0)
	for i := 0; i < 10_000; i++ {
		next := last + 1 + rng.Int63n(3) // U may overlap or extend exactly
		ev := delta(last+1, next, orderbook.Level{
			Price: d(fmt.Sprintf("%d.%02d", 90+rng.Intn(20), rng.Intn(100))),
			Qty:   d(fmt.Sprintf("%d", rng.Intn(5))), // zeros exercise deletes
		})
		if action, err := s.OnDelta(ev); err != nil || action != orderbook.ActionApply {
			t.Fatalf("burst frame %d: action=%v err=%v", i, action, err)
		}
		last = next
	}
	mustHealthy(t, book, last)
}
