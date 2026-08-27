package venue

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/screener"
)

// Crypto.com Exchange collector (T-078, docs/research/venues/cryptocom.md).
// Sources, all accessed 2026-08-27 (each page's own .md rendering under
// https://exchange-developer.crypto.com/exchange/v1/docs/api/rest/):
//   - GET https://api.crypto.com/exchange/v1/public/get-instruments:
//     result.data[] {symbol, inst_type (CCY_PAIR|PERPETUAL_SWAP|FUTURE),
//     base_ccy, quote_ccy, price_tick_size, qty_tick_size, tradable,
//     contract_size} — public-get-instruments.md
//   - GET /public/get-tickers (instrument_name omitted → all): data[]
//     {i, b "current best bid price, null if there aren't any bids",
//     k "current best ask price", a, t (ms)}; NO size fields exist
//     (VERIFIED-ABSENT) → quotes carry LiquidityUnknown, sizes zero —
//     public-get-tickers.md
//   - GET /public/get-valuations?instrument_name&valuation_type&count,
//     ONE instrument per call: mark_price ("per minute data of mark
//     price"), funding_hist ("hourly data of the funding rate settled
//     in past hourly settlement" → IntervalH 1, next settlement at the
//     top of the hour), estimated_funding_rate ("estimated funding rate
//     for the next interval") — public-get-valuations.md. The collector
//     round-robins FundingCallsPerPoll contracts per Perps() poll
//     (3 calls each) and carries last-known values; a contract not yet
//     visited is omitted until its first visit.
//   - currency networks: private/get-currency-networks is POST with
//     api_key + sig (private-get-currency-networks.md) and no public
//     equivalent → Networks answers "unknown (venue requires API key)".
//   - rate limit: "For public market data calls, rate limits are per
//     API method, per IP address: All — 100 requests per second each";
//     exceeded → HTTP 429 code 42901 TOO_MANY_REQUESTS
//     (rest-common-api-reference.md). Gate: 50 req / 1 s.
//   - fees: UNVERIFIED (research §6 — the official fee page renders
//     only via JavaScript): placeholders spot taker 50 bps, perp taker
//     7 bps → Verified=false.
//
// Every PERPETUAL_SWAP quotes in USD (research §2; there is no
// USDT-margined perp — same situation as Kraken's PF_ contracts).
type cryptocomCollector struct {
	base  string
	gate_ *gate
	cl    *client
	inst  *instrumentCache
	rr    *fundingRR // round-robin pick + funding_hist / estimated rates
	marks *fundingRR // storage only (set/get): last-known mark per symbol
	now   func() time.Time
}

const cryptocomBase = "https://api.crypto.com/exchange/v1"

func newCryptoCom(opts Options) *cryptocomCollector {
	now := opts.now()
	c := &cryptocomCollector{base: cryptocomBase, now: now,
		rr: newFundingRR(opts.fundingCalls()), marks: newFundingRR(0)}
	if opts.SpotBase != "" {
		c.base = opts.SpotBase
	}
	c.gate_ = newGate(50, time.Second, now)
	c.cl = newClient(screener.VenueCryptoCom, c.gate_)
	c.inst = newInstrumentCache(opts, c.fetchInstruments)
	return c
}

func (c *cryptocomCollector) ID() screener.Venue { return screener.VenueCryptoCom }
func (c *cryptocomCollector) Fees() Fees {
	// UNVERIFIED placeholders — docs/research/venues/cryptocom.md §6.
	return Fees{SpotTakerBps: bps("0.005"), PerpTakerBps: bps("0.0007"), Verified: false}
}
func (c *cryptocomCollector) RateLimited() int { return int(c.gate_.limited.Load()) }

// cryptocomEnvelope is {id, method, code, result}; code 0 = success
// (rest-common-api-reference.md; live emits a bare 0, doc examples a
// quoted "0" — num accepts both).
type cryptocomEnvelope[T any] struct {
	Code   num    `json:"code"`
	Method string `json:"method"`
	Result T      `json:"result"`
}

