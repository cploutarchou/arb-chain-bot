package ai

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/notification"
	"github.com/cploutarchou/arb-chain-bot/internal/strategy"
)

func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func testInput(kind AnalysisKind) Input {
	return Input{
		Kind: kind, At: time.Unix(1_700_000_000, 0), Mode: "PAPER",
		ConfigVersion: 1, Params: strategy.DefaultParams(),
		Scanner: ScannerSummary{Evaluations: 500, Qualified: 0, Rejected: 500},
		Feed:    FeedSummary{Frames: 10_000, SeqGaps: 2},
		Alerts:  AlertSummary{Active: 1},
	}
}

func newTestService(t *testing.T, adv Advisor) (*Service, *strategy.Service, *[]notification.Event) {
	t.Helper()
	stratSvc := strategy.NewService(strategy.NewMemoryStore(), testLogger(), nil)
	if _, err := stratSvc.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var events []notification.Event
	n := 0
	svc := &Service{
		Advisor:  adv,
		Strategy: stratSvc,
		Notify: func(ev notification.Event) {
			mu.Lock()
			events = append(events, ev)
			mu.Unlock()
		},
		Log:   testLogger(),
		IDGen: func() string { n++; return fmt.Sprintf("ai-%d", n) },
		Now:   func() time.Time { return time.Unix(1_700_000_100, 0) },
	}
	return svc, stratSvc, &events
}

// Acceptance: FakeAdvisor end-to-end — analysis → recommendation →
// approve → new config version; reject preserves history.
func TestFakeAdvisorEndToEnd(t *testing.T) {
	svc, stratSvc, events := newTestService(t, Fake{})
	ctx := context.Background()

	res, err := svc.RunAnalysis(ctx, testInput(KindHourlyHealth))
	if err != nil {
		t.Fatal(err)
	}
	if res.PromptVersion != PromptVersion || res.Model != "fake-advisor" {
		t.Fatalf("result meta = %+v", res)
	}
	if len(res.Recommendations) != 1 {
		t.Fatalf("recommendations = %+v", res.Recommendations)
	}
	rec := res.Recommendations[0]
	if rec.Parameter != "scanner.ttl_ms" || rec.RecommendedValue != "500" || rec.CurrentValue != "400" {
		t.Fatalf("rec = %+v", rec)
	}
	if rec.Status != "proposed" || rec.ExpiresAt.IsZero() {
		t.Fatalf("rec lifecycle = %+v", rec)
	}
	if len(*events) == 0 || (*events)[0].Key != "ai:analysis:hourly_health" {
		t.Fatalf("analysis notification missing: %+v", *events)
	}

	// Approve → the strategy service gains a version with the change.
	snap, err := svc.Approve(ctx, rec.ID, "u-admin", "web")
	if err != nil {
		t.Fatal(err)
	}
	if snap.Version != 2 || snap.Params.Scanner.TTLMs != 500 {
		t.Fatalf("approved snapshot = %+v", snap)
	}
	if got := stratSvc.Current().Params.Scanner.TTLMs; got != 500 {
		t.Fatalf("live config ttl = %d", got)
	}
	if got := svc.Recommendations("approved"); len(got) != 1 || got[0].DecidedBy != "u-admin" {
		t.Fatalf("approved list = %+v", got)
	}

	// Double decision refused.
	if _, err := svc.Approve(ctx, rec.ID, "u-admin", "web"); !errors.Is(err, ErrRecommendationDecided) {
		t.Fatalf("double approve err = %v", err)
	}

	// A second analysis proposes again (config changed, so the fake
	// recommends 600 now); reject and confirm history is retained.
	res2, err := svc.RunAnalysis(ctx, func() Input {
		in := testInput(KindDailyPerformance)
		in.Params = stratSvc.Current().Params
		in.ConfigVersion = 2
		return in
	}())
	if err != nil {
		t.Fatal(err)
	}
	rec2 := res2.Recommendations[0]
	if err := svc.Reject(ctx, rec2.ID, "u-operator"); err != nil {
		t.Fatal(err)
	}
	all := svc.Recommendations("")
	if len(all) != 2 {
		t.Fatalf("history pruned: %+v", all)
	}
	rejected := svc.Recommendations("rejected")
	if len(rejected) != 1 || rejected[0].DecidedBy != "u-operator" {
		t.Fatalf("rejected list = %+v", rejected)
	}
	// Config unchanged by rejection.
	if got := stratSvc.Current().Version; got != 2 {
		t.Fatalf("version after reject = %d", got)
	}
}

// scripted returns fixed raw output.
type scripted struct{ raw string }

func (s scripted) Name() string  { return "scripted" }
func (s scripted) Model() string { return "scripted" }
func (s scripted) Analyze(context.Context, string) (string, error) {
	return s.raw, nil
}

