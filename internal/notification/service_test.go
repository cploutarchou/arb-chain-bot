package notification

import (
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"
)

type captureSink struct {
	name string
	mu   sync.Mutex
	got  []Delivery
}

func (c *captureSink) Name() string { return c.name }
func (c *captureSink) Deliver(d Delivery) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.got = append(c.got, d)
}
func (c *captureSink) deliveries() []Delivery {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Delivery(nil), c.got...)
}

func testService(routes map[string][]string) (*Service, *captureSink, *captureSink, *time.Time) {
	now := time.Unix(1_700_000_000, 0)
	svc := NewService(slog.New(slog.NewTextHandler(io.Discard, nil)),
		Config{Cooldown: time.Minute, Routes: routes}, func() time.Time { return now })
	web := &captureSink{name: "web"}
	tg := &captureSink{name: "telegram"}
	svc.Register(web)
	svc.Register(tg)
	return svc, web, tg, &now
}

func TestSeverityRouting(t *testing.T) {
	svc, web, tg, _ := testService(map[string][]string{
		"INFO":     {"web"},
		"WARNING":  {"web", "telegram"},
		"CRITICAL": {"web", "telegram"},
	})
	svc.Notify(Event{Severity: SeverityInfo, Key: "a", Title: "info"})
	svc.Notify(Event{Severity: SeverityWarning, Key: "b", Title: "warn"})
	if len(web.deliveries()) != 2 {
		t.Fatalf("web = %d deliveries", len(web.deliveries()))
	}
	if got := tg.deliveries(); len(got) != 1 || got[0].Title != "warn" {
		t.Fatalf("telegram = %+v", got)
	}
}

func TestCooldownDedupAndAggregation(t *testing.T) {
	svc, web, _, now := testService(nil)
	ev := Event{Severity: SeverityWarning, Key: "gap:BTCUSDT", Title: "gap"}
	svc.Notify(ev)
	// Four duplicates inside the window are suppressed.
	for i := 0; i < 4; i++ {
		*now = now.Add(10 * time.Second)
		svc.Notify(ev)
	}
	if got := web.deliveries(); len(got) != 1 {
		t.Fatalf("cooldown leaked: %d deliveries", len(got))
	}
	// After the window, one delivery carries the suppressed count.
	*now = now.Add(2 * time.Minute)
	svc.Notify(ev)
	got := web.deliveries()
	if len(got) != 2 || got[1].Suppressed != 4 {
		t.Fatalf("aggregate = %+v", got)
	}
}

func TestCriticalBypassesCooldown(t *testing.T) {
	svc, web, _, now := testService(nil)
	ev := Event{Severity: SeverityCritical, Key: "breaker", Title: "open"}
	svc.Notify(ev)
	*now = now.Add(time.Second)
	svc.Notify(ev)
	if len(web.deliveries()) != 2 {
		t.Fatalf("critical suppressed: %d deliveries", len(web.deliveries()))
	}
}

func TestUnroutedSeverityDefaultsToWeb(t *testing.T) {
	svc, web, tg, _ := testService(map[string][]string{})
	svc.Notify(Event{Severity: SeverityWarning, Key: "x", Title: "t"})
	if len(web.deliveries()) != 1 || len(tg.deliveries()) != 0 {
		t.Fatalf("default route: web=%d tg=%d", len(web.deliveries()), len(tg.deliveries()))
	}
}

func TestRecentRing(t *testing.T) {
	svc, _, _, now := testService(nil)
	for i := 0; i < 70; i++ {
		*now = now.Add(2 * time.Minute)
		svc.Notify(Event{Severity: SeverityInfo, Key: "k", Title: "t"})
	}
	recent := svc.Recent(0)
	if len(recent) != recentCap {
		t.Fatalf("recent = %d, want %d", len(recent), recentCap)
	}
	if got := svc.Recent(5); len(got) != 5 {
		t.Fatalf("limited recent = %d", len(got))
	}
}
