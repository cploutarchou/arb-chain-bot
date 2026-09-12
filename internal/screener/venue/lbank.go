package venue

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/screener"
)

// LBank collector (T-075 Tier-4, docs/research/venues/lbank.md): spot
// only — the contract host (lbkperp.lbank.com) is not exercisable from
// this environment and the docs site does not render, so perps are
// DEFERRED (research §3). Endpoint facts live-verified 2026-09-13:
//   - GET /v2/currencyPairs.do → ["btc_usdt", …] (lowercase
//     base_quote); GET /v2/accuracy.do?symbol= → quantityAccuracy,
//     priceAccuracy, minOrderAmount (quote units), minTranQua (base
//     step) — the instrument constraints.
//   - GET /v2/ticker/24hr.do?symbol=all (the parameter IS symbol=all;
//     symbolAll=1 / symbol_all=true / no-arg all refuse with 10001)
//     → [{symbol, ticker {high, vol, low, change, turnover, latest},
//     timestamp (ms)}] — every pair in ONE request, NO bid/ask and no
//     sizes: the 24 h stats identify the pairs, nothing more.
//   - GET /v2/depth.do?symbol=&size=1 → {asks [["price","qty"]…],
//     bids […], timestamp (ms)} PER SYMBOL — the only top-of-book
//     source. Spot() therefore sweeps BooksPerPoll symbols
//     round-robin and carries every other symbol's last-known quote
//     with ITS OWN timestamp (the Coinbase pattern, X10): untouched
//     symbols age out through the normal data-age gates instead of
//     being dropped or fabricated from `latest`.
//   - rate limits: UNVERIFIED (docs unreachable; ccxt's 20 ms poll
//     default is a client convention) → a conservative 8 req/s venue
//     gate, and the per-poll depth budget is bounded by BooksPerPoll.
//   - fees: UNVERIFIED (the support fee article answers 403) → the
//     common base-tier placeholder 0.10 %, Verified=false.
//   - Networks(): no public deposit/withdraw status in the v2 surface
//     → key-gated unknowns.
type lbankCollector struct {
	base  string
	gate  *gate
	c     *client
	inst  *instrumentCache
	rr    *fundingRR // round-robin over pairs for depth.do
	mu    sync.Mutex
	known map[string]screener.Quote
	now   func() time.Time
}

const lbankBase = "https://api.lbkex.com"

func newLBank(opts Options) *lbankCollector {
	now := opts.now()
	perPoll := opts.booksPerPoll()
	c := &lbankCollector{base: lbankBase, now: now, known: map[string]screener.Quote{}}
	if opts.SpotBase != "" {
		c.base = opts.SpotBase
	}
	c.rr = newFundingRR(perPoll)
	c.gate = newGate(8, time.Second, now)
	c.c = newClient(screener.VenueLBank, c.gate)
	c.inst = newInstrumentCache(opts, c.fetchInstruments)
	return c
}

func (c *lbankCollector) ID() screener.Venue { return screener.VenueLBank }
func (c *lbankCollector) Fees() Fees {
	return Fees{SpotTakerBps: bps("0.001"), PerpTakerBps: bps("0.001"), Verified: false}
}
func (c *lbankCollector) RateLimited() int { return int(c.gate.limited.Load()) }

type lbankEnvelope[T any] struct {
	Result    string `json:"result"` // "true" | "false"
	ErrorCode int    `json:"error_code"`
	Msg       string `json:"msg"`
	Data      T      `json:"data"`
}

func (e lbankEnvelope[T]) ok() error {
	if e.Result != "true" || e.ErrorCode != 0 {
		return &lbankError{result: e.Result, code: e.ErrorCode, msg: e.Msg}
	}
	return nil
}

type lbankError struct {
	result string
	code   int
	msg    string
}

func (e *lbankError) Error() string {
	return "lbank: result=" + e.result + " error_code=" + strconv.Itoa(e.code) + " msg=" + e.msg
}

type lbankPair struct {
	Symbol string `json:"symbol"` // btc_usdt
	Ticker struct {
		Turnover num `json:"turnover"`
	} `json:"ticker"`
}

func lbankSplit(symbol string) (base, quote string, ok bool) {
	b, q, found := strings.Cut(symbol, "_")
	return strings.ToUpper(b), strings.ToUpper(q), found && b != "" && q != ""
}

