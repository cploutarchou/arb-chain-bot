package binance

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/exchange"
	"github.com/cploutarchou/arb-chain-bot/internal/orderbook"
)

// Audit P0: OnDelta on the session goroutine raced OnSnapshot in resync
// goroutines. This drives both concurrently (run with -race) and pins
// the single-flight property: overlapping gap handling never spawns a
// second REST fetch for the same market.
func TestSyncerConcurrentDeltaAndSnapshotIsRaceFree(t *testing.T) {
	id := exchange.MarketID{Exchange: ID, Symbol: "BTCUSDT"}
	book := orderbook.New(id, 0)
	s := NewSyncer(book, 0)

	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(1)
	go func() { // delta writer (session goroutine role)
		defer wg.Done()
		u := int64(1)
		for {
			select {
			case <-stop:
				return
			default:
			}
			_, _ = s.OnDelta(orderbook.DepthEvent{
				Market: id, FirstUpdateID: u, FinalUpdateID: u + 1,
				Bids: []orderbook.Level{lvl("100", "1")},
			})
			_ = s.Synced()
			u += 2
		}
	}()
	wg.Add(1)
	go func() { // snapshot splicer (resync goroutine role)
		defer wg.Done()
		for i := 0; i < 200; i++ {
			_ = s.OnSnapshot(orderbook.DepthEvent{
				Market: id, IsSnapshot: true, FinalUpdateID: int64(i * 50),
				Bids: []orderbook.Level{lvl("99", "2")},
			})
		}
	}()
	time.Sleep(50 * time.Millisecond)
	close(stop)
	wg.Wait()
}

func TestResyncSingleFlightPerMarket(t *testing.T) {
	// A REST server slow enough that overlapping resync attempts would
	// overlap; depth responses are valid snapshots.
	var mu sync.Mutex
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
		time.Sleep(80 * time.Millisecond)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"lastUpdateId": 1000,
			"bids":         [][]string{{"100", "1"}},
			"asks":         [][]string{{"101", "1"}},
		})
	}))
	defer srv.Close()

	f := &Feed{
		REST:    NewRESTClient(srv.URL),
		Books:   orderbook.NewSet(),
		Symbols: []exchange.Symbol{"BTCUSDT"},
		Log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	id := exchange.MarketID{Exchange: ID, Symbol: "BTCUSDT"}
	book := orderbook.New(id, 0)
	f.Books.Add(book)
	f.syncers = map[exchange.MarketID]*Syncer{id: NewSyncer(book, 0)}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f.resyncMarket(t.Context(), id)
		}()
	}
	wg.Wait()
	mu.Lock()
	got := calls
	mu.Unlock()
	if got != 1 {
		t.Fatalf("REST snapshot calls = %d, want 1 (single flight)", got)
	}
	if f.Stats.Resyncs.Load() != 1 {
		t.Fatalf("resync counter = %d, want 1", f.Stats.Resyncs.Load())
	}
}

func lvl(p, q string) orderbook.Level {
	return orderbook.Level{Price: d(p), Qty: d(q)}
}

func TestCaptureSnapshotsTapsWithoutSplicing(t *testing.T) {
	var mu sync.Mutex
	var served []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		served = append(served, r.URL.Query().Get("symbol"))
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"lastUpdateId": 4242,
			"bids":         [][]string{{"100", "1"}},
			"asks":         [][]string{{"101", "1"}},
		})
	}))
	defer srv.Close()

	var tapped []exchange.Symbol
	var bodies [][]byte
	f := &Feed{
		REST:    NewRESTClient(srv.URL),
		Books:   orderbook.NewSet(),
		Symbols: []exchange.Symbol{"BTCUSDT", "ETHUSDT"},
		Log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		SnapTap: func(sym exchange.Symbol, body []byte, _ time.Time) {
			tapped = append(tapped, sym)
			bodies = append(bodies, body)
		},
	}
	id := exchange.MarketID{Exchange: ID, Symbol: "BTCUSDT"}
	book := orderbook.New(id, 0)
	f.Books.Add(book)
	f.syncers = map[exchange.MarketID]*Syncer{id: NewSyncer(book, 0)}

	if err := f.CaptureSnapshots(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(tapped) != 2 || tapped[0] != "BTCUSDT" || tapped[1] != "ETHUSDT" {
		t.Fatalf("tapped = %v", tapped)
	}
	if !strings.Contains(string(bodies[0]), `"lastUpdateId":4242`) {
		t.Fatalf("raw body not forwarded: %s", bodies[0])
	}
	mu.Lock()
	defer mu.Unlock()
	if len(served) != 2 {
		t.Fatalf("REST calls = %d, want 2", len(served))
	}
	// The live book is untouched: capture only feeds the recording.
	if f.Stats.Resyncs.Load() != 0 || book.Meta().State == orderbook.StateHealthy {
		t.Fatalf("capture must not splice into the live book (resyncs=%d state=%v)", f.Stats.Resyncs.Load(), book.Meta().State)
	}
}