func TestInvalidOutputRejected(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"not json", "here are my thoughts..."},
		{"unknown field", `{"summary":"s","surprise":1}`},
		{"missing summary", `{"findings":[]}`},
		{"unknown parameter", `{"summary":"s","recommendations":[{"parameter":"risk.does_not_exist","recommended_value":"5","evidence":"e","reason":"r","confidence":"0.5","expected_effect":"x","risks":"y"}]}`},
		{"out of bounds value", `{"summary":"s","recommendations":[{"parameter":"scanner.ttl_ms","recommended_value":"999999","evidence":"e","reason":"r","confidence":"0.5","expected_effect":"x","risks":"y"}]}`},
		{"confidence > 1", `{"summary":"s","recommendations":[{"parameter":"scanner.ttl_ms","recommended_value":"500","evidence":"e","reason":"r","confidence":"1.5","expected_effect":"x","risks":"y"}]}`},
		{"trailing content", `{"summary":"s"} extra`},
	}
	for _, tc := range cases {
		svc, stratSvc, events := newTestService(t, scripted{raw: tc.raw})
		_, err := svc.RunAnalysis(context.Background(), testInput(KindHourlyHealth))
		if !errors.Is(err, ErrInvalidOutput) {
			t.Errorf("%s: err = %v, want ErrInvalidOutput", tc.name, err)
		}
		if svc.Failures() != 1 {
			t.Errorf("%s: failures = %d", tc.name, svc.Failures())
		}
		found := false
		for _, ev := range *events {
			if ev.Severity == notification.SeverityWarning && ev.Key == "ai:failure" {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: no WARNING alert", tc.name)
		}
		if stratSvc.Current().Version != 1 {
			t.Errorf("%s: config mutated by invalid output", tc.name)
		}
	}
}

func TestMarkdownFencedJSONAccepted(t *testing.T) {
	svc, _, _ := newTestService(t, scripted{raw: "```json\n{\"summary\":\"fine\"}\n```"})
	res, err := svc.RunAnalysis(context.Background(), testInput(KindHourlyHealth))
	if err != nil {
		t.Fatal(err)
	}
	if res.Summary != "fine" {
		t.Fatalf("summary = %q", res.Summary)
	}
}

// failing simulates a provider outage.
type failing struct{ block chan struct{} }

func (f failing) Name() string  { return "failing" }
func (f failing) Model() string { return "failing" }
func (f failing) Analyze(ctx context.Context, _ string) (string, error) {
	if f.block != nil {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-f.block:
		}
	}
	return "", errors.New("connection refused")
}

// Acceptance: provider outage does not affect the platform — the call
// errors (bounded by timeout), raises a WARNING, and concurrent
// strategy reads (the scanner's dependency) proceed unhindered.
func TestProviderOutageIsIsolated(t *testing.T) {
	svc, stratSvc, events := newTestService(t, failing{block: make(chan struct{})})
	svc.Timeout = 50 * time.Millisecond

	// Concurrent "scanner": continuously reads the hot-swap config while
	// the advisor hangs to its timeout.
	stop := make(chan struct{})
	reads := make(chan int64, 1)
	go func() {
		var n int64
		for {
			select {
			case <-stop:
				reads <- n
				return
			default:
				_ = stratSvc.Current()
				n++
			}
		}
	}()

	start := time.Now()
	_, err := svc.RunAnalysis(context.Background(), testInput(KindHourlyHealth))
	elapsed := time.Since(start)
	close(stop)

	if err == nil {
		t.Fatal("outage must surface as an error")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("outage blocked for %v; timeout not enforced", elapsed)
	}
	if n := <-reads; n == 0 {
		t.Fatal("config reads starved during provider outage")
	}
	if svc.Failures() != 1 {
		t.Fatalf("failures = %d", svc.Failures())
	}
	warned := false
	for _, ev := range *events {
		if ev.Key == "ai:failure" && ev.Severity == notification.SeverityWarning {
			warned = true
		}
	}
	if !warned {
		t.Fatal("no WARNING on outage")
	}
}

func TestSchedulerSurvivesFailures(t *testing.T) {
	svc, _, _ := newTestService(t, failing{})
	sched := &Scheduler{
		Service:  svc,
		InputFor: testInput,
		Log:      testLogger(),
		Hourly:   10 * time.Millisecond,
		Daily:    time.Hour, Weekly: time.Hour,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_ = sched.Run(ctx)
	if svc.Requests() < 2 {
		t.Fatalf("scheduler stopped after failure: requests = %d", svc.Requests())
	}
	if svc.Failures() != svc.Requests() {
		t.Fatalf("failures = %d, requests = %d", svc.Failures(), svc.Requests())
	}
}

func TestPromptCarriesNoSecrets(t *testing.T) {
	in := testInput(KindHourlyHealth)
	prompt, err := buildPrompt(in)
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{"api", "key", "token", "password"} {
		lower := strings.ToLower(prompt)
		// The words appear only if a field carried them; the typed input
		// has no secret-bearing fields at all.
		if strings.Contains(lower, needle+"=") || strings.Contains(lower, needle+"\":") {
			t.Fatalf("prompt contains suspicious field %q", needle)
		}
	}
}

func TestAnthropicProviderAgainstFakeEndpoint(t *testing.T) {
	// Exercised through the same Analyze path with a local server.
	srv := newFakeAnthropic(t, `{"summary":"remote ok"}`)
	adv := NewAnthropic("test-key", "claude-sonnet-5")
	adv.BaseURL = srv
	svc, _, _ := newTestService(t, adv)
	res, err := svc.RunAnalysis(context.Background(), testInput(KindHourlyHealth))
	if err != nil {
		t.Fatal(err)
	}
	if res.Summary != "remote ok" || res.Model != "claude-sonnet-5" {
		t.Fatalf("res = %+v", res)
	}
}
