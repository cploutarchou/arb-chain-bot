package notification

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"
)

func newTestCenter() *Center {
	n := 0
	return &Center{
		Log:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		IDGen: func() string { n++; return fmt.Sprintf("al-%d", n) },
		Now:   func() time.Time { return time.Unix(1_700_000_000, 0) },
	}
}

func deliver(c *Center, sev Severity, key string, suppressed int) {
	c.Deliver(Delivery{Event: Event{
		Severity: sev, Key: key, Title: "t:" + key, Body: "b",
		At: time.Unix(1_700_000_000, 0),
	}, Suppressed: suppressed})
}

func TestCenterLifecycle(t *testing.T) {
	c := newTestCenter()
	deliver(c, SeverityWarning, "gap:BTC", 0)
	deliver(c, SeverityWarning, "gap:BTC", 2) // folds into the same alert

	alerts := c.List("", 0)
	if len(alerts) != 1 || alerts[0].Count != 4 || alerts[0].State != AlertActive {
		t.Fatalf("folded alert = %+v", alerts)
	}
	id := alerts[0].ID

	// Ack → acked; double ack → ErrBadTransition.
	a, err := c.Ack(id, "u1")
	if err != nil || a.State != AlertAcked || a.AckedBy != "u1" {
		t.Fatalf("ack = %+v err=%v", a, err)
	}
	if _, err := c.Ack(id, "u1"); !errors.Is(err, ErrBadTransition) {
		t.Fatalf("double ack err = %v", err)
	}

	// Acked alerts still fold deliveries (the incident continues).
	deliver(c, SeverityWarning, "gap:BTC", 0)
	if got := c.List("", 0)[0]; got.Count != 5 || got.State != AlertAcked {
		t.Fatalf("acked fold = %+v", got)
	}

	// Resolve; the next delivery opens a NEW alert.
	if _, err := c.Resolve(id, "u1"); err != nil {
		t.Fatal(err)
	}
	deliver(c, SeverityWarning, "gap:BTC", 0)
	all := c.List("", 0)
	if len(all) != 2 {
		t.Fatalf("post-resolve alerts = %+v", all)
	}
	if c.ActiveCount() != 1 {
		t.Fatalf("active = %d", c.ActiveCount())
	}
	if _, err := c.Ack("missing", "u1"); !errors.Is(err, ErrAlertNotFound) {
		t.Fatalf("missing err = %v", err)
	}
}

func TestCenterSeverityEscalation(t *testing.T) {
	c := newTestCenter()
	deliver(c, SeverityInfo, "x", 0)
	deliver(c, SeverityCritical, "x", 0)
	a := c.List("", 0)[0]
	if a.SevName != "CRITICAL" {
		t.Fatalf("escalation = %+v", a)
	}
	deliver(c, SeverityInfo, "x", 0)
	if got := c.List("", 0)[0]; got.SevName != "CRITICAL" {
		t.Fatalf("severity lowered: %+v", got)
	}
}

func TestCenterEvictionNeverDropsActiveCritical(t *testing.T) {
	c := newTestCenter()
	// Fill beyond cap with critical actives plus some resolvable noise.
	for i := 0; i < centerCap+50; i++ {
		deliver(c, SeverityCritical, fmt.Sprintf("crit:%d", i), 0)
	}
	for i := 0; i < 100; i++ {
		deliver(c, SeverityInfo, fmt.Sprintf("info:%d", i), 0)
	}
	// Every critical alert must survive.
	crit := 0
	for _, a := range c.List("", 0) {
		if a.SevName == "CRITICAL" {
			crit++
		}
	}
	if crit != centerCap+50 {
		t.Fatalf("critical survivors = %d, want %d", crit, centerCap+50)
	}
}

type memCenterStore struct {
	mu      sync.Mutex
	upserts []Alert
	states  []string
}

func (m *memCenterStore) UpsertAlert(_ context.Context, a Alert) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.upserts = append(m.upserts, a)
	return nil
}

func (m *memCenterStore) UpdateAlertState(_ context.Context, id string, st AlertState, actor string, _ time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.states = append(m.states, id+":"+string(st)+":"+actor)
	return nil
}

func TestCenterPersistsAsync(t *testing.T) {
	c := newTestCenter()
	store := &memCenterStore{}
	c.Store = store
	deliver(c, SeverityWarning, "k", 0)
	id := c.List("", 0)[0].ID
	if _, err := c.Ack(id, "u1"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		store.mu.Lock()
		done := len(store.upserts) == 1 && len(store.states) == 1
		store.mu.Unlock()
		if done {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("persistence not observed: %+v %+v", store.upserts, store.states)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestRegisterAlwaysReceivesEverySeverity(t *testing.T) {
	svc, _, _, _ := testService(map[string][]string{"INFO": {"telegram"}})
	c := newTestCenter()
	svc.RegisterAlways(c)
	// INFO routes only to telegram, yet the center records it.
	svc.Notify(Event{Severity: SeverityInfo, Key: "k", Title: "t"})
	if got := c.List("", 0); len(got) != 1 {
		t.Fatalf("center missed unrouted-severity delivery: %+v", got)
	}
}

// Acceptance (audit CR-P1-4): the first deliveries race in from several
// router goroutines; lazy init must not double-create the state maps
// (run with -race).
func TestCenterConcurrentFirstDeliveriesRaceFree(t *testing.T) {
	var seq int64
	var idMu sync.Mutex
	c := &Center{
		Log:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		IDGen: func() string { idMu.Lock(); defer idMu.Unlock(); seq++; return fmt.Sprintf("al-%d", seq) },
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			deliver(c, SeverityWarning, fmt.Sprintf("race:%d", i), 0)
		}(i)
	}
	wg.Wait()
	if got := len(c.List("", 100)); got != 8 {
		t.Fatalf("alerts after concurrent first deliveries = %d, want 8", got)
	}
}
