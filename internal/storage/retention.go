package storage

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// retentionRuleSQL is one prunable rule: countSQL takes a single cutoff
// argument and reports how many rows are eligible; deleteSQL takes the
// cutoff plus a row-limit argument and removes at most that many. Both
// use "id IN (SELECT ... LIMIT $2)" (or the composite-key equivalent for
// funding_history, which has no surrogate id) rather than DELETE ...
// LIMIT, which PostgreSQL does not support directly.
type retentionRuleSQL struct {
	countSQL  string
	deleteSQL string
}

// retentionRules is the fixed set of tables this job knows how to prune —
// deliberately closed over the telemetry-class tables
// docs/audit/database-audit.md D5 and docs/audit/infra-delivery-audit.md
// I9 name. Financial evidence (paper_sessions, paper_cycles, orders,
// fills) and the append-only tables migration 000018 protects
// (audit_events, risk_events) have no entry here at all: there is no
// window field or env var that can reach them, and RetentionScheduler
// only ever prunes a rule name that exists in this map (see CountPrunable
// / PruneBatch below), so a typo in configuration fails loudly instead of
// silently deleting the wrong thing.
//
// opportunities splits into "qualified" and "rejected" because
// docs/data-flow.md §6 documents two different windows for them
// (90d/14d). Both branches additionally require that no paper_cycles row
// references the opportunity: paper_cycles.opportunity_id is a foreign
// key with no ON DELETE clause, so deleting a referenced opportunity
// would either fail outright or (worse, if the FK is ever loosened) sever
// a financial-evidence row's link to the opportunity that produced it.
// Every qualified opportunity that was actually executed has a cycle, so
// in practice this guard is what makes the "qualified" rule prune only
// the ones that were never executed (e.g. capital reservation lost the
// race) once they age out.
var retentionRules = map[string]retentionRuleSQL{
	"opportunities_qualified": {
		countSQL: `
			SELECT count(*) FROM opportunities o
			WHERE o.status = 'QUALIFIED' AND o.detected_at < $1
			  AND NOT EXISTS (SELECT 1 FROM paper_cycles pc WHERE pc.opportunity_id = o.id)`,
		deleteSQL: `
			DELETE FROM opportunities WHERE id IN (
				SELECT o.id FROM opportunities o
				WHERE o.status = 'QUALIFIED' AND o.detected_at < $1
				  AND NOT EXISTS (SELECT 1 FROM paper_cycles pc WHERE pc.opportunity_id = o.id)
				LIMIT $2)`,
	},
	"opportunities_rejected": {
		countSQL: `
			SELECT count(*) FROM opportunities o
			WHERE o.status = 'REJECTED' AND o.detected_at < $1
			  AND NOT EXISTS (SELECT 1 FROM paper_cycles pc WHERE pc.opportunity_id = o.id)`,
		deleteSQL: `
			DELETE FROM opportunities WHERE id IN (
				SELECT o.id FROM opportunities o
				WHERE o.status = 'REJECTED' AND o.detected_at < $1
				  AND NOT EXISTS (SELECT 1 FROM paper_cycles pc WHERE pc.opportunity_id = o.id)
				LIMIT $2)`,
	},
	"exchange_health": {
		countSQL:  `SELECT count(*) FROM exchange_health WHERE ts < $1`,
		deleteSQL: `DELETE FROM exchange_health WHERE id IN (SELECT id FROM exchange_health WHERE ts < $1 LIMIT $2)`,
	},
	"system_events": {
		countSQL:  `SELECT count(*) FROM system_events WHERE ts < $1`,
		deleteSQL: `DELETE FROM system_events WHERE id IN (SELECT id FROM system_events WHERE ts < $1 LIMIT $2)`,
	},
	"funding_history": {
		countSQL: `SELECT count(*) FROM funding_history WHERE at < $1`,
		deleteSQL: `
			DELETE FROM funding_history WHERE (venue, base, at) IN (
				SELECT venue, base, at FROM funding_history WHERE at < $1 LIMIT $2)`,
	},
	"sessions": {
		countSQL:  `SELECT count(*) FROM sessions WHERE COALESCE(revoked_at, expires_at) < $1`,
		deleteSQL: `DELETE FROM sessions WHERE id IN (SELECT id FROM sessions WHERE COALESCE(revoked_at, expires_at) < $1 LIMIT $2)`,
	},
	// ended_at IS NOT NULL: a recording session still in progress has no
	// end time and must never be swept up regardless of how old
	// started_at is.
	"recording_metadata": {
		countSQL: `SELECT count(*) FROM market_recording_metadata WHERE ended_at IS NOT NULL AND ended_at < $1`,
		deleteSQL: `
			DELETE FROM market_recording_metadata WHERE id IN (
				SELECT id FROM market_recording_metadata WHERE ended_at IS NOT NULL AND ended_at < $1 LIMIT $2)`,
	},
}