func (c *lbankCollector) fetchInstruments(ctx context.Context) ([]Instrument, error) {
	var pairs lbankEnvelope[[]lbankPair]
	if err := c.c.getJSON(ctx, 1, c.base, "/v2/ticker/24hr.do", url.Values{"symbol": {"all"}}, &pairs); err != nil {
		return nil, err
	}
	if err := pairs.ok(); err != nil {
		return nil, err
	}
	var acc lbankEnvelope[[]struct {
		Symbol           string `json:"symbol"`
		QuantityAccuracy num    `json:"quantityAccuracy"`
		PriceAccuracy    num    `json:"priceAccuracy"`
		MinOrderAmount   num    `json:"minOrderAmount"`
		MinTranQua       num    `json:"minTranQua"`
	}]
	if err := c.c.getJSON(ctx, 1, c.base, "/v2/accuracy.do", nil, &acc); err != nil {
		return nil, err
	}
	if err := acc.ok(); err != nil {
		return nil, err
	}
	type constraints struct{ step, tick, minNotional string }
	bySymbol := make(map[string]constraints, len(acc.Data))
	for _, a := range acc.Data {
		// quantityAccuracy 5 → step 10^-5; priceAccuracy 2 → tick
		// 10^-2. Accuracies are small non-negative counts; anything
		// outside 0..18 leaves the field empty rather than guessing a
		// scale.
		qa, qaOK := pow10(-a.QuantityAccuracy.IntPart())
		pa, paOK := pow10(-a.PriceAccuracy.IntPart())
		if !qaOK || !paOK {
			continue
		}
		bySymbol[a.Symbol] = constraints{
			step:        qa.String(),
			tick:        pa.String(),
			minNotional: a.MinOrderAmount.String(),
		}
	}
	out := make([]Instrument, 0, len(pairs.Data))
	for _, p := range pairs.Data {
		base, quote, ok := lbankSplit(p.Symbol)
		if !ok {
			continue
		}
		in := Instrument{
			Venue: screener.VenueLBank, Kind: KindSpot, Symbol: p.Symbol,
			Base: base, Quote: quote,
			// Every pair in the bulk ticker is a listed, trading market.
			Tradable: true,
		}
		if cs, ok := bySymbol[p.Symbol]; ok {
			in.StepSize, in.TickSize, in.MinNotional = cs.step, cs.tick, cs.minNotional
		}
		out = append(out, in)
	}
	return out, nil
}

func (c *lbankCollector) Instruments(ctx context.Context) ([]Instrument, error) {
	return c.inst.refresh(ctx)
}

type lbankDepth struct {
	Asks      [][2]num `json:"asks"` // [[price, qty]…] best-first
	Bids      [][2]num `json:"bids"`
	Timestamp num      `json:"timestamp"` // ms
}

// Spot refreshes the round-robin window's top of book (one depth.do
// per pair) and returns every pair with a known quote, each at its own
// observation time. A rate-limited answer mid-poll publishes the
// partial set (Coinbase X10): the untouched pairs keep their previous
// quote and age out through the normal data-age gates.
func (c *lbankCollector) Spot(ctx context.Context) ([]screener.Quote, error) {
	inst, err := c.inst.ensure(ctx, KindSpot)
	if err != nil {
		return nil, err
	}
	symbols := make([]string, 0, len(inst))
	for s, in := range inst {
		if in.Tradable {
			symbols = append(symbols, s)
		}
	}
	for _, s := range c.rr.pick(symbols) {
		var depth lbankEnvelope[lbankDepth]
		if err := c.c.getJSON(ctx, 1, c.base, "/v2/depth.do", url.Values{"symbol": {s}, "size": {"1"}}, &depth); err != nil {
			var he *HTTPError
			if errors.As(err, &he) && he.RateLimit() {
				// Publish the partial set (Coinbase X10): the untouched
				// pairs keep their previous quote and age out through
				// the normal data-age gates; the venue gate's
				// Retry-After backoff paces the next sweep and
				// RateLimited() has already counted the answer.
				break
			}
			return nil, err
		}
		if err := depth.ok(); err != nil {
			return nil, err
		}
		d := depth.Data
		if len(d.Asks) == 0 || len(d.Bids) == 0 {
			continue // one-sided book: keep the previous quote if any
		}
		bid, ask := d.Bids[0], d.Asks[0]
		if !bid[0].IsPositive() || !ask[0].IsPositive() {
			continue
		}
		at := c.now()
		if d.Timestamp.IsPositive() {
			at = time.UnixMilli(d.Timestamp.IntPart())
		}
		in := inst[s]
		c.mu.Lock()
		c.known[s] = screener.Quote{
			Venue: screener.VenueLBank, Base: in.Base, Quote: in.Quote,
			Bid: bid[0].Decimal, BidQty: bid[1].Decimal,
			Ask: ask[0].Decimal, AskQty: ask[1].Decimal,
			At: at,
		}
		c.mu.Unlock()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]screener.Quote, 0, len(c.known))
	for s, q := range c.known {
		if in, ok := inst[s]; ok && in.Tradable {
			out = append(out, q)
		}
	}
	return out, nil
}

func (c *lbankCollector) Perps(ctx context.Context) ([]screener.Perp, error) {
	// Deferred (research §3): the contract host is not exercisable from
	// this environment and no verified swap path exists.
	return []screener.Perp{}, nil
}

func (c *lbankCollector) Networks(ctx context.Context) (map[string]screener.NetworkStatus, error) {
	return c.inst.keyGatedNetworks(ctx)
}
