package venue

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/screener"
)

// BingX collector (T-078, docs/research/venues/bingx.md). Sources, all
// accessed 2026-08-27 — official "BingX API Docs V3"
// (https://bingx-api.github.io/docs-v3/, BingX-API GitHub org; the site
// is a JS app, facts read from its content bundle
// static/js/app.1297ec6437891215cd4e.js which carries the EN field
// tables verbatim). REST host https://open-api.bingx.com.
//   - spot instruments GET /openApi/spot/v1/common/symbols ("Spot
//     trading symbols"): symbol ("Trading pair, e.g., BTC-USDT"),
//     status ("0 offline, 1 online, 5 pre-open, 10 accessed,
//     25 suspended, 29 pre-delisted, 30 delisted"), tickSize ("Price
//     tick size"), stepSize ("Quantity step size"), minNotional
//     ("Minimum trading notional"), apiStateBuy ("Buy allowed"),
//     apiStateSell ("Sell allowed"). No base/quote fields exist
//     (VERIFIED-ABSENT); the documented symbol format is hyphenated
//     ('There must be a hyphen/ "-" in the trading pair symbol. eg:
//     BTC-USDT') so base/quote are the two hyphen-separated halves of
//     the venue's own instrument-list entries.
//   - spot quotes GET /openApi/spot/v2/quote/bookTicker ("Symbol Order
//     Book Ticker"; symbol "omit to return all symbols"): data[]
//     {time (ms), symbol, bidPrice ("Best bid price"), bidQty ("Best
//     bid quantity"), askPrice, askQty} — strings.
//   - perp instruments GET /openApi/swap/v2/quote/contracts ("USDT-M
//     Perp Futures symbols"): symbol, asset ("contract trading
//     asset"), currency ("settlement and margin currency asset"),
//     status ("1 online, 25 forbidden to open positions, 5 pre-online,
//     0 offline"), pricePrecision/quantityPrecision (decimal places),
//     tradeMinUSDT, makerFeeRate/takerFeeRate.
//   - perp bid/ask GET /openApi/swap/v2/quote/ticker ("24hr Ticker
//     Price Change Statistics", symbol omitted → all): bidPrice,
//     bidQty, askPrice, askQty, closeTime.
//   - perp mark/funding GET /openApi/swap/v2/quote/premiumIndex ("Mark
//     Price and Funding Rate", symbol omitted → all): markPrice
//     ("current mark price"), indexPrice ("index price"),
//     lastFundingRate ("Last updated funding rate"), nextFundingTime
//     (ms); the documented example response also carries
//     fundingIntervalHours (8). No predicted rate is published
//     (VERIFIED-ABSENT) → PredictedFundingRate zero.
//   - funding history (not polled): GET /openApi/swap/v2/quote/fundingRate
//     ?symbol&limit ("Get Funding Rate History") → fundingRate,
//     fundingTime (ms), limit default 100 max 1000.
//   - currency/chain status: wallet config endpoints are authenticated;
//     no public equivalent → "unknown (venue requires API key)".
//   - rate limits: every market endpoint above is group 1 — "IP Rate
//     Limit :500 requests per 10 seconds."; error reference: 429 "Too
//     Many Requests — Requests are too frequent, rate-limited by the
//     system.", 418 "Continued access after receiving 429, IP has been
//     banned. Stop requests and wait." Gate: 100 req / 10 s.
//   - fees: perp taker 0.05% VERIFIED ("Taker (filled instantly):
//     0.05%", official support article
//     https://bingx.com/en/support/articles/360046487573-perpetual-futures-fee-schedule,
//     matching takerFeeRate 0.0005 on the BTC-USDT contracts row); spot
//     taker UNVERIFIED (the official schedule page renders only via JS)
//     → placeholder 10 bps, Verified=false.
type bingxCollector struct {
	base  string
	gate_ *gate
	cl    *client
	inst  *instrumentCache
	now   func() time.Time
}

const bingxBase = "https://open-api.bingx.com"

func newBingX(opts Options) *bingxCollector {
	now := opts.now()
	c := &bingxCollector{base: bingxBase, now: now}
	if opts.SpotBase != "" {
		c.base = opts.SpotBase
	}
	c.gate_ = newGate(100, 10*time.Second, now)
	c.cl = newClient(screener.VenueBingX, c.gate_)
	c.inst = newInstrumentCache(opts, c.fetchInstruments)
	return c
}

