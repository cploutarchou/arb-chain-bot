package venue

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/screener"
)

// inBandCounter counts, per venue, the in-band rate-limit answers
// (*InBandRateLimit — a "too many requests" body inside an HTTP 200,
// MEXC code 510 style) that reach the Poller. The gate counts those in
// the SAME counter as HTTP 429/418/403 (Collector.RateLimited), so the
// soak splits the two by observing the error type here and subtracting.
// Keyed by venue, not by collector instance, so a RestartStale
// replacement keeps accumulating into the same row.
type inBandCounter struct {
	mu sync.Mutex
	n  map[screener.Venue]int
}

func (c *inBandCounter) note(v screener.Venue, err error) {
	var ib *InBandRateLimit
	if err == nil || !errors.As(err, &ib) {
		return
	}
	c.mu.Lock()
	c.n[v]++
	c.mu.Unlock()
}

func (c *inBandCounter) get(v screener.Venue) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n[v]
}

// countingCollector is a pass-through wrapper that only inspects the
// errors Spot/Perps return; it changes no request behaviour.
type countingCollector struct {
	Collector
	counter *inBandCounter
}

func (w countingCollector) Spot(ctx context.Context) ([]screener.Quote, error) {
	q, err := w.Collector.Spot(ctx)
	w.counter.note(w.ID(), err)
	return q, err
}

func (w countingCollector) Perps(ctx context.Context) ([]screener.Perp, error) {
	p, err := w.Collector.Perps(ctx)
	w.counter.note(w.ID(), err)
	return p, err
}

// TestSoakLive polls EVERY known venue's REAL public endpoints in-process
// (Tier-2 and Tier-3 venues are force-enabled here — the soak is the gate
// for enabling them by default, T-075/T-078) for SCREENER_SOAK_MINUTES
// (default 5) and prints, per venue: completed polls, the min and last
// spot/perp counts (a venue must serve a non-trivial book THROUGHOUT, not
// just at the end), poll latency, HTTP 429/418/403 hits, in-band
// rate-limit hits, failed polls and the last error.
// Guarded by SCREENER_SOAK=1 so CI never touches the network.
func TestSoakLive(t *testing.T) {
	if os.Getenv("SCREENER_SOAK") != "1" {
		t.Skip("set SCREENER_SOAK=1 to run the live soak")
	}
	minutes := 5
	if v, err := strconv.Atoi(os.Getenv("SCREENER_SOAK_MINUTES")); err == nil && v > 0 {
		minutes = v
	}
	settings := screener.Defaults()
	for id, vs := range settings.Venues {
		vs.Enabled = true
		settings.Venues[id] = vs
	}
	book := screener.NewBook()
	funding := screener.NewMemoryFundingStore()
	// sample is ONE completed poll attempt (success or failure), keyed
	// off VenueStatus.LastPollAt advancing — never a repeat of an
	// unchanged status row, which would inflate counts and latencies.
	type sample struct {
		ms    int64
		err   string
		spot  int
		perps int
	}
	counter := &inBandCounter{n: map[screener.Venue]int{}}
	p := &Poller{Book: book, Funding: funding, Current: func() screener.Settings { return settings },
		NewCollector: func(id screener.Venue, opts Options) (Collector, error) {
			c, err := New(id, opts)
			if err != nil {
				return nil, err
			}
			return countingCollector{Collector: c, counter: counter}, nil
		}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(minutes)*time.Minute)
	defer cancel()
	startedAt := time.Now()
	if err := p.Start(ctx); err != nil {
		t.Fatal(err)
	}
	samples := map[screener.Venue][]sample{}
	seenAt := map[screener.Venue]time.Time{}
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for ctx.Err() == nil {
		select {
		case <-ctx.Done():
		case <-tick.C:
			for _, st := range p.Status() {
				if st.LastPollAt == nil || !st.LastPollAt.After(seenAt[st.ID]) {
					continue
				}
				seenAt[st.ID] = *st.LastPollAt
				samples[st.ID] = append(samples[st.ID], sample{ms: st.PollMS, err: st.LastError, spot: st.SpotPairs, perps: st.PerpContracts})
			}
		}
	}
	p.Stop()
	// Authoritative end-of-run counters (the status map survives Stop).
	final := map[screener.Venue]screener.VenueStatus{}
	for _, st := range p.Status() {
		final[st.ID] = st
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\nSOAK %d min (wall %s), poll_interval_s=%d, funding_calls_per_poll=%d\n",
		minutes, time.Since(startedAt).Truncate(time.Second), settings.PollIntervalS, settings.FundingCallsPerPoll)
	fmt.Fprintf(&b, "polls = completed successful polls (venue status); attempts includes failed polls.\n")
	fmt.Fprintf(&b, "spot/perps = min..last across successful polls. 429s = HTTP 429/418/403; inband = in-band rate limit (MEXC 510 style); errs = failed polls.\n")
	fmt.Fprintf(&b, "%-9s %6s %6s %11s %11s %8s %8s %8s %6s %7s %6s  %s\n",
		"venue", "polls", "attpt", "spot", "perps", "min_ms", "avg_ms", "max_ms", "429s", "inband", "errs", "last_error")
	for _, id := range screener.OrderedVenues {
		ss := samples[id]
		var mn, mx, sum int64
		errs := 0
		last := ""
		minSpot, lastSpot, minPerps, lastPerps := -1, -1, -1, -1
		for i, s := range ss {
			if i == 0 || s.ms < mn {
				mn = s.ms
			}
			if s.ms > mx {
				mx = s.ms
			}
			sum += s.ms
			if s.err != "" {
				errs++
				last = s.err
				continue
			}
			lastSpot, lastPerps = s.spot, s.perps
			if minSpot < 0 || s.spot < minSpot {
				minSpot = s.spot
			}
			if minPerps < 0 || s.perps < minPerps {
				minPerps = s.perps
			}
		}
		avg := int64(0)
		if len(ss) > 0 {
			avg = sum / int64(len(ss))
		}
		st := final[id]
		inBand := counter.get(id)
		http429 := st.RateLimited - inBand
		if st.LastError != "" {
			last = st.LastError
		}
		fmt.Fprintf(&b, "%-9s %6d %6d %11s %11s %8d %8d %8d %6d %7d %6d  %s\n", id, st.Polls, len(ss),
			rng(minSpot, lastSpot), rng(minPerps, lastPerps), mn, avg, mx, http429, inBand, errs, last)
	}
	fmt.Fprintf(&b, "book: pairs=%d perps=%d\n", len(book.Pairs()), len(book.Perps()))
	series := 0
	for _, pp := range book.Perps() {
		s, _ := funding.ListFunding(context.Background(), pp.Base, []screener.Venue{pp.Venue}, time.Time{})
		for _, x := range s {
			series += len(x.Points)
		}
	}
	fmt.Fprintf(&b, "funding history rows recorded: %d\n", series)
	t.Log(b.String())
	if path := os.Getenv("SCREENER_SOAK_OUT"); path != "" {
		_ = os.WriteFile(path, []byte(b.String()), 0o644)
	}
}

