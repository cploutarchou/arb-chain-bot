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
// funding_calls_per_poll / perp_quote_preference are hot;
// venues.*.enabled is restart-scoped (a venue loop starts only in
// Start), matching screener.FieldTiming.
//
// One perp contract per (venue, base): a venue that lists several
// perpetuals on one base (Binance USDⓈ-M: USDT- and USDC-margined) has
// them reduced to the contract settings.perp_quote_preference ranks
// first BEFORE anything reaches the Book or funding history, because
// funding_history is keyed (venue, base, at) and the carry models read
// it by (venue, base); the discarded contracts are counted in
// VenueStatus.PerpsDropped and logged once each.
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
	// dropLogged remembers which discarded contracts have been logged so
	// the same contract is not reported on every poll.
	dropLogged map[screener.PerpKey]bool
	loops      map[screener.Venue]*venueLoop
	ctx        context.Context // the Start context; venue loops derive from it
	cancel     context.CancelFunc
	wg         sync.WaitGroup
	running    bool
}

// venueLoop is one venue's goroutine handle: cancelling it (self-heal)
// unblocks any in-flight HTTP call through its context; done closes
// when the goroutine has exited. startedAt bounds staleness for a loop
// that has never completed a poll.
type venueLoop struct {
	cancel    context.CancelFunc
	done      chan struct{}
	startedAt time.Time
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
	p.dropLogged = map[screener.PerpKey]bool{}
	p.loops = map[screener.Venue]*venueLoop{}
	settings := p.Current()
	p.ctx, p.cancel = context.WithCancel(ctx)
	for _, id := range screener.OrderedVenues {
		vs, ok := settings.Venues[id]
		st := &screener.VenueStatus{ID: id, Enabled: ok && vs.Enabled}
		p.status[id] = st
		if !st.Enabled {
			continue
		}
		p.startLoopLocked(id, st, settings, time.Now().UTC())
	}
	p.running = true
	// X7: the book is overwrite-only, so a delisted pair or an offline
	// venue would keep its last quote forever. One eviction sweep per
	// interval, at 3 × interval, drops exactly what no live venue can
	// still republish; it rides the poller's context and wait group so
	// Stop shuts it down with everything else.
	p.wg.Add(1)
	go p.evictLoop(p.ctx)
	return nil
}

// evictFactor is how many poll intervals a quote may outlive its last
// observation before the sweep removes it (audit X7).
const evictFactor = 3

