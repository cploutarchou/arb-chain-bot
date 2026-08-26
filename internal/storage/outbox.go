package storage

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// Outbox decouples the hot path from PostgreSQL: producers enqueue
// non-blocking; a single writer drains in batches. Overflow increments a
// counter and invokes the breaker hook — the hot path never waits
// (docs/architecture.md §12).
type Outbox struct {
	Store     *Store
	Log       *slog.Logger
	SessionID string

	QueueSize     int
	FlushInterval time.Duration

	// OnPersistError feeds the persistence circuit breaker.
	OnPersistError func(error)

	once    sync.Once
	ch      chan Record
	dropped atomic.Int64
	written atomic.Int64
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
		o.ch = make(chan Record, o.QueueSize)
	})
}

// Enqueue never blocks; false means the record was dropped (counted).
func (o *Outbox) Enqueue(rec Record) bool {
	o.init()
	select {
	case o.ch <- rec:
		return true
	default:
		o.dropped.Add(1)
		return false
	}
}

// Dropped and Written expose counters (metrics + breaker thresholds).
func (o *Outbox) Dropped() int64 { return o.dropped.Load() }
func (o *Outbox) Written() int64 { return o.written.Load() }

func (o *Outbox) Name() string { return "outbox" }

// Run drains until ctx cancels, then flushes what is already queued with
// a short deadline (graceful shutdown, docs/architecture.md §5).
func (o *Outbox) Run(ctx context.Context) error {
	o.init()
	ticker := time.NewTicker(o.FlushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			flushCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			o.drain(flushCtx)
			cancel()
			return ctx.Err()
		case rec := <-o.ch:
			o.write(ctx, rec)
		case <-ticker.C:
			// Tick keeps the loop responsive for future batch writes; the
			// per-record write above is sufficient at paper volumes.
		}
	}
}

func (o *Outbox) drain(ctx context.Context) {
	for {
		select {
		case rec := <-o.ch:
			o.write(ctx, rec)
		default:
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
	default:
		o.Log.Warn("outbox: unknown record kind", "kind", rec.Kind)
		return
	}
	if err != nil {
		o.Log.Error("outbox write failed", "kind", rec.Kind, "error", err)
		if o.OnPersistError != nil {
			o.OnPersistError(err)
		}
		return
	}
	o.written.Add(1)
}
