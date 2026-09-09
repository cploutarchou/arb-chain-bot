package storage

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/execution"
	"github.com/cploutarchou/arb-chain-bot/internal/opportunity"
	"github.com/cploutarchou/arb-chain-bot/internal/risk"
)

var errStoreDown = errors.New("store down")

// fakeWriter stands in for the database: writes fail while down is set,
// block until the context ends while blocking is set, and are recorded
// otherwise. Ping answers down the same way a real pool would.
type fakeWriter struct {
	down     atomic.Bool
	blocking atomic.Bool

	mu    sync.Mutex
	kinds []string
}

func (f *fakeWriter) record(ctx context.Context, kind string) error {
	if f.blocking.Load() {
		<-ctx.Done()
		return ctx.Err()
	}
	if f.down.Load() {
		return errStoreDown
	}
	f.mu.Lock()
	f.kinds = append(f.kinds, kind)
	f.mu.Unlock()
	return nil
}

func (f *fakeWriter) InsertOpportunity(ctx context.Context, _ *opportunity.Opportunity, _ *risk.Decision) error {
	return f.record(ctx, "opportunity")
}

func (f *fakeWriter) InsertCycle(ctx context.Context, _ string, _ *execution.CycleResult) error {
	return f.record(ctx, "cycle")
}

func (f *fakeWriter) InsertRiskEvent(ctx context.Context, _ RiskEvent) error {
	return f.record(ctx, "risk_event")
}

func (f *fakeWriter) InsertLedgerSnapshot(ctx context.Context, _ *LedgerSnapshot) error {
	return f.record(ctx, "ledger_snapshot")
}

func (f *fakeWriter) Ping(context.Context) error {
	if f.down.Load() {
		return errStoreDown
	}
	return nil
}

func (f *fakeWriter) written() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.kinds)
}

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func cycleRecord(id string) Record {
	return Record{Kind: "cycle", SessionID: "s", Cycle: &execution.CycleResult{CycleID: id, Outcome: execution.OutcomeAllFilled}}
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// A refused write is counted, reported through OnPersistError, and puts
// the outbox into the failing state; the next successful write (or the
// probe, below) ends the episode exactly once through OnPersistRecovered.
func TestOutboxCountsWriteFailuresAndReportsRecovery(t *testing.T) {
	w := &fakeWriter{}
	w.down.Store(true)
	var errs, recoveries atomic.Int64
	o := &Outbox{
		Store: w, Log: quietLogger(), FlushInterval: 5 * time.Millisecond, ProbeInterval: time.Hour,
		OnPersistError:     func(error) { errs.Add(1) },
		OnPersistRecovered: func() { recoveries.Add(1) },
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = o.Run(ctx); close(done) }()

	if !o.Enqueue(cycleRecord("c1")) {
		t.Fatal("enqueue refused on an empty queue")
	}
	eventually(t, "write failure", func() bool { return o.WriteFailures() == 1 })
	if errs.Load() != 1 || !o.Failing() || o.Written() != 0 {
		t.Fatalf("after failure: errs=%d failing=%v written=%d", errs.Load(), o.Failing(), o.Written())
	}

	w.down.Store(false)
	o.Enqueue(cycleRecord("c2"))
	eventually(t, "successful write", func() bool { return o.Written() == 1 })
	if recoveries.Load() != 1 || o.Failing() {
		t.Fatalf("after recovery: recoveries=%d failing=%v", recoveries.Load(), o.Failing())
	}
	o.Enqueue(cycleRecord("c3"))
	eventually(t, "second write", func() bool { return o.Written() == 2 })
	if recoveries.Load() != 1 {
		t.Fatalf("recovery reported %d times, want once per episode", recoveries.Load())
	}

	cancel()
	<-done
	if o.WriteFailures()+o.Written()+o.Dropped() != 3 {
		t.Fatalf("records unaccounted for: failed=%d written=%d dropped=%d",
			o.WriteFailures(), o.Written(), o.Dropped())
	}
}

// With nothing left to write, the tick probes the store while failing so
// the persistence breaker closes as soon as the database is back rather
// than waiting for the next record to prove it.
func TestOutboxProbeEndsFailingEpisode(t *testing.T) {
	w := &fakeWriter{}
	w.down.Store(true)
	var recoveries atomic.Int64
	o := &Outbox{
		Store: w, Log: quietLogger(), FlushInterval: 2 * time.Millisecond, ProbeInterval: 5 * time.Millisecond,
		OnPersistRecovered: func() { recoveries.Add(1) },
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = o.Run(ctx) }()

	o.Enqueue(cycleRecord("c1"))
	eventually(t, "failing state", o.Failing)
	time.Sleep(20 * time.Millisecond) // probes while down must not report recovery
	if recoveries.Load() != 0 {
		t.Fatalf("recovery reported while the store was down")
	}
	w.down.Store(false)
	eventually(t, "probe recovery", func() bool { return recoveries.Load() == 1 && !o.Failing() })
}

// Cancellation flushes what is queued, then refuses (and counts) anything
// enqueued afterwards instead of parking it in a channel nobody drains.
func TestOutboxDrainsOnCancelThenRefusesLateRecords(t *testing.T) {
	w := &fakeWriter{}
	o := &Outbox{Store: w, Log: quietLogger()}
	for _, id := range []string{"c1", "c2", "c3"} {
		o.Enqueue(cycleRecord(id))
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := o.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v", err)
	}
	if w.written() != 3 || o.Written() != 3 {
		t.Fatalf("drained %d (counter %d), want 3", w.written(), o.Written())
	}
	if o.Enqueue(cycleRecord("late")) {
		t.Fatal("enqueue accepted after shutdown")
	}
	if o.Dropped() != 1 || w.written() != 3 {
		t.Fatalf("late record: dropped=%d written=%d", o.Dropped(), w.written())
	}
}

// Records still queued when the drain window expires are lost — and
// counted as such, so the total of written + refused + dropped always
// equals what was enqueued.
func TestOutboxCountsRecordsLostAtDrainDeadline(t *testing.T) {
	w := &fakeWriter{}
	w.blocking.Store(true)
	var errs atomic.Int64
	o := &Outbox{Store: w, Log: quietLogger(), DrainTimeout: 20 * time.Millisecond,
		OnPersistError: func(error) { errs.Add(1) }}
	for _, id := range []string{"c1", "c2", "c3"} {
		o.Enqueue(cycleRecord(id))
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	_ = o.Run(ctx)
	if time.Since(start) > 2*time.Second {
		t.Fatalf("drain took %s, want bounded by DrainTimeout", time.Since(start))
	}
	if o.Written() != 0 {
		t.Fatalf("written = %d with a blocking store", o.Written())
	}
	if got := o.WriteFailures() + o.Dropped(); got != 3 {
		t.Fatalf("accounted %d of 3 records (failed=%d dropped=%d)", got, o.WriteFailures(), o.Dropped())
	}
	if o.WriteFailures() < 1 || errs.Load() < 1 {
		t.Fatalf("the write that hit the deadline must count as a failure: failed=%d errs=%d", o.WriteFailures(), errs.Load())
	}
}

// Overflow is counted at the queue, as before.
func TestOutboxCountsOverflow(t *testing.T) {
	o := &Outbox{Store: &fakeWriter{}, Log: quietLogger(), QueueSize: 2}
	for i := 0; i < 5; i++ {
		o.Enqueue(cycleRecord("c"))
	}
	if o.Dropped() != 3 || o.Depth() != 2 {
		t.Fatalf("dropped=%d depth=%d", o.Dropped(), o.Depth())
	}
}
