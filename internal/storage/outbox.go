package storage

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/execution"
	"github.com/cploutarchou/arb-chain-bot/internal/opportunity"
	"github.com/cploutarchou/arb-chain-bot/internal/risk"
)

// RecordWriter is the persistence surface the outbox drains into. *Store
// satisfies it; tests substitute a fake to drive the failure, recovery
// and shutdown paths without a database.
type RecordWriter interface {
	InsertOpportunity(ctx context.Context, op *opportunity.Opportunity, dec *risk.Decision) error
	InsertCycle(ctx context.Context, sessionID string, res *execution.CycleResult) error
	InsertRiskEvent(ctx context.Context, e RiskEvent) error
	Ping(ctx context.Context) error
}

// Outbox decouples the hot path from PostgreSQL: producers enqueue
// non-blocking; a single writer drains records one by one. Every record
// that does not reach the database is counted — refused at the queue
// (Dropped: full, or closed after Run returned), refused by the database
// (WriteFailures), or still queued when the shutdown drain window
// expires (Dropped) — and a failing database is reported through
// OnPersistError / OnPersistRecovered so the engine can pause
// qualification instead of generating evidence it cannot keep
// (docs/architecture.md §12).
type Outbox struct {
	Store     RecordWriter
	Log       *slog.Logger
	SessionID string

	QueueSize     int
	FlushInterval time.Duration
	// DrainTimeout bounds the final flush after Run's context is
	// cancelled; records still queued when it expires are counted as
	// dropped. Defaults to 5s.
	DrainTimeout time.Duration
	// ProbeInterval is how often a failing writer pings the store to
	// detect recovery when no new record arrives to prove it. Defaults
	// to 5s.
	ProbeInterval time.Duration

	// OnPersistError is called on every failed write (idempotent
	// consumers: the persistence breaker trips once and stays open).
	// OnPersistRecovered is called once per failing episode, when a
	// write or a probe succeeds again.
	OnPersistError     func(error)
	OnPersistRecovered func()

	once sync.Once
	ch   chan Record

	// closeMu orders Enqueue against shutdown: Enqueue holds the read
	// lock across its send, shutdown takes the write lock to flip closed,
	// so any Enqueue that observed closed == false has completed its send
	// before the final drain starts and cannot slip in behind it.
	closeMu sync.RWMutex
	closed  bool

	dropped     atomic.Int64
	written     atomic.Int64
	writeFailed atomic.Int64
	failing     atomic.Bool
}

// init runs the lazy defaults exactly once: Enqueue (hot-path producers)
// and Run (writer goroutine) start concurrently, and the unguarded
// version could create two channels — records enqueued into the orphan
// were silently lost (audit CR-P1-4).
func (o *Outbox) init() {
	o.once.Do(func() {
		if o.QueueSize <= 0 {
			o.QueueSize = 4096
		}
		if o.FlushInterval <= 0 {
			o.FlushInterval = 250 * time.Millisecond
		}
		if o.DrainTimeout <= 0 {
			o.DrainTimeout = 5 * time.Second
		}
		if o.ProbeInterval <= 0 {
			o.ProbeInterval = 5 * time.Second
		}
		if o.Log == nil {
			o.Log = slog.Default()
		}
		o.ch = make(chan Record, o.QueueSize)
	})
}

// Enqueue never blocks; false means the record was dropped (counted):
// the queue is full, or Run has already begun its final drain.
func (o *Outbox) Enqueue(rec Record) bool {
	o.init()
	o.closeMu.RLock()
	defer o.closeMu.RUnlock()
	if o.closed {
		o.dropped.Add(1)
		return false
	}
	select {
	case o.ch <- rec:
		return true
	default:
		o.dropped.Add(1)
		return false
	}
}

// Dropped counts records that never reached the writer: refused by a
// full queue, enqueued after shutdown began, or still queued when the
// drain window expired. Written counts records the database accepted.
// WriteFailures counts records the database refused. Every enqueued
// record ends in exactly one of Written / WriteFailures / Dropped.
func (o *Outbox) Dropped() int64       { return o.dropped.Load() }
func (o *Outbox) Written() int64       { return o.written.Load() }
func (o *Outbox) WriteFailures() int64 { return o.writeFailed.Load() }

