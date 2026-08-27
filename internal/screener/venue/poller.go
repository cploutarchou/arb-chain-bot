package venue

import (
	"context"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/screener"
)

// Poller runs one goroutine per enabled venue at the settings poll
// interval, writing every collector's Spot()/Perps() into the Book and
// keeping per-venue status. The settings snapshot is re-read from
// Current() each tick so poll_interval_s / perps_enabled /
// funding_calls_per_poll are hot; venues.*.enabled is restart-scoped
// (a venue loop starts only in Start), matching screener.FieldTiming.
type Poller struct {
	Book    *screener.Book
	Current func() screener.Settings
	Funding screener.FundingStore // optional
	Log     *slog.Logger
	// NewCollector is injectable for tests (nil → New).
	NewCollector func(screener.Venue, Options) (Collector, error)
	// Opts is passed to every collector (test bases, clock).
	Opts Options

	mu      sync.Mutex
	status  map[screener.Venue]*screener.VenueStatus
	lastFnd map[screener.PerpKey]fundingSeen
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	running bool
}

type fundingSeen struct {
	rate string
	next time.Time
}

// Start launches the venue loops; it returns immediately. Calling it
// while running is a no-op.
func (p *Poller) Start(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.running {
		return nil
	}
	if p.Log == nil {
		p.Log = slog.Default()
	}
	if p.NewCollector == nil {
		p.NewCollector = New
	}
	p.status = map[screener.Venue]*screener.VenueStatus{}
	p.lastFnd = map[screener.PerpKey]fundingSeen{}
	settings := p.Current()
	ctx, p.cancel = context.WithCancel(ctx)
	for _, id := range screener.OrderedVenues {
		vs, ok := settings.Venues[id]
		st := &screener.VenueStatus{ID: id, Enabled: ok && vs.Enabled}
		p.status[id] = st
		if !st.Enabled {
			continue
		}
		opts := p.Opts
		if opts.FundingCallsPerPoll == 0 {
			opts.FundingCallsPerPoll = settings.FundingCallsPerPoll
		}
		c, err := p.NewCollector(id, opts)
		if err != nil {
			st.LastError = err.Error()
			continue
		}
		p.wg.Add(1)
		go p.loop(ctx, c, st)
	}
	p.running = true
	return nil
}

// Stop cancels every loop and waits for them to exit.
func (p *Poller) Stop() {
	p.mu.Lock()
	if !p.running {
		p.mu.Unlock()
		return
	}
	p.cancel()
	p.running = false
	p.mu.Unlock()
	p.wg.Wait()
}

// Running reports whether Start has been called and Stop has not.
func (p *Poller) Running() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.running
}

// Status returns a copy of every venue's status in OrderedVenues order.
func (p *Poller) Status() []screener.VenueStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]screener.VenueStatus, 0, len(p.status))
	for _, id := range screener.OrderedVenues {
		if st, ok := p.status[id]; ok {
			out = append(out, *st)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (p *Poller) loop(ctx context.Context, c Collector, st *screener.VenueStatus) {
	defer p.wg.Done()
	for {
		settings := p.Current()
		p.pollOnce(ctx, c, st, settings)
		interval := time.Duration(settings.PollIntervalS) * time.Second
		if interval < 2*time.Second {
			interval = 2 * time.Second
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

func (p *Poller) pollOnce(ctx context.Context, c Collector, st *screener.VenueStatus, settings screener.Settings) {
	start := time.Now()
	quotes, err := c.Spot(ctx)
	var perps []screener.Perp
	if err == nil {
		if vs := settings.Venues[c.ID()]; vs.PerpsEnabled {
			perps, err = c.Perps(ctx)
		}
	}
	elapsed := time.Since(start)
	if err == nil {
		for _, q := range quotes {
			p.Book.SetQuote(q)
		}
		for _, pp := range perps {
			p.Book.SetPerp(pp)
			p.recordFunding(ctx, pp)
		}
	} else if ctx.Err() == nil {
		p.Log.Warn("screener poll failed", "venue", c.ID(), "error", err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now().UTC()
	st.LastPollAt = &now
	st.PollMS = elapsed.Milliseconds()
	st.RateLimited = c.RateLimited()
	if err != nil {
		st.Online = false
		st.LastError = err.Error()
		return
	}
	st.Online = true
	st.LastError = ""
	st.SpotPairs = len(quotes)
	st.PerpContracts = len(perps)
	st.Polls++
}

// recordFunding appends a funding_history row when a contract's settled
// rate changes: the venue's NextFundingAt advancing past the previously
// seen one means the previously reported rate settled at that time.
func (p *Poller) recordFunding(ctx context.Context, pp screener.Perp) {
	if p.Funding == nil || pp.NextFundingAt.IsZero() {
		return
	}
	key := screener.PerpKey{Venue: pp.Venue, Base: pp.Base}
	rate := pp.FundingRate.String()
	p.mu.Lock()
	prev, seen := p.lastFnd[key]
	p.lastFnd[key] = fundingSeen{rate: rate, next: pp.NextFundingAt}
	p.mu.Unlock()
	if !seen || !pp.NextFundingAt.After(prev.next) {
		return
	}
	if err := p.Funding.UpsertFunding(ctx, pp.Venue, pp.Base, prev.next, prev.rate); err != nil && ctx.Err() == nil {
		p.Log.Warn("funding history upsert failed", "venue", pp.Venue, "base", pp.Base, "error", err)
	}
}
