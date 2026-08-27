package app

import (
	"fmt"
	"testing"
	"time"
)

// TestShouldPersistRiskRejectThrottlesRepeats covers BL-31's outbox
// protection: the first (triangle, reason) rejection is always due; a
// repeat within the cooldown window is not; a different reason for the
// SAME triangle, or the same reason for a DIFFERENT triangle, is its own
// key and is due independently; after the cooldown elapses the same key
// is due again.
func TestShouldPersistRiskRejectThrottlesRepeats(t *testing.T) {
	e := &Engine{}
	t0 := time.Unix(1_700_000_000, 0)

	if !e.shouldPersistRiskReject("tri-a", "MIN_EDGE", t0) {
		t.Fatal("first occurrence must be due")
	}
	if e.shouldPersistRiskReject("tri-a", "MIN_EDGE", t0.Add(time.Second)) {
		t.Fatal("repeat within the cooldown must not be due")
	}
	if !e.shouldPersistRiskReject("tri-a", "BREAKER_OPEN", t0.Add(time.Second)) {
		t.Fatal("a different reason on the same triangle is its own key")
	}
	if !e.shouldPersistRiskReject("tri-b", "MIN_EDGE", t0.Add(time.Second)) {
		t.Fatal("the same reason on a different triangle is its own key")
	}
	if !e.shouldPersistRiskReject("tri-a", "MIN_EDGE", t0.Add(riskRejectCooldown+time.Second)) {
		t.Fatal("after the cooldown elapses the same key is due again")
	}
}

// TestShouldPersistRiskRejectBoundsMapSize mirrors countReject's guard:
// a pathological number of distinct (triangle, reason) pairs must not
// grow the cooldown map without bound.
func TestShouldPersistRiskRejectBoundsMapSize(t *testing.T) {
	e := &Engine{}
	now := time.Unix(1_700_000_000, 0)
	due := 0
	for i := 0; i < 5000; i++ {
		if e.shouldPersistRiskReject(string(rune('a'+i%26)), string(rune('A'+i/26%26)), now) {
			due++
		}
	}
	e.oppMu.Lock()
	size := len(e.rejectPersistedAt)
	e.oppMu.Unlock()
	if size > 4096 {
		t.Fatalf("cooldown map grew unbounded: %d entries", size)
	}
}

// TestShouldPersistRiskRejectAtCapStillUpdatesKnownKeys is the review
// P2-2 regression: once the map is AT capacity, a key that is already
// IN the map and whose cooldown has expired must still be allowed
// through (it updates in place, it does not grow the map) — the bug was
// that the old cap check ran unconditionally and refused this case too,
// so risk_events silently stopped recording for triangles that kept
// rejecting for the SAME reason once any 4096 distinct pairs had ever
// been seen.
func TestShouldPersistRiskRejectAtCapStillUpdatesKnownKeys(t *testing.T) {
	e := &Engine{}
	now := time.Unix(1_700_000_000, 0)

	// Fill the map to capacity with distinct, never-repeating keys.
	for i := 0; i < 4096; i++ {
		key := fmt.Sprintf("tri-%d", i)
		if !e.shouldPersistRiskReject(key, "MIN_EDGE", now) {
			t.Fatalf("seeding entry %d: expected due", i)
		}
	}
	e.oppMu.Lock()
	size := len(e.rejectPersistedAt)
	e.oppMu.Unlock()
	if size < 4096 {
		t.Fatalf("setup: map not at capacity: %d entries", size)
	}

	// A NEW, never-before-seen key must be refused (still bounded) while
	// at capacity and before any entries expire.
	if e.shouldPersistRiskReject("brand-new-triangle", "MIN_EDGE", now) {
		t.Fatal("a genuinely new key at capacity must be refused")
	}

	// An EXISTING key whose cooldown has expired must still be allowed
	// through even though the map is at capacity — this is the bug: the
	// old code refused it too.
	later := now.Add(riskRejectCooldown + time.Second)
	if !e.shouldPersistRiskReject("tri-0", "MIN_EDGE", later) {
		t.Fatal("an existing key past its cooldown must persist even at capacity (P2-2 regression)")
	}

	// The cap-refusal warning logs at most once, not once per refused
	// call.
	e.oppMu.Lock()
	logged := e.rejectCapLogged
	e.oppMu.Unlock()
	if !logged {
		t.Fatal("expected the at-capacity warning to have been recorded")
	}
}

// TestShouldPersistRiskRejectAtCapPrunesExpiredEntries covers the other
// half of the P2-2 fix: once entries whose cooldown has elapsed exist in
// an at-capacity map, a genuinely NEW key must be able to reclaim that
// space instead of being refused forever.
func TestShouldPersistRiskRejectAtCapPrunesExpiredEntries(t *testing.T) {
	e := &Engine{}
	now := time.Unix(1_700_000_000, 0)

	for i := 0; i < 4096; i++ {
		key := fmt.Sprintf("tri-%d", i)
		if !e.shouldPersistRiskReject(key, "MIN_EDGE", now) {
			t.Fatalf("seeding entry %d: expected due", i)
		}
	}

	later := now.Add(riskRejectCooldown + time.Second)
	if !e.shouldPersistRiskReject("brand-new-triangle", "MIN_EDGE", later) {
		t.Fatal("a new key must reclaim space once earlier entries have expired")
	}
}
