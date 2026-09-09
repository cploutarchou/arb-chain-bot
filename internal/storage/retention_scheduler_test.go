package storage

import (
	"context"
	"testing"
	"time"
)

// fakeRetentionRunner drives RetentionScheduler in tests without a
// database, the same way outbox_test.go's fakes drive Outbox.
type fakeRetentionRunner struct {
	counts   map[string]int64   // rule -> value CountPrunable returns
	batchSeq map[string][]int64 // rule -> successive PruneBatch return values (0 once exhausted)

	countCalls       int
	batchCalls       int
	batchCallsByRule map[string]int
}

func (f *fakeRetentionRunner) CountPrunable(_ context.Context, rule string, _ time.Time) (int64, error) {
	f.countCalls++
	return f.counts[rule], nil
}

func (f *fakeRetentionRunner) PruneBatch(_ context.Context, rule string, _ time.Time, _ int) (int64, error) {
	f.batchCalls++
	if f.batchCallsByRule == nil {
		f.batchCallsByRule = map[string]int{}
	}
	f.batchCallsByRule[rule]++
	seq := f.batchSeq[rule]
	if len(seq) == 0 {
		return 0, nil
	}
	n := seq[0]
	f.batchSeq[rule] = seq[1:]
	return n, nil
}

// TestRetentionSchedulerNextRun mirrors
// internal/screener/report.TestNextRun: a pure function, no clock or
// database involved.
func TestRetentionSchedulerNextRun(t *testing.T) {
	sch := &RetentionScheduler{RunAtHour: 3, RunAtMinute: 0}
	cases := []struct{ now, want string }{
		{"2026-08-27T23:59:00Z", "2026-08-28T03:00:00Z"},
		{"2026-08-28T03:00:00Z", "2026-08-29T03:00:00Z"},
		{"2026-08-28T02:59:59Z", "2026-08-28T03:00:00Z"},
		{"2026-08-28T12:00:00+03:00", "2026-08-29T03:00:00Z"}, // 09:00 UTC
	}
	for _, c := range cases {
		now, err := time.Parse(time.RFC3339, c.now)
		if err != nil {
			t.Fatal(err)
		}
		want, err := time.Parse(time.RFC3339, c.want)
		if err != nil {
			t.Fatal(err)
		}
		if got := sch.NextRun(now); !got.Equal(want) {
			t.Fatalf("NextRun(%s) = %s, want %s", c.now, got, want)
		}
	}
}

// TestRetentionSchedulerRunsAtSlotThenStops mirrors
// internal/screener/report.TestSchedulerRunsAtSlot: a deterministic,
// single-goroutine drive of Run via a fake clock/After, with no real
// sleeping and no database.
func TestRetentionSchedulerRunsAtSlotThenStops(t *testing.T) {
	f := &fakeRetentionRunner{}
	now := time.Date(2026, 8, 28, 2, 59, 0, 0, time.UTC)
	var waits []time.Duration
	ctx, cancel := context.WithCancel(context.Background())
	sch := &RetentionScheduler{
		Runner: f, Log: discardTestLogger(),
		RunAtHour: 3, RunAtMinute: 0,
		Windows: map[string]time.Duration{"system_events": time.Hour},
		Now:     func() time.Time { return now },
		After: func(d time.Duration) <-chan time.Time {
			waits = append(waits, d)
			ch := make(chan time.Time, 1)
			if len(waits) == 1 {
				ch <- now.Add(d) // first slot fires immediately
			} else {
				cancel() // second wait: stop the loop
			}
			return ch
		},
	}
	if err := sch.Run(ctx); err != nil {
		t.Fatalf("Run err = %v", err)
	}
	if len(waits) != 2 || waits[0] != time.Minute || waits[1] != time.Minute {
		// now is fixed at 02:59: every wait is 1 minute to 03:00.
		t.Fatalf("waits = %v", waits)
	}
	if sch.Sweeps() != 1 {
		t.Fatalf("sweeps = %d, want exactly 1 (one slot fired before shutdown)", sch.Sweeps())
	}
}

