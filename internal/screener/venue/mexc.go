package venue

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/screener"
)

// MEXC collector. Sources (accessed 2026-08-27):
//   - spot GET /api/v3/ticker/bookTicker (all), weight 1: symbol,
//     bidPrice, bidQty, askPrice, askQty, no timestamp
//     https://mexcdevelop.github.io/apidocs/spot_v3_en/#order-book-ticker
//   - spot GET /api/v3/exchangeInfo: status "1" online / "2" pause / "3"
//     offline, isSpotTradingAllowed, baseAsset, quoteAsset,
//     baseSizePrecision (step), quoteAmountPrecision (min quote amount);
//     no filters[] for price ticks → TickSize "" (same doc, #exchange-information)
//   - spot rate limit: 500 req / 10 s per endpoint per IP; 418/429
//     wording mirrors Binance (same doc, #limits)
//   - contract GET /api/v1/contract/ticker (all), 20 req/2 s: symbol,
//     lastPrice, bid1, ask1, indexPrice, fairPrice (= mark), fundingRate,
//     timestamp — numbers are bare JSON numbers, parsed as decimals from
//     their literal text
//     https://mexcdevelop.github.io/apidocs/contract_v1_en/#get-contract-trend-data (ticker section)
//   - contract GET /api/v1/contract/funding_rate/{symbol}, 20 req/2 s,
//     PER SYMBOL: fundingRate, collectCycle (hours), nextSettleTime (ms)
//   - contract GET /api/v1/contract/detail, 1 req / 5 s: symbol,
//     baseCoin, quoteCoin, settleCoin, contractSize, priceUnit, volUnit,
//     minVol, state 0 enabled|1 delivery|2 completed|3 offline|4 pause,
//     apiAllowed
//   - currency/chain status: /api/v3/capital/config/getall needs a key.
//   - fees: UNVERIFIED from primary (promos vary; research §6.6 spot
//     taker 0.05 %, USDT-M taker 0.02 %) → Verified=false.
//
// One venue gate at 5 req/s fits the strictest bulk limit (20/2 s); the
// contract detail list is only fetched on instrument refresh (≥10 min),
// far under its 1 req / 5 s.
type mexcCollector struct {
	spotBase string
	perpBase string
	gate     *gate
	c        *client
	inst     *instrumentCache
	rr       *fundingRR
	now      func() time.Time
}

const (
	mexcSpotBase = "https://api.mexc.com"
	mexcPerpBase = "https://contract.mexc.com"
)

func newMEXC(opts Options) *mexcCollector {
	now := opts.now()
	c := &mexcCollector{spotBase: mexcSpotBase, perpBase: mexcPerpBase, now: now, rr: newFundingRR(opts.fundingCalls())}
	if opts.SpotBase != "" {
		c.spotBase = opts.SpotBase
	}
	if opts.PerpBase != "" {
		c.perpBase = opts.PerpBase
	}
	c.gate = newGate(10, 2*time.Second, now)
	c.c = newClient(screener.VenueMEXC, c.gate)
	c.inst = newInstrumentCache(opts, c.fetchInstruments)
	return c
}

func (c *mexcCollector) ID() screener.Venue { return screener.VenueMEXC }
func (c *mexcCollector) Fees() Fees {
	return Fees{SpotTakerBps: bps("0.0005"), PerpTakerBps: bps("0.0002"), Verified: false}
}
func (c *mexcCollector) RateLimited() int { return int(c.gate.limited.Load()) }

type mexcExchangeInfo struct {
	Symbols []struct {
		Symbol               string `json:"symbol"`
		Status               string `json:"status"`
		BaseAsset            string `json:"baseAsset"`
		QuoteAsset           string `json:"quoteAsset"`
		IsSpotTradingAllowed bool   `json:"isSpotTradingAllowed"`
		BaseSizePrecision    string `json:"baseSizePrecision"`
		QuoteAmountPrecision string `json:"quoteAmountPrecision"`
	} `json:"symbols"`
}

type mexcContractEnvelope[T any] struct {
	Success bool   `json:"success"`
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    T      `json:"data"`
}

type mexcContract struct {
	Symbol       string `json:"symbol"`
	BaseCoin     string `json:"baseCoin"`
	QuoteCoin    string `json:"quoteCoin"`
	SettleCoin   string `json:"settleCoin"`
	ContractSize num    `json:"contractSize"`
	PriceUnit    num    `json:"priceUnit"`
	VolUnit      num    `json:"volUnit"`
	State        int    `json:"state"`
	APIAllowed   bool   `json:"apiAllowed"`
}

func (c *mexcCollector) fetchInstruments(ctx context.Context) ([]Instrument, error) {
	var spot mexcExchangeInfo
	if err := c.c.getJSON(ctx, 1, c.spotBase, "/api/v3/exchangeInfo", nil, &spot); err != nil {
		return nil, err
	}
	var fut mexcContractEnvelope[[]mexcContract]
	if err := c.c.getJSON(ctx, 1, c.perpBase, "/api/v1/contract/detail", nil, &fut); err != nil {
		return nil, err
	}
	if !fut.Success {
		return nil, fmt.Errorf("mexc: contract/detail: code %d: %s", fut.Code, fut.Message)
	}
	out := make([]Instrument, 0, len(spot.Symbols)+len(fut.Data))
	for _, s := range spot.Symbols {
		out = append(out, Instrument{Venue: screener.VenueMEXC, Kind: KindSpot, Symbol: s.Symbol,
			Base: s.BaseAsset, Quote: s.QuoteAsset, Tradable: s.Status == "1" && s.IsSpotTradingAllowed,
			StepSize: s.BaseSizePrecision, MinNotional: s.QuoteAmountPrecision})
	}
	for _, f := range fut.Data {
		if f.SettleCoin != "USDT" {
			continue
		}
		out = append(out, Instrument{Venue: screener.VenueMEXC, Kind: KindPerp, Symbol: f.Symbol,
			Base: f.BaseCoin, Quote: f.QuoteCoin, Tradable: f.State == 0 && f.APIAllowed,
			TickSize: f.PriceUnit.String(), StepSize: f.VolUnit.String(), CtVal: f.ContractSize.String()})
	}
	return out, nil
}