func (c *bingxCollector) ID() screener.Venue { return screener.VenueBingX }
func (c *bingxCollector) Fees() Fees {
	// Spot taker UNVERIFIED (see package comment) → Verified=false even
	// though the perp taker 5 bps is verified.
	return Fees{SpotTakerBps: bps("0.001"), PerpTakerBps: bps("0.0005"), Verified: false}
}
func (c *bingxCollector) RateLimited() int { return int(c.gate_.limited.Load()) }

// bingxEnvelope is {code, msg?, timestamp?, data}; code 0 = success.
type bingxEnvelope[T any] struct {
	Code      int64  `json:"code"`
	Msg       string `json:"msg"`
	Timestamp int64  `json:"timestamp"`
	Data      T      `json:"data"`
}

func bingxCheck(code int64, msg, path string) error {
	if code != 0 {
		return fmt.Errorf("bingx: %s: code %d: %s", path, code, msg)
	}
	return nil
}

type bingxSymbol struct {
	Symbol       string `json:"symbol"`
	Status       int    `json:"status"`
	TickSize     num    `json:"tickSize"`
	StepSize     num    `json:"stepSize"`
	MinNotional  num    `json:"minNotional"`
	APIStateBuy  bool   `json:"apiStateBuy"`
	APIStateSell bool   `json:"apiStateSell"`
}

type bingxContract struct {
	Symbol            string `json:"symbol"`
	Asset             string `json:"asset"`    // base
	Currency          string `json:"currency"` // quote / margin
	Status            int    `json:"status"`
	PricePrecision    int32  `json:"pricePrecision"`
	QuantityPrecision int32  `json:"quantityPrecision"`
	TradeMinUSDT      num    `json:"tradeMinUSDT"`
}

func (c *bingxCollector) fetchInstruments(ctx context.Context) ([]Instrument, error) {
	var spot bingxEnvelope[struct {
		Symbols []bingxSymbol `json:"symbols"`
	}]
	if err := c.cl.getJSON(ctx, 1, c.base, "/openApi/spot/v1/common/symbols", nil, &spot); err != nil {
		return nil, err
	}
	if err := bingxCheck(spot.Code, spot.Msg, "common/symbols"); err != nil {
		return nil, err
	}
	var perp bingxEnvelope[[]bingxContract]
	if err := c.cl.getJSON(ctx, 1, c.base, "/openApi/swap/v2/quote/contracts", nil, &perp); err != nil {
		return nil, err
	}
	if err := bingxCheck(perp.Code, perp.Msg, "quote/contracts"); err != nil {
		return nil, err
	}
	out := make([]Instrument, 0, len(spot.Data.Symbols)+len(perp.Data))
	for _, s := range spot.Data.Symbols {
		// Documented hyphenated symbol format (package comment).
		base, quote, ok := strings.Cut(s.Symbol, "-")
		if !ok || base == "" || quote == "" {
			continue
		}
		out = append(out, Instrument{Venue: screener.VenueBingX, Kind: KindSpot, Symbol: s.Symbol,
			Base: base, Quote: quote,
			// status 1 = online; buy AND sell must both be allowed.
			Tradable: s.Status == 1 && s.APIStateBuy && s.APIStateSell,
			TickSize: s.TickSize.String(), StepSize: s.StepSize.String(), MinNotional: s.MinNotional.String()})
	}
	for _, ct := range perp.Data {
		if ct.Currency != "USDT" { // USDT-margined perpetuals only
			continue
		}
		out = append(out, Instrument{Venue: screener.VenueBingX, Kind: KindPerp, Symbol: ct.Symbol,
			Base: ct.Asset, Quote: ct.Currency, Tradable: ct.Status == 1,
			TickSize:    decimal.New(1, -ct.PricePrecision).String(),
			StepSize:    decimal.New(1, -ct.QuantityPrecision).String(),
			MinNotional: ct.TradeMinUSDT.String()})
	}
	return out, nil
}

func (c *bingxCollector) Instruments(ctx context.Context) ([]Instrument, error) {
	return c.inst.refresh(ctx)
}

