package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// RetentionConfig is cmd/worker's pruning policy for the telemetry-class
// tables named in docs/audit/database-audit.md D5 and
// docs/audit/infra-delivery-audit.md I9. It is loaded independently of
// Bootstrap (config.go): retention applies to exactly one binary
// (cmd/worker), every field has a documented default, and nothing here
// needs to be threaded through the shared engine wiring in internal/app.
//
// Financial evidence (paper_sessions, paper_cycles, orders, fills) and
// the two append-only tables the migration 000018 trigger protects
// (audit_events, risk_events) are never configurable here: there is no
// field for them, so a typo in an environment variable cannot make them
// eligible for pruning. See internal/storage/retention.go for the exact
// rules a window name maps to.
type RetentionConfig struct {
	// OpportunitiesQualified/Rejected split the single "opportunities"
	// policy in docs/data-flow.md §6 ("qualified kept 90d, sampled
	// rejections 14d") into the two windows it actually documents; a
	// qualified opportunity that produced a paper cycle is never pruned
	// regardless of age (internal/storage/retention.go guards on it),
	// since the cycle — kept forever — references it.
	OpportunitiesQualified time.Duration
	OpportunitiesRejected  time.Duration
	// ExchangeHealth and SystemEvents default to the 30d in
	// docs/data-flow.md §6.
	ExchangeHealth time.Duration
	SystemEvents   time.Duration
	// FundingHistory has no documented window; funding rows are already
	// deduplicated per settlement (docs/audit/database-audit.md, verified
	// correct) so growth is slow, but they are not evidence and the audit
	// still names them as a candidate — default generous (180d).
	FundingHistory time.Duration
	// Sessions prunes expired/revoked login sessions ("expired
	// sessions/tokens" in the roadmap item): a session becomes eligible
	// this long after it expired, or after it was revoked, whichever
	// happened — sessions never active in the first place are not kept
	// around indefinitely just because nobody logged them out.
	Sessions time.Duration
	// RecordingMetadata prunes market_recording_metadata rows for
	// sessions that have ended. Defaults to 0 (disabled): the segment
	// files a metadata row describes are pruned only "by disk budget
	// with explicit operator-approved pruning" (docs/data-flow.md §6),
	// never automatically, and this job never touches those files — it
	// only ever removes the bookkeeping row, and only once an operator
	// opts in by setting this above zero.
	RecordingMetadata time.Duration

	// RunAtHour/RunAtMinute is the UTC time-of-day the nightly sweep
	// runs (default 03:00, after the screener report scheduler's 00:05
	// slot and outside typical trading-hours review). Mirrors
	// internal/screener/report.Scheduler's fixed-slot design.
	RunAtHour   int
	RunAtMinute int

	// BatchSize bounds every DELETE issued by the job; BatchSleep pauses
	// between successive batches of the same rule so a large backlog
	// cannot hold the connection pool or lock queue back-to-back.
	BatchSize  int
	BatchSleep time.Duration
	// StatementTimeout bounds each individual count/delete statement.
	StatementTimeout time.Duration
	// DryRun reports how many rows each rule WOULD remove without
	// deleting anything; see internal/storage/retention.go.
	DryRun bool
}

// defaultRetentionConfig documents every default in one place; Load
// mutates a copy of it as it applies overrides.
func defaultRetentionConfig() RetentionConfig {
	const day = 24 * time.Hour
	return RetentionConfig{
		OpportunitiesQualified: 90 * day,
		OpportunitiesRejected:  14 * day,
		ExchangeHealth:         30 * day,
		SystemEvents:           30 * day,
		FundingHistory:         180 * day,
		Sessions:               30 * day,
		RecordingMetadata:      0,
		RunAtHour:              3,
		RunAtMinute:            0,
		BatchSize:              500,
		BatchSleep:             200 * time.Millisecond,
		StatementTimeout:       5 * time.Second,
		DryRun:                 false,
	}
}

