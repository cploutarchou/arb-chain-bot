package binance

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/cploutarchou/arb-chain-bot/internal/orderbook"
)

// Audit M1: Feed.Run's reconnect backoff only ever doubled, so a handful
// of historical failures (including the feed's own daily pre-emptive
// rollovers) left every later reconnect paying BackoffMax. These tests
// drive Run/session against a real in-process WS server with no
// snapshot/REST traffic involved (Symbols is empty), isolating the
// reconnect-timing state machine from book syncing.

// serverConn is one accepted WS upgrade, tagged with its 1-based dial
// attempt number (Feed.Run dials one session at a time, so attempts
// arrive on the channel in order).
type serverConn struct {
	n    int
	conn *websocket.Conn
}

// closeEvent reports when a connection was observed closed, from
// whichever side initiated it (the test, for simulated failures, or the
// feed itself, for a pre-emptive rollover).
type closeEvent struct {
	n  int
	at time.Time
}

// reconnectTestServer accepts successive WS upgrades and, for each one,
// both hands it to the test (via conns) and watches it for closure (via
// closed) so tests can measure the gap between one session ending and
// the next one starting without any cooperation from Feed internals.
func reconnectTestServer(t *testing.T) (wsHost string, conns <-chan serverConn, closed <-chan closeEvent) {
	t.Helper()
	var upgrader websocket.Upgrader
	var attempts atomic.Int64
	connCh := make(chan serverConn, 16)
	closedCh := make(chan closeEvent, 16)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		n := int(attempts.Add(1))
		connCh <- serverConn{n: n, conn: conn}
		go func() {
			for {
				if _, _, err := conn.ReadMessage(); err != nil {
					closedCh <- closeEvent{n: n, at: time.Now()}
					return
				}
			}
		}()
	}))
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http"), connCh, closedCh
}