func (c *bingxCollector) Spot(ctx context.Context) ([]screener.Quote, error) {
	inst, err := c.inst.ensure(ctx, KindSpot)
	if err != nil {
		return nil, err
	}
	var env bingxEnvelope[[]struct {
		Time     int64  `json:"time"`
		Symbol   string `json:"symbol"`
		BidPrice num    `json:"bidPrice"`
		BidQty   num    `json:"bidQty"`
		AskPrice num    `json:"askPrice"`
		AskQty   num    `json:"askQty"`
	}]
	if err := c.cl.getJSON(ctx, 1, c.base, "/openApi/spot/v2/quote/bookTicker", nil, &env); err != nil {
		return nil, err
	}
	if err := bingxCheck(env.Code, env.Msg, "spot bookTicker"); err != nil {
		return nil, err
	}
	recv := c.now()
	out := make([]screener.Quote, 0, len(env.Data))
	for _, t := range env.Data {
		in, ok := inst[t.Symbol]
		if !ok || !in.Tradable || !t.BidPrice.IsPositive() || !t.AskPrice.IsPositive() {
			continue
		}
		at := recv
		if t.Time > 0 {
			at = time.UnixMilli(t.Time)
		}
		out = append(out, screener.Quote{Venue: screener.VenueBingX, Base: in.Base, Quote: in.Quote,
			Bid: t.BidPrice.Decimal, BidQty: t.BidQty.Decimal, Ask: t.AskPrice.Decimal, AskQty: t.AskQty.Decimal, At: at})
	}
	return out, nil
}

func (c *bingxCollector) Perps(ctx context.Context) ([]screener.Perp, error) {
	inst, err := c.inst.ensure(ctx, KindPerp)
	if err != nil {
		return nil, err
	}
	var tick bingxEnvelope[[]struct {
		Symbol    string `json:"symbol"`
		BidPrice  num    `json:"bidPrice"`
		BidQty    num    `json:"bidQty"`
		AskPrice  num    `json:"askPrice"`
		AskQty    num    `json:"askQty"`
		CloseTime int64  `json:"closeTime"`
	}]
	if err := c.cl.getJSON(ctx, 1, c.base, "/openApi/swap/v2/quote/ticker", nil, &tick); err != nil {
		return nil, err
	}
	if err := bingxCheck(tick.Code, tick.Msg, "swap ticker"); err != nil {
		return nil, err
	}
	var prem bingxEnvelope[[]struct {
		Symbol               string `json:"symbol"`
		MarkPrice            num    `json:"markPrice"`
		IndexPrice           num    `json:"indexPrice"`
		LastFundingRate      num    `json:"lastFundingRate"`
		NextFundingTime      int64  `json:"nextFundingTime"`
		FundingIntervalHours int    `json:"fundingIntervalHours"`
	}]
	if err := c.cl.getJSON(ctx, 1, c.base, "/openApi/swap/v2/quote/premiumIndex", nil, &prem); err != nil {
		return nil, err
	}
	if err := bingxCheck(prem.Code, prem.Msg, "premiumIndex"); err != nil {
		return nil, err
	}
	type bookSide struct {
		bid, bidQty, ask, askQty decimal.Decimal
		at                       time.Time
	}
	books := make(map[string]bookSide, len(tick.Data))
	for _, t := range tick.Data {
		b := bookSide{bid: t.BidPrice.Decimal, bidQty: t.BidQty.Decimal, ask: t.AskPrice.Decimal, askQty: t.AskQty.Decimal}
		if t.CloseTime > 0 {
			b.at = time.UnixMilli(t.CloseTime)
		}
		books[t.Symbol] = b
	}
	recv := c.now()
	out := make([]screener.Perp, 0, len(prem.Data))
	for _, pr := range prem.Data {
		in, ok := inst[pr.Symbol]
		if !ok || !in.Tradable || !pr.MarkPrice.IsPositive() {
			continue
		}
		b := books[pr.Symbol]
		at := recv
		if !b.at.IsZero() {
			at = b.at
		}
		var next time.Time
		if pr.NextFundingTime > 0 {
			next = time.UnixMilli(pr.NextFundingTime)
		}
		out = append(out, screener.Perp{Venue: screener.VenueBingX, Base: in.Base, Quote: in.Quote,
			Mark: pr.MarkPrice.Decimal, Index: pr.IndexPrice.Decimal, Bid: b.bid, Ask: b.ask,
			BidQty: b.bidQty, AskQty: b.askQty,
			FundingRate: pr.LastFundingRate.Decimal,
			IntervalH:   pr.FundingIntervalHours, NextFundingAt: next, At: at})
	}
	return out, nil
}

// Networks: BingX's wallet deposit/withdraw config is key-gated
// (research §4).
func (c *bingxCollector) Networks(ctx context.Context) (map[string]screener.NetworkStatus, error) {
	return c.inst.keyGatedNetworks(ctx)
}
