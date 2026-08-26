// Package paper drives simulated cycles from qualified scanner events:
// reserve capital (idempotent, conflict-serialized), revalidate TTL,
// execute through the simulated executor, settle the ledger, and apply
// the result to the portfolio (docs/data-flow.md §2). Pause/resume is a
// shared-state control surface for web and Telegram alike.
package paper

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/execution"
	"github.com/cploutarchou/arb-chain-bot/internal/graph"
	"github.com/cploutarchou/arb-chain-bot/internal/opportunity"
	"github.com/cploutarchou/arb-chain-bot/internal/portfolio"
	"github.com/cploutarchou/arb-chain-bot/internal/reservation"
	"github.com/cploutarchou/arb-chain-bot/internal/scanner"
)

// Engine consumes qualified opportunities and settles simulated cycles.
type Engine struct {
	Executor  execution.Executor
	Resv      *reservation.Manager
	Portfolio *portfolio.Portfolio
	Triangles map[string]graph.Triangle // triangle ID → definition
	In        <-chan scanner.Event
	Clock     func() time.Time
	IDGen     func() string

	// MaxConcurrent bounds simultaneous simulations (risk's concurrency
	// check reads Active()).
	MaxConcurrent int

	// OnResult receives every settled result (hub publish, persistence).
	OnResult func(execution.CycleResult)

	running atomic.Bool
	active  atomic.Int32

	statsMu sync.Mutex
	stats   Stats
}

// Stats are the paper console counters.
type Stats struct {
	Received  int64
	Started   int64
	Skipped   int64 // paused, expired, reservation conflicts, capital
	Completed int64
	Failed    int64
}

func (e *Engine) Name() string { return "paper" }

// Active reports in-flight simulations (scanner risk context).
func (e *Engine) Active() int { return int(e.active.Load()) }

// Running reports the pause state.
func (e *Engine) Running() bool { return e.running.Load() }

// Pause / Resume are the shared control surface (audited by callers).
func (e *Engine) Pause()  { e.running.Store(false) }
func (e *Engine) Resume() { e.running.Store(true) }

// Snapshot returns the counters.
func (e *Engine) Snapshot() Stats {
	e.statsMu.Lock()
	defer e.statsMu.Unlock()
	return e.stats
}

func (e *Engine) bump(f func(*Stats)) {
	e.statsMu.Lock()
	f(&e.stats)
	e.statsMu.Unlock()
}

// Run consumes events until ctx cancels. Started paused=false by default:
// the caller decides the initial state before Run.
func (e *Engine) Run(ctx context.Context) error {
	if e.MaxConcurrent <= 0 {
		e.MaxConcurrent = 3
	}
	sem := make(chan struct{}, e.MaxConcurrent)
	var wg sync.WaitGroup
	defer wg.Wait()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev, ok := <-e.In:
			if !ok {
				return nil
			}
			if ev.Opportunity.Status != opportunity.StatusQualified {
				continue
			}
			e.bump(func(s *Stats) { s.Received++ })
			if !e.running.Load() {
				e.bump(func(s *Stats) { s.Skipped++ })
				continue
			}
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return ctx.Err()
			}
			wg.Add(1)
			e.active.Add(1)
			go func(ev scanner.Event) {
				defer func() { <-sem; e.active.Add(-1); wg.Done() }()
				e.runCycle(ctx, ev)
			}(ev)
		}
	}
}

func (e *Engine) runCycle(ctx context.Context, ev scanner.Event) {
	op := ev.Opportunity // copy; the engine owns this instance's lifecycle now
	tri, ok := e.Triangles[op.TriangleID]
	if !ok {
		e.bump(func(s *Stats) { s.Skipped++ })
		return
	}
	now := e.Clock()
	if op.Expired(now) {
		e.bump(func(s *Stats) { s.Skipped++ })
		return
	}

	// Reserve with per-market-side conflict keys: two triangles must not
	// simulate consuming the same displayed depth simultaneously
	// (docs/risk.md §4).
	conflicts := make([]string, 0, 3)
	for _, leg := range tri.Legs {
		conflicts = append(conflicts, fmt.Sprintf("mkt:%s:%s", leg.Market, leg.Side))
	}
	res, err := e.Resv.Reserve(op.ID, op.Start, op.Quote.InputConsumed, op.TriangleID, conflicts)
	if err != nil {
		if !errors.Is(err, reservation.ErrConflict) && !errors.Is(err, reservation.ErrInsufficientFunds) {
			e.bump(func(s *Stats) { s.Skipped++ })
			return
		}
		e.bump(func(s *Stats) { s.Skipped++ })
		return
	}
	if res.State != reservation.StateActive {
		// Idempotent replay of an already-settled reservation: skip.
		e.bump(func(s *Stats) { s.Skipped++ })
		return
	}
	_ = op.Transition(opportunity.StatusReserved, "")

	_ = op.Transition(opportunity.StatusSimulating, "")
	plan := execution.CyclePlan{
		CycleID:     e.IDGen(),
		Opportunity: &op,
		Triangle:    tri,
	}
	result, err := e.Executor.ExecuteCycle(ctx, plan)
	if err != nil {
		// Executor errors are configuration/programmer faults; release the
		// hold so capital is never stranded.
		_ = e.Resv.Release(res.ID)
		e.bump(func(s *Stats) { s.Skipped++ })
		return
	}

	switch result.Outcome {
	case execution.OutcomeRejected, execution.OutcomeExpired:
		// Nothing deployed: full release, no portfolio effect beyond count.
		_ = e.Resv.Release(res.ID)
		_ = op.Transition(opportunity.StatusFailed, string(result.Outcome))
		e.bump(func(s *Stats) { s.Skipped++ })
	default:
		if err := e.Resv.Settle(res.ID, result.InputConsumed); err == nil {
			if result.FinalAmount.IsPositive() {
				_ = e.Resv.Credit(result.StartAsset, result.FinalAmount)
			}
		}
		_ = e.Portfolio.ApplyCycle(result, false)
		if result.Outcome.Complete() {
			_ = op.Transition(opportunity.StatusCompleted, "")
			e.bump(func(s *Stats) { s.Started++; s.Completed++ })
		} else {
			_ = op.Transition(opportunity.StatusFailed, string(result.Outcome))
			e.bump(func(s *Stats) { s.Started++; s.Failed++ })
		}
	}
	if e.OnResult != nil {
		e.OnResult(result)
	}
}
