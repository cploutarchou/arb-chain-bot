package marketdata

import (
	"context"
	"sync"
	"time"
)

// ServerTimeSource is the venue-agnostic seam ClockMonitor polls: any
// exchange REST client that reports its clock plus the request's round
// trip satisfies it with no adapter. binance.RESTClient.ServerTime has
// this exact signature, so it implements ServerTimeSource structurally.
type ServerTimeSource interface {
	// ServerTime returns the venue's current clock and how long the
	// request round trip took (used to compensate for network latency
	// when estimating the offset).
	ServerTime(ctx context.Context) (server time.Time, rtt time.Duration, err error)
}

// Defaults for ClockMonitor's polling policy (docs/research/market-data.md
// §4: the clock manager polls a venue's time endpoint, maintains an
// RTT-compensated offset estimate, and a degraded reading marks data
// unsafe and suspends qualification — SKILL.md §12).
const (
	DefaultClockCheckInterval = 30 * time.Second
	DefaultClockOffsetLimit   = 500 * time.Millisecond
	DefaultClockMaxFailures   = 3
)

// ClockMonitor estimates this process's clock offset against one venue's
// server time and reports whether local-clock-based measurements (book
// age, latency) are currently safe to trust. It is a background poller
// only: Run drives it on its own goroutine, nothing here sits on the hot
// path, and it holds no book state.
//
// Healthy is false until the first successful probe (an unchecked clock
// is never a safe clock) and flips false again whenever the last measured
// |offset| exceeds Limit, or MaxFailures consecutive probes have errored
// (an unreachable venue clock is itself a reason to distrust the last
// estimate, not just let it go stale). Fewer than MaxFailures consecutive
// failures keep the last good reading in force, so one transient REST
// hiccup does not flap the gate.
type ClockMonitor struct {
	// Source reports the venue's clock. binance.RESTClient satisfies this
	// directly.
	Source ServerTimeSource

	// Interval is the polling cadence. Zero uses DefaultClockCheckInterval.
	Interval time.Duration
	// Limit is the maximum tolerated |offset| before Healthy reports
	// false. Zero uses DefaultClockOffsetLimit.
	Limit time.Duration
	// MaxFailures is how many consecutive probe errors before Healthy
	// reports false regardless of the last known offset. Zero (or
	// negative) uses DefaultClockMaxFailures.
	MaxFailures int

	// now, when set, replaces time.Now for deterministic tests; nil in
	// production.
	now func() time.Time

	mu          sync.RWMutex
	probed      bool // at least one successful probe so far
	offset      time.Duration
	lastErr     error
	consecutive int // consecutive probe failures
	healthy     bool
}

// NewClockMonitor builds a monitor against source with the documented
// defaults; set the exported fields before calling Run to override them.
func NewClockMonitor(source ServerTimeSource) *ClockMonitor {
	return &ClockMonitor{
		Source:      source,
		Interval:    DefaultClockCheckInterval,
		Limit:       DefaultClockOffsetLimit,
		MaxFailures: DefaultClockMaxFailures,
	}
}

func (m *ClockMonitor) timeNow() time.Time {
	if m.now != nil {
		return m.now()
	}
	return time.Now()
}

func (m *ClockMonitor) interval() time.Duration {
	if m.Interval <= 0 {
		return DefaultClockCheckInterval
	}
	return m.Interval
}

func (m *ClockMonitor) limit() time.Duration {
	if m.Limit <= 0 {
		return DefaultClockOffsetLimit
	}
	return m.Limit
}

func (m *ClockMonitor) maxFailures() int {
	if m.MaxFailures <= 0 {
		return DefaultClockMaxFailures
	}
	return m.MaxFailures
}

// Healthy reports whether the clock offset is currently trusted. Scanners
// should gate qualification on this exactly as they gate on book health.
func (m *ClockMonitor) Healthy() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.healthy
}

// Offset returns the last measured offset (server clock minus local
// clock, round-trip compensated). Positive means the venue clock reads
// ahead of the local clock. Zero before the first successful probe.
func (m *ClockMonitor) Offset() time.Duration {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.offset
}

// LastError returns the most recent probe's error, or nil after a
// successful probe (or before the first probe has run).
func (m *ClockMonitor) LastError() error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.lastErr
}

// probe runs one measurement and updates Healthy/Offset accordingly. It
// never returns an error: a failed probe is absorbed into the consecutive-
// failure count exactly like every other probe, which is what lets a
// single transient REST failure not immediately flip Healthy to false.
func (m *ClockMonitor) probe(ctx context.Context) {
	sendAt := m.timeNow()
	server, rtt, err := m.Source.ServerTime(ctx)

	m.mu.Lock()
	defer m.mu.Unlock()
	if err != nil {
		m.lastErr = err
		m.consecutive++
		m.recomputeLocked()
		return
	}
	// The venue timestamp is generated somewhere inside the request's
	// round trip; halving rtt assumes symmetric network latency and
	// estimates the local instant it corresponds to (the standard
	// simplified NTP offset estimate, and exactly the "round-trip
	// compensation" docs/research/market-data.md §4 calls for).
	localAtServer := sendAt.Add(rtt / 2)
	m.offset = server.Sub(localAtServer)
	m.lastErr = nil
	m.consecutive = 0
	m.probed = true
	m.recomputeLocked()
}

// recomputeLocked derives Healthy from the current offset/failure state.
// Caller holds mu.
func (m *ClockMonitor) recomputeLocked() {
	withinLimit := m.offset <= m.limit() && -m.offset <= m.limit()
	m.healthy = m.probed && withinLimit && m.consecutive < m.maxFailures()
}

// Run polls Source on Interval until ctx is cancelled, probing once
// immediately so a freshly started monitor does not report the
// never-probed unhealthy state for a full Interval before its first
// reading. It always returns a non-nil error on exit (ctx.Err()),
// matching Feed.Run/Recorder.Run's "runs until told to stop" contract.
func (m *ClockMonitor) Run(ctx context.Context) error {
	m.probe(ctx)
	t := time.NewTicker(m.interval())
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			m.probe(ctx)
		}
	}
}
