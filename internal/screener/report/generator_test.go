package report

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/notification"
	"github.com/cploutarchou/arb-chain-bot/internal/screener"
	"github.com/cploutarchou/arb-chain-bot/internal/screener/alerts"
	"github.com/cploutarchou/arb-chain-bot/internal/screener/paperexec"
)

type fakeNotifier struct {
	mu     sync.Mutex
	events []notification.Event
}

func (f *fakeNotifier) Notify(ev notification.Event) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, ev)
}

func newGen(t *testing.T) (*Generator, *paperexec.MemoryLedger, *MemoryStore, *fakeNotifier, string) {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := screener.NewService(screener.NewBook(), screener.NewMemoryStore(), log, nil)
	if _, err := svc.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	svc.Rules = screener.NewMemoryRuleStore()
	svc.Events = screener.NewMemoryEventStore()
	ledger := paperexec.NewMemoryLedger()
	store := NewMemoryStore()
	fn := &fakeNotifier{}
	dir := t.TempDir()
	n := 0
	g := &Generator{Svc: svc, Ledger: ledger, Store: store, Notify: fn.Notify, Dir: dir, Log: log,
		IDGen: func() string { n++; return itoa(n) }, Now: func() time.Time { return t0.Add(5 * 24 * time.Hour) }, Seed: 1}
	return g, ledger, store, fn, dir
}

