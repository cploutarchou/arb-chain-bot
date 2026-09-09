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

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/exchange"
	"github.com/cploutarchou/arb-chain-bot/internal/execution"
	"github.com/cploutarchou/arb-chain-bot/internal/graph"
	"github.com/cploutarchou/arb-chain-bot/internal/opportunity"
	"github.com/cploutarchou/arb-chain-bot/internal/portfolio"
	"github.com/cploutarchou/arb-chain-bot/internal/reservation"
	"github.com/cploutarchou/arb-chain-bot/internal/risk"
	"github.com/cploutarchou/arb-chain-bot/internal/scanner"
)

// ErrActive: Reset requires the engine to be fully idle. Paused alone is
// not enough — Pause only stops NEW cycles from starting; a cycle already
// in flight (simulated latency, executor call) could still Settle/Credit
// against the ledger after Reset rebuilds it, corrupting the fresh
// session. Callers must wait for Active()==0 as well as !Running().
var ErrActive = errors.New("paper: engine must be paused with zero active simulations before reset")

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
	// check reads Active()). Live wiring sets it from the strategy's
	// max_concurrent_simulations at assembly; Run sizes its semaphore
	// once, so a hot-swapped value applies at the next restart while the
	// risk gate honours it immediately.
	MaxConcurrent int

	// OnResult receives every settled result (hub publish, persistence).
	OnResult func(execution.CycleResult)

	// Revalidate, when set, re-prices and re-gates a qualified opportunity
	// immediately before capital is reserved (scanner.Revalidate in live
	// wiring): books, capital, breakers and the clock all move between
	// qualification and execution, and the plan the scanner emitted may
	// no longer be executable or no longer clear the limits. ok=false
	// means the check could not run (unknown triangle, missing book) and
	// is treated as a rejection. A nil hook executes the plan as
	// qualified.
	Revalidate func(op opportunity.Opportunity) (fresh opportunity.Opportunity, dec risk.Decision, ok bool)
	// OnRevalidationReject receives every opportunity the revalidation
	// refused (reject counts, persistence, alerts).
	OnRevalidationReject func(op opportunity.Opportunity, dec risk.Decision)

	// Invariants, when set, replaces the ledger's own CheckInvariants
	// (tests inject failures); the default checks Resv after every
	// settlement and release.
	Invariants func() error
	// OnInvariantViolation receives a ledger invariant failure detected
	// after a settlement (docs/risk.md §4: simulation-inconsistency
	// breaker + CRITICAL alert). The engine pauses itself before calling
	// it: a ledger that no longer conserves capital must not run.
	OnInvariantViolation func(err error)

	running atomic.Bool
	active  atomic.Int32

	statsMu sync.Mutex
	stats   Stats
}

// Stats are the paper console counters.
type Stats struct {
	Received  int64
	Started   int64
	Skipped   int64 // paused, expired, reservation conflicts, capital, revalidation
	Completed int64
	Failed    int64
	// Revalidated counts opportunities re-checked before execution;
	// RevalidationRejected those refused the second time (a subset of
	// Skipped). InvariantViolations counts ledger conservation failures;
	// any non-zero value means the engine paused itself.
	Revalidated          int64
	RevalidationRejected int64
	InvariantViolations  int64
}

func (e *Engine) Name() string { return "paper" }

// Active reports in-flight simulations (scanner risk context).
func (e *Engine) Active() int { return int(e.active.Load()) }

// QueueDepth and QueueCapacity expose the inbound event-queue backlog
// (BL-18: system health's queue-depth panel). len/cap on a channel need
// no synchronization; nil In (paper wired without a feed, e.g. some
// tests) reports zero rather than panicking.
func (e *Engine) QueueDepth() int {
	if e.In == nil {
		return 0
	}
	return len(e.In)
}

func (e *Engine) QueueCapacity() int {
	if e.In == nil {
		return 0
	}
	return cap(e.In)
}

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

// Reset rebuilds the reservation ledger and portfolio to fresh initial
// balances and zeroes the session counters, without touching any pricing
// or settlement arithmetic (BL-10, paper reset). It refuses while the
// engine is running or any simulation is in flight (ErrActive) — the
// caller (console API) is expected to have paused first; Reset itself
// only re-checks Running()/Active() as its own last line of defense
// rather than trusting the caller blindly.
func (e *Engine) Reset(initial map[exchange.Asset]decimal.Decimal) error {
	if e.Running() || e.Active() > 0 {
		return ErrActive
	}
	e.Resv.Reset(initial)
	e.Portfolio.Reset(initial)
	e.statsMu.Lock()
	e.stats = Stats{}
	e.statsMu.Unlock()
	return nil
}

func (e *Engine) bump(f func(*Stats)) {
	e.statsMu.Lock()
	f(&e.stats)
	e.statsMu.Unlock()
}

// checkInvariants verifies the ledger's conservation laws after every
// mutation this engine makes. A violation pauses the engine first — no
// further cycle may start on a ledger that has stopped conserving
// capital — and then reports (docs/risk.md §4).
func (e *Engine) checkInvariants() {
	check := e.Invariants
	if check == nil {
		check = e.Resv.CheckInvariants
	}
	err := check()
	if err == nil {
		return
	}
	e.Pause()
	e.bump(func(s *Stats) { s.InvariantViolations++ })
	if e.OnInvariantViolation != nil {
		e.OnInvariantViolation(err)
	}
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

	// Revalidate before committing capital: the books, the ledger and the
	// breakers have all moved since the scanner qualified this plan.
	if e.Revalidate != nil {
		fresh, dec, ok := e.Revalidate(op)
		e.bump(func(s *Stats) { s.Revalidated++ })
		if !ok || !dec.Allowed {
			e.bump(func(s *Stats) { s.Skipped++; s.RevalidationRejected++ })
			if !ok && dec.ReasonCode == "" {
				dec.ReasonCode = risk.ReasonRevalidation
			}
			if e.OnRevalidationReject != nil {
				e.OnRevalidationReject(op, dec)
			}
			return
		}
		op = fresh
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
		// Conflicting hold, insufficient capital, or the same opportunity
		// already executing under this key (ErrDuplicateActive): skip.
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
		e.checkInvariants()
	default:
		if err := e.Resv.Settle(res.ID, result.InputConsumed); err == nil {
			if result.FinalAmount.IsPositive() {
				_ = e.Resv.Credit(result.StartAsset, result.FinalAmount)
			}
		}
		_ = e.Portfolio.ApplyCycle(result, false)
		e.checkInvariants()
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
