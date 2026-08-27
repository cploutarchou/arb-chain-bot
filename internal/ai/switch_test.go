package ai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestSwitchNilIsNotAFailure: with no advisor installed RunAnalysis
// returns ErrAdvisorDisabled early — request/failure counters and the
// notifier untouched (design §4.1: disabled is not a failure).
func TestSwitchNilIsNotAFailure(t *testing.T) {
	sw := &Switch{}
	svc, _, events := newTestService(t, sw)
	_, err := svc.RunAnalysis(context.Background(), testInput(KindHourlyHealth))
	if !errors.Is(err, ErrAdvisorDisabled) {
		t.Fatalf("err = %v, want ErrAdvisorDisabled", err)
	}
	if svc.Requests() != 0 || svc.Failures() != 0 {
		t.Fatalf("counters touched: requests=%d failures=%d", svc.Requests(), svc.Failures())
	}
	if len(*events) != 0 {
		t.Fatalf("notifier touched: %+v", *events)
	}
	if sw.Name() != "disabled" || sw.Model() != "" || sw.Enabled() {
		t.Fatalf("nil switch identity = %q/%q enabled=%v", sw.Name(), sw.Model(), sw.Enabled())
	}
}

// TestSwitchHotSwapObservedByNextRun: installing a provider at runtime
// is picked up by the very next RunAnalysis, and removing it again goes
// back to the disabled skip.
func TestSwitchHotSwapObservedByNextRun(t *testing.T) {
	sw := &Switch{}
	svc, _, events := newTestService(t, sw)
	ctx := context.Background()

	sw.Set(Fake{})
	res, err := svc.RunAnalysis(ctx, testInput(KindHourlyHealth))
	if err != nil {
		t.Fatalf("after Set(Fake): %v", err)
	}
	if res.Model != "fake-advisor" || svc.Requests() != 1 {
		t.Fatalf("res.Model=%q requests=%d", res.Model, svc.Requests())
	}
	sw.Set(nil)
	if _, err := svc.RunAnalysis(ctx, testInput(KindHourlyHealth)); !errors.Is(err, ErrAdvisorDisabled) {
		t.Fatalf("after Set(nil): %v", err)
	}
	if svc.Requests() != 1 || svc.Failures() != 0 {
		t.Fatalf("disabled run touched counters: requests=%d failures=%d", svc.Requests(), svc.Failures())
	}
	// Only the one "analysis complete" info event, no failure warnings.
	for _, ev := range *events {
		if ev.Key == "ai:failure" {
			t.Fatalf("unexpected failure event: %+v", ev)
		}
	}
}

// TestSchedulerCadenceChangeWithoutRestart drives the loop with a fake
// clock: the scheduler reads Cadence at every wake, so a cadence set
// while it is running is honoured on the next wake, and a zero cadence
// disarms that kind (settings-expansion §4.1/§7).
func TestSchedulerCadenceChangeWithoutRestart(t *testing.T) {
	svc, _, _ := newTestService(t, Fake{})
	base := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	var clock atomic.Pointer[time.Time]
	clock.Store(&base)
	var hourly atomic.Int64 // minutes; 0 = disarmed
	started := make(chan struct{})
	var startOnce sync.Once
	sched := &Scheduler{
		Service: svc, InputFor: testInput, Log: testLogger(),
		Cadence: func() Cadence {
			startOnce.Do(func() { close(started) })
			return Cadence{HourlyMinutes: int(hourly.Load())}
		},
		Wake: 2 * time.Millisecond,
		now:  func() time.Time { return *clock.Load() },
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { _ = sched.Run(ctx); close(done) }()
	// The loop stamps its last-fire baseline from the clock at entry;
	// only advance the clock once that has happened.
	<-started

	advance := func(d time.Duration) {
		next := clock.Load().Add(d)
		clock.Store(&next)
	}
	waitRequests := func(want int64, msg string) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for svc.Requests() != want {
			if time.Now().After(deadline) {
				t.Fatalf("%s: requests=%d want %d", msg, svc.Requests(), want)
			}
			time.Sleep(time.Millisecond)
		}
	}

	// Disarmed: an hour of fake time passes, nothing fires.
	advance(time.Hour)
	time.Sleep(20 * time.Millisecond)
	if svc.Requests() != 0 {
		t.Fatalf("zero cadence fired %d times", svc.Requests())
	}

	// Enable 15-minute cadence at runtime: the next wake sees it. The
	// last-fire baseline is the loop start, an hour ago in fake time, so
	// the first fire happens on the next wake.
	hourly.Store(15)
	waitRequests(1, "cadence enabled at runtime")
	// Less than an interval later: no second fire.
	advance(10 * time.Minute)
	time.Sleep(20 * time.Millisecond)
	if svc.Requests() != 1 {
		t.Fatalf("fired before the interval elapsed: %d", svc.Requests())
	}
	advance(6 * time.Minute)
	waitRequests(2, "second interval")

	// Disarm again at runtime: a day passes, nothing more fires.
	hourly.Store(0)
	advance(24 * time.Hour)
	time.Sleep(20 * time.Millisecond)
	if svc.Requests() != 2 {
		t.Fatalf("zero cadence did not disarm: %d", svc.Requests())
	}
	cancel()
	<-done
}

