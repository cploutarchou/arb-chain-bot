package app

import (
	"sync"
	"testing"
	"time"
)

// TestRateSamplerCurrentDoesNotConsumeTheDelta is the review P2-1
// regression: the old rateSampler.rate(now, val) method mutated its
// internal (lastAt, lastVal) state on EVERY call, so if two callers
// polled it within the same interval, the second caller's call
// implicitly ended the window early and reported an understated rate —
// worse, a caller that merely wants to READ the current rate (without
// itself being the ticker driving the sample) had no way to do so
// without disturbing the next real sample.
//
// Now sample() (the ticker's job, single writer) advances the window;
// current() (any number of readers) only reads the last sampled value.
// Any number of concurrent current() calls between two sample() calls
// must return the identical value.
func TestRateSamplerCurrentDoesNotConsumeTheDelta(t *testing.T) {
	var s rateSampler
	t0 := time.Unix(1_700_000_000, 0)
	t1 := t0.Add(time.Second)

	s.sample(t0, 0)
	s.sample(t1, 100) // 100 units over 1s = 100/s

	const readers = 8
	rates := make([]float64, readers)
	var wg sync.WaitGroup
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rates[i], _ = s.current()
		}(i)
	}
	wg.Wait()

	for i, r := range rates {
		if r != 100 {
			t.Fatalf("reader %d: current() = %v, want 100 (a reader must never see a value diminished by another concurrent reader)", i, r)
		}
	}

	// A read-only current() call afterward must STILL report the same
	// value — proving current() truly never mutates the window.
	if r, _ := s.current(); r != 100 {
		t.Fatalf("current() after concurrent reads = %v, want 100 (current() must not mutate state)", r)
	}
}

// TestRateSamplerSampleAdvancesTheWindow is the write-side complement:
// only sample() (the single-writer ticker call) may advance the delta
// window.
func TestRateSamplerSampleAdvancesTheWindow(t *testing.T) {
	var s rateSampler
	t0 := time.Unix(1_700_000_000, 0)

	s.sample(t0, 0)
	if r, at := s.current(); r != 0 || !at.Equal(t0) {
		t.Fatalf("first sample: current() = (%v, %v), want (0, %v)", r, at, t0)
	}

	t1 := t0.Add(2 * time.Second)
	s.sample(t1, 50) // 50 units over 2s = 25/s
	if r, at := s.current(); r != 25 || !at.Equal(t1) {
		t.Fatalf("second sample: current() = (%v, %v), want (25, %v)", r, at, t1)
	}
}
