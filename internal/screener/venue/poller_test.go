package venue

import (
	"context"
	"testing"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/screener"
)

// TestPollerFillsBookAndStatus drives the poller over the fixture servers
// for two venues and checks the book, the status rows and funding
// history bookkeeping.
func TestPollerFillsBookAndStatus(t *testing.T) {
	servers := map[screener.Venue]*fixtureServer{}
	for _, id := range []screener.Venue{screener.VenueBinance, screener.VenueGate} {
		servers[id] = newFixtureServer(t, id)
	}
	settings := screener.Defaults()
	settings.PollIntervalS = 2
	for id := range settings.Venues {
		vs := settings.Venues[id]
		vs.Enabled = servers[id] != nil
		settings.Venues[id] = vs
	}
	book := screener.NewBook()
	funding := screener.NewMemoryFundingStore()
	p := &Poller{Book: book, Funding: funding, Current: func() screener.Settings { return settings },
		NewCollector: func(id screener.Venue, opts Options) (Collector, error) {
			fs := servers[id]
			opts.SpotBase, opts.PerpBase = fs.URL, fs.URL
			return New(id, opts)
		}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := p.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := p.Start(ctx); err != nil { // idempotent
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		st := p.Status()
		online := 0
		for _, s := range st {
			if s.Online {
				online++
			}
		}
		if online == 2 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	st := p.Status()
	if len(st) != len(screener.OrderedVenues) {
		t.Fatalf("status rows = %d", len(st))
	}
	for _, s := range st {
		switch s.ID {
		case screener.VenueBinance, screener.VenueGate:
			if !s.Online || s.SpotPairs == 0 || s.PerpContracts == 0 || s.LastPollAt == nil || s.Polls == 0 {
				t.Fatalf("%s status = %+v", s.ID, s)
			}
		default:
			if s.Enabled || s.Online {
				t.Fatalf("%s should be disabled: %+v", s.ID, s)
			}
		}
	}
	if len(book.QuotesFor("BTC", "USDT")) != 2 {
		t.Fatalf("BTC/USDT quotes = %v", book.QuotesFor("BTC", "USDT"))
	}
	if _, ok := book.PerpFor(screener.VenueGate, "BTC", "USDT"); !ok {
		t.Fatal("gate BTC perp missing from book")
	}
	// Funding history: a NextFundingAt advance records the previous rate.
	first := screener.Perp{Venue: screener.VenueBinance, Base: "BTC", FundingRate: dec("0.0001"), NextFundingAt: time.Unix(1000, 0)}
	p.recordFunding(ctx, nil, first)
	p.recordFunding(ctx, nil, first) // unchanged → nothing
	series, _ := funding.ListFunding(ctx, "BTC", []screener.Venue{screener.VenueBinance}, time.Time{})
	if len(series) != 0 && len(series[0].Points) != 0 {
		t.Fatalf("premature funding row: %+v", series)
	}
	p.recordFunding(ctx, nil, screener.Perp{Venue: screener.VenueBinance, Base: "BTC", FundingRate: dec("0.0002"), NextFundingAt: time.Unix(29800, 0)})
	series, _ = funding.ListFunding(ctx, "BTC", []screener.Venue{screener.VenueBinance}, time.Time{})
	if len(series) != 1 || len(series[0].Points) != 1 || series[0].Points[0].Rate != "0.0001" || !series[0].Points[0].At.Equal(time.Unix(1000, 0)) {
		t.Fatalf("funding series = %+v", series)
	}
	p.Stop()
	p.Stop()
	if p.Running() {
		t.Fatal("still running after Stop")
	}
}