// TestRetentionSchedulerDryRunNeverDeletes: dry-run reports the count a
// live sweep would remove but never calls PruneBatch.
func TestRetentionSchedulerDryRunNeverDeletes(t *testing.T) {
	f := &fakeRetentionRunner{counts: map[string]int64{"exchange_health": 7}}
	sch := &RetentionScheduler{
		Runner: f, Log: discardTestLogger(), DryRun: true,
		Windows: map[string]time.Duration{"exchange_health": 24 * time.Hour},
	}
	sch.applyDefaults()
	sch.sweep(context.Background())

	counts, err := sch.LastSweep()
	if err != nil {
		t.Fatalf("LastSweep err = %v", err)
	}
	if counts["exchange_health"] != 7 {
		t.Fatalf("counts = %+v, want exchange_health=7", counts)
	}
	if f.batchCalls != 0 {
		t.Fatalf("dry run invoked PruneBatch %d times, want 0", f.batchCalls)
	}
	if f.countCalls != 1 {
		t.Fatalf("countCalls = %d, want 1", f.countCalls)
	}
}

// TestRetentionSchedulerBatchesUntilExhausted: a rule keeps batching
// until a batch returns fewer rows than requested, and the total is the
// sum of every batch.
func TestRetentionSchedulerBatchesUntilExhausted(t *testing.T) {
	f := &fakeRetentionRunner{batchSeq: map[string][]int64{"system_events": {2, 2, 1}}}
	sch := &RetentionScheduler{
		Runner: f, Log: discardTestLogger(),
		Windows:    map[string]time.Duration{"system_events": time.Hour},
		BatchSize:  2,
		BatchSleep: time.Millisecond,
	}
	sch.applyDefaults()
	sch.sweep(context.Background())

	counts, err := sch.LastSweep()
	if err != nil {
		t.Fatalf("LastSweep err = %v", err)
	}
	if counts["system_events"] != 5 {
		t.Fatalf("total = %d, want 5 (2+2+1)", counts["system_events"])
	}
	if got := f.batchCallsByRule["system_events"]; got != 3 {
		t.Fatalf("batch calls = %d, want 3", got)
	}
}

// TestRetentionSchedulerSkipsDisabledWindow: a rule mapped to <= 0 is
// never sent to the runner at all — this is what keeps an unconfigured
// or explicitly zeroed rule (e.g. recording_metadata's default) inert.
func TestRetentionSchedulerSkipsDisabledWindow(t *testing.T) {
	f := &fakeRetentionRunner{}
	sch := &RetentionScheduler{
		Runner: f, Log: discardTestLogger(),
		Windows: map[string]time.Duration{"recording_metadata": 0, "sessions": -time.Hour},
	}
	sch.applyDefaults()
	sch.sweep(context.Background())

	if f.countCalls != 0 || f.batchCalls != 0 {
		t.Fatalf("disabled rules reached the runner: countCalls=%d batchCalls=%d", f.countCalls, f.batchCalls)
	}
	counts, err := sch.LastSweep()
	if err != nil {
		t.Fatalf("LastSweep err = %v", err)
	}
	if len(counts) != 0 {
		t.Fatalf("counts = %+v, want empty", counts)
	}
}

// TestRetentionUnknownRuleErrors: *Store rejects a rule name outside
// retentionRules before ever touching the connection pool, so this needs
// no database.
func TestRetentionUnknownRuleErrors(t *testing.T) {
	var s Store
	ctx := context.Background()
	if _, err := s.CountPrunable(ctx, "not-a-real-rule", time.Now()); err == nil {
		t.Fatal("CountPrunable: want an error for an unknown rule")
	}
	if _, err := s.PruneBatch(ctx, "not-a-real-rule", time.Now(), 10); err == nil {
		t.Fatal("PruneBatch: want an error for an unknown rule")
	}
}
