package app

import (
	"context"
	"errors"
	"testing"
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
