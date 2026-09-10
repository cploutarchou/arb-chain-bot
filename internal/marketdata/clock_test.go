package marketdata

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/exchange/binance"
)

// binance.RESTClient.ServerTime has the exact ServerTimeSource signature,
// so it satisfies the interface with no adapter — pinning the "venue-
// agnostic through a small interface the Binance REST client implements"
// requirement at compile time.
var _ ServerTimeSource = (*binance.RESTClient)(nil)

// fakeTimeSource is a deterministic ServerTimeSource for table-driven
// offset-math tests, independent of any real clock or network.
type fakeTimeSource struct {
	server time.Time
	rtt    time.Duration
	err    error
}

func (f fakeTimeSource) ServerTime(context.Context) (time.Time, time.Duration, error) {
	return f.server, f.rtt, f.err
}

// fakeClock returns a func() time.Time that yields times in sequence,
// repeating the last one once exhausted.
func fakeClock(times ...time.Time) func() time.Time {
	i := 0
	return func() time.Time {
		t := times[i]
		if i < len(times)-1 {
			i++
		}
		return t
	}
}

// TestClockMonitorOffsetHalvesRoundTrip pins the exact offset formula:
// server time compared against the local clock with the round trip
// halved (docs/research/market-data.md §4's "round-trip compensation"),
// fully deterministic via injected send/receive timestamps.
func TestClockMonitorOffsetHalvesRoundTrip(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	cases := []struct {
		name       string
		sendAt     time.Time
		recvAt     time.Time
		server     time.Time
		rtt        time.Duration
		wantOffset time.Duration
	}{
		{
			name:       "server exactly midpoint: zero offset",
			sendAt:     base,
			recvAt:     base.Add(100 * time.Millisecond),
			server:     base.Add(50 * time.Millisecond),
			rtt:        100 * time.Millisecond,
			wantOffset: 0,
		},
		{
			name:       "server ahead of local by 300ms",
			sendAt:     base,
			recvAt:     base.Add(100 * time.Millisecond),
			server:     base.Add(350 * time.Millisecond),
			rtt:        100 * time.Millisecond,
			wantOffset: 300 * time.Millisecond,
		},
		{
			name:       "server behind local by 200ms",
			sendAt:     base,
			recvAt:     base.Add(40 * time.Millisecond),
			server:     base.Add(-180 * time.Millisecond),
			rtt:        40 * time.Millisecond,
			wantOffset: -200 * time.Millisecond,
		},
		{
			name:       "zero rtt: offset is a plain difference",
			sendAt:     base,
			recvAt:     base,
			server:     base.Add(10 * time.Millisecond),
			rtt:        0,
			wantOffset: 10 * time.Millisecond,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := NewClockMonitor(fakeTimeSource{server: c.server, rtt: c.rtt})
			m.now = fakeClock(c.sendAt, c.recvAt)
			m.probe(context.Background())
			if got := m.Offset(); got != c.wantOffset {
				t.Fatalf("offset = %s, want %s", got, c.wantOffset)
			}
		})
	}
}

// TestClockMonitorNeverProbedIsUnhealthy: a freshly constructed monitor
// has not established a baseline yet and must not report healthy.
func TestClockMonitorNeverProbedIsUnhealthy(t *testing.T) {
	m := NewClockMonitor(fakeTimeSource{server: time.Now()})
	if m.Healthy() {
		t.Fatal("healthy = true before any probe ran")
	}
	if got := m.Offset(); got != 0 {
		t.Fatalf("offset = %s, want 0 before any probe", got)
	}
}

// TestClockMonitorToleratesFailuresBelowThreshold: consecutive failures
// below MaxFailures keep the last good reading (and Healthy) in force —
// a single flaky REST call must not flap the gate.
func TestClockMonitorToleratesFailuresBelowThreshold(t *testing.T) {
	m := NewClockMonitor(fakeTimeSource{server: time.Now()})
	m.MaxFailures = 3
	m.probe(context.Background())
	if !m.Healthy() {
		t.Fatalf("baseline probe unhealthy: %v", m.LastError())
	}

	failing := fakeTimeSource{err: errors.New("timeout")}
	m.Source = failing
	for i := 0; i < m.MaxFailures-1; i++ {
		m.probe(context.Background())
		if !m.Healthy() {
			t.Fatalf("healthy flipped false after only %d/%d consecutive failures", i+1, m.MaxFailures)
		}
		if m.LastError() == nil {
			t.Fatal("LastError = nil after a failing probe")
		}
	}
	m.probe(context.Background()) // the MaxFailures-th consecutive failure
	if m.Healthy() {
		t.Fatal("healthy = true after MaxFailures consecutive probe failures")
	}

	// Recovery: one successful probe clears the streak.
	m.Source = fakeTimeSource{server: time.Now()}
	m.probe(context.Background())
	if !m.Healthy() {
		t.Fatalf("did not recover after a successful probe: %v", m.LastError())
	}
	if m.LastError() != nil {
		t.Fatalf("LastError = %v, want nil after recovery", m.LastError())
	}
}