// Failing reports whether the last write failed and no write or probe
// has succeeded since (the persistence breaker's condition).
func (o *Outbox) Failing() bool { return o.failing.Load() }

// Depth and Capacity expose the current queue backlog (BL-18: system
// health's queue-depth panel). init() is called so a Depth() probe
// before Run/Enqueue never races the lazy channel construction.
func (o *Outbox) Depth() int {
	o.init()
	return len(o.ch)
}

func (o *Outbox) Capacity() int {
	o.init()
	return cap(o.ch)
}

func (o *Outbox) Name() string { return "outbox" }

// Run drains until ctx cancels, then refuses further records and
// flushes what is already queued within DrainTimeout (graceful
// shutdown, docs/architecture.md §5). While the store is failing the
// tick doubles as a recovery probe.
func (o *Outbox) Run(ctx context.Context) error {
	o.init()
	ticker := time.NewTicker(o.FlushInterval)
	defer ticker.Stop()
	var lastProbe time.Time
	for {
		select {
		case <-ctx.Done():
			o.shutdown()
			return ctx.Err()
		case rec := <-o.ch:
			o.write(ctx, rec)
		case now := <-ticker.C:
			if o.failing.Load() && now.Sub(lastProbe) >= o.ProbeInterval {
				lastProbe = now
				o.probe(ctx)
			}
		}
	}
}

// shutdown flips closed under the write lock (see closeMu), then drains
// the queue under DrainTimeout. Records still queued when the window
// expires are counted as dropped and reported once, with the count —
// never silently abandoned in a channel nobody reads again.
func (o *Outbox) shutdown() {
	o.closeMu.Lock()
	o.closed = true
	o.closeMu.Unlock()

	flushCtx, cancel := context.WithTimeout(context.Background(), o.DrainTimeout)
	defer cancel()
	var lost int64
	for {
		select {
		case rec := <-o.ch:
			if flushCtx.Err() != nil {
				lost++
				continue
			}
			o.write(flushCtx, rec)
		default:
			if lost > 0 {
				o.dropped.Add(lost)
				o.Log.Error("outbox: shutdown drain window expired; queued records lost",
					"lost", lost, "window", o.DrainTimeout.String())
			}
			return
		}
	}
}

func (o *Outbox) write(ctx context.Context, rec Record) {
	var err error
	switch rec.Kind {
	case "opportunity":
		err = o.Store.InsertOpportunity(ctx, rec.Opportunity, rec.Decision)
	case "cycle":
		// The result itself carries OpportunityID (set by the simulator
		// from the plan), so no caller has to re-attach the linkage.
		err = o.Store.InsertCycle(ctx, rec.SessionID, rec.Cycle)
	case "risk_event":
		if rec.RiskEvent != nil {
			err = o.Store.InsertRiskEvent(ctx, *rec.RiskEvent)
		}
	default:
		o.dropped.Add(1)
		o.Log.Warn("outbox: unknown record kind dropped", "kind", rec.Kind)
		return
	}
	if err != nil {
		o.writeFailed.Add(1)
		o.failing.Store(true)
		o.Log.Error("outbox write failed", "kind", rec.Kind, "error", err,
			"write_failures", o.writeFailed.Load())
		if o.OnPersistError != nil {
			o.OnPersistError(err)
		}
		return
	}
	o.written.Add(1)
	o.recovered()
}

// probe asks the store whether it is reachable again; a successful ping
// ends the failing episode even when no record arrives to prove it.
func (o *Outbox) probe(ctx context.Context) {
	pctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := o.Store.Ping(pctx); err != nil {
		return
	}
	o.recovered()
}

func (o *Outbox) recovered() {
	if o.failing.CompareAndSwap(true, false) {
		o.Log.Info("outbox: persistence recovered",
			"written", o.written.Load(), "write_failures", o.writeFailed.Load())
		if o.OnPersistRecovered != nil {
			o.OnPersistRecovered()
		}
	}
}
