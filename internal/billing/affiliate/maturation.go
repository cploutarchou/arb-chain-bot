// MaturationJob (T-084): the cmd/worker component that writes `matured`
// rows as accruals cross their 45-day matures_at. Mirrors the retention
// scheduler's shape: run once at boot, then every Interval; each pass is
// idempotent (DueMaturations never re-proposes a matured transaction),
// so a restart or an overlapping run can only ever be a no-op.
package affiliate

import (
	"context"
	"crypto/rand"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/oklog/ulid/v2"
)

// MaturationStore is the persistence surface the job needs.
type MaturationStore interface {
	DueMaturations(ctx context.Context, now time.Time) ([]Entry, error)
	Insert(ctx context.Context, e Entry) error
}

// MaturationJob turns due accruals into matured ledger rows.
type MaturationJob struct {
	Store MaturationStore
	Log   *slog.Logger
	// Interval between passes; defaults to 1h. The §5 monthly payout
	// run on the 15th reads whatever has matured by then — hourly
	// passes keep the report's dates honest without any hurry.
	Interval time.Duration
	// Timeout bounds each pass's query; defaults to 30s.
	Timeout time.Duration
	// Now, After and IDGen are injectable for tests.
	Now   func() time.Time
	After func(d time.Duration) <-chan time.Time
	IDGen func() string

	passes  atomic.Int64
	matured atomic.Int64
}

// Name implements app.Component.
func (j *MaturationJob) Name() string { return "affiliate-maturation" }

func (j *MaturationJob) defaults() {
	if j.Interval <= 0 {
		j.Interval = time.Hour
	}
	if j.Timeout <= 0 {
		j.Timeout = 30 * time.Second
	}
	if j.Now == nil {
		j.Now = time.Now
	}
	if j.After == nil {
		j.After = time.After
	}
	if j.IDGen == nil {
		j.IDGen = func() string { return "affmat-" + ulid.MustNew(ulid.Timestamp(time.Now()), rand.Reader).String() }
	}
	if j.Log == nil {
		j.Log = slog.Default()
	}
}

// Run implements app.Component: one pass at boot, then one per
// Interval, until ctx is cancelled.
func (j *MaturationJob) Run(ctx context.Context) error {
	j.defaults()
	j.pass(ctx)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-j.After(j.Interval):
			j.pass(ctx)
		}
	}
}

// Stats reports the job's progress (boot introspection).
func (j *MaturationJob) Stats() (passes, matured int64) {
	return j.passes.Load(), j.matured.Load()
}

func (j *MaturationJob) pass(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, j.Timeout)
	defer cancel()
	now := j.Now()
	due, err := j.Store.DueMaturations(ctx, now)
	if err != nil {
		j.Log.Error("affiliate maturation: due query failed", "error", err)
		return
	}
	var written int
	for _, e := range due {
		if err := j.Store.Insert(ctx, Mature(e, j.IDGen(), now)); err != nil {
			j.Log.Error("affiliate maturation: insert failed", "transaction_id", e.TransactionID, "error", err)
			continue
		}
		written++
	}
	j.passes.Add(1)
	j.matured.Add(int64(written))
	if written > 0 || j.Log.Enabled(ctx, slog.LevelDebug) {
		j.Log.Info("affiliate maturation pass", "due", len(due), "matured", written)
	}
}