// timeServer fakes Binance's GET /api/v3/time: serverTime() supplies the
// clock to report (called fresh per request, so tests can move the
// simulated drift between probes) and fail, when non-nil, makes the
// endpoint answer with a server error instead.
func timeServer(t *testing.T, serverTime func() time.Time, fail func() bool) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if fail != nil && fail() {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]int64{"serverTime": serverTime().UnixMilli()})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestClockMonitorHealthyWithinThreshold exercises the full seam against
// a real HTTP round trip through binance.RESTClient.
func TestClockMonitorHealthyWithinThreshold(t *testing.T) {
	const drift = 100 * time.Millisecond
	srv := timeServer(t, func() time.Time { return time.Now().Add(drift) }, nil)
	m := NewClockMonitor(binance.NewRESTClient(srv.URL))
	m.Limit = 300 * time.Millisecond

	m.probe(context.Background())
	if !m.Healthy() {
		t.Fatalf("healthy = false within threshold: offset=%s err=%v", m.Offset(), m.LastError())
	}
	// Real localhost RTT is sub-millisecond; the measured offset should
	// track the injected drift closely.
	if got := m.Offset(); got < 50*time.Millisecond || got > 200*time.Millisecond {
		t.Fatalf("offset = %s, want roughly %s", got, drift)
	}
}

// TestClockMonitorUnhealthyOnDrift: an offset beyond Limit is unhealthy
// even though every probe succeeds.
func TestClockMonitorUnhealthyOnDrift(t *testing.T) {
	srv := timeServer(t, func() time.Time { return time.Now().Add(800 * time.Millisecond) }, nil)
	m := NewClockMonitor(binance.NewRESTClient(srv.URL))
	m.Limit = 500 * time.Millisecond // documented default; explicit for test clarity

	m.probe(context.Background())
	if m.Healthy() {
		t.Fatalf("healthy = true with offset %s exceeding limit %s", m.Offset(), m.Limit)
	}
	if m.LastError() != nil {
		t.Fatalf("LastError = %v, want nil (the probe itself succeeded)", m.LastError())
	}
}

// TestClockMonitorUnhealthyOnProbeFailures: an unreachable venue clock
// past MaxFailures consecutive probes is unhealthy regardless of any
// earlier good offset.
func TestClockMonitorUnhealthyOnProbeFailures(t *testing.T) {
	var failing atomic.Bool
	srv := timeServer(t, time.Now, failing.Load)
	m := NewClockMonitor(binance.NewRESTClient(srv.URL))
	m.MaxFailures = 3

	m.probe(context.Background())
	if !m.Healthy() {
		t.Fatalf("baseline probe unhealthy: %v", m.LastError())
	}

	failing.Store(true)
	for i := 0; i < m.MaxFailures; i++ {
		m.probe(context.Background())
	}
	if m.Healthy() {
		t.Fatal("healthy = true after MaxFailures consecutive probe failures")
	}
	if m.LastError() == nil {
		t.Fatal("LastError = nil after a failing probe")
	}
}

// TestClockMonitorRecoversWhenDriftClears: once the venue clock drifts
// back inside Limit, Healthy must return to true on the next probe.
func TestClockMonitorRecoversWhenDriftClears(t *testing.T) {
	var drift atomic.Int64
	drift.Store(int64(900 * time.Millisecond))
	srv := timeServer(t, func() time.Time { return time.Now().Add(time.Duration(drift.Load())) }, nil)
	m := NewClockMonitor(binance.NewRESTClient(srv.URL))
	m.Limit = 500 * time.Millisecond

	m.probe(context.Background())
	if m.Healthy() {
		t.Fatal("healthy = true despite 900ms drift")
	}

	drift.Store(int64(20 * time.Millisecond))
	m.probe(context.Background())
	if !m.Healthy() {
		t.Fatalf("did not recover after drift cleared: offset=%s err=%v", m.Offset(), m.LastError())
	}
}

// TestClockMonitorRunProbesImmediatelyAndOnInterval exercises the public
// Run(ctx) loop end to end: an immediate first probe, further probes on
// Interval, and a clean ctx.Canceled exit.
func TestClockMonitorRunProbesImmediatelyAndOnInterval(t *testing.T) {
	var calls atomic.Int64
	srv := timeServer(t, func() time.Time { calls.Add(1); return time.Now() }, nil)
	m := NewClockMonitor(binance.NewRESTClient(srv.URL))
	m.Interval = 10 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- m.Run(ctx) }()

	deadline := time.Now().Add(2 * time.Second)
	for calls.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := calls.Load(); got < 3 {
		t.Fatalf("probe calls = %d, want >= 3 within the deadline", got)
	}
	if !m.Healthy() {
		t.Fatalf("healthy = false after successful probes: %v", m.LastError())
	}

	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run() error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not exit after context cancellation")
	}
}

// TestClockMonitorDefaults pins the documented zero-value defaults.
func TestClockMonitorDefaults(t *testing.T) {
	m := NewClockMonitor(fakeTimeSource{})
	if got := m.interval(); got != DefaultClockCheckInterval {
		t.Fatalf("interval() = %s, want %s", got, DefaultClockCheckInterval)
	}
	if got := m.limit(); got != DefaultClockOffsetLimit {
		t.Fatalf("limit() = %s, want %s", got, DefaultClockOffsetLimit)
	}
	if got := m.maxFailures(); got != DefaultClockMaxFailures {
		t.Fatalf("maxFailures() = %d, want %d", got, DefaultClockMaxFailures)
	}

	m.Interval, m.Limit, m.MaxFailures = -1, -1, -1
	if got := m.interval(); got != DefaultClockCheckInterval {
		t.Fatalf("interval() with negative override = %s, want default", got)
	}
	if got := m.limit(); got != DefaultClockOffsetLimit {
		t.Fatalf("limit() with negative override = %s, want default", got)
	}
	if got := m.maxFailures(); got != DefaultClockMaxFailures {
		t.Fatalf("maxFailures() with negative override = %d, want default", got)
	}
}