func TestGeneratorRunFilesStoresAndNotifies(t *testing.T) {
	g, ledger, store, fn, dir := newGen(t)
	in := synthetic()
	for _, r := range in.Rules {
		if _, err := g.Svc.Rules.InsertRule(context.Background(), r, "t"); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range in.Positions {
		_ = ledger.InsertPosition(context.Background(), p)
	}
	for _, e := range in.Executions {
		_ = ledger.InsertExecution(context.Background(), e)
	}
	for _, e := range in.Events {
		_ = g.Svc.Events.InsertEvent(context.Background(), e)
	}
	// "now" = 2026-08-30 00:05 UTC → previous day 2026-08-29 (Saturday: e5, e6).
	now := time.Date(2026, 8, 30, 0, 5, 0, 0, time.UTC)
	res, err := g.Run(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if res.Day != "2026-08-29" || len(res.Errors) != 0 {
		t.Fatalf("run = %+v", res)
	}
	// Scopes: (spot, "") and (spot, r1) × (day, cumulative) = 4 reports.
	if len(res.Reports) != 4 {
		t.Fatalf("reports = %d: %+v", len(res.Reports), res.Reports)
	}
	list, _ := store.ListReports(context.Background(), 0)
	if len(list) != 4 {
		t.Fatalf("stored = %d", len(list))
	}
	var dayAll, cumAll Summary
	for _, s := range list {
		if s.RuleID == "" && s.PeriodLabel == PeriodDay {
			dayAll = s
		}
		if s.RuleID == "" && s.PeriodLabel == PeriodCumulative {
			cumAll = s
		}
	}
	if dayAll.N != 2 || !dayAll.NetPnLQuote.Equal(d("3")) {
		t.Fatalf("day summary = %+v", dayAll)
	}
	if cumAll.N != 6 || !cumAll.NetPnLQuote.Equal(d("17")) || cumAll.GatePassed != 0 || cumAll.GateTotal != 8 {
		t.Fatalf("cumulative summary = %+v", cumAll)
	}
	rep, err := store.GetReport(context.Background(), cumAll.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Payload.DataAgeMs == nil || *rep.Payload.DataAgeMs != int64((now.Sub(t0.Add(98*time.Hour)))/time.Millisecond) {
		t.Fatalf("data age = %v", rep.Payload.DataAgeMs)
	}
	if !strings.Contains(rep.Markdown, "| n | 6 |") || !strings.Contains(rep.Markdown, alerts.Footer) || !strings.Contains(rep.Markdown, "0 / 8 pass") {
		t.Fatalf("markdown:\n%s", rep.Markdown)
	}
	for _, bad := range []string{"guaranteed profit", "risk-free", "track record"} {
		if strings.Contains(strings.ToLower(rep.Markdown), bad) {
			t.Fatalf("markdown contains %q", bad)
		}
	}
	// Files under <dir>/screener-reports/<date>/.
	outDir := filepath.Join(dir, "screener-reports", "2026-08-29")
	entries, err := os.ReadDir(outDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 8 {
		t.Fatalf("files = %d, want 8 (4 md + 4 json)", len(entries))
	}
	raw, err := os.ReadFile(filepath.Join(outDir, "cross_venue_spot__cumulative.json"))
	if err != nil {
		t.Fatal(err)
	}
	var p Payload
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	if p.Stats.N != 6 || len(p.Gate) != 8 || p.Model != Model {
		t.Fatalf("json payload = %+v", p)
	}
	if _, err := os.Stat(filepath.Join(outDir, "cross_venue_spot__r1__day.md")); err != nil {
		t.Fatal(err)
	}
	// One Telegram summary, measurement wording, fixed footer.
	fn.mu.Lock()
	defer fn.mu.Unlock()
	if len(fn.events) != 1 {
		t.Fatalf("notifications = %d, want 1", len(fn.events))
	}
	ev := fn.events[0]
	if ev.Key != "screener:report:2026-08-29" || ev.Severity != notification.SeverityInfo {
		t.Fatalf("event = %+v", ev)
	}
	if !strings.HasSuffix(ev.Body, alerts.Footer) || !strings.Contains(ev.Body, "cross_venue_spot cumulative: n=6, net 17.0000 quote, gate 0/8 pass") {
		t.Fatalf("body = %q", ev.Body)
	}
	if g.LastRun() == nil || g.LastRun().Day != "2026-08-29" {
		t.Fatal("last run not recorded")
	}
}

func TestGeneratorRunEmptyLedgerIsHonest(t *testing.T) {
	g, _, store, fn, _ := newGen(t)
	res, err := g.Run(context.Background(), time.Date(2026, 8, 30, 0, 5, 0, 0, time.UTC))
	if err != nil || len(res.Reports) != 0 {
		t.Fatalf("res = %+v err = %v", res, err)
	}
	list, _ := store.ListReports(context.Background(), 0)
	if len(list) != 0 {
		t.Fatal("reports stored for an empty ledger")
	}
	fn.mu.Lock()
	defer fn.mu.Unlock()
	if len(fn.events) != 1 || !strings.Contains(fn.events[0].Body, "nothing measured") {
		t.Fatalf("events = %+v", fn.events)
	}
}

func TestNextRun(t *testing.T) {
	cases := []struct{ now, want string }{
		{"2026-08-27T23:59:00Z", "2026-08-28T00:05:00Z"},
		{"2026-08-28T00:05:00Z", "2026-08-29T00:05:00Z"},
		{"2026-08-28T00:04:59Z", "2026-08-28T00:05:00Z"},
		{"2026-08-28T12:00:00+03:00", "2026-08-29T00:05:00Z"},
	}
	for _, c := range cases {
		now, _ := time.Parse(time.RFC3339, c.now)
		want, _ := time.Parse(time.RFC3339, c.want)
		if got := NextRun(now); !got.Equal(want) {
			t.Fatalf("NextRun(%s) = %s, want %s", c.now, got, want)
		}
	}
}

func TestSchedulerRunsAtSlot(t *testing.T) {
	g, _, _, fn, _ := newGen(t)
	now := time.Date(2026, 8, 28, 0, 4, 0, 0, time.UTC)
	var waits []time.Duration
	ctx, cancel := context.WithCancel(context.Background())
	s := &Scheduler{Gen: g, Now: func() time.Time { return now }, After: func(d time.Duration) <-chan time.Time {
		waits = append(waits, d)
		ch := make(chan time.Time, 1)
		if len(waits) == 1 {
			ch <- now.Add(d) // the first slot fires at once
		} else {
			cancel() // second wait: stop the loop
		}
		return ch
	}}
	_ = s.Run(ctx)
	if len(waits) != 2 || waits[0] != time.Minute || waits[1] != time.Minute {
		// now is fixed at 00:04: every wait is 1 minute to 00:05.
		t.Fatalf("waits = %v", waits)
	}
	fn.mu.Lock()
	defer fn.mu.Unlock()
	if len(fn.events) < 1 {
		t.Fatal("scheduler did not run the generator at the slot")
	}
}