func cryptocomCheck(code num, path string) error {
	if !code.IsZero() {
		return fmt.Errorf("cryptocom: %s: code %s", path, code.String())
	}
	return nil
}

type cryptocomInstrument struct {
	Symbol        string `json:"symbol"`
	InstType      string `json:"inst_type"`
	BaseCcy       string `json:"base_ccy"`
	QuoteCcy      string `json:"quote_ccy"`
	PriceTickSize string `json:"price_tick_size"`
	QtyTickSize   string `json:"qty_tick_size"`
	Tradable      bool   `json:"tradable"`
	ContractSize  string `json:"contract_size"`
}

func (c *cryptocomCollector) fetchInstruments(ctx context.Context) ([]Instrument, error) {
	var env cryptocomEnvelope[struct {
		Data []cryptocomInstrument `json:"data"`
	}]
	if err := c.cl.getJSON(ctx, 1, c.base, "/public/get-instruments", nil, &env); err != nil {
		return nil, err
	}
	if err := cryptocomCheck(env.Code, "get-instruments"); err != nil {
		return nil, err
	}
	out := make([]Instrument, 0, len(env.Result.Data))
	for _, in := range env.Result.Data {
		switch in.InstType {
		case "CCY_PAIR":
			out = append(out, Instrument{Venue: screener.VenueCryptoCom, Kind: KindSpot, Symbol: in.Symbol,
				Base: in.BaseCcy, Quote: in.QuoteCcy, Tradable: in.Tradable,
				TickSize: in.PriceTickSize, StepSize: in.QtyTickSize})
		case "PERPETUAL_SWAP":
			// USD-quoted perpetual, hourly funding settlement (§3 of the
			// research doc); FUTURE (dated) contracts are skipped.
			out = append(out, Instrument{Venue: screener.VenueCryptoCom, Kind: KindPerp, Symbol: in.Symbol,
				Base: in.BaseCcy, Quote: in.QuoteCcy, Tradable: in.Tradable,
				TickSize: in.PriceTickSize, StepSize: in.QtyTickSize, CtVal: in.ContractSize, IntervalH: 1})
		}
	}
	return out, nil
}

func (c *cryptocomCollector) Instruments(ctx context.Context) ([]Instrument, error) {
	return c.inst.refresh(ctx)
}

type cryptocomTicker struct {
	I string `json:"i"`
	B num    `json:"b"` // best bid (null when none)
	K num    `json:"k"` // best ask (null when none)
	T num    `json:"t"` // published timestamp, ms
}

func (c *cryptocomCollector) fetchTickers(ctx context.Context) ([]cryptocomTicker, error) {
	var env cryptocomEnvelope[struct {
		Data []cryptocomTicker `json:"data"`
	}]
	if err := c.cl.getJSON(ctx, 1, c.base, "/public/get-tickers", nil, &env); err != nil {
		return nil, err
	}
	if err := cryptocomCheck(env.Code, "get-tickers"); err != nil {
		return nil, err
	}
	return env.Result.Data, nil
}

func (c *cryptocomCollector) Spot(ctx context.Context) ([]screener.Quote, error) {
	inst, err := c.inst.ensure(ctx, KindSpot)
	if err != nil {
		return nil, err
	}
	ticks, err := c.fetchTickers(ctx)
	if err != nil {
		return nil, err
	}
	recv := c.now()
	out := make([]screener.Quote, 0, len(ticks))
	for _, t := range ticks {
		in, ok := inst[t.I]
		if !ok || !in.Tradable || !t.B.IsPositive() || !t.K.IsPositive() {
			continue
		}
		at := recv
		if t.T.IsPositive() {
			at = time.UnixMilli(t.T.IntPart())
		}
		out = append(out, screener.Quote{Venue: screener.VenueCryptoCom, Base: in.Base, Quote: in.Quote,
			Bid: t.B.Decimal, Ask: t.K.Decimal, At: at,
			// get-tickers publishes no bid/ask sizes (VERIFIED-ABSENT).
			LiquidityUnknown: true})
	}
	return out, nil
}

