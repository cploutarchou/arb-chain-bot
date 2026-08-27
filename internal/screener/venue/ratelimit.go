package venue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/screener"
)

// gate is the per-venue request-rate gate, modelled on
// internal/exchange/binance/weight.go's restGate: a sliding window of
// spent cost (request weight for Binance, request count elsewhere)
// under a ceiling, plus a hard block until Retry-After after a
// 429/418/403. It only delays, never drops.
type gate struct {
	mu          sync.Mutex
	limit       int
	window      time.Duration
	spent       []spend
	blockedTill time.Time
	now         func() time.Time
	limited     atomic.Int64
}

type spend struct {
	at time.Time
	w  int
}

func newGate(limit int, window time.Duration, now func() time.Time) *gate {
	return &gate{limit: limit, window: window, now: now}
}

// wait blocks until cost fits in the window and no block is active,
// then records the spend.
func (g *gate) wait(ctx context.Context, cost int) error {
	for {
		g.mu.Lock()
		now := g.now()
		g.prune(now)
		d := g.delay(now, cost)
		if d <= 0 {
			g.spent = append(g.spent, spend{at: now, w: cost})
			g.mu.Unlock()
			return nil
		}
		g.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(d):
		}
	}
}

func (g *gate) delay(now time.Time, cost int) time.Duration {
	if d := g.blockedTill.Sub(now); d > 0 {
		return d
	}
	used := 0
	for _, s := range g.spent {
		used += s.w
	}
	if used+cost <= g.limit {
		return 0
	}
	// Wait until enough of the oldest spends leave the window.
	freed := 0
	for _, s := range g.spent {
		freed += s.w
		if used-freed+cost <= g.limit {
			return s.at.Add(g.window).Sub(now) + 10*time.Millisecond
		}
	}
	return g.window
}

func (g *gate) prune(now time.Time) {
	cut := now.Add(-g.window)
	i := 0
	for i < len(g.spent) && g.spent[i].at.Before(cut) {
		i++
	}
	g.spent = g.spent[i:]
}

// observe inspects an error and, for a rate-limit response (429, 418,
// or 403 — Bybit answers "access too frequent" with 403,
// https://bybit-exchange.github.io/docs/v5/rate-limit accessed
// 2026-08-27), blocks the gate until Retry-After (integer seconds or an
// HTTP-date; default 60 s when absent/unparsable). Returns true when it
// was a rate-limit response.
func (g *gate) observe(err error) bool {
	var he *HTTPError
	if !errors.As(err, &he) || !he.RateLimit() {
		return false
	}
	g.limited.Add(1)
	until := g.now().Add(he.retryAfter(g.now) + time.Second)
	g.mu.Lock()
	if until.After(g.blockedTill) {
		g.blockedTill = until
	}
	g.mu.Unlock()
	return true
}

// blockedFor reports the remaining block, zero when none.
func (g *gate) blockedFor() time.Duration {
	g.mu.Lock()
	defer g.mu.Unlock()
	if d := g.blockedTill.Sub(g.now()); d > 0 {
		return d
	}
	return 0
}

// HTTPError is a non-2xx public REST response.
type HTTPError struct {
	Venue      screener.Venue
	Status     int
	RetryAfter string
	Body       string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("%s: HTTP %d (retry-after %q): %s", e.Venue, e.Status, e.RetryAfter, e.Body)
}

// RateLimit reports whether the status is one a venue uses for
// "too many requests" / ban.
func (e *HTTPError) RateLimit() bool {
	return e.Status == http.StatusTooManyRequests || e.Status == http.StatusTeapot || e.Status == http.StatusForbidden
}

func (e *HTTPError) retryAfter(now func() time.Time) time.Duration {
	if secs, err := strconv.Atoi(e.RetryAfter); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(e.RetryAfter); err == nil {
		if d := t.Sub(now()); d > 0 {
			return d
		}
	}
	if e.Status == http.StatusForbidden {
		// Bybit documents a ≥10 min cool-down for its 403 (see observe).
		return 10 * time.Minute
	}
	return time.Minute
}

// client is the shared unauthenticated GET helper: waits on the gate,
// issues the request, records rate-limit responses on the gate.
type client struct {
	venue screener.Venue
	hc    *http.Client
	gate  *gate
}

func newClient(v screener.Venue, g *gate) *client {
	return &client{venue: v, hc: &http.Client{Timeout: 20 * time.Second}, gate: g}
}

const maxBody = 64 << 20

// getJSON GETs base+path?q, charging cost on the gate, and decodes into
// out. Decoding goes through encoding/json with decimal.Decimal fields,
// which parse the exact JSON number text (quoted or bare) — no float64.
func (c *client) getJSON(ctx context.Context, cost int, base, path string, q url.Values, out any) error {
	body, err := c.get(ctx, cost, base, path, q)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("%s: decode %s: %w", c.venue, path, err)
	}
	return nil
}

func (c *client) get(ctx context.Context, cost int, base, path string, q url.Values) ([]byte, error) {
	if err := c.gate.wait(ctx, cost); err != nil {
		return nil, err
	}
	u := base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", c.venue, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, fmt.Errorf("%s: read %s: %w", c.venue, path, err)
	}
	if resp.StatusCode != http.StatusOK {
		he := &HTTPError{Venue: c.venue, Status: resp.StatusCode, RetryAfter: resp.Header.Get("Retry-After"), Body: truncate(body, 256)}
		c.gate.observe(he)
		return nil, he
	}
	return body, nil
}

func truncate(b []byte, n int) string {
	if len(b) > n {
		return string(b[:n]) + "…"
	}
	return string(b)
}
