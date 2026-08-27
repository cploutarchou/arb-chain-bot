package venue

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/screener"
)

// Bitget collector (v2 REST). Bitget moved its documentation to a UTA
// (v3) site; the v2 pages now live under /api-doc/classic/ and rendered
// on 2026-08-27 — all fields below are verified there:
//   - GET /api/v2/spot/market/tickers, 20 req/1 s IP: symbol, lastPr,
//     bidPr, askPr, bidSz, askSz, ts (ms)
//     https://www.bitget.com/api-doc/classic/spot/market/Get-Tickers
//   - GET /api/v2/spot/public/symbols, 20 req/1 s IP: symbol, baseCoin,
//     quoteCoin, status offline|gray|online|halt, pricePrecision,
//     quantityPrecision, minTradeUSDT
//     https://www.bitget.com/api-doc/classic/spot/market/Get-Symbols
//   - GET /api/v2/mix/market/tickers?productType=USDT-FUTURES, 20 req/1 s:
//     symbol, lastPr, bidPr, askPr, bidSz, askSz, indexPrice, markPrice,
//     fundingRate, ts
//     https://www.bitget.com/api-doc/classic/contract/market/Get-All-Symbol-Ticker
//   - GET /api/v2/mix/market/contracts?productType=…, 20 req/s: symbol,
//     baseCoin, quoteCoin, symbolStatus listed|normal|maintain|limit_open|
//     restrictedAPI|off, pricePlace, volumePlace, sizeMultiplier,
//     minTradeNum, minTradeUSDT, fundInterval (hours), symbolType
//     https://www.bitget.com/api-doc/classic/contract/market/Get-All-Symbols-Contracts
//   - GET /api/v2/mix/market/current-fund-rate?symbol=…&productType=…,
//     20 req/1 s, PER SYMBOL: fundingRate, fundingRateInterval (h, one
//     of 1/2/4/8), nextUpdate (ms)
//     https://www.bitget.com/api-doc/classic/contract/market/Get-Current-Funding-Rate
//   - UNVERIFIED: ban behaviour / status code on excess (the classic
//     rate-limit page 404s) — the gate treats 429/418/403 alike; the
//     venue gate is a conservative 10 req/s, half the documented
//     per-endpoint limit.
//   - UNVERIFIED: currency/chain status (treated as key-gated) and fees
//     (research §4.6: spot 0.1 %, USDT-M taker 0.06 %) → Verified=false.
//
// pricePrecision/pricePlace and quantityPrecision/volumePlace are
// decimal-place counts, converted to tick/step strings (10^-n).
type bitgetCollector struct {
	base string
	gate *gate
	c    *client
	inst *instrumentCache
	rr   *fundingRR
	now  func() time.Time
}

const bitgetBase = "https://api.bitget.com"

func newBitget(opts Options) *bitgetCollector {
	now := opts.now()
	c := &bitgetCollector{base: bitgetBase, now: now, rr: newFundingRR(opts.fundingCalls())}
	if opts.SpotBase != "" {
		c.base = opts.SpotBase
	}
	c.gate = newGate(10, time.Second, now)
	c.c = newClient(screener.VenueBitget, c.gate)
	c.inst = newInstrumentCache(opts, c.fetchInstruments)
	return c
}

func (c *bitgetCollector) ID() screener.Venue { return screener.VenueBitget }
func (c *bitgetCollector) Fees() Fees {
	return Fees{SpotTakerBps: bps("0.001"), PerpTakerBps: bps("0.0006"), Verified: false}
}
func (c *bitgetCollector) RateLimited() int { return int(c.gate.limited.Load()) }

type bitgetEnvelope[T any] struct {
	Code string `json:"code"`
	Msg  string `json:"msg"`
	Data []T    `json:"data"`
}

func bitgetCheck(code, msg, path string) error {
	if code != "00000" {
		return fmt.Errorf("bitget: %s: code %s: %s", path, code, msg)
	}
	return nil
}

