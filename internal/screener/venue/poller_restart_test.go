package venue

import (
	"context"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/screener"
)

// wedgedCollector blocks in Spot until its context is cancelled the
// first N times, then answers instantly — a venue whose HTTP call hangs.
type wedgedCollector struct {
	id     screener.Venue
	wedged *atomic.Int32 // remaining calls that block
	calls  atomic.Int32
}

func (w *wedgedCollector) ID() screener.Venue                                { return w.id }
func (w *wedgedCollector) Instruments(context.Context) ([]Instrument, error) { return nil, nil }
func (w *wedgedCollector) Perps(context.Context) ([]screener.Perp, error)    { return nil, nil }
func (w *wedgedCollector) Networks(context.Context) (map[string]screener.NetworkStatus, error) {
	return nil, nil
}
func (w *wedgedCollector) Fees() Fees       { return Fees{} }
func (w *wedgedCollector) RateLimited() int { return 0 }
func (w *wedgedCollector) Spot(ctx context.Context) ([]screener.Quote, error) {
	w.calls.Add(1)
	if w.wedged.Add(-1) >= 0 {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return []screener.Quote{{Venue: w.id, Base: "BTC", Quote: "USDT", At: time.Now()}}, nil
}

// TestPollerRestartStale: a venue whose first poll never returns is
// replaced by RestartStale once it is older than maxAge; the status
// row shows restarts=1 and the replacement loop completes a poll.
func TestPollerRestartStale(t *testing.T) {
	settings := screener.Defaults()
	settings.PollIntervalS = 2
	for id := range settings.Venues {
		vs := settings.Venues[id]
		vs.Enabled = id == screener.VenueBinance
		settings.Venues[id] = vs
	}
	var wedged atomic.Int32
	wedged.Store(1) // first Spot call blocks until cancelled
	var made atomic.Int32
	p := &Poller{Book: screener.NewBook(), Current: func() screener.Settings { return settings },
		NewCollector: func(id screener.Venue, _ Options) (Collector, error) {
			made.Add(1)
			return &wedgedCollector{id: id, wedged: &wedged}, nil
		}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := p.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer p.Stop()
	time.Sleep(30 * time.Millisecond)
	// Not stale yet: nothing restarted.
	if got := p.RestartStale(time.Now().UTC(), time.Hour); len(got) != 0 {
		t.Fatalf("restarted %v before the stale window", got)
	}
	// Pretend 5 × 2 s have passed: the wedged loop is replaced.
	got := p.RestartStale(time.Now().UTC().Add(11*time.Second), 10*time.Second)
	if len(got) != 1 || got[0] != screener.VenueBinance {
		t.Fatalf("restarted = %v, want [binance]", got)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for _, st := range p.Status() {
			if st.ID == screener.VenueBinance && st.Online && st.Polls == 1 && st.Restarts == 1 {
				if made.Load() != 2 {
					t.Fatalf("collectors built = %d, want 2", made.Load())
				}
				if len(p.Book.QuotesFor("BTC", "USDT")) != 1 {
					t.Fatal("replacement loop did not fill the book")
				}
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("replacement loop never polled: %+v", p.Status())
}

// TestAutomationHealsStaleCollectors: the automation tick calls
// RestartStale with 5 × poll_interval_s through the Service seam.
func TestAutomationHealsStaleCollectors(t *testing.T) {
	settings := screener.Defaults()
	settings.PollIntervalS = 2
	for id := range settings.Venues {
		vs := settings.Venues[id]
		vs.Enabled = id == screener.VenueOKX
		settings.Venues[id] = vs
	}
	var wedged atomic.Int32
	wedged.Store(1)
	svc := screener.NewService(screener.NewBook(), screener.NewMemoryStore(), slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	if _, err := svc.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	p := &Poller{Book: svc.Book, Current: func() screener.Settings { return settings },
		NewCollector: func(id screener.Venue, _ Options) (Collector, error) {
			return &wedgedCollector{id: id, wedged: &wedged}, nil
		}}
	svc.Collectors = p
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := svc.StartCollectors(ctx); err != nil {
		t.Fatal(err)
	}
	defer svc.StopCollectors()
	a := screener.NewAutomation(svc)
	a.TickOnce(ctx, time.Now().UTC()) // fresh: no restart
	for _, st := range p.Status() {
		if st.Restarts != 0 {
			t.Fatalf("restarted early: %+v", st)
		}
	}
	// Defaults().PollIntervalS is 5 (the automation reads the SERVICE
	// document, not the poller's test settings): stale after 25 s.
	a.TickOnce(ctx, time.Now().UTC().Add(26*time.Second))
	var okx screener.VenueStatus
	for _, st := range p.Status() {
		if st.ID == screener.VenueOKX {
			okx = st
		}
	}
	if okx.Restarts != 1 {
		t.Fatalf("okx status = %+v, want restarts=1", okx)
	}
}
