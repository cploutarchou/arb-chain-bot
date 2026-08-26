package app

import (
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