// LoadRetention reads RetentionConfig from the environment. Every
// variable is optional; an invalid (unparseable or negative) value is a
// hard error rather than a silently ignored default, matching Load's own
// fail-fast policy in config.go.
//
// Durations accept a plain "<number>d" day suffix (e.g. "30d") in
// addition to anything time.ParseDuration understands ("720h", "5m30s"),
// since the roadmap item and docs/data-flow.md's own retention policy are
// both written in days.
func LoadRetention() (RetentionConfig, error) {
	c := defaultRetentionConfig()

	durations := []struct {
		env string
		dst *time.Duration
	}{
		{"RETENTION_OPPORTUNITIES_QUALIFIED", &c.OpportunitiesQualified},
		{"RETENTION_OPPORTUNITIES_REJECTED", &c.OpportunitiesRejected},
		{"RETENTION_EXCHANGE_HEALTH", &c.ExchangeHealth},
		{"RETENTION_SYSTEM_EVENTS", &c.SystemEvents},
		{"RETENTION_FUNDING_HISTORY", &c.FundingHistory},
		{"RETENTION_SESSIONS", &c.Sessions},
		{"RETENTION_RECORDING_METADATA", &c.RecordingMetadata},
		{"RETENTION_BATCH_SLEEP", &c.BatchSleep},
		{"RETENTION_STATEMENT_TIMEOUT", &c.StatementTimeout},
	}
	for _, f := range durations {
		v := os.Getenv(f.env)
		if v == "" {
			continue
		}
		d, err := parseRetentionDuration(v)
		if err != nil {
			return RetentionConfig{}, fmt.Errorf("config: invalid %s: %w", f.env, err)
		}
		if d < 0 {
			return RetentionConfig{}, fmt.Errorf("config: %s must not be negative", f.env)
		}
		*f.dst = d
	}

	if v := os.Getenv("RETENTION_RUN_AT_UTC"); v != "" {
		hour, minute, err := parseHHMM(v)
		if err != nil {
			return RetentionConfig{}, fmt.Errorf("config: invalid RETENTION_RUN_AT_UTC: %w", err)
		}
		c.RunAtHour, c.RunAtMinute = hour, minute
	}

	if v := os.Getenv("RETENTION_BATCH_SIZE"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return RetentionConfig{}, fmt.Errorf("config: invalid RETENTION_BATCH_SIZE %q: must be a positive integer", v)
		}
		c.BatchSize = n
	}

	if v := os.Getenv("RETENTION_DRY_RUN"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return RetentionConfig{}, fmt.Errorf("config: invalid RETENTION_DRY_RUN %q: %w", v, err)
		}
		c.DryRun = b
	}

	return c, nil
}

// parseRetentionDuration accepts a bare day count ("30d") or anything
// time.ParseDuration accepts ("720h", "5s", "200ms"); Go's own duration
// grammar has no day unit, and every default in this file is naturally
// expressed in days.
func parseRetentionDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if days, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.ParseFloat(days, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid day count %q", s)
		}
		return time.Duration(n * float64(24*time.Hour)), nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q (want e.g. \"30d\" or \"720h\"): %w", s, err)
	}
	return d, nil
}

// parseHHMM parses a 24-hour "HH:MM" UTC time-of-day.
func parseHHMM(s string) (hour, minute int, err error) {
	h, m, ok := strings.Cut(s, ":")
	if !ok {
		return 0, 0, fmt.Errorf("want HH:MM, got %q", s)
	}
	hour, err = strconv.Atoi(h)
	if err != nil || hour < 0 || hour > 23 {
		return 0, 0, fmt.Errorf("invalid hour in %q", s)
	}
	minute, err = strconv.Atoi(m)
	if err != nil || minute < 0 || minute > 59 {
		return 0, 0, fmt.Errorf("invalid minute in %q", s)
	}
	return hour, minute, nil
}