// rng renders "min..last", or "-" when the venue never completed a poll.
func rng(min, last int) string {
	if min < 0 {
		return "-"
	}
	return fmt.Sprintf("%d..%d", min, last)
}

// fakeInBandCollector returns an *InBandRateLimit from Perps so the
// soak's in-band accounting is proved by test, not only by absence of
// hits in a live run.
type fakeInBandCollector struct {
	Collector
	err error
}

func (f fakeInBandCollector) ID() screener.Venue { return screener.VenueMEXC }
func (f fakeInBandCollector) Spot(context.Context) ([]screener.Quote, error) {
	return nil, nil
}
func (f fakeInBandCollector) Perps(context.Context) ([]screener.Perp, error) {
	return nil, f.err
}

func TestInBandCounterCountsOnlyInBandErrors(t *testing.T) {
	counter := &inBandCounter{n: map[screener.Venue]int{}}
	ib := &InBandRateLimit{Venue: screener.VenueMEXC, Path: "/api/v1/contract/ticker", Code: 510,
		Msg: "Requests are too frequent", Pause: 10 * time.Second}
	w := countingCollector{Collector: fakeInBandCollector{err: fmt.Errorf("wrapped: %w", ib)}, counter: counter}
	if _, err := w.Perps(context.Background()); err == nil {
		t.Fatal("want the in-band error back")
	}
	if got := counter.get(screener.VenueMEXC); got != 1 {
		t.Fatalf("in-band count = %d, want 1", got)
	}
	// A plain error (HTX's "request limit" envelope, say) must NOT count.
	other := countingCollector{Collector: fakeInBandCollector{err: errors.New("htx: batch_merged: invalid-parameter: request limit")}, counter: counter}
	if _, err := other.Perps(context.Background()); err == nil {
		t.Fatal("want the plain error back")
	}
	if got := counter.get(screener.VenueMEXC); got != 1 {
		t.Fatalf("in-band count = %d after a plain error, want 1", got)
	}
	if _, err := w.Spot(context.Background()); err != nil {
		t.Fatalf("Spot passthrough: %v", err)
	}
}