// valuation fetches the latest get-valuations point for one instrument.
func (c *cryptocomCollector) valuation(ctx context.Context, instrument, typ string) (num, time.Time, error) {
	var env cryptocomEnvelope[struct {
		Data []struct {
			V num `json:"v"`
			T num `json:"t"`
		} `json:"data"`
	}]
	q := url.Values{"instrument_name": {instrument}, "valuation_type": {typ}, "count": {"1"}}
	if err := c.cl.getJSON(ctx, 1, c.base, "/public/get-valuations", q, &env); err != nil {
		return num{}, time.Time{}, err
	}
	if err := cryptocomCheck(env.Code, "get-valuations/"+typ); err != nil {
		return num{}, time.Time{}, err
	}
	if len(env.Result.Data) == 0 {
		return num{}, time.Time{}, nil
	}
	d := env.Result.Data[0]
	return d.V, time.UnixMilli(d.T.IntPart()), nil
}

func (c *cryptocomCollector) Perps(ctx context.Context) ([]screener.Perp, error) {
	inst, err := c.inst.ensure(ctx, KindPerp)
	if err != nil {
		return nil, err
	}
	ticks, err := c.fetchTickers(ctx)
	if err != nil {
		return nil, err
	}
	tickBy := make(map[string]cryptocomTicker, len(ticks))
	for _, t := range ticks {
		tickBy[t.I] = t
	}
	symbols := make([]string, 0, len(inst))
	for s, in := range inst {
		if in.Tradable {
			symbols = append(symbols, s)
		}
	}
	// Round-robin: mark_price + funding_hist + estimated_funding_rate
	// for FundingCallsPerPoll contracts; the rest carry last-known.
	for _, s := range c.rr.pick(symbols) {
		mark, _, err := c.valuation(ctx, s, "mark_price")
		if err != nil {
			return nil, err
		}
		if mark.IsPositive() {
			c.marks.set(s, fundingInfo{Rate: mark.Decimal, At: c.now()})
		}
		rate, _, err := c.valuation(ctx, s, "funding_hist")
		if err != nil {
			return nil, err
		}
		est, _, err := c.valuation(ctx, s, "estimated_funding_rate")
		if err != nil {
			return nil, err
		}
		c.rr.set(s, fundingInfo{Rate: rate.Decimal, Predicted: est.Decimal, IntervalH: 1, At: c.now()})
	}
	recv := c.now()
	next := recv.Truncate(time.Hour).Add(time.Hour) // hourly settlement (research §3)
	out := make([]screener.Perp, 0, len(symbols))
	for _, s := range symbols {
		mk, ok := c.marks.get(s)
		if !ok || !mk.Rate.IsPositive() {
			continue // not visited yet — appears on a later poll
		}
		in := inst[s]
		t := tickBy[s]
		at := recv
		if t.T.IsPositive() {
			at = time.UnixMilli(t.T.IntPart())
		}
		p := screener.Perp{Venue: screener.VenueCryptoCom, Base: in.Base, Quote: in.Quote,
			Mark: mk.Rate, Bid: t.B.Decimal, Ask: t.K.Decimal,
			IntervalH: 1, NextFundingAt: next, At: at}
		if fi, ok := c.rr.get(s); ok {
			p.FundingRate = fi.Rate
			p.PredictedFundingRate = fi.Predicted
		}
		out = append(out, p)
	}
	return out, nil
}

// Networks: currency/network status is key-gated (research §4).
func (c *cryptocomCollector) Networks(ctx context.Context) (map[string]screener.NetworkStatus, error) {
	return c.inst.keyGatedNetworks(ctx)
}
