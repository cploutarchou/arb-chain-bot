package binance

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"time"
)

// Binance meters REST calls by request weight per IP per minute
// (docs/research/exchanges.md §Binance: 6000/min; GET /api/v3/depth costs
// 5/25/50/250 for limit ≤100/≤500/≤1000/≤5000). Exceeding it answers 429,
// and continuing to call while over budget escalates to 418 with an IP
// ban of minutes to days. A 46-symbol universe primed at 250 weight per
// snapshot exhausts the budget in under six seconds, so every snapshot
// fetch in this package goes through one restGate that
//
//   - keeps a sliding 60 s window of spent weight under restWeightCeiling
//     (well below the venue's 6000 so WS bookkeeping and jitter fit), and
//   - after a 429/418 refuses to issue any call until the Retry-After
//     deadline has passed (repeated calls during a ban extend it).
//
// The gate only delays; it never drops a request. Callers keep their own
// bounded retry loops.
const restWeightCeiling = 4500

// depthWeight is the documented weight of GET /api/v3/depth for a limit.
func depthWeight(limit int) int {
	switch {
	case limit <= 100:
		return 5
	case limit <= 500:
		return 25
	case limit <= 1000:
		return 50
	default:
		return 250
	}
}

type restGate struct {
	mu          sync.Mutex
	spent       []weightSpend // oldest first
	blockedTill time.Time
	now         func() time.Time
}

type weightSpend struct {
	at time.Time
	w  int
}

func newRESTGate() *restGate { return &restGate{now: time.Now} }

// wait blocks until cost weight fits in the window and no ban is active,
// then records the spend. It returns early only when ctx ends.
func (g *restGate) wait(ctx context.Context, cost int) error {
	for {
		g.mu.Lock()
		now := g.now()
		g.prune(now)
		delay := g.delay(now, cost)
		if delay <= 0 {
			g.spent = append(g.spent, weightSpend{at: now, w: cost})
			g.mu.Unlock()
			return nil
		}
		g.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}
}

// delay returns how long until cost fits; the caller holds mu.
func (g *restGate) delay(now time.Time, cost int) time.Duration {
	if d := g.blockedTill.Sub(now); d > 0 {
		return d
	}
	used := 0
	for _, s := range g.spent {
		used += s.w
	}
	if used+cost <= restWeightCeiling {
		return 0
	}
	// The window frees weight as its oldest entries age out; wait for the
	// oldest one (plus a little slack) rather than polling.
	return g.spent[0].at.Add(time.Minute).Sub(now) + 50*time.Millisecond
}

func (g *restGate) prune(now time.Time) {
	cut := now.Add(-time.Minute)
	i := 0
	for i < len(g.spent) && g.spent[i].at.Before(cut) {
		i++
	}
	g.spent = g.spent[i:]
}

// observe inspects a REST error and, for 429/418, blocks the gate until
// the venue's Retry-After (default one minute when absent or unparsable).
// It returns true when the error was a rate-limit response.
func (g *restGate) observe(err error) bool {
	var he *HTTPError
	if !errors.As(err, &he) || (he.Status != 429 && he.Status != 418) {
		return false
	}
	secs, perr := strconv.Atoi(he.RetryAfter)
	if perr != nil || secs <= 0 {
		secs = 60
	}
	until := g.now().Add(time.Duration(secs)*time.Second + time.Second)
	g.mu.Lock()
	if until.After(g.blockedTill) {
		g.blockedTill = until
	}
	g.mu.Unlock()
	return true
}

// blockedFor reports the remaining ban, zero when none.
func (g *restGate) blockedFor() time.Duration {
	g.mu.Lock()
	defer g.mu.Unlock()
	if d := g.blockedTill.Sub(g.now()); d > 0 {
		return d
	}
	return 0
}
