package venue

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/screener"
)

// TestSoakLive polls EVERY known venue's REAL public endpoints in-process
// (Tier-2 venues are force-enabled here — the soak is the gate for
// enabling them by default, T-075) for SCREENER_SOAK_MINUTES (default 5)
// and prints per-venue pairs, poll latency, rate-limit hits and errors.
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
	type sample struct {
		ms    int64
		err   string
		spot  int
		perps int
		rl    int
	}
	p := &Poller{Book: book, Funding: funding, Current: func() screener.Settings { return settings }}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(minutes)*time.Minute)
	defer cancel()
	if err := p.Start(ctx); err != nil {
		t.Fatal(err)
	}
	samples := map[screener.Venue][]sample{}
	seenPolls := map[screener.Venue]int64{}
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for ctx.Err() == nil {
		select {
		case <-ctx.Done():
		case <-tick.C:
			for _, st := range p.Status() {
				if st.LastPollAt == nil || st.Polls == seenPolls[st.ID] && st.LastError == "" {
					continue
				}
				seenPolls[st.ID] = st.Polls
				samples[st.ID] = append(samples[st.ID], sample{ms: st.PollMS, err: st.LastError, spot: st.SpotPairs, perps: st.PerpContracts, rl: st.RateLimited})
			}
		}
	}
	p.Stop()
	var b strings.Builder
	fmt.Fprintf(&b, "\nSOAK %d min, poll_interval_s=%d, funding_calls_per_poll=%d\n", minutes, settings.PollIntervalS, settings.FundingCallsPerPoll)
	fmt.Fprintf(&b, "%-8s %6s %6s %6s %8s %8s %8s %6s %6s  %s\n", "venue", "polls", "spot", "perps", "min_ms", "avg_ms", "max_ms", "429s", "errs", "last_error")
	for _, id := range screener.OrderedVenues {
		ss := samples[id]
		var mn, mx, sum int64
		errs := 0
		last := ""
		spot, perps, rl := 0, 0, 0
		seen := map[string]bool{}
		for i, s := range ss {
			if i == 0 || s.ms < mn {
				mn = s.ms
			}
			if s.ms > mx {
				mx = s.ms
			}
			sum += s.ms
			if s.err != "" {
				if !seen[s.err] {
					errs++
					seen[s.err] = true
				}
				last = s.err
			} else {
				spot, perps = s.spot, s.perps
			}
			rl = s.rl
		}
		avg := int64(0)
		if len(ss) > 0 {
			avg = sum / int64(len(ss))
		}
		fmt.Fprintf(&b, "%-8s %6d %6d %6d %8d %8d %8d %6d %6d  %s\n", id, len(ss), spot, perps, mn, avg, mx, rl, errs, last)
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
