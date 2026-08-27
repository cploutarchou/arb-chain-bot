package app

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// TestReadModelHealthIncludesQueuesAndRates covers BL-18's engine-derived
// additions to Health(): a message rate under feed, a latency
// percentile snapshot (present but empty — the harness's WS host is
// unroutable, so zero frames land), and the paper engine's inbound
// queue depth (ModePaper is on in newTestEngine's settings). Persistence
// is off in this harness, so queues.outbox is correctly absent.
func TestReadModelHealthIncludesQueuesAndRates(t *testing.T) {
	e, stratSvc := newTestEngine(t)
	if err := runOnce(t, e); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}

	rm := NewReadModel(e, stratSvc)
	h, ok := rm.Health().(map[string]any)
	if !ok {
		t.Fatalf("Health() = %T, want map[string]any", rm.Health())
	}

	feed, ok := h["feed"].(map[string]any)
	if !ok {
		t.Fatalf("feed section missing or wrong type: %+v", h)
	}
	if _, ok := feed["msgs_per_sec"]; !ok {
		t.Fatalf("msgs_per_sec missing: %+v", feed)
	}
	lat, ok := feed["latency_ms"].(LatencySnapshot)
	if !ok {
		t.Fatalf("latency_ms missing or wrong type: %+v", feed)
	}
	if lat.N != 0 {
		t.Fatalf("expected zero latency samples against an unroutable WS host, got n=%d", lat.N)
	}

	queues, ok := h["queues"].(map[string]any)
	if !ok {
		t.Fatalf("queues section missing: %+v", h)
	}
	if _, ok := queues["paper"]; !ok {
		t.Fatalf("paper queue depth missing (PAPER mode): %+v", queues)
	}
	if _, ok := queues["outbox"]; ok {
		t.Fatalf("outbox queue depth present without persistence: %+v", queues)
	}
}

// TestReadModelHealthConcurrentPollersSeeSameRate is the review P2-1
// regression at the readModel/Engine integration level (latency_test.go
// covers rateSampler in isolation): once the engine's ticker has taken a
// sample, any number of concurrent Health() callers — simulating
// multiple browser tabs or web + Telegram polling at once — must observe
// the SAME msgs_per_sec, not a value diminished by however many other
// pollers happened to read it first.
func TestReadModelHealthConcurrentPollersSeeSameRate(t *testing.T) {
	e, stratSvc := newTestEngine(t)
	if err := runOnce(t, e); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}

	rate := e.currentMsgRate()
	if rate == nil {
		t.Fatal("expected a message-rate sampler once Run has reached its reset block")
	}
	t0 := time.Unix(1_700_000_000, 0)
	rate.sample(t0, 0)
	rate.sample(t0.Add(time.Second), 100) // pretend the ticker observed 100 frames/sec

	rm := NewReadModel(e, stratSvc)
	const pollers = 8
	got := make([]float64, pollers)
	var wg sync.WaitGroup
	for i := 0; i < pollers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			h := rm.Health().(map[string]any)
			feed := h["feed"].(map[string]any)
			got[i] = feed["msgs_per_sec"].(float64)
		}(i)
	}
	wg.Wait()

	for i, v := range got {
		if v != 100 {
			t.Fatalf("poller %d: msgs_per_sec = %v, want 100 (concurrent pollers must not steal each other's delta)", i, v)
		}
	}
}
