package venue

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/screener"
)

// TestMEXCInBandRateLimit510: the contract API answers HTTP 200 with
// {"code":510,"msg":"Requests are too frequent"} (recorded fixture
// contract_ticker_510.json). The collector must surface it as an
// InBandRateLimit, count it as rate_limited and pause the venue gate
// for mexcInBandPause (10 s) as if it were a 429 — the docs publish no
// Retry-After for code 510.
func TestMEXCInBandRateLimit510(t *testing.T) {
	now := time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	dir := filepath.Join("..", "testdata", "mexc")
	tickerHits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		file := ""
		switch r.URL.Path {
		case "/api/v3/exchangeInfo":
			file = "exchangeInfo.json"
		case "/api/v1/contract/detail":
			file = "contract_detail.json"
		case "/api/v1/contract/ticker":
			tickerHits++
			file = "contract_ticker_510.json"
		default:
			t.Errorf("unrouted request %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		b, err := os.ReadFile(filepath.Join(dir, file))
		if err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK) // the whole point: not a 429
		_, _ = w.Write(b)
	}))
	defer srv.Close()
	c := newMEXC(Options{SpotBase: srv.URL, PerpBase: srv.URL, Now: clock})

	_, err := c.Perps(context.Background())
	var ib *InBandRateLimit
	if !errors.As(err, &ib) {
		t.Fatalf("err = %v, want *InBandRateLimit", err)
	}
	if ib.Code != 510 || ib.Msg != "Requests are too frequent" || ib.Path != "/api/v1/contract/ticker" || ib.Pause != 10*time.Second {
		t.Fatalf("in-band error = %+v", ib)
	}
	if tickerHits != 1 {
		t.Fatalf("ticker hits = %d, want 1 (no retry inside the call)", tickerHits)
	}
	if got := c.RateLimited(); got != 1 {
		t.Fatalf("RateLimited() = %d, want 1", got)
	}
	if got := c.gate.blockedFor(); got != 10*time.Second {
		t.Fatalf("gate blocked for %s, want 10s", got)
	}
	// While paused, a request waits (the gate only delays, never
	// drops): with the clock frozen it must observe ctx cancellation
	// rather than proceed.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := c.gate.wait(ctx, 1); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("wait during pause = %v, want deadline exceeded", err)
	}
	// After the pause the gate opens again.
	now = now.Add(10*time.Second + time.Millisecond)
	if got := c.gate.blockedFor(); got != 0 {
		t.Fatalf("still blocked for %s after the pause", got)
	}
	if err := c.gate.wait(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	// Spot-side bookkeeping: the poller copies RateLimited() into the
	// venue status row, which is what exchange_rate_limited_total reads.
	if c.ID() != screener.VenueMEXC {
		t.Fatal("wrong venue")
	}
}