// TestSchedulerFallbackDurations keeps the pre-T-059 constructor shape
// working: with Cadence nil the Hourly/Daily/Weekly durations drive the
// loop exactly as the old tickers did.
func TestSchedulerFallbackDurations(t *testing.T) {
	svc, _, _ := newTestService(t, Fake{})
	sched := &Scheduler{
		Service: svc, InputFor: testInput, Log: testLogger(),
		Hourly: 5 * time.Millisecond, Daily: time.Hour, Weekly: time.Hour,
		Wake: 2 * time.Millisecond,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_ = sched.Run(ctx)
	if svc.Requests() < 2 {
		t.Fatalf("fallback cadence fired %d times, want >= 2", svc.Requests())
	}
}

// TestDailyBudgetSkipsAndResetsOnUTCRollover: the per-process counter
// refuses the (max+1)th analysis of a UTC day at INFO without touching
// failures, and a new UTC day resets it.
func TestDailyBudgetSkipsAndResetsOnUTCRollover(t *testing.T) {
	svc, _, events := newTestService(t, Fake{})
	day := time.Date(2026, 8, 27, 23, 0, 0, 0, time.UTC)
	var now atomic.Pointer[time.Time]
	now.Store(&day)
	svc.Now = func() time.Time { return *now.Load() }
	svc.Limits = func() Budget { return Budget{MaxAnalysesPerDay: 2} }
	ctx := context.Background()

	for i := 0; i < 2; i++ {
		if _, err := svc.RunAnalysis(ctx, testInput(KindHourlyHealth)); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}
	if _, err := svc.RunAnalysis(ctx, testInput(KindHourlyHealth)); !errors.Is(err, ErrBudgetExhausted) {
		t.Fatalf("third run = %v, want ErrBudgetExhausted", err)
	}
	if svc.Failures() != 0 || svc.Requests() != 2 {
		t.Fatalf("budget skip touched counters: requests=%d failures=%d", svc.Requests(), svc.Failures())
	}
	for _, ev := range *events {
		if ev.Key == "ai:failure" {
			t.Fatalf("budget skip raised a failure alert: %+v", ev)
		}
	}
	u := svc.Usage()
	if u.AnalysesToday != 2 || u.MaxPerDay != 2 || u.LastAnalysis == nil {
		t.Fatalf("usage = %+v", u)
	}

	next := day.Add(2 * time.Hour) // 01:00 next UTC day
	now.Store(&next)
	if _, err := svc.RunAnalysis(ctx, testInput(KindHourlyHealth)); err != nil {
		t.Fatalf("after rollover: %v", err)
	}
	if u := svc.Usage(); u.AnalysesToday != 1 {
		t.Fatalf("counter did not reset on rollover: %+v", u)
	}
}

// TestMaxOutputTokensReachesRequestBody: the platform budget lands in
// the Messages API request as max_tokens, and the 0 fallback stays
// 2048 (the pre-T-059 constant).
func TestMaxOutputTokensReachesRequestBody(t *testing.T) {
	var got atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			MaxTokens int64 `json:"max_tokens"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		got.Store(req.MaxTokens)
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"{\"summary\":\"ok\"}"}]}`))
	}))
	defer srv.Close()

	adv := NewAnthropic("test-key", "claude-sonnet-5")
	adv.BaseURL = srv.URL
	adv.MaxTokens = 4096
	if _, err := adv.Analyze(context.Background(), "p"); err != nil {
		t.Fatal(err)
	}
	if got.Load() != 4096 {
		t.Fatalf("max_tokens = %d, want 4096", got.Load())
	}
	adv.MaxTokens = 0
	if _, err := adv.Analyze(context.Background(), "p"); err != nil {
		t.Fatal(err)
	}
	if got.Load() != 2048 {
		t.Fatalf("fallback max_tokens = %d, want 2048", got.Load())
	}
}
