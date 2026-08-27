package venue

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/screener"
)

// Bitfinex collector (T-078, docs/research/venues/bitfinex.md). Sources,
// all accessed 2026-08-27 (docs.bitfinex.com pages, .md renderings):
//   - spot GET https://api-pub.bitfinex.com/v2/tickers?symbols=ALL,
//     "Rate Limit: 30 reqs/min"; trading-pair rows (prefix "t"):
//     [SYMBOL, BID, BID_SIZE, ASK, ASK_SIZE, …] where BID_SIZE is the
//     "Sum of the 25 highest bid sizes" and ASK_SIZE the "Sum of the 25
//     lowest ask sizes" — a documented near-top aggregate, NOT a
//     top-of-book size; stored as BidQty/AskQty with that caveat.
//     Funding rows (prefix "f", 18 fields) are skipped. No timestamp
//     (VERIFIED-ABSENT) → At = poll time.
//     https://docs.bitfinex.com/reference/rest-public-tickers
//   - instruments: ONE multi-key GET /v2/conf/{k1},{k2},… call
//     (https://docs.bitfinex.com/reference/rest-public-conf):
//     pub:list:pair:exchange ("valid exchange trading pairs"),
//     pub:list:pair:futures ("valid derivative pairs"),
//     pub:map:currency:undl ("maps derivatives symbols to their
//     underlying currency (e.g. BTCF0 -> BTC)"), pub:map:currency:sym
//     ("map symbols to their API symbols (e.g. DSH -> DASH)"; live also
//     UST -> USDt), pub:info:tx:status ("wallet status for each currency
//     for deposits and withdrawals (1 = active, 0 = maintenance)":
//     [METHOD, DEP_STATUS, WD_STATUS, …]), pub:map:tx:method
//     ("maps currencies to their appropriate method(s)"),
//     pub:list:currency ("all currencies available on the platform").
//     A pair string is BASE:QUOTE when it contains a colon, else 3+3
//     letters (doc examples "tBTCUSD", "tBTCUST"; conf "AAVE:USD");
//     both halves must be in pub:list:currency or the pair is dropped —
//     Bitfinex publishes no explicit base/quote field (VERIFIED-ABSENT).
//     Names are canonicalised through pub:map:currency:sym then
//     uppercased (UST/USTF0 → USDT, DSH → DASH, ALG → ALGO).
//   - perps GET /v2/status/deriv?keys=ALL ("90 reqs/min"): [KEY, MTS, _,
//     DERIV_PRICE, SPOT_PRICE, _, INSURANCE_FUND_BALANCE, _,
//     NEXT_FUNDING_EVT_MTS, NEXT_FUNDING_ACCRUED ("current accrued
//     funding for next 8h period"), NEXT_FUNDING_STEP, _,
//     CURRENT_FUNDING ("funding applied in the current 8h period"), _,
//     _, MARK_PRICE ("price based on the BFX Composite Index"), …] →
//     IntervalH 8 (the "8h period" wording), NextFundingAt =
//     NEXT_FUNDING_EVT_MTS, FundingRate = CURRENT_FUNDING,
//     PredictedFundingRate = NEXT_FUNDING_ACCRUED.
//     https://docs.bitfinex.com/reference/rest-public-derivatives-status
//     Only USDt-settled pairs (quote USTF0) are kept.
//   - rate limits: "between 10 and 90 requests per minute, depending on
//     the … endpoint. If an IP address is rate limited, the IP is
//     blocked for 60 seconds" and the API returns
//     {"error":"ERR_RATE_LIMIT"}.
//     https://docs.bitfinex.com/docs/requirements-and-limitations
//     Gate: 24 req / 60 s (≈2 requests per poll at a 5 s cadence keeps
//     the tickers endpoint under half its documented 30/min).
//   - fees: UNVERIFIED — https://www.bitfinex.com/fees (2026-08-27)
//     lists a promotional "Zero" for spot and derivatives maker+taker;
//     a promo zero is not a durable regular-tier number (nothing is
//     described as guaranteed), so placeholders 20 bps spot / 6.5 bps
//     perp stay flagged unverified → Verified=false.
//
// All numbers are BARE JSON floats, parsed from their literal text via
// decimal (num) — never through float64.
type bitfinexCollector struct {
	base  string
	gate_ *gate
	cl    *client
	inst  *instrumentCache
	now   func() time.Time

	mu       sync.Mutex
	tickers  []bitfinexTicker // last bulk tickers (shared by Spot/Perps)
	tickerAt time.Time
	conf     *bitfinexConf // last conf (symbol maps + tx status)
}