func (c *mexcCollector) Instruments(ctx context.Context) ([]Instrument, error) {
	return c.inst.refresh(ctx)
}

func (c *mexcCollector) Spot(ctx context.Context) ([]screener.Quote, error) {
	inst, err := c.inst.ensure(ctx, KindSpot)
	if err != nil {
		return nil, err
	}
	var rows []binanceBookTicker // identical shape (symbol/bidPrice/bidQty/askPrice/askQty)
	if err := c.c.getJSON(ctx, 1, c.spotBase, "/api/v3/ticker/bookTicker", nil, &rows); err != nil {
		return nil, err
	}
	at := c.now()
	out := make([]screener.Quote, 0, len(rows))
	for _, r := range rows {
		in, ok := inst[r.Symbol]
		if !ok || !in.Tradable || !r.BidPrice.IsPositive() || !r.AskPrice.IsPositive() {
			continue
		}
		out = append(out, screener.Quote{Venue: screener.VenueMEXC, Base: in.Base, Quote: in.Quote,
			Bid: r.BidPrice.Decimal, BidQty: r.BidQty.Decimal, Ask: r.AskPrice.Decimal, AskQty: r.AskQty.Decimal, At: at})
	}
	return out, nil
}

type mexcTicker struct {
	Symbol      string `json:"symbol"`
	LastPrice   num    `json:"lastPrice"`
	Bid1        num    `json:"bid1"`
	Ask1        num    `json:"ask1"`
	IndexPrice  num    `json:"indexPrice"`
	FairPrice   num    `json:"fairPrice"`
	FundingRate num    `json:"fundingRate"`
	Timestamp   int64  `json:"timestamp"`
}

type mexcFunding struct {
	Symbol         string `json:"symbol"`
	FundingRate    num    `json:"fundingRate"`
	CollectCycle   int    `json:"collectCycle"`
	NextSettleTime int64  `json:"nextSettleTime"`
}

func (c *mexcCollector) Perps(ctx context.Context) ([]screener.Perp, error) {
	inst, err := c.inst.ensure(ctx, KindPerp)
	if err != nil {
		return nil, err
	}
	var env mexcContractEnvelope[[]mexcTicker]
	if err := c.c.getJSON(ctx, 1, c.perpBase, "/api/v1/contract/ticker", nil, &env); err != nil {
		return nil, err
	}
	if !env.Success {
		return nil, fmt.Errorf("mexc: contract/ticker: code %d: %s", env.Code, env.Message)
	}
	symbols := make([]string, 0, len(inst))
	for s, in := range inst {
		if in.Tradable {
			symbols = append(symbols, s)
		}
	}
	for _, s := range c.rr.pick(symbols) {
		var f mexcContractEnvelope[mexcFunding]
		if err := c.c.getJSON(ctx, 1, c.perpBase, "/api/v1/contract/funding_rate/"+url.PathEscape(s), nil, &f); err != nil {
			return nil, err
		}
		if !f.Success {
			return nil, fmt.Errorf("mexc: funding_rate/%s: code %d: %s", s, f.Code, f.Message)
		}
		c.rr.set(f.Data.Symbol, fundingInfo{Rate: f.Data.FundingRate.Decimal, IntervalH: f.Data.CollectCycle,
			NextAt: time.UnixMilli(f.Data.NextSettleTime), At: c.now()})
	}
	recv := c.now()
	out := make([]screener.Perp, 0, len(env.Data))
	for _, t := range env.Data {
		in, ok := inst[t.Symbol]
		if !ok || !in.Tradable || !t.FairPrice.IsPositive() {
			continue
		}
		at := recv
		if t.Timestamp > 0 {
			at = time.UnixMilli(t.Timestamp)
		}
		p := screener.Perp{Venue: screener.VenueMEXC, Base: in.Base, Quote: in.Quote,
			Mark: t.FairPrice.Decimal, Index: t.IndexPrice.Decimal, Bid: t.Bid1.Decimal, Ask: t.Ask1.Decimal,
			FundingRate: t.FundingRate.Decimal, PredictedFundingRate: t.FundingRate.Decimal, At: at}
		if fi, ok := c.rr.get(t.Symbol); ok {
			p.IntervalH, p.NextFundingAt = fi.IntervalH, fi.NextAt
		}
		out = append(out, p)
	}
	return out, nil
}

// Networks: GET /api/v3/capital/config/getall requires an API key
// (spot_v3_en #query-the-currency-information, 2026-08-27).
func (c *mexcCollector) Networks(ctx context.Context) (map[string]screener.NetworkStatus, error) {
	return c.inst.keyGatedNetworks(ctx)
}
