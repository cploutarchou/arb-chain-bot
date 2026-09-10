package realtime

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"
)

func snapConst(v any) SnapshotFunc {
	return func() (json.RawMessage, error) { return json.Marshal(v) }
}

func recv(t *testing.T, s *Sink) Message {
	t.Helper()
	select {
	case m := <-s.Ch():
		return m
	default:
		t.Fatal("no message queued")
		return Message{}
	}
}

func TestSubscribeDeliversSnapshotThenDiffs(t *testing.T) {
	h := NewHub(8)
	h.RegisterTopic("scanner", snapConst(map[string]any{"rows": 0}))
	s := h.Attach()
	defer h.Detach(s)

	if err := h.Subscribe(s, "scanner"); err != nil {
		t.Fatal(err)
	}
	m := recv(t, s)
	if !m.Snapshot || m.Topic != "scanner" || m.Seq != 0 {
		t.Fatalf("snapshot = %+v", m)
	}
	_ = h.Publish("scanner", map[string]any{"row": 1})
	_ = h.Publish("scanner", map[string]any{"row": 2})
	d1, d2 := recv(t, s), recv(t, s)
	if d1.Seq != 1 || d2.Seq != 2 || d1.Snapshot {
		t.Fatalf("diffs = %+v %+v", d1, d2)
	}
}

func TestUnsubscribedTopicsNotDelivered(t *testing.T) {
	h := NewHub(8)
	h.RegisterTopic("scanner", snapConst(1))
	h.RegisterTopic("alerts", snapConst(2))
	s := h.Attach()
	defer h.Detach(s)
	_ = h.Subscribe(s, "alerts")
	recv(t, s) // alerts snapshot
	_ = h.Publish("scanner", "x")
	select {
	case m := <-s.Ch():
		t.Fatalf("unexpected delivery: %+v", m)
	default:
	}
	// Unknown topics are silently ignored on subscribe.
	if err := h.Subscribe(s, "nope"); err != nil {
		t.Fatal(err)
	}
}

// A slow client's diffs are dropped once the queue fills; after draining,
// RecoverIfLagged delivers a resync snapshot and diffs resume.
func TestLaggingClientResync(t *testing.T) {
	h := NewHub(2)
	h.RegisterTopic("scanner", snapConst("SNAP"))
	s := h.Attach()
	defer h.Detach(s)
	_ = h.Subscribe(s, "scanner")
	recv(t, s) // initial snapshot

	for i := 0; i < 5; i++ { // queue cap 2 → laggging after 2
		_ = h.Publish("scanner", i)
	}
	if got := len(s.ch); got != 2 {
		t.Fatalf("queued = %d", got)
	}
	// Drain (transport write pump).
	recv(t, s)
	recv(t, s)
	// Further diffs while lagged are dropped entirely.
	_ = h.Publish("scanner", "dropped")
	if len(s.ch) != 0 {
		t.Fatal("lagged sink received diff")
	}
	if err := h.RecoverIfLagged(s); err != nil {
		t.Fatal(err)
	}
	m := recv(t, s)
	if !m.Snapshot || !m.Resync || m.Seq != 6 {
		t.Fatalf("resync = %+v", m)
	}
	_ = h.Publish("scanner", "after")
	if m := recv(t, s); m.Seq != 7 || m.Snapshot {
		t.Fatalf("post-resync diff = %+v", m)
	}
}

func TestClientOpProtocol(t *testing.T) {
	h := NewHub(8)
	h.RegisterTopic("pnl", snapConst("p"))
	s := h.Attach()
	defer h.Detach(s)
	if err := h.HandleClientOp(context.Background(), s, []byte(`{"op":"subscribe","topics":["pnl"]}`), nil); err != nil {
		t.Fatal(err)
	}
	recv(t, s)
	if err := h.HandleClientOp(context.Background(), s, []byte(`{"op":"unsubscribe","topics":["pnl"]}`), nil); err != nil {
		t.Fatal(err)
	}
	_ = h.Publish("pnl", "x")
	if len(s.ch) != 0 {
		t.Fatal("delivered after unsubscribe")
	}
	if err := h.HandleClientOp(context.Background(), s, []byte(`not json`), nil); err == nil {
		t.Fatal("malformed op accepted")
	}
}

// Publish under concurrent attach/detach/subscribe must be race-free.
func TestConcurrentPublish(t *testing.T) {
	h := NewHub(16)
	h.RegisterTopic("scanner", snapConst("s"))
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					s := h.Attach()
					_ = h.Subscribe(s, "scanner")
					for len(s.Ch()) > 0 {
						<-s.Ch()
					}
					h.Detach(s)
				}
			}
		}()
	}
	for i := 0; i < 2000; i++ {
		_ = h.Publish("scanner", i)
	}
	close(stop)
	wg.Wait()
	if h.Clients() != 0 {
		t.Fatalf("clients = %d", h.Clients())
	}
}

// TestHandleClientOpAuthorization (audit S11): a subscribe the caller's
// authorization refuses yields one error frame per refused topic and
// subscribes nothing for it; permitted topics snapshot as before;
// unsubscribe stays unconditional.
func TestHandleClientOpAuthorization(t *testing.T) {
	h := NewHub(8)
	h.RegisterTopic("scanner", func() (json.RawMessage, error) { return json.RawMessage(`{"n":1}`), nil })
	h.RegisterTopic("health", func() (json.RawMessage, error) { return json.RawMessage(`{"n":2}`), nil })
	s := h.Attach()
	defer h.Detach(s)

	// VIEWER-shaped allow: dashboard topics yes, system topics and
	// unknown names no (fail closed).
	allow := func(t Topic) bool { return t == "scanner" }
	if err := h.HandleClientOp(context.Background(), s,
		[]byte(`{"op":"subscribe","topics":["scanner","health","not-registered"]}`), allow); err != nil {
		t.Fatal(err)
	}

	sawScanner := false
	healthRefused, unknownRefused := false, false
	deadline := time.After(2 * time.Second)
	for !(sawScanner && healthRefused && unknownRefused) {
		select {
		case <-deadline:
			t.Fatalf("frames incomplete: scanner=%v healthRefused=%v unknownRefused=%v", sawScanner, healthRefused, unknownRefused)
		case msg := <-s.Ch():
			switch {
			case msg.Topic == "scanner" && msg.Snapshot:
				sawScanner = true
			case msg.Topic == "health" && msg.Error == "forbidden":
				healthRefused = true
			case msg.Topic == "not-registered" && msg.Error == "forbidden":
				unknownRefused = true
			}
		default:
			time.Sleep(time.Millisecond)
		}
	}

	// The refused topic was never subscribed: a publish to it delivers
	// nothing to this sink.
	if err := h.Publish("health", map[string]int{"x": 1}); err != nil {
		t.Fatal(err)
	}
	select {
	case msg := <-s.Ch():
		if msg.Topic == "health" && msg.Error == "" {
			t.Fatal("refused topic still receives data")
		}
	case <-time.After(50 * time.Millisecond):
	}

	// Unsubscribe is never authorization-gated (dropping a stream is
	// not a disclosure).
	if err := h.HandleClientOp(context.Background(), s,
		[]byte(`{"op":"unsubscribe","topics":["scanner"]}`), func(Topic) bool { return false }); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	n := len(s.subs)
	s.mu.Unlock()
	if n != 0 {
		t.Fatalf("subs after unsubscribe = %d", n)
	}
}
