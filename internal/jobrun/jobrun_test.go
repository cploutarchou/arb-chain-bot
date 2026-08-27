package jobrun

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestNewOwnerIDIsUniquePerCall(t *testing.T) {
	a, b := NewOwnerID(), NewOwnerID()
	if a == "" || b == "" {
		t.Fatal("owner id must not be empty")
	}
	if a == b {
		t.Fatal("two calls must not collide (this would defeat the whole owner/heartbeat scheme)")
	}
}

func TestReclaimable(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	const mine, other = "owner-mine", "owner-other"

	cases := []struct {
		name        string
		isMine      bool
		rowOwner    string
		heartbeatAt time.Time
		want        bool
	}{
		{"mine is never reclaimed even if heartbeat looks stale", true, other, now.Add(-time.Hour), false},
		{"same owner id (should not happen, but defensive) is never reclaimed", false, mine, now, false},
		{"a different owner's fresh heartbeat is not reclaimed", false, other, now.Add(-time.Second), false},
		{"a different owner's heartbeat right at the staleness edge is not yet reclaimed", false, other, now.Add(-StaleAfter + time.Second), false},
		{"a different owner's stale heartbeat is reclaimed", false, other, now.Add(-StaleAfter - time.Second), true},
		{"a zero heartbeat (pre-migration row, or never heartbeated) is always reclaimed", false, other, time.Time{}, true},
		{"a zero heartbeat with no owner recorded at all is reclaimed", false, "", time.Time{}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Reclaimable(c.isMine, mine, c.rowOwner, c.heartbeatAt, now); got != c.want {
				t.Fatalf("Reclaimable(mine=%v, owner=%q, heartbeat=%v) = %v, want %v",
					c.isMine, c.rowOwner, c.heartbeatAt, got, c.want)
			}
		})
	}
}

func TestGateAwaitTimesOutBeforePin(t *testing.T) {
	var g Gate
	_, err := g.Await(20 * time.Millisecond)
	if !errors.Is(err, ErrNotStarted) {
		t.Fatalf("Await before Pin = %v, want ErrNotStarted", err)
	}
}

func TestGateAwaitReturnsThePinnedContext(t *testing.T) {
	var g Gate
	type key struct{}
	ctx := context.WithValue(context.Background(), key{}, "marker")
	g.Pin(ctx)

	got, err := g.Await(time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if got.Value(key{}) != "marker" {
		t.Fatal("Await did not return the pinned context")
	}
	if c := g.Current(); c.Value(key{}) != "marker" {
		t.Fatal("Current did not return the pinned context")
	}
}

// TestGateAwaitUnblocksConcurrentlyWithPin is the "Start reachable
// before Run" regression at the Gate level: a caller blocked in Await
// BEFORE Pin is ever called must unblock the moment Pin runs, with the
// context Pin supplied — not time out, not see context.Background().
func TestGateAwaitUnblocksConcurrentlyWithPin(t *testing.T) {
	var g Gate
	type key struct{}
	ctx := context.WithValue(context.Background(), key{}, "late-pin")

	var wg sync.WaitGroup
	results := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := g.Await(2 * time.Second)
			if err != nil {
				results <- err
				return
			}
			if got.Value(key{}) != "late-pin" {
				results <- errors.New("wrong context observed")
				return
			}
			results <- nil
		}()
	}
	time.Sleep(20 * time.Millisecond) // let every goroutine reach Await first
	g.Pin(ctx)
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
}

// TestGatePinIsIdempotentAcrossRepeatedRunCalls covers advisor point #2:
// a second Pin (a later Run call — restart of the component) must never
// panic (double-close), and Await after it observes the NEW context.
func TestGatePinIsIdempotentAcrossRepeatedRunCalls(t *testing.T) {
	var g Gate
	type key struct{}
	first := context.WithValue(context.Background(), key{}, "first")
	second := context.WithValue(context.Background(), key{}, "second")

	g.Pin(first)
	if got, err := g.Await(time.Second); err != nil || got.Value(key{}) != "first" {
		t.Fatalf("after first Pin: %v, %v", got, err)
	}

	// Must not panic.
	g.Pin(second)
	if got, err := g.Await(time.Second); err != nil || got.Value(key{}) != "second" {
		t.Fatalf("after second Pin: %v, %v", got, err)
	}
}

func TestBoundEvictsOldestFirstAndDeletesFromMap(t *testing.T) {
	byID := map[string]*int{}
	order := []string{}
	for i := 0; i < 5; i++ {
		id := string(rune('a' + i))
		v := i
		byID[id] = &v
		order = append(order, id)
		order = Bound(order, byID, 3)
	}
	if len(order) != 3 || len(byID) != 3 {
		t.Fatalf("order=%v byID has %d entries, want 3/3", order, len(byID))
	}
	// The three most recently appended (c, d, e) survive; a, b evicted.
	want := []string{"c", "d", "e"}
	for i, id := range want {
		if order[i] != id {
			t.Fatalf("order = %v, want %v", order, want)
		}
	}
	for _, evicted := range []string{"a", "b"} {
		if _, ok := byID[evicted]; ok {
			t.Fatalf("%s should have been deleted from byID", evicted)
		}
	}
}

func TestBoundIsNoOpUnderCapacity(t *testing.T) {
	byID := map[string]*int{"a": new(int)}
	order := []string{"a"}
	got := Bound(order, byID, 200)
	if len(got) != 1 || len(byID) != 1 {
		t.Fatalf("under-capacity Bound mutated state: order=%v byID=%v", got, byID)
	}
}

func TestClampLimit(t *testing.T) {
	cases := []struct {
		limit, def, max, want int
	}{
		{0, 50, 200, 50},    // unset -> default
		{-5, 50, 200, 50},   // negative -> default
		{10, 50, 200, 10},   // in range -> unchanged
		{200, 50, 200, 200}, // exactly at the cap -> unchanged
		{500, 50, 200, 200}, // over the cap -> CLAMPED TO THE CAP, not bounced to default
		{201, 50, 200, 200}, // just over -> clamped
	}
	for _, c := range cases {
		if got := ClampLimit(c.limit, c.def, c.max); got != c.want {
			t.Fatalf("ClampLimit(%d, %d, %d) = %d, want %d", c.limit, c.def, c.max, got, c.want)
		}
	}
}
