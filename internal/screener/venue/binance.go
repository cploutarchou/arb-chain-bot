package venue

import (
	"context"
	"net/url"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/screener"
)

// Binance collector. Sources (all accessed 2026-08-27):
//   - spot bookTicker, weight 4 for all symbols, fields symbol/bidPrice/
//     bidQty/askPrice/askQty, no timestamp:
//     https://developers.binance.com/docs/binance-spot-api-docs/rest-api/market-data-endpoints
//   - spot exchangeInfo, weight 20, status TRADING/HALT/BREAK, filters
//     PRICE_FILTER.tickSize, LOT_SIZE.stepSize, NOTIONAL.minNotional:
//     same page; rateLimits REQUEST_WEIGHT 6000/min confirmed live from
//     the response's rateLimits[] on 2026-08-27.
//   - USDⓈ-M bookTicker (symbol/bidPrice/bidQty/askPrice/askQty/time):
//     https://developers.binance.com/docs/derivatives/usds-margined-futures/market-data/rest-api/Symbol-Order-Book-Ticker
//   - premiumIndex (markPrice/indexPrice/lastFundingRate/nextFundingTime/
//     time), weight 10 without symbol:
//     https://developers.binance.com/docs/derivatives/usds-margined-futures/market-data/rest-api/Mark-Price
//   - fundingInfo (fundingIntervalHours for symbols adjusted away from
//     the 8 h default):
//     https://developers.binance.com/docs/derivatives/usds-margined-futures/market-data/rest-api/Get-Funding-Rate-Info
//   - futures exchangeInfo (baseAsset/quoteAsset/contractType/status),
//     REQUEST_WEIGHT 2400/min confirmed live from rateLimits[]:
//     https://developers.binance.com/docs/derivatives/usds-margined-futures/market-data/rest-api/Exchange-Information
//   - 429/418 + Retry-After semantics:
//     https://developers.binance.com/docs/binance-spot-api-docs/rest-api/limits
//   - fees: spot regular taker 0.100 %
//     https://www.binance.com/en/fee/schedule ; USDⓈ-M regular taker
//     0.0500 % https://www.binance.com/en/fee/futureFee
//
// Binance's lastFundingRate is the rate accruing for the OPEN interval
// (Mark-Price doc), so it is reported as both FundingRate and
// PredictedFundingRate; there is no separate settled-rate field in the
// bulk response.
//
// The USDⓈ-M exchangeInfo lists USDT- AND USDC-margined perpetuals
// (quoteAsset and marginAsset agree on every PERPETUAL row of the
// 2026-08-27 recording: AAVEUSDT quote USDT, AAVEUSDC quote USDC, 5 USDC
// contracts among 55). Perps() reports each with its own Quote and does
// not choose between them: the Poller keeps one contract per (venue,
// base) by settings.perp_quote_preference, so the choice is one
// configurable policy for every venue instead of a filter hidden here.
type binanceCollector struct {
	opts     Options
	spotBase string
	perpBase string
	spotGate *gate
	perpGate *gate
	spot     *client
	perp     *client
	inst     *instrumentCache
	now      func() time.Time
}

const (
	binanceSpotBase = "https://api.binance.com"
	binancePerpBase = "https://fapi.binance.com"
	// Ceilings sit well under the documented 6000/2400 weight per minute
	// so the triangular engine's own Binance REST use still fits.
	binanceSpotWeightCeiling = 3000
	binancePerpWeightCeiling = 1200
	binanceWeightBookTicker  = 4  // spot bookTicker, all symbols
	binanceWeightExInfo      = 20 // spot exchangeInfo
	binanceWeightFutBook     = 5  // fapi bookTicker without symbol (doc: 5)
	binanceWeightPremium     = 10 // premiumIndex without symbol
	binanceWeightFundingInfo = 1
	binanceWeightFutExInfo   = 1
)