const (
	bitfinexBase = "https://api-pub.bitfinex.com"
	// The exact conf key list, in order — the fixture records this URL.
	bitfinexConfKeys = "pub:list:pair:exchange,pub:list:pair:futures,pub:map:currency:undl,pub:map:currency:sym,pub:info:tx:status,pub:map:tx:method,pub:list:currency"
)

func newBitfinex(opts Options) *bitfinexCollector {
	now := opts.now()
	c := &bitfinexCollector{base: bitfinexBase, now: now}
	if opts.SpotBase != "" {
		c.base = opts.SpotBase
	}
	c.gate_ = newGate(24, time.Minute, now)
	c.cl = newClient(screener.VenueBitfinex, c.gate_)
	c.inst = newInstrumentCache(opts, c.fetchInstruments)
	return c
}

func (c *bitfinexCollector) ID() screener.Venue { return screener.VenueBitfinex }
func (c *bitfinexCollector) Fees() Fees {
	// UNVERIFIED placeholders — docs/research/venues/bitfinex.md §6.
	return Fees{SpotTakerBps: bps("0.002"), PerpTakerBps: bps("0.00065"), Verified: false}
}
func (c *bitfinexCollector) RateLimited() int { return int(c.gate_.limited.Load()) }

// bitfinexConf is the decoded multi-key /v2/conf answer (one array per
// key, in request order).
type bitfinexConf struct {
	ExchangePairs []string
	FuturesPairs  []string
	Undl          map[string]string   // BTCF0 -> BTC
	Sym           map[string]string   // DSH -> DASH, UST -> USDt
	TxStatus      [][]json.RawMessage // [METHOD, DEP, WD, …]
	TxMethod      map[string][]string // METHOD -> currencies
	Currencies    map[string]bool
}

func (c *bitfinexCollector) fetchConf(ctx context.Context) (*bitfinexConf, error) {
	var raw []json.RawMessage
	if err := c.cl.getJSON(ctx, 1, c.base, "/v2/conf/"+bitfinexConfKeys, nil, &raw); err != nil {
		return nil, err
	}
	if len(raw) != 7 {
		return nil, fmt.Errorf("bitfinex: conf returned %d arrays, want 7", len(raw))
	}
	conf := &bitfinexConf{Undl: map[string]string{}, Sym: map[string]string{},
		TxMethod: map[string][]string{}, Currencies: map[string]bool{}}
	if err := json.Unmarshal(raw[0], &conf.ExchangePairs); err != nil {
		return nil, fmt.Errorf("bitfinex: conf pair:exchange: %w", err)
	}
	if err := json.Unmarshal(raw[1], &conf.FuturesPairs); err != nil {
		return nil, fmt.Errorf("bitfinex: conf pair:futures: %w", err)
	}
	var pairs [][2]string
	if err := json.Unmarshal(raw[2], &pairs); err != nil {
		return nil, fmt.Errorf("bitfinex: conf currency:undl: %w", err)
	}
	for _, p := range pairs {
		conf.Undl[p[0]] = p[1]
	}
	pairs = nil
	if err := json.Unmarshal(raw[3], &pairs); err != nil {
		return nil, fmt.Errorf("bitfinex: conf currency:sym: %w", err)
	}
	for _, p := range pairs {
		conf.Sym[p[0]] = p[1]
	}
	if err := json.Unmarshal(raw[4], &conf.TxStatus); err != nil {
		return nil, fmt.Errorf("bitfinex: conf tx:status: %w", err)
	}
	var methods [][]json.RawMessage
	if err := json.Unmarshal(raw[5], &methods); err != nil {
		return nil, fmt.Errorf("bitfinex: conf tx:method: %w", err)
	}
	for _, m := range methods {
		if len(m) != 2 {
			continue
		}
		var name string
		var curs []string
		if err := json.Unmarshal(m[0], &name); err != nil {
			continue
		}
		if err := json.Unmarshal(m[1], &curs); err != nil {
			continue
		}
		conf.TxMethod[name] = curs
	}
	var currencies []string
	if err := json.Unmarshal(raw[6], &currencies); err != nil {
		return nil, fmt.Errorf("bitfinex: conf list:currency: %w", err)
	}
	for _, cur := range currencies {
		conf.Currencies[cur] = true
	}
	c.mu.Lock()
	c.conf = conf
	c.mu.Unlock()
	return conf, nil
}

// canonical maps a Bitfinex API currency name to the cross-venue name:
// pub:map:currency:sym first (UST → USDt, DSH → DASH), then uppercase.
func (conf *bitfinexConf) canonical(name string) string {
	if mapped, ok := conf.Sym[name]; ok && mapped != "" {
		name = mapped
	}
	return strings.ToUpper(name)
}