// RetentionRuleNames returns the rule names *Store knows how to prune,
// sorted for stable logging. cmd/worker uses it to log the effective
// policy at startup.
func RetentionRuleNames() []string {
	names := make([]string, 0, len(retentionRules))
	for name := range retentionRules {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// CountPrunable reports how many rows the named rule would remove right
// now, without deleting anything (dry-run and pre-sweep observability).
func (s *Store) CountPrunable(ctx context.Context, rule string, cutoff time.Time) (int64, error) {
	q, ok := retentionRules[rule]
	if !ok {
		return 0, fmt.Errorf("storage: unknown retention rule %q", rule)
	}
	var n int64
	if err := s.Pool.QueryRow(ctx, q.countSQL, cutoff).Scan(&n); err != nil {
		return 0, fmt.Errorf("storage: count %s: %w", rule, err)
	}
	return n, nil
}

// PruneBatch deletes at most batchSize rows eligible under the named
// rule and reports how many were actually removed. A result below
// batchSize means the rule found nothing left to do for this cutoff.
func (s *Store) PruneBatch(ctx context.Context, rule string, cutoff time.Time, batchSize int) (int64, error) {
	q, ok := retentionRules[rule]
	if !ok {
		return 0, fmt.Errorf("storage: unknown retention rule %q", rule)
	}
	if batchSize <= 0 {
		batchSize = 500
	}
	tag, err := s.Pool.Exec(ctx, q.deleteSQL, cutoff, batchSize)
	if err != nil {
		return 0, fmt.Errorf("storage: prune %s: %w", rule, err)
	}
	return tag.RowsAffected(), nil
}

// RetentionRunner is the persistence surface RetentionScheduler sweeps
// through. *Store satisfies it against PostgreSQL (via CountPrunable /
// PruneBatch above); unit tests substitute a fake so the schedule and
// dry-run behaviour can be verified without a database, the same way
// Outbox's RecordWriter lets outbox_test.go drive failure/recovery
// without one.
type RetentionRunner interface {
	CountPrunable(ctx context.Context, rule string, cutoff time.Time) (int64, error)
	PruneBatch(ctx context.Context, rule string, cutoff time.Time, batchSize int) (int64, error)
}

// RetentionScheduler is the cmd/worker app.Component that prunes the
// telemetry-class tables named in retentionRules on a nightly schedule
// (docs/audit/database-audit.md D5, docs/audit/infra-delivery-audit.md
// I9: cmd/worker builds no components today). It mirrors
// internal/screener/report.Scheduler's fixed-UTC-slot design: sleep
// until the next RunAtHour:RunAtMinute, sweep, repeat; a missed slot
// (process down) is not back-filled.
type RetentionScheduler struct {
	Runner RetentionRunner
	Log    *slog.Logger

	// Windows maps a rule name (see retentionRules / RetentionRuleNames)
	// to the maximum age a row may reach before it is eligible. A rule
	// absent from this map, or mapped to <= 0, is skipped entirely for
	// every sweep — this is how an unconfigured or explicitly zeroed
	// rule stays inert rather than defaulting to "prune everything".
	Windows map[string]time.Duration

	// RunAtHour/RunAtMinute is the UTC time-of-day each sweep runs.
	RunAtHour, RunAtMinute int

	// BatchSize bounds every DELETE; BatchSleep pauses between
	// successive batches of the SAME rule. Both fall back to sane
	// defaults (500 / 200ms) when left at the zero value.
	BatchSize  int
	BatchSleep time.Duration
	// StatementTimeout bounds each individual count/delete call.
	// Defaults to 5s.
	StatementTimeout time.Duration
	// DryRun reports counts only; PruneBatch is never called.
	DryRun bool

	// Now and After are injectable for tests (default time.Now /
	// time.After), matching internal/screener/report.Scheduler exactly.
	Now   func() time.Time
	After func(d time.Duration) <-chan time.Time

	mu         sync.Mutex
	lastCounts map[string]int64
	lastErr    error
	sweeps     atomic.Int64
}

// Name implements app.Component.
func (r *RetentionScheduler) Name() string { return "retention" }

// NextRun returns the next RunAtHour:RunAtMinute UTC slot strictly after
// now (pure function, unit-tested independent of any clock or database —
// see TestRetentionSchedulerNextRun).
func (r *RetentionScheduler) NextRun(now time.Time) time.Time {
	now = now.UTC()
	next := time.Date(now.Year(), now.Month(), now.Day(), r.RunAtHour, r.RunAtMinute, 0, 0, time.UTC)
	if !next.After(now) {
		next = next.Add(24 * time.Hour)
	}
	return next
}

func (r *RetentionScheduler) applyDefaults() {
	if r.BatchSize <= 0 {
		r.BatchSize = 500
	}
	if r.BatchSleep <= 0 {
		r.BatchSleep = 200 * time.Millisecond
	}
	if r.StatementTimeout <= 0 {
		r.StatementTimeout = 5 * time.Second
	}
	if r.Now == nil {
		r.Now = time.Now
	}
	if r.After == nil {
		r.After = time.After
	}
	if r.Log == nil {
		r.Log = slog.Default()
	}
}

// Run implements app.Component: sleep until the next scheduled slot,
// sweep, repeat, until ctx is cancelled.
func (r *RetentionScheduler) Run(ctx context.Context) error {
	r.applyDefaults()
	for {
		wait := r.NextRun(r.Now()).Sub(r.Now())
		r.Log.Info("retention: sweep scheduled", "next_run_utc", r.NextRun(r.Now()).Format(time.RFC3339))
		select {
		case <-ctx.Done():
			return nil
		case <-r.After(wait):
		}
		r.sweep(ctx)
	}
}

// sweep runs every configured rule once, in a stable order, tolerating
// one rule's failure without skipping the rest.
func (r *RetentionScheduler) sweep(ctx context.Context) {
	now := r.Now()
	rules := make([]string, 0, len(r.Windows))
	for rule := range r.Windows {
		rules = append(rules, rule)
	}
	sort.Strings(rules)

	counts := make(map[string]int64, len(rules))
	var firstErr error
	for _, rule := range rules {
		window := r.Windows[rule]
		if window <= 0 {
			continue
		}
		cutoff := now.Add(-window)
		n, err := r.pruneRule(ctx, rule, cutoff)
		counts[rule] = n
		switch {
		case err != nil && ctx.Err() != nil:
			// Shutdown mid-sweep, not a rule failure: nothing to log as
			// an error, the scheduler is exiting anyway.
		case err != nil:
			if firstErr == nil {
				firstErr = fmt.Errorf("%s: %w", rule, err)
			}
			r.Log.Error("retention: rule failed", "rule", rule, "cutoff", cutoff, "rows_deleted", n, "dry_run", r.DryRun, "error", err)
		default:
			r.Log.Info("retention: rule complete", "rule", rule, "cutoff", cutoff, "rows_deleted", n, "dry_run", r.DryRun)
		}
	}

	r.mu.Lock()
	r.lastCounts = counts
	r.lastErr = firstErr
	r.mu.Unlock()
	r.sweeps.Add(1)
}

// pruneRule runs one rule to exhaustion (dry-run: a single count; live:
// batch after batch, sleeping BatchSleep between them, until a batch
// returns fewer rows than BatchSize or ctx is cancelled) and returns the
// total rows removed (or, in dry-run, the total that would be).
func (r *RetentionScheduler) pruneRule(ctx context.Context, rule string, cutoff time.Time) (int64, error) {
	if r.DryRun {
		qctx, cancel := context.WithTimeout(ctx, r.StatementTimeout)
		defer cancel()
		return r.Runner.CountPrunable(qctx, rule, cutoff)
	}
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		qctx, cancel := context.WithTimeout(ctx, r.StatementTimeout)
		n, err := r.Runner.PruneBatch(qctx, rule, cutoff, r.BatchSize)
		cancel()
		total += n
		if err != nil {
			return total, err
		}
		if n < int64(r.BatchSize) {
			return total, nil
		}
		select {
		case <-ctx.Done():
			return total, ctx.Err()
		case <-time.After(r.BatchSleep):
		}
	}
}

// LastSweep returns a copy of the most recent sweep's per-rule row
// counts (or would-be counts, in dry-run) and the first error
// encountered, if any. Zero-value before the first sweep completes.
func (r *RetentionScheduler) LastSweep() (map[string]int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]int64, len(r.lastCounts))
	for k, v := range r.lastCounts {
		out[k] = v
	}
	return out, r.lastErr
}

// Sweeps counts completed sweeps (including ones where a rule failed);
// system-health/observability surface and a test hook.
func (r *RetentionScheduler) Sweeps() int64 { return r.sweeps.Load() }