func newBinance(opts Options) *binanceCollector {
	now := opts.now()
	c := &binanceCollector{opts: opts, spotBase: binanceSpotBase, perpBase: binancePerpBase, now: now}
	if opts.SpotBase != "" {
		c.spotBase = opts.SpotBase
	}
	if opts.PerpBase != "" {
		c.perpBase = opts.PerpBase
	}
	c.spotGate = newGate(binanceSpotWeightCeiling, time.Minute, now)
	c.perpGate = newGate(binancePerpWeightCeiling, time.Minute, now)
	c.spot = newClient(screener.VenueBinance, c.spotGate)
	c.perp = newClient(screener.VenueBinance, c.perpGate)
	c.inst = newInstrumentCache(opts, c.fetchInstruments)
	return c
}

func (c *binanceCollector) ID() screener.Venue { return screener.VenueBinance }

func (c *binanceCollector) Fees() Fees {
	return Fees{SpotTakerBps: bps("0.001"), PerpTakerBps: bps("0.0005"), Verified: true}
}

func (c *binanceCollector) RateLimited() int {
	return int(c.spotGate.limited.Load() + c.perpGate.limited.Load())
}

type binanceExchangeInfo struct {
	Symbols []struct {
		Symbol       string `json:"symbol"`
		Status       string `json:"status"`
		BaseAsset    string `json:"baseAsset"`
		QuoteAsset   string `json:"quoteAsset"`
		ContractType string `json:"contractType"` // futures only
		Filters      []struct {
			FilterType  string `json:"filterType"`
			TickSize    string `json:"tickSize"`
			StepSize    string `json:"stepSize"`
			MinNotional string `json:"minNotional"`
		} `json:"filters"`
	} `json:"symbols"`
}

func (c *binanceCollector) fetchInstruments(ctx context.Context) ([]Instrument, error) {
	var spot binanceExchangeInfo
	if err := c.spot.getJSON(ctx, binanceWeightExInfo, c.spotBase, "/api/v3/exchangeInfo", nil, &spot); err != nil {
		return nil, err
	}
	var fut binanceExchangeInfo
	if err := c.perp.getJSON(ctx, binanceWeightFutExInfo, c.perpBase, "/fapi/v1/exchangeInfo", nil, &fut); err != nil {
		return nil, err
	}
	out := make([]Instrument, 0, len(spot.Symbols)+len(fut.Symbols))
	for _, s := range spot.Symbols {
		in := Instrument{Venue: screener.VenueBinance, Kind: KindSpot, Symbol: s.Symbol, Base: s.BaseAsset, Quote: s.QuoteAsset, Tradable: s.Status == "TRADING"}
		for _, f := range s.Filters {
			switch f.FilterType {
			case "PRICE_FILTER":
				in.TickSize = f.TickSize
			case "LOT_SIZE":
				in.StepSize = f.StepSize
			case "NOTIONAL":
				in.MinNotional = f.MinNotional
			}
		}
		out = append(out, in)
	}
	for _, s := range fut.Symbols {
		if s.ContractType != "PERPETUAL" {
			continue
		}
		in := Instrument{Venue: screener.VenueBinance, Kind: KindPerp, Symbol: s.Symbol, Base: s.BaseAsset, Quote: s.QuoteAsset, Tradable: s.Status == "TRADING"}
		for _, f := range s.Filters {
			switch f.FilterType {
			case "PRICE_FILTER":
				in.TickSize = f.TickSize
			case "LOT_SIZE":
				in.StepSize = f.StepSize
			case "MIN_NOTIONAL":
				in.MinNotional = f.MinNotional
			}
		}
		out = append(out, in)
	}
	return out, nil
}

func (c *binanceCollector) Instruments(ctx context.Context) ([]Instrument, error) {
	return c.inst.refresh(ctx)
}

type binanceBookTicker struct {
	Symbol   string `json:"symbol"`
	BidPrice num    `json:"bidPrice"`
	BidQty   num    `json:"bidQty"`
	AskPrice num    `json:"askPrice"`
	AskQty   num    `json:"askQty"`
	Time     int64  `json:"time"` // futures only
}

