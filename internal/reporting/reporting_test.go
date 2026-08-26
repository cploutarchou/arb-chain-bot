package reporting

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/notification"
)

type fakeHistory struct{ inserted []Report }

func (f *fakeHistory) OpportunityAggregates(context.Context, time.Time, time.Time) (int, string, string, error) {
	return 12, "42.5", "17.2", nil
}

func (f *fakeHistory) CycleAggregates(context.Context, time.Time, time.Time) (int, int, int, string, string, int, error) {
	return 10, 7, 3, "-1.2", "-8.5", 7, nil
}

func (f *fakeHistory) FailedCycles(context.Context, time.Time, time.Time, int) ([]FailedCycle, error) {
	return []FailedCycle{{ID: "cyc-9", Outcome: "TIMEOUT"}}, nil
}

func (f *fakeHistory) TriangleLeaders(context.Context, time.Time, time.Time, int) ([]TriangleStat, []TriangleStat, error) {
	return []TriangleStat{{TriangleID: "tri-a", Cycles: 6, NetPnL: "12.5"}},
		[]TriangleStat{{TriangleID: "tri-b", Cycles: 4, NetPnL: "-3.1"}}, nil
}

func (f *fakeHistory) InsertReport(_ context.Context, r Report) error {
	f.inserted = append(f.inserted, r)
	return nil
}

func testGenerator(h HistorySource, events *[]notification.Event) *Generator {
	n := 0
	return &Generator{
		Sources: Sources{
			System: func() SystemSection {
				return SystemSection{Mode: "PAPER", Ready: true, ConfigVersion: 3, ActiveAlerts: 1}
			},
			Exchange: func() ExchangeSection {
				return ExchangeSection{Exchange: "binance", Frames: 100_000, Reconnects: 7, SeqGaps: 12, BooksHealthy: 6, BooksTotal: 6}
			},
			Scanner: func() ScannerSection {
				return ScannerSection{Evaluations: 5000, Qualified: 12, Rejected: 4988}
			},
			PnL: func() []AssetSection {
				return []AssetSection{{Asset: "USDT", Realized: "21.4", Fees: "3.1", Drawdown: "0.0100"}}
			},
			Capital: func() []CapitalSection {
				return []CapitalSection{{Asset: "USDT", Available: "9000", Reserved: "1000", Utilization: "10.00%"}}
			},
			Risk: func() RiskSection {
				return RiskSection{BreakersOpen: 1, RejectReasons: map[string]int64{"RISK_MIN_EDGE": 4000}}
			},
			AI: func() AISection {
				return AISection{Available: true, Summary: "healthy hour", Proposed: 2}
			},
			Incidents: func(time.Time) []Incident {
				return []Incident{{Severity: "CRITICAL", Title: "Breaker OPEN", Count: 3, LastAt: time.Unix(1_700_000_000, 0)}}
			},
			History: h,
		},
		Notify: func(ev notification.Event) { *events = append(*events, ev) },
		Log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		IDGen:  func() string { n++; return "rep-1" },
		Now:    func() time.Time { return time.Unix(1_700_000_000, 0) },
	}
}

func TestGenerateFullReport(t *testing.T) {
	h := &fakeHistory{}
	var events []notification.Event
	g := testGenerator(h, &events)

	r, err := g.Generate(context.Background(), KindDaily)
	if err != nil {
		t.Fatal(err)
	}
	if r.Kind != KindDaily || r.PeriodEnd.Sub(r.PeriodStart) != 24*time.Hour {
		t.Fatalf("period = %+v", r)
	}
	if r.Scanner.QualRate != "0.24%" {
		t.Fatalf("qual rate = %q", r.Scanner.QualRate)
	}
	if r.Opportunities.QualifiedPersisted != 12 || r.Opportunities.BestBps != "42.5" {
		t.Fatalf("opps = %+v", r.Opportunities)
	}
	if r.PaperCycles.SuccessRate != "70.00%" {
		t.Fatalf("cycles = %+v", r.PaperCycles)
	}
	if r.Slippage.WorstBps != "-8.5" || r.Slippage.Samples != 7 {
		t.Fatalf("slippage = %+v", r.Slippage)
	}
	if len(r.FailedCycles) != 1 || r.FailedCycles[0].Outcome != "TIMEOUT" {
		t.Fatalf("failed = %+v", r.FailedCycles)
	}
	if len(r.TopTriangles) != 1 || r.TopTriangles[0].TriangleID != "tri-a" {
		t.Fatalf("top = %+v", r.TopTriangles)
	}
	if r.RiskEvents.BreakersOpen != 1 || len(r.Incidents) != 1 {
		t.Fatalf("risk/incidents = %+v / %+v", r.RiskEvents, r.Incidents)
	}
	if !r.AIFindings.Available || r.AIFindings.Proposed != 2 {
		t.Fatalf("ai = %+v", r.AIFindings)
	}
	// Rule-based actions fire on reconnects, gaps, and the open breaker.
	joined := strings.Join(r.Actions, " | ")
	for _, want := range []string{"reconnect", "sequence gaps", "circuit breakers"} {
		if !strings.Contains(joined, want) {
			t.Errorf("actions missing %q: %s", want, joined)
		}
	}
	if r.Executive == "" || !strings.Contains(r.Executive, "5000 evaluations") {
		t.Fatalf("executive = %q", r.Executive)
	}

	// Persisted + digest notification emitted.
	if len(h.inserted) != 1 || h.inserted[0].ID != "rep-1" {
		t.Fatalf("persisted = %+v", h.inserted)
	}
	if len(events) != 1 || events[0].Key != "report:daily" {
		t.Fatalf("events = %+v", events)
	}
	if !strings.Contains(events[0].Body, "tri-a") {
		t.Fatalf("digest lacks top triangle: %s", events[0].Body)
	}
}

