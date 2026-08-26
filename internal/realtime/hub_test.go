package realtime

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
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
	if err := h.HandleClientOp(context.Background(), s, []byte(`{"op":"subscribe","topics":["pnl"]}`)); err != nil {
		t.Fatal(err)
	}
	recv(t, s)
	if err := h.HandleClientOp(context.Background(), s, []byte(`{"op":"unsubscribe","topics":["pnl"]}`)); err != nil {
		t.Fatal(err)
	}
	_ = h.Publish("pnl", "x")
	if len(s.ch) != 0 {
		t.Fatal("delivered after unsubscribe")
	}
	if err := h.HandleClientOp(context.Background(), s, []byte(`not json`)); err == nil {
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