func (c *binanceCollector) Spot(ctx context.Context) ([]screener.Quote, error) {
	inst, err := c.inst.ensure(ctx, KindSpot)
	if err != nil {
		return nil, err
	}
	var rows []binanceBookTicker
	if err := c.spot.getJSON(ctx, binanceWeightBookTicker, c.spotBase, "/api/v3/ticker/bookTicker", nil, &rows); err != nil {
		return nil, err
	}
	at := c.now() // VERIFIED-ABSENT: no timestamp in spot bookTicker
	out := make([]screener.Quote, 0, len(rows))
	for _, r := range rows {
		in, ok := inst[r.Symbol]
		if !ok || !in.Tradable || !r.BidPrice.IsPositive() || !r.AskPrice.IsPositive() {
			continue
		}
		out = append(out, screener.Quote{Venue: screener.VenueBinance, Base: in.Base, Quote: in.Quote,
			Bid: r.BidPrice.Decimal, BidQty: r.BidQty.Decimal, Ask: r.AskPrice.Decimal, AskQty: r.AskQty.Decimal, At: at})
	}
	return out, nil
}

type binancePremiumIndex struct {
	Symbol          string `json:"symbol"`
	MarkPrice       num    `json:"markPrice"`
	IndexPrice      num    `json:"indexPrice"`
	LastFundingRate num    `json:"lastFundingRate"`
	NextFundingTime int64  `json:"nextFundingTime"`
	Time            int64  `json:"time"`
}

type binanceFundingInfo struct {
	Symbol               string `json:"symbol"`
	FundingIntervalHours int    `json:"fundingIntervalHours"`
}

func (c *binanceCollector) Perps(ctx context.Context) ([]screener.Perp, error) {
	inst, err := c.inst.ensure(ctx, KindPerp)
	if err != nil {
		return nil, err
	}
	var books []binanceBookTicker
	if err := c.perp.getJSON(ctx, binanceWeightFutBook, c.perpBase, "/fapi/v1/ticker/bookTicker", nil, &books); err != nil {
		return nil, err
	}
	var prem []binancePremiumIndex
	if err := c.perp.getJSON(ctx, binanceWeightPremium, c.perpBase, "/fapi/v1/premiumIndex", nil, &prem); err != nil {
		return nil, err
	}
	var finfo []binanceFundingInfo
	if err := c.perp.getJSON(ctx, binanceWeightFundingInfo, c.perpBase, "/fapi/v1/fundingInfo", nil, &finfo); err != nil {
		return nil, err
	}
	intervals := make(map[string]int, len(finfo))
	for _, f := range finfo {
		intervals[f.Symbol] = f.FundingIntervalHours
	}
	bookBy := make(map[string]binanceBookTicker, len(books))
	for _, b := range books {
		bookBy[b.Symbol] = b
	}
	recv := c.now()
	out := make([]screener.Perp, 0, len(prem))
	for _, p := range prem {
		in, ok := inst[p.Symbol]
		if !ok || !in.Tradable || !p.MarkPrice.IsPositive() {
			continue
		}
		b := bookBy[p.Symbol]
		h := intervals[p.Symbol]
		if h == 0 {
			h = 8 // fundingInfo lists only symbols adjusted away from the 8 h default
		}
		at := recv
		if p.Time > 0 {
			at = time.UnixMilli(p.Time)
		}
		out = append(out, screener.Perp{Venue: screener.VenueBinance, Base: in.Base, Quote: in.Quote,
			Mark: p.MarkPrice.Decimal, Index: p.IndexPrice.Decimal, Bid: b.BidPrice.Decimal, Ask: b.AskPrice.Decimal,
			FundingRate: p.LastFundingRate.Decimal, PredictedFundingRate: p.LastFundingRate.Decimal,
			IntervalH: h, NextFundingAt: time.UnixMilli(p.NextFundingTime), At: at})
	}
	return out, nil
}

// Networks: GET /sapi/v1/capital/config/getall is signed
// (https://developers.binance.com/docs/wallet/capital, 2026-08-27) — no
// public equivalent, so every asset is "unknown (venue requires API key)".
func (c *binanceCollector) Networks(ctx context.Context) (map[string]screener.NetworkStatus, error) {
	return c.inst.keyGatedNetworks(ctx)
}

var _ = url.Values{}