func TestGenerateWithoutHistoryIsHonest(t *testing.T) {
	var events []notification.Event
	g := testGenerator(nil, &events)
	g.Sources.History = nil
	r, err := g.Generate(context.Background(), KindWeekly)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Opportunities.FromMemory {
		t.Fatal("memory-only flag missing")
	}
	found := false
	for _, n := range r.Notes {
		if strings.Contains(n, "persistence disabled") {
			found = true
		}
	}
	if !found {
		t.Fatalf("honest note missing: %+v", r.Notes)
	}
	if r.PeriodEnd.Sub(r.PeriodStart) != 7*24*time.Hour {
		t.Fatalf("weekly period = %v", r.PeriodEnd.Sub(r.PeriodStart))
	}
}

func TestSchedulerEmitsReports(t *testing.T) {
	h := &fakeHistory{}
	var events []notification.Event
	g := testGenerator(h, &events)
	s := &Scheduler{Generator: g, Log: g.Log, Daily: 15 * time.Millisecond, Weekly: time.Hour}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_ = s.Run(ctx)
	if len(h.inserted) < 2 {
		t.Fatalf("scheduler produced %d reports", len(h.inserted))
	}
}

// Acceptance (audit P1-3): scanner counters are cumulative at the
// source, so the second report of a kind must show the PERIOD delta,
// while a different kind keeps its own independent baseline.
func TestScannerSectionIsPerPeriodDelta(t *testing.T) {
	h := &fakeHistory{}
	var events []notification.Event
	g := testGenerator(h, &events)
	cum := ScannerSection{Evaluations: 5000, Qualified: 12, Rejected: 4988}
	g.Sources.Scanner = func() ScannerSection { return cum }

	first, err := g.Generate(context.Background(), KindDaily)
	if err != nil {
		t.Fatal(err)
	}
	if first.Scanner.Evaluations != 5000 || first.Scanner.Qualified != 12 {
		t.Fatalf("first report should be cumulative: %+v", first.Scanner)
	}
	noted := false
	for _, n := range first.Notes {
		if strings.Contains(n, "since process start") {
			noted = true
		}
	}
	if !noted {
		t.Fatalf("first report must disclose missing baseline: %+v", first.Notes)
	}

	cum = ScannerSection{Evaluations: 8000, Qualified: 20, Rejected: 7980}
	second, err := g.Generate(context.Background(), KindDaily)
	if err != nil {
		t.Fatal(err)
	}
	if second.Scanner.Evaluations != 3000 || second.Scanner.Qualified != 8 || second.Scanner.Rejected != 2992 {
		t.Fatalf("second report should be the period delta: %+v", second.Scanner)
	}
	if second.Scanner.QualRate != "0.27%" { // 8/3000
		t.Fatalf("delta qual rate = %q", second.Scanner.QualRate)
	}

	// A different kind has no baseline yet — cumulative again.
	weekly, err := g.Generate(context.Background(), KindWeekly)
	if err != nil {
		t.Fatal(err)
	}
	if weekly.Scanner.Evaluations != 8000 {
		t.Fatalf("weekly baseline must be independent: %+v", weekly.Scanner)
	}
}