// placesToStep converts a decimal-places count ("4") to "0.0001".
func placesToStep(places string) string {
	n, err := strconv.Atoi(places)
	if err != nil || n < 0 || n > 30 {
		return ""
	}
	return decimal.New(1, int32(-n)).String()
}

type bitgetSpotSymbol struct {
	Symbol            string `json:"symbol"`
	BaseCoin          string `json:"baseCoin"`
	QuoteCoin         string `json:"quoteCoin"`
	Status            string `json:"status"`
	PricePrecision    string `json:"pricePrecision"`
	QuantityPrecision string `json:"quantityPrecision"`
	MinTradeUSDT      string `json:"minTradeUSDT"`
}

type bitgetContract struct {
	Symbol         string `json:"symbol"`
	BaseCoin       string `json:"baseCoin"`
	QuoteCoin      string `json:"quoteCoin"`
	SymbolStatus   string `json:"symbolStatus"`
	SymbolType     string `json:"symbolType"`
	PricePlace     string `json:"pricePlace"`
	SizeMultiplier string `json:"sizeMultiplier"`
	MinTradeUSDT   string `json:"minTradeUSDT"`
	FundInterval   string `json:"fundInterval"`
}

func (c *bitgetCollector) fetchInstruments(ctx context.Context) ([]Instrument, error) {
	var spot bitgetEnvelope[bitgetSpotSymbol]
	if err := c.c.getJSON(ctx, 1, c.base, "/api/v2/spot/public/symbols", nil, &spot); err != nil {
		return nil, err
	}
	if err := bitgetCheck(spot.Code, spot.Msg, "symbols"); err != nil {
		return nil, err
	}
	var fut bitgetEnvelope[bitgetContract]
	if err := c.c.getJSON(ctx, 1, c.base, "/api/v2/mix/market/contracts", url.Values{"productType": {"USDT-FUTURES"}}, &fut); err != nil {
		return nil, err
	}
	if err := bitgetCheck(fut.Code, fut.Msg, "contracts"); err != nil {
		return nil, err
	}
	out := make([]Instrument, 0, len(spot.Data)+len(fut.Data))
	for _, s := range spot.Data {
		out = append(out, Instrument{Venue: screener.VenueBitget, Kind: KindSpot, Symbol: s.Symbol,
			Base: s.BaseCoin, Quote: s.QuoteCoin, Tradable: s.Status == "online",
			TickSize: placesToStep(s.PricePrecision), StepSize: placesToStep(s.QuantityPrecision), MinNotional: s.MinTradeUSDT})
	}
	for _, f := range fut.Data {
		if f.SymbolType != "perpetual" {
			continue
		}
		h, _ := strconv.Atoi(f.FundInterval)
		out = append(out, Instrument{Venue: screener.VenueBitget, Kind: KindPerp, Symbol: f.Symbol,
			Base: f.BaseCoin, Quote: f.QuoteCoin, Tradable: f.SymbolStatus == "normal",
			TickSize: placesToStep(f.PricePlace), StepSize: f.SizeMultiplier, MinNotional: f.MinTradeUSDT, IntervalH: h})
	}
	return out, nil
}

func (c *bitgetCollector) Instruments(ctx context.Context) ([]Instrument, error) {
	return c.inst.refresh(ctx)
}

type bitgetTicker struct {
	Symbol      string `json:"symbol"`
	LastPr      num    `json:"lastPr"`
	BidPr       num    `json:"bidPr"`
	AskPr       num    `json:"askPr"`
	BidSz       num    `json:"bidSz"`
	AskSz       num    `json:"askSz"`
	IndexPrice  num    `json:"indexPrice"`
	MarkPrice   num    `json:"markPrice"`
	FundingRate num    `json:"fundingRate"`
	Ts          string `json:"ts"`
}

