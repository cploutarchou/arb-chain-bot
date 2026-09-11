package venue

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/screener"
)

// coinbaseBurstServer serves the products list and per-product books
// from the recorded fixtures, failing product_book calls from the
// failFrom-th call (0 = never) with a 429 — the conformance fixtures
// are all-success and cannot express a mid-poll rate limit.
func coinbaseBurstServer(t *testing.T, failFrom *atomic.Int32) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	dir := filepath.Join("..", "testdata", "coinbase")
	var calls atomic.Int32
	serve := func(w http.ResponseWriter, name string) {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Errorf("fixture %s: %v", name, err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(b)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/brokerage/market/products", func(w http.ResponseWriter, _ *http.Request) {
		serve(w, "products.json")
	})
	mux.HandleFunc("/api/v3/brokerage/market/product_book", func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if f := failFrom.Load(); f > 0 && n >= f {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"rate limited"}`))
			return
		}
		serve(w, "product_book_"+r.URL.Query().Get("product_id")+".json")
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &calls
}

// TestCoinbaseRateLimitPublishesPartialPollAndHalvesBurst (audit X10):
// a 429 mid-poll must not discard the books already refreshed — the
// partial set publishes — and the burst halves for the next poll.
func TestCoinbaseRateLimitPublishesPartialPollAndHalvesBurst(t *testing.T) {
	var failFrom atomic.Int32
	srv, calls := coinbaseBurstServer(t, &failFrom)
	c, err := New(screener.VenueCoinbase, Options{
		SpotBase: srv.URL, PerpBase: srv.URL, BooksPerPoll: 4, Now: time.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	// Clean poll: books land and nothing errors.
	quotes, err := c.Spot(ctx)
	if err != nil || len(quotes) == 0 {
		t.Fatalf("clean poll: %d quotes, err %v", len(quotes), err)
	}
	base := calls.Load()

	// The third product_book of the next poll answers 429.
	failFrom.Store(base + 3)
	quotes, err = c.Spot(ctx)
	if err != nil {
		t.Fatalf("rate-limited poll errored instead of degrading: %v", err)
	}
	if len(quotes) == 0 {
		t.Fatal("rate-limited poll published nothing — the books refreshed before the 429 were discarded")
	}
	if got := calls.Load() - base; got != 3 {
		t.Fatalf("poll with 429 made %d book calls, want 3 (two ok, third limited)", got)
	}

	// The next poll runs at the halved burst: two book calls, all clean.
	failFrom.Store(0)
	before := calls.Load()
	if _, err := c.Spot(ctx); err != nil {
		t.Fatal(err)
	}
	if got := calls.Load() - before; got != 2 {
		t.Fatalf("post-429 poll made %d book calls, want the halved burst of 2", got)
	}
}
