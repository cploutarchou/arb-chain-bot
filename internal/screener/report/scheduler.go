package report

import (
	"context"
	"time"
)

// RunAtUTC is the nightly slot: 00:05 UTC, when the previous UTC day is
// complete and the last funding settlement of the day has been booked.
var RunAtUTC = struct{ Hour, Minute int }{0, 5}

// Scheduler is the app.Component that runs the Generator every day at
// RunAtUTC. It sleeps until the next slot, runs, and repeats; a missed
// slot (process down) is NOT back-filled automatically — the operator
// runs POST /screener/reports/run for the missed day, and the run is
// logged either way.
type Scheduler struct {
	Gen *Generator
	// Now is injectable for tests.
	Now func() time.Time
	// After is injectable for tests (default time.After).
	After func(d time.Duration) <-chan time.Time
}

// Name implements app.Component.
func (s *Scheduler) Name() string { return "screener-reports" }

// NextRun returns the next RunAtUTC slot strictly after now.
func NextRun(now time.Time) time.Time {
	now = now.UTC()
	next := time.Date(now.Year(), now.Month(), now.Day(), RunAtUTC.Hour, RunAtUTC.Minute, 0, 0, time.UTC)
	if !next.After(now) {
		next = next.Add(24 * time.Hour)
	}
	return next
}

// Run implements app.Component.
func (s *Scheduler) Run(ctx context.Context) error {
	now := s.Now
	if now == nil {
		now = time.Now
	}
	after := s.After
	if after == nil {
		after = time.After
	}
	for {
		wait := NextRun(now()).Sub(now())
		s.Gen.log().Info("screener reports scheduled", "next_run_utc", NextRun(now()).Format(time.RFC3339))
		select {
		case <-ctx.Done():
			return nil
		case <-after(wait):
		}
		if _, err := s.Gen.Run(ctx, now()); err != nil && ctx.Err() == nil {
			s.Gen.log().Error("screener nightly report failed", "error", err)
		}
	}
}