func newReconnectFeed(wsHost string) *Feed {
	return &Feed{
		WSHost: wsHost,
		Books:  orderbook.NewSet(),
		// No symbols: session() still runs the full connect/read/preempt
		// loop, but fetchSnapshots has nothing to fetch, so these tests
		// never touch REST or the syncer.
		Symbols: nil,
		Log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func recvConn(t *testing.T, ch <-chan serverConn) serverConn {
	t.Helper()
	select {
	case sc := <-ch:
		return sc
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for a WS connection")
		return serverConn{}
	}
}

func recvClosed(t *testing.T, ch <-chan closeEvent) closeEvent {
	t.Helper()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for a connection close")
		return closeEvent{}
	}
}

// TestReconnectBackoffResetsAfterStableSession pins the M1 fix: repeated
// quick failures ratchet backoff up as before, but once a session survives
// past StableAfter, the NEXT reconnect goes out after BackoffMin again,
// not a continued (or maxed-out) ratchet.
func TestReconnectBackoffResetsAfterStableSession(t *testing.T) {
	wsHost, conns, closed := reconnectTestServer(t)
	f := newReconnectFeed(wsHost)
	f.BackoffMin = 80 * time.Millisecond
	f.BackoffMax = 2 * time.Second
	f.StableAfter = 250 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runDone := make(chan error, 1)
	go func() { runDone <- f.Run(ctx) }()

	// Attempts 1 and 2 fail almost immediately (well under StableAfter):
	// backoff must ratchet from BackoffMin to 2xBackoffMin between them.
	sc1 := recvConn(t, conns)
	time.AfterFunc(10*time.Millisecond, func() { _ = sc1.conn.Close() })
	x1 := recvClosed(t, closed)
	if x1.n != 1 {
		t.Fatalf("closed attempt = %d, want 1", x1.n)
	}

	sc2 := recvConn(t, conns)
	c2At := time.Now()
	time.AfterFunc(10*time.Millisecond, func() { _ = sc2.conn.Close() })
	x2 := recvClosed(t, closed)
	if x2.n != 2 {
		t.Fatalf("closed attempt = %d, want 2", x2.n)
	}

	// Attempt 3 stays up past StableAfter before failing: the session was
	// healthy, so the wait that follows must reset to BackoffMin instead
	// of continuing to double.
	sc3 := recvConn(t, conns)
	c3At := time.Now()
	time.AfterFunc(300*time.Millisecond, func() { _ = sc3.conn.Close() })
	x3 := recvClosed(t, closed)
	if x3.n != 3 {
		t.Fatalf("closed attempt = %d, want 3", x3.n)
	}

	sc4 := recvConn(t, conns)
	c4At := time.Now()
	if sc4.n != 4 {
		t.Fatalf("connect attempt = %d, want 4", sc4.n)
	}

	if got := f.Stats.Reconnects.Load(); got != 3 {
		t.Fatalf("reconnects = %d, want 3", got)
	}

	gap1 := c2At.Sub(x1.at) // ~BackoffMin
	gap2 := c3At.Sub(x2.at) // ~2xBackoffMin (ratcheted)
	gap3 := c4At.Sub(x3.at) // reset back to ~BackoffMin

	if gap1 < 20*time.Millisecond || gap1 > 200*time.Millisecond {
		t.Fatalf("gap1 = %s, want roughly BackoffMin (%s)", gap1, f.BackoffMin)
	}
	if gap2 < 60*time.Millisecond || gap2 > 350*time.Millisecond {
		t.Fatalf("gap2 = %s, want roughly 2xBackoffMin (ratcheted)", gap2)
	}
	// Left un-reset, the third wait would ratchet to ~4xBackoffMin
	// (320ms) or beyond; 250ms leaves a wide, non-flaky margin below that
	// while comfortably above a "reset to zero" bug.
	if gap3 < 20*time.Millisecond {
		t.Fatalf("gap3 = %s, suspiciously small (want ~BackoffMin, not zero-delay)", gap3)
	}
	if gap3 > 250*time.Millisecond {
		t.Fatalf("gap3 = %s, backoff did not reset after a stable session (want roughly BackoffMin, not a continued ratchet)", gap3)
	}

	cancel()
	select {
	case err := <-runDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run() error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not exit after context cancellation")
	}
}

// TestReconnectPreemptiveReconnectSkipsBackoff pins the second half of M1:
// the scheduled rollover ahead of Binance's 24h cut must reconnect at
// once, never waiting out BackoffMin (let alone a ratcheted value).
func TestReconnectPreemptiveReconnectSkipsBackoff(t *testing.T) {
	wsHost, conns, closed := reconnectTestServer(t)
	f := newReconnectFeed(wsHost)
	// Deliberately large so a regression (routing the pre-emptive return
	// through the normal backoff wait) would be impossible to miss.
	f.BackoffMin = 500 * time.Millisecond
	f.BackoffMax = 2 * time.Second
	f.PreemptAfter = 60 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runDone := make(chan error, 1)
	go func() { runDone <- f.Run(ctx) }()

	sc1 := recvConn(t, conns)
	connect1At := time.Now()
	_ = sc1

	// Nothing external closes this session; the feed's own PreemptAfter
	// timer inside session() ends it, and the server's read loop observes
	// the resulting close.
	x1 := recvClosed(t, closed)
	if x1.n != 1 {
		t.Fatalf("closed attempt = %d, want 1", x1.n)
	}
	if age := x1.at.Sub(connect1At); age < 40*time.Millisecond || age > 500*time.Millisecond {
		t.Fatalf("session 1 lifetime = %s, want roughly PreemptAfter (%s)", age, f.PreemptAfter)
	}

	sc2 := recvConn(t, conns)
	connect2At := time.Now()
	if sc2.n != 2 {
		t.Fatalf("connect attempt = %d, want 2", sc2.n)
	}

	gap := connect2At.Sub(x1.at)
	if gap >= f.BackoffMin {
		t.Fatalf("pre-emptive reconnect waited %s, want well under BackoffMin (%s)", gap, f.BackoffMin)
	}
	if gap > 200*time.Millisecond {
		t.Fatalf("pre-emptive reconnect gap too large: %s", gap)
	}

	if got := f.Stats.Reconnects.Load(); got != 1 {
		t.Fatalf("reconnects = %d, want 1", got)
	}

	cancel()
	select {
	case err := <-runDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run() error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not exit after context cancellation")
	}
}
