package binance

import (
	"context"
	"testing"
	"time"
)

func TestRESTGateWindowAndBan(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	g := &restGate{now: func() time.Time { return now }}

	// 90 × 50 = 4500 fits exactly; the 91st must wait for the oldest.
	for i := 0; i < 90; i++ {
		if d := g.delay(now, 50); d != 0 {
			t.Fatalf("call %d delayed %v", i, d)
		}
		g.spent = append(g.spent, weightSpend{at: now, w: 50})
	}
	if d := g.delay(now, 50); d <= 0 {
		t.Fatal("window overflow was not delayed")
	}
	now = now.Add(61 * time.Second)
	g.prune(now)
	if len(g.spent) != 0 || g.delay(now, 250) != 0 {
		t.Fatalf("window did not roll: %d entries", len(g.spent))
	}

	// A 418 with Retry-After blocks every call until the deadline.
	if !g.observe(&HTTPError{Status: 418, RetryAfter: "30"}) {
		t.Fatal("418 not recognised")
	}
	if g.observe(&HTTPError{Status: 500}) {
		t.Fatal("500 treated as rate limit")
	}
	if d := g.blockedFor(); d < 30*time.Second || d > 32*time.Second {
		t.Fatalf("blockedFor = %v", d)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := g.wait(ctx, 5); err == nil {
		t.Fatal("wait returned during ban")
	}
	now = now.Add(40 * time.Second)
	if err := g.wait(context.Background(), 5); err != nil {
		t.Fatal(err)
	}
}

func TestDepthWeight(t *testing.T) {
	for limit, want := range map[int]int{50: 5, 100: 5, 500: 25, 1000: 50, 5000: 250} {
		if got := depthWeight(limit); got != want {
			t.Errorf("depthWeight(%d) = %d, want %d", limit, got, want)
		}
	}
	if depthWeight(SnapshotDepthLimit) > 50 {
		t.Fatalf("SnapshotDepthLimit %d costs %d weight; a 60-symbol universe must prime within one minute", SnapshotDepthLimit, depthWeight(SnapshotDepthLimit))
	}
}