func (p *Poller) evictLoop(ctx context.Context) {
	defer p.wg.Done()
	for {
		settings := p.Current()
		interval := time.Duration(settings.PollIntervalS) * time.Second
		if interval < 2*time.Second {
			interval = 2 * time.Second
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
		quotes, perps := p.Book.EvictOlderThan(time.Now().UTC().Add(-evictFactor * interval))
		if quotes+perps > 0 {
			p.Log.Warn("screener book evicted stale quotes", "quotes", quotes, "perps", perps, "older_than", (evictFactor * interval).String())
		}
	}
}

// startLoopLocked builds a fresh collector for id and launches its
// goroutine. Requires p.mu.
func (p *Poller) startLoopLocked(id screener.Venue, st *screener.VenueStatus, settings screener.Settings, now time.Time) {
	opts := p.Opts
	if opts.FundingCallsPerPoll == 0 {
		opts.FundingCallsPerPoll = settings.FundingCallsPerPoll
	}
	c, err := p.NewCollector(id, opts)
	if err != nil {
		st.LastError = err.Error()
		return
	}
	ctx, cancel := context.WithCancel(p.ctx)
	vl := &venueLoop{cancel: cancel, done: make(chan struct{}), startedAt: now}
	p.loops[id] = vl
	p.wg.Add(1)
	go func() {
		defer close(vl.done)
		p.loop(ctx, c, st)
	}()
}

// RestartStale implements screener.StaleRestarter: every enabled venue
// whose last completed poll (or loop start) is older than maxAge gets
// its goroutine cancelled and replaced with a fresh collector; the
// venue's Restarts counter increments and LastError says why. The old
// goroutine is not waited for synchronously (a wedged HTTP call
// returns on cancel, but the caller — the automation tick — must not
// block on it); it exits on its own and the WaitGroup in Stop still
// covers it.
func (p *Poller) RestartStale(now time.Time, maxAge time.Duration) []screener.Venue {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.running || maxAge <= 0 {
		return nil
	}
	settings := p.Current()
	var restarted []screener.Venue
	for _, id := range screener.OrderedVenues {
		st, ok := p.status[id]
		if !ok || !st.Enabled {
			continue
		}
		vl, ok := p.loops[id]
		if !ok {
			continue
		}
		last := vl.startedAt
		if st.LastPollAt != nil && st.LastPollAt.After(last) {
			last = *st.LastPollAt
		}
		if now.Sub(last) <= maxAge {
			continue
		}
		vl.cancel()
		st.Restarts++
		st.Online = false
		st.LastError = "restarted: no completed poll for " + now.Sub(last).Truncate(time.Second).String()
		p.startLoopLocked(id, st, settings, now)
		restarted = append(restarted, id)
	}
	return restarted
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

var _ screener.StaleRestarter = (*Poller)(nil)

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
	var perps, dropped []screener.Perp
	if err == nil {
		if vs := settings.Venues[c.ID()]; vs.PerpsEnabled {
			perps, err = c.Perps(ctx)
		}
	}
	elapsed := time.Since(start)
	if err == nil {
		perps, dropped = selectPerpContracts(perps, settings.EffectivePerpQuotePreference())
		for _, q := range quotes {
			p.Book.SetQuote(q)
		}
		for _, pp := range perps {
			p.Book.SetPerp(pp)
			p.recordFunding(ctx, c, pp)
		}
		p.logDropped(dropped, perps)
	} else if ctx.Err() == nil {
		p.Log.Warn("screener poll failed", "venue", c.ID(), "error", err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if ctx.Err() != nil {
		// Cancelled mid-poll (Stop, or replaced by RestartStale): the
		// replacement loop owns the status row from here on.
		return
	}
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
	st.PerpsDropped = len(dropped)
	st.Polls++
}

// selectPerpContracts keeps one contract per (venue, base): the one
// whose quote/margin asset ranks first in prefer. An asset absent from
// prefer ranks after every listed one, and equal ranks fall back to the
// quote string, so the choice never depends on the order the venue
// listed its symbols (the order-dependent overwrite this replaces kept
// AAVEUSDC over AAVEUSDT because the venue listed it later). A base
// with a single contract is kept whatever its quote — nothing that was
// tracked before is dropped, only the colliding duplicates. kept keeps
// the input order; dropped is what the caller counts and logs.
func selectPerpContracts(perps []screener.Perp, prefer []string) (kept, dropped []screener.Perp) {
	rank := func(quote string) int {
		for i, q := range prefer {
			if q == quote {
				return i
			}
		}
		return len(prefer)
	}
	type contractBase struct {
		venue screener.Venue
		base  string
	}
	winner := map[contractBase]int{} // index into perps
	for i, pp := range perps {
		k := contractBase{pp.Venue, pp.Base}
		j, seen := winner[k]
		if !seen {
			winner[k] = i
			continue
		}
		ri, rj := rank(pp.Quote), rank(perps[j].Quote)
		if ri < rj || (ri == rj && pp.Quote < perps[j].Quote) {
			winner[k] = i
		}
	}
	if len(winner) == len(perps) {
		return perps, nil // no base listed twice: the usual case
	}
	isWinner := make(map[int]bool, len(winner))
	for _, i := range winner {
		isWinner[i] = true
	}
	kept = make([]screener.Perp, 0, len(winner))
	for i, pp := range perps {
		if isWinner[i] {
			kept = append(kept, pp)
		} else {
			dropped = append(dropped, pp)
		}
	}
	return kept, dropped
}

// logDropped reports each discarded contract ONCE (not every poll) with
// the contract that is tracked in its place, so the status counter has
// a log line behind it without a line per poll per contract.
func (p *Poller) logDropped(dropped, kept []screener.Perp) {
	if len(dropped) == 0 {
		return
	}
	keptQuote := make(map[string]string, len(kept))
	for _, pp := range kept {
		keptQuote[pp.Base] = pp.Quote
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.dropLogged == nil {
		p.dropLogged = map[screener.PerpKey]bool{}
	}
	for _, pp := range dropped {
		k := screener.PerpKey{Venue: pp.Venue, Base: pp.Base, Quote: pp.Quote}
		if p.dropLogged[k] {
			continue
		}
		p.dropLogged[k] = true
		p.Log.Info("screener perp contract dropped: one contract per (venue, base)",
			"venue", pp.Venue, "base", pp.Base, "quote", pp.Quote, "kept_quote", keptQuote[pp.Base], "reason", "perp_quote_preference")
	}
}

// recordFunding appends a funding_history row when a contract's settled
// rate changes: the venue's NextFundingAt advancing past the previously
// seen one means a rate settled at that time. WHICH rate depends on the
// collector's bulk-field semantics (audit X4): for accruing-rate venues
// it is the previously seen one; for venues whose field reports the
// previous period's settled rate it is the newly observed one. The
// last-seen state is per contract (venue, base, quote) so a second
// contract on the same base can never advance — or be attributed —
// another contract's settlement; the store itself is (venue, base)
// keyed, which is why pollOnce admits one contract per base.
func (p *Poller) recordFunding(ctx context.Context, c Collector, pp screener.Perp) {
	if p.Funding == nil || pp.NextFundingAt.IsZero() {
		return
	}
	key := screener.PerpKey{Venue: pp.Venue, Base: pp.Base, Quote: pp.Quote}
	rate := pp.FundingRate.String()
	p.mu.Lock()
	prev, seen := p.lastFnd[key]
	p.lastFnd[key] = fundingSeen{rate: rate, next: pp.NextFundingAt}
	p.mu.Unlock()
	if !seen || !pp.NextFundingAt.After(prev.next) {
		return
	}
	settled := prev.rate
	if fp, ok := c.(PreviousPeriodFundingReporter); ok && fp.FundingIsPreviousPeriod() {
		settled = rate
	}
	if err := p.Funding.UpsertFunding(ctx, pp.Venue, pp.Base, prev.next, settled); err != nil && ctx.Err() == nil {
		p.Log.Warn("funding history upsert failed", "venue", pp.Venue, "base", pp.Base, "error", err)
	}
}
