// Package jobrun holds the small pieces of logic internal/replay.Runner
// and internal/campaign.Runner share (review T-058 P3(h)): both are
// near-identical "launch a background job, track it in memory, persist
// it, reconcile orphans on restart" components with their own Run/Store
// types (replay.Run vs campaign.Run, replay.RunStore vs
// campaign.RunStore) — not similar enough to collapse into one generic
// Runner, but similar enough that fixing a bug in one and not the other
// is exactly how they drift apart. jobrun factors out the parts that
// are genuinely identical: the lifetime-context handoff between Run and
// Start, the in-memory ring bound, page-size clamping, and the
// owner/heartbeat reclaim predicate used by orphan reconciliation. Each
// Runner still owns its own execute()/persist()/List()/reconcileOrphans
// wiring around these pieces.
package jobrun

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"
)

// RingCap bounds both the in-memory run maps each Runner keeps (review
// P3(h): unbounded before this, one entry per run ever started for the
// life of the process) and the default depth orphan reconciliation
// looks back over on startup. 200 matches the API's own documented page
// cap (List already refused anything larger).
const RingCap = 200

// NewOwnerID returns a random per-process identifier. Tagging every
// persisted run row with the writing process's identity lets orphan
// reconciliation on startup distinguish "a row a PREVIOUS instance of
// THIS process abandoned" (reclaim it) from "a row a DIFFERENT,
// still-live process is actively updating" (leave it alone) — review
// P3(h): the old reconciliation logic treated both cases identically
// (anything not in ITS OWN fresh in-memory map, which is always empty
// at boot, got reclaimed), so two processes sharing one Postgres (a
// rolling-deploy overlap, or an operator running two instances against
// the same database) would each mark the other's active run "failed"
// the moment they started.
func NewOwnerID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand failing means the process's entropy source is
		// broken — a condition far more serious than this reconciliation
		// helper. Degrade to a still-unique-per-process value instead of
		// panicking a background component over it.
		return "owner-" + time.Now().UTC().Format(time.RFC3339Nano)
	}
	return hex.EncodeToString(b)
}

// HeartbeatInterval is how often an in-progress run's persisted row is
// re-stamped with the current owner id and timestamp, independent of
// whatever progress callbacks that run happens to fire (a replay's
// Progress only fires at start and finish — see replay.Progress's doc
// comment — so relying on progress alone would let a long-running
// replay's heartbeat go stale for its entire duration).
const HeartbeatInterval = 10 * time.Second

// StaleAfter is how long a row's heartbeat may go unrefreshed before
// reconciliation treats its owner as dead and reclaims the row. Kept
// comfortably larger than HeartbeatInterval (5x) so one slow tick, a GC
// pause, or a single dropped write can never cause a live run's row to
// be reclaimed out from under it.
const StaleAfter = 5 * HeartbeatInterval

// Reclaimable reports whether a "queued"/"running" row belongs to a
// dead owner and should be marked failed by orphan reconciliation.
//
//   - mine is true when the row's id is already tracked in the CALLING
//     process's own in-memory map — never reclaim a run this exact
//     process instance is running.
//   - ownerID is the calling process's own NewOwnerID().
//   - rowOwner/heartbeatAt are the row's last-persisted owner_id/
//     heartbeat_at. A zero heartbeatAt means either a pre-migration row
//     (no owner/heartbeat ever recorded) or a row that was inserted but
//     never heartbeated — both cases carry no live-owner signal at all,
//     so they are always reclaimable (matches the pre-P3(h) behavior
//     for anything this process didn't itself start).
func Reclaimable(mine bool, ownerID, rowOwner string, heartbeatAt, now time.Time) bool {
	if mine {
		return false
	}
	if rowOwner != "" && rowOwner == ownerID {
		// Should not happen in practice (a fresh process gets a fresh
		// owner id on every restart, so it can never match a persisted
		// row from a previous life), but never reclaim a row THIS exact
		// process instance is the recorded owner of.
		return false
	}
	if heartbeatAt.IsZero() {
		return true
	}
	return now.Sub(heartbeatAt) >= StaleAfter
}

// ErrNotStarted is returned by Gate.Await when its timeout elapses
// before Pin has ever been called.
var ErrNotStarted = errors.New("jobrun: component has not started yet (Run has not pinned a lifetime context)")

// Gate pins a Component's lifetime context exactly once per Run call
// and lets Start-style callers wait for it, bounded, instead of racing
// ahead with context.Background() when Start is reachable before Run
// (review P3(h)): a job launched that way had its execution bound to
// context.Background — which a supervised shutdown never cancels — so
// a Start call that won the race against Run would survive a graceful
// shutdown of the very process that launched it.
type Gate struct {
	mu    sync.Mutex
	ctx   context.Context
	ready chan struct{}
}

// Pin records ctx as the current lifetime context and releases any
// Await call waiting on it. Idempotent: safe to call more than once
// (a Runner's Run is an app.Component method — nothing here assumes a
// process only ever calls it once — so a second Pin re-arms a fresh
// ready channel rather than double-closing the previous one).
func (g *Gate) Pin(ctx context.Context) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.ready == nil {
		g.ready = make(chan struct{})
	}
	select {
	case <-g.ready:
		// Already closed by an earlier Pin: this is a later Run call
		// (or a restart of the component); re-arm so THIS pin's close
		// is the one Await's caller actually observes.
		g.ready = make(chan struct{})
	default:
	}
	g.ctx = ctx
	close(g.ready)
}

// Await blocks until Pin has been called at least once, or timeout
// elapses, and returns the pinned context. Callers that need a bounded
// lifetime context (Start, before launching background work under it)
// must treat a timeout as a hard refusal — never fall back to
// context.Background().
func (g *Gate) Await(timeout time.Duration) (context.Context, error) {
	g.mu.Lock()
	if g.ready == nil {
		g.ready = make(chan struct{})
	}
	ready := g.ready
	g.mu.Unlock()
	select {
	case <-ready:
		g.mu.Lock()
		defer g.mu.Unlock()
		return g.ctx, nil
	case <-time.After(timeout):
		return nil, ErrNotStarted
	}
}

// Current returns the pinned context without waiting — context.Background()
// if Pin has never been called. For call sites that already know they
// run after a successful Await (e.g. deep inside an already-launched
// background job) and only need the context, not the wait.
func (g *Gate) Current() context.Context {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.ctx == nil {
		return context.Background()
	}
	return g.ctx
}

// Bound caps order (an id list, oldest-first) to at most capN entries,
// deleting evicted ids from byID too (review P3(h)): both Runners kept
// an in-memory map alongside their persisted history with no upper
// bound — one entry per run ever started for the life of the process,
// a slow, unbounded memory leak on any installation that runs replays
// or campaigns regularly over weeks/months.
func Bound[T any](order []string, byID map[string]*T, capN int) []string {
	for len(order) > capN {
		oldest := order[0]
		order = order[1:]
		delete(byID, oldest)
	}
	return order
}

// ClampLimit resolves a caller-supplied page size against a default and
// a hard maximum (review P3(h)): the shape this replaces collapsed ANY
// out-of-range value to the default —
// `if limit <= 0 || limit > max { limit = def }` — so a caller asking
// for anything above the documented cap silently got the DEFAULT
// instead of the cap. limit <= 0 (unset) still gets def; anything above
// max is clamped TO max, never bounced down to def.
func ClampLimit(limit, def, max int) int {
	if limit <= 0 {
		return def
	}
	if limit > max {
		return max
	}
	return limit
}