// splitPair applies the documented pair format (BASE:QUOTE with a colon,
// else 3+3 letters) and validates both halves against pub:list:currency.
func (conf *bitfinexConf) splitPair(pair string) (base, quote string, ok bool) {
	if i := strings.IndexByte(pair, ':'); i >= 0 {
		base, quote = pair[:i], pair[i+1:]
	} else if len(pair) == 6 {
		base, quote = pair[:3], pair[3:]
	} else {
		return "", "", false
	}
	if !conf.Currencies[base] || !conf.Currencies[quote] {
		return "", "", false
	}
	return base, quote, true
}

func (c *bitfinexCollector) fetchInstruments(ctx context.Context) ([]Instrument, error) {
	conf, err := c.fetchConf(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Instrument, 0, len(conf.ExchangePairs)+len(conf.FuturesPairs))
	for _, pair := range conf.ExchangePairs {
		base, quote, ok := conf.splitPair(pair)
		if !ok {
			continue
		}
		// Presence in pub:list:pair:exchange ("valid exchange trading
		// pairs") is the tradable signal; no status field exists.
		out = append(out, Instrument{Venue: screener.VenueBitfinex, Kind: KindSpot, Symbol: "t" + pair,
			Base: conf.canonical(base), Quote: conf.canonical(quote), Tradable: true})
	}
	for _, pair := range conf.FuturesPairs {
		base, quote, ok := conf.splitPair(pair)
		if !ok || quote != "USTF0" { // USDt-settled perpetuals only
			continue
		}
		if undl, ok := conf.Undl[base]; ok && undl != "" {
			base = undl // BTCF0 -> BTC (pub:map:currency:undl)
		}
		out = append(out, Instrument{Venue: screener.VenueBitfinex, Kind: KindPerp, Symbol: "t" + pair,
			Base: conf.canonical(base), Quote: conf.canonical(quote), Tradable: true, IntervalH: 8})
	}
	return out, nil
}

func (c *bitfinexCollector) Instruments(ctx context.Context) ([]Instrument, error) {
	return c.inst.refresh(ctx)
}

// bitfinexTicker is one trading-pair row of /v2/tickers.
type bitfinexTicker struct {
	Symbol  string
	Bid     num
	BidSize num
	Ask     num
	AskSize num
}

// fetchTickers pulls the bulk tickers, caching the rows briefly so the
// Perps() call in the same poll reuses the Spot() response instead of
// spending a second request on the 30 reqs/min endpoint.
func (c *bitfinexCollector) fetchTickers(ctx context.Context) ([]bitfinexTicker, error) {
	c.mu.Lock()
	if c.tickers != nil && c.now().Sub(c.tickerAt) < 3*time.Second {
		rows := c.tickers
		c.mu.Unlock()
		return rows, nil
	}
	c.mu.Unlock()
	var raw [][]json.RawMessage
	if err := c.cl.getJSON(ctx, 1, c.base, "/v2/tickers?symbols=ALL", nil, &raw); err != nil {
		return nil, err
	}
	rows := make([]bitfinexTicker, 0, len(raw))
	for _, r := range raw {
		// Trading pairs have 12 fields; funding rows ("f…") 18.
		if len(r) < 12 {
			continue
		}
		var sym string
		if err := json.Unmarshal(r[0], &sym); err != nil || !strings.HasPrefix(sym, "t") {
			continue
		}
		t := bitfinexTicker{Symbol: sym}
		for i, dst := range map[int]*num{1: &t.Bid, 2: &t.BidSize, 3: &t.Ask, 4: &t.AskSize} {
			if err := dst.UnmarshalJSON(r[i]); err != nil {
				return nil, fmt.Errorf("bitfinex: tickers %s field %d: %w", sym, i, err)
			}
		}
		rows = append(rows, t)
	}
	c.mu.Lock()
	c.tickers, c.tickerAt = rows, c.now()
	c.mu.Unlock()
	return rows, nil
}

func (c *bitfinexCollector) Spot(ctx context.Context) ([]screener.Quote, error) {
	inst, err := c.inst.ensure(ctx, KindSpot)
	if err != nil {
		return nil, err
	}
	rows, err := c.fetchTickers(ctx)
	if err != nil {
		return nil, err
	}
	at := c.now() // VERIFIED-ABSENT: no timestamp on ticker rows
	out := make([]screener.Quote, 0, len(rows))
	for _, t := range rows {
		in, ok := inst[t.Symbol]
		if !ok || !in.Tradable || !t.Bid.IsPositive() || !t.Ask.IsPositive() {
			continue
		}
		out = append(out, screener.Quote{Venue: screener.VenueBitfinex, Base: in.Base, Quote: in.Quote,
			// BID_SIZE/ASK_SIZE are the documented sum of the 25 best
			// levels (see package comment), the venue's only published
			// size figure.
			Bid: t.Bid.Decimal, BidQty: t.BidSize.Decimal, Ask: t.Ask.Decimal, AskQty: t.AskSize.Decimal, At: at})
	}
	return out, nil
}