func (c *bitgetCollector) Spot(ctx context.Context) ([]screener.Quote, error) {
	inst, err := c.inst.ensure(ctx, KindSpot)
	if err != nil {
		return nil, err
	}
	var env bitgetEnvelope[bitgetTicker]
	if err := c.c.getJSON(ctx, 1, c.base, "/api/v2/spot/market/tickers", nil, &env); err != nil {
		return nil, err
	}
	if err := bitgetCheck(env.Code, env.Msg, "spot tickers"); err != nil {
		return nil, err
	}
	recv := c.now()
	out := make([]screener.Quote, 0, len(env.Data))
	for _, t := range env.Data {
		in, ok := inst[t.Symbol]
		if !ok || !in.Tradable || !t.BidPr.IsPositive() || !t.AskPr.IsPositive() {
			continue
		}
		out = append(out, screener.Quote{Venue: screener.VenueBitget, Base: in.Base, Quote: in.Quote,
			Bid: t.BidPr.Decimal, BidQty: t.BidSz.Decimal, Ask: t.AskPr.Decimal, AskQty: t.AskSz.Decimal, At: okxTime(t.Ts, recv)})
	}
	return out, nil
}

type bitgetFundRate struct {
	Symbol              string `json:"symbol"`
	FundingRate         num    `json:"fundingRate"`
	FundingRateInterval string `json:"fundingRateInterval"`
	NextUpdate          string `json:"nextUpdate"`
}

func (c *bitgetCollector) Perps(ctx context.Context) ([]screener.Perp, error) {
	inst, err := c.inst.ensure(ctx, KindPerp)
	if err != nil {
		return nil, err
	}
	var env bitgetEnvelope[bitgetTicker]
	if err := c.c.getJSON(ctx, 1, c.base, "/api/v2/mix/market/tickers", url.Values{"productType": {"USDT-FUTURES"}}, &env); err != nil {
		return nil, err
	}
	if err := bitgetCheck(env.Code, env.Msg, "mix tickers"); err != nil {
		return nil, err
	}
	// next-funding time is per symbol only: round-robin N per poll.
	symbols := make([]string, 0, len(inst))
	for s, in := range inst {
		if in.Tradable {
			symbols = append(symbols, s)
		}
	}
	for _, s := range c.rr.pick(symbols) {
		var f bitgetEnvelope[bitgetFundRate]
		if err := c.c.getJSON(ctx, 1, c.base, "/api/v2/mix/market/current-fund-rate", url.Values{"symbol": {s}, "productType": {"USDT-FUTURES"}}, &f); err != nil {
			return nil, err
		}
		if err := bitgetCheck(f.Code, f.Msg, "current-fund-rate"); err != nil {
			return nil, err
		}
		for _, r := range f.Data {
			h, _ := strconv.Atoi(r.FundingRateInterval)
			c.rr.set(r.Symbol, fundingInfo{Rate: r.FundingRate.Decimal, IntervalH: h, NextAt: okxTime(r.NextUpdate, time.Time{}), At: c.now()})
		}
	}
	recv := c.now()
	out := make([]screener.Perp, 0, len(env.Data))
	for _, t := range env.Data {
		in, ok := inst[t.Symbol]
		if !ok || !in.Tradable || !t.MarkPrice.IsPositive() {
			continue
		}
		p := screener.Perp{Venue: screener.VenueBitget, Base: in.Base, Quote: in.Quote,
			Mark: t.MarkPrice.Decimal, Index: t.IndexPrice.Decimal, Bid: t.BidPr.Decimal, Ask: t.AskPr.Decimal,
			FundingRate: t.FundingRate.Decimal, PredictedFundingRate: t.FundingRate.Decimal,
			IntervalH: in.IntervalH, At: okxTime(t.Ts, recv)}
		if fi, ok := c.rr.get(t.Symbol); ok {
			p.NextFundingAt = fi.NextAt
			if fi.IntervalH > 0 {
				p.IntervalH = fi.IntervalH
			}
		}
		out = append(out, p)
	}
	return out, nil
}

// Networks: no public chain-status endpoint verified (research §4.4) —
// treated as key-gated.
func (c *bitgetCollector) Networks(ctx context.Context) (map[string]screener.NetworkStatus, error) {
	return c.inst.keyGatedNetworks(ctx)
}