func (c *bitfinexCollector) Perps(ctx context.Context) ([]screener.Perp, error) {
	inst, err := c.inst.ensure(ctx, KindPerp)
	if err != nil {
		return nil, err
	}
	rows, err := c.fetchTickers(ctx)
	if err != nil {
		return nil, err
	}
	tickBy := make(map[string]bitfinexTicker, len(rows))
	for _, t := range rows {
		tickBy[t.Symbol] = t
	}
	var raw [][]json.RawMessage
	if err := c.cl.getJSON(ctx, 1, c.base, "/v2/status/deriv?keys=ALL", nil, &raw); err != nil {
		return nil, err
	}
	recv := c.now()
	out := make([]screener.Perp, 0, len(raw))
	for _, r := range raw {
		if len(r) < 16 {
			continue
		}
		var key string
		if err := json.Unmarshal(r[0], &key); err != nil {
			continue
		}
		in, ok := inst[key]
		if !ok || !in.Tradable {
			continue
		}
		var mts, nextMTS, accrued, current, mark num
		for i, dst := range map[int]*num{1: &mts, 8: &nextMTS, 9: &accrued, 12: &current, 15: &mark} {
			if err := dst.UnmarshalJSON(r[i]); err != nil {
				return nil, fmt.Errorf("bitfinex: status/deriv %s field %d: %w", key, i, err)
			}
		}
		if !mark.IsPositive() {
			continue
		}
		at := recv
		if mts.IsPositive() {
			at = time.UnixMilli(mts.IntPart())
		}
		var next time.Time
		if nextMTS.IsPositive() {
			next = time.UnixMilli(nextMTS.IntPart())
		}
		t := tickBy[key]
		out = append(out, screener.Perp{Venue: screener.VenueBitfinex, Base: in.Base, Quote: in.Quote,
			Mark: mark.Decimal, Bid: t.Bid.Decimal, Ask: t.Ask.Decimal,
			FundingRate: current.Decimal, PredictedFundingRate: accrued.Decimal,
			IntervalH: 8, NextFundingAt: next, At: at})
	}
	return out, nil
}

// Networks: pub:info:tx:status is per deposit/withdraw METHOD;
// pub:map:tx:method maps a method to its currencies. A currency is open
// when at least one of its methods has DEP_STATUS=1 AND WD_STATUS=1
// (1 = active, 0 = maintenance), closed when it has methods but none
// fully active, unknown when no method lists it.
func (c *bitfinexCollector) Networks(ctx context.Context) (map[string]screener.NetworkStatus, error) {
	c.mu.Lock()
	conf := c.conf
	c.mu.Unlock()
	if conf == nil {
		var err error
		if conf, err = c.fetchConf(ctx); err != nil {
			return nil, err
		}
	}
	type sides struct{ open, seen bool }
	byCurrency := map[string]*sides{}
	for _, row := range conf.TxStatus {
		if len(row) < 3 {
			continue
		}
		var method string
		var dep, wd num
		if err := json.Unmarshal(row[0], &method); err != nil {
			continue
		}
		if dep.UnmarshalJSON(row[1]) != nil || wd.UnmarshalJSON(row[2]) != nil {
			continue
		}
		for _, cur := range conf.TxMethod[method] {
			name := conf.canonical(cur)
			s := byCurrency[name]
			if s == nil {
				s = &sides{}
				byCurrency[name] = s
			}
			s.seen = true
			if dep.IntPart() == 1 && wd.IntPart() == 1 {
				s.open = true
			}
		}
	}
	out := make(map[string]screener.NetworkStatus, len(conf.Currencies))
	for cur := range conf.Currencies {
		name := conf.canonical(cur)
		switch s := byCurrency[name]; {
		case s == nil || !s.seen:
			out[name] = screener.UnknownNetworkStatus("no deposit/withdraw method published")
		case s.open:
			out[name] = screener.NetworkStatus{Status: screener.NetworkOpen}
		default:
			out[name] = screener.NetworkStatus{Status: screener.NetworkClosed, Reason: "all methods in maintenance"}
		}
	}
	return out, nil
}
