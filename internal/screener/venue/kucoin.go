package venue

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/screener"
)

// KuCoin collector (T-075, docs/research/venues/kucoin.md). Sources, all
// accessed 2026-08-27:
//   - spot GET /api/v1/market/allTickers, weight 15: data.time (ms) +
//     data.ticker[] {symbol, buy (best bid), sell (best ask),
//     bestBidSize, bestAskSize, last, takerFeeRate, makerFeeRate}
//     https://www.kucoin.com/docs-new/rest/spot-trading/market-data/get-all-tickers
//   - spot GET /api/v2/symbols, weight 4: symbol, baseCurrency,
//     quoteCurrency, enableTrading, priceIncrement, baseIncrement,
//     minFunds (minimum order value)
//     https://www.kucoin.com/docs-new/rest/spot-trading/market-data/get-all-symbols
//   - spot GET /api/v3/currencies, weight 3, PUBLIC: chains[]
//     {chainName, isDepositEnabled, isWithdrawEnabled}
//     https://www.kucoin.com/docs-new/rest/spot-trading/market-data/get-all-currencies
//   - futures GET /api/v1/contracts/active, weight 3: symbol,
//     baseCurrency, quoteCurrency, settleCurrency, multiplier (coins per
//     lot), tickSize, lotSize, status (Init|Open|BeingSettled|Settled|
//     Paused|Closed|CancelOnly), fundingFeeRate, predictedFundingFeeRate,
//     nextFundingRateDateTime (ms), fundingRateGranularity (ms),
//     markPrice, indexPrice, isInverse, takerFeeRate, makerFeeRate;
//     numbers are bare JSON numbers, parsed from their literal text
//     https://www.kucoin.com/docs-new/rest/futures-trading/market-data/get-all-symbols
//   - futures GET /api/v1/allTickers, weight 5: symbol, bestBidPrice,
//     bestBidSize, bestAskPrice, bestAskSize, ts (nanoseconds)
//     https://www.kucoin.com/docs-new/rest/futures-trading/market-data/get-all-tickers
//   - funding history (not polled): GET /api/v1/contract/funding-rates
//     ?symbol&from&to → fundingRate, timepoint
//     https://www.kucoin.com/docs-new/rest/futures-trading/funding-fees/get-public-funding-history
//   - rate limit: public pool 2000 weight / 30 s per IP, spot and
//     futures each; excess → HTTP 429 with code 429000
//     https://www.kucoin.com/docs-new/rate-limit-rule-classic.md
//   - fees, VERIFIED: spot takerFeeRate "0.001" on the allTickers row of
//     BTC-USDT (takerCoefficient 1.00; the doc names the field "Fee rate
//     for market takers"); futures takerFeeRate 0.0006 on the
//     contracts/active row of XBTUSDTM, matching the LV0 "0.060 %" of
//     https://www.kucoin.com/announcement/en-futures-fee → Verified=true.
//
// KuCoin Futures denotes Bitcoin as "XBT" (doc example baseCurrency
// "XBT" for XBTUSDTM) while KuCoin Spot lists "BTC"; the single cited
// alias below maps the perp base to the spot name so basis pairs up.
// No other alias is applied — a futures base with no spot pair simply
// never matches.
type kucoinCollector struct {
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
	kucoinSpotBase = "https://api.kucoin.com"
	kucoinPerpBase = "https://api-futures.kucoin.com"
	// 600 of the documented 2000 weight per 30 s, per host.
	kucoinWeightCeiling  = 600
	kucoinWeightTickers  = 15
	kucoinWeightSymbols  = 4
	kucoinWeightCurrency = 3
	kucoinWeightContract = 3
	kucoinWeightFutTick  = 5
)

func newKuCoin(opts Options) *kucoinCollector {
	now := opts.now()
	c := &kucoinCollector{spotBase: kucoinSpotBase, perpBase: kucoinPerpBase, now: now}
	if opts.SpotBase != "" {
		c.spotBase = opts.SpotBase
	}
	if opts.PerpBase != "" {
		c.perpBase = opts.PerpBase
	}
	c.spotGate = newGate(kucoinWeightCeiling, 30*time.Second, now)
	c.perpGate = newGate(kucoinWeightCeiling, 30*time.Second, now)
	c.spot = newClient(screener.VenueKuCoin, c.spotGate)
	c.perp = newClient(screener.VenueKuCoin, c.perpGate)
	c.inst = newInstrumentCache(opts, c.fetchInstruments)
	return c
}

func (c *kucoinCollector) ID() screener.Venue { return screener.VenueKuCoin }
func (c *kucoinCollector) Fees() Fees {
	return Fees{SpotTakerBps: bps("0.001"), PerpTakerBps: bps("0.0006"), Verified: true}
}
func (c *kucoinCollector) RateLimited() int {
	return int(c.spotGate.limited.Load() + c.perpGate.limited.Load())
}

type kucoinEnvelope[T any] struct {
	Code string `json:"code"`
	Msg  string `json:"msg"`
	Data T      `json:"data"`
}

func kucoinCheck(code, msg, path string) error {
	if code != "200000" {
		return fmt.Errorf("kucoin: %s: code %s: %s", path, code, msg)
	}
	return nil
}

type kucoinSymbol struct {
	Symbol         string `json:"symbol"`
	BaseCurrency   string `json:"baseCurrency"`
	QuoteCurrency  string `json:"quoteCurrency"`
	EnableTrading  bool   `json:"enableTrading"`
	PriceIncrement string `json:"priceIncrement"`
	BaseIncrement  string `json:"baseIncrement"`
	MinFunds       string `json:"minFunds"`
}

type kucoinContract struct {
	Symbol                  string `json:"symbol"`
	BaseCurrency            string `json:"baseCurrency"`
	QuoteCurrency           string `json:"quoteCurrency"`
	SettleCurrency          string `json:"settleCurrency"`
	Multiplier              num    `json:"multiplier"`
	TickSize                num    `json:"tickSize"`
	LotSize                 num    `json:"lotSize"`
	Status                  string `json:"status"`
	IsInverse               bool   `json:"isInverse"`
	FundingFeeRate          num    `json:"fundingFeeRate"`
	PredictedFundingFeeRate num    `json:"predictedFundingFeeRate"`
	NextFundingRateDateTime num    `json:"nextFundingRateDateTime"`
	FundingRateGranularity  num    `json:"fundingRateGranularity"`
	MarkPrice               num    `json:"markPrice"`
	IndexPrice              num    `json:"indexPrice"`
}

// kucoinPerpBase maps the futures base name to the spot base name (see
// the package comment above: XBT is the only documented divergence).
func kucoinPerpBaseName(base string) string {
	if base == "XBT" {
		return "BTC"
	}
	return base
}

func (c *kucoinCollector) fetchContracts(ctx context.Context) ([]kucoinContract, error) {
	var env kucoinEnvelope[[]kucoinContract]
	if err := c.perp.getJSON(ctx, kucoinWeightContract, c.perpBase, "/api/v1/contracts/active", nil, &env); err != nil {
		return nil, err
	}
	if err := kucoinCheck(env.Code, env.Msg, "contracts/active"); err != nil {
		return nil, err
	}
	return env.Data, nil
}

func (c *kucoinCollector) fetchInstruments(ctx context.Context) ([]Instrument, error) {
	var spot kucoinEnvelope[[]kucoinSymbol]
	if err := c.spot.getJSON(ctx, kucoinWeightSymbols, c.spotBase, "/api/v2/symbols", nil, &spot); err != nil {
		return nil, err
	}
	if err := kucoinCheck(spot.Code, spot.Msg, "symbols"); err != nil {
		return nil, err
	}
	contracts, err := c.fetchContracts(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Instrument, 0, len(spot.Data)+len(contracts))
	for _, s := range spot.Data {
		out = append(out, Instrument{Venue: screener.VenueKuCoin, Kind: KindSpot, Symbol: s.Symbol,
			Base: s.BaseCurrency, Quote: s.QuoteCurrency, Tradable: s.EnableTrading,
			TickSize: s.PriceIncrement, StepSize: s.BaseIncrement, MinNotional: s.MinFunds})
	}
	for _, ct := range contracts {
		// USDT-margined linear perpetuals only: settleCurrency USDT, not
		// inverse, and a perpetual funding cycle (delivery contracts
		// publish no fundingRateGranularity).
		if ct.SettleCurrency != "USDT" || ct.IsInverse || !ct.FundingRateGranularity.IsPositive() {
			continue
		}
		out = append(out, Instrument{Venue: screener.VenueKuCoin, Kind: KindPerp, Symbol: ct.Symbol,
			Base: kucoinPerpBaseName(ct.BaseCurrency), Quote: ct.QuoteCurrency, Tradable: ct.Status == "Open",
			TickSize: ct.TickSize.String(), StepSize: ct.LotSize.String(), CtVal: ct.Multiplier.String(),
			IntervalH: int(ct.FundingRateGranularity.IntPart() / 3600000)})
	}
	return out, nil
}

func (c *kucoinCollector) Instruments(ctx context.Context) ([]Instrument, error) {
	return c.inst.refresh(ctx)
}

type kucoinAllTickers struct {
	Time   int64 `json:"time"`
	Ticker []struct {
		Symbol      string `json:"symbol"`
		Buy         num    `json:"buy"`
		Sell        num    `json:"sell"`
		BestBidSize num    `json:"bestBidSize"`
		BestAskSize num    `json:"bestAskSize"`
	} `json:"ticker"`
}

func (c *kucoinCollector) Spot(ctx context.Context) ([]screener.Quote, error) {
	inst, err := c.inst.ensure(ctx, KindSpot)
	if err != nil {
		return nil, err
	}
	var env kucoinEnvelope[kucoinAllTickers]
	if err := c.spot.getJSON(ctx, kucoinWeightTickers, c.spotBase, "/api/v1/market/allTickers", nil, &env); err != nil {
		return nil, err
	}
	if err := kucoinCheck(env.Code, env.Msg, "allTickers"); err != nil {
		return nil, err
	}
	at := c.now()
	if env.Data.Time > 0 {
		at = time.UnixMilli(env.Data.Time)
	}
	out := make([]screener.Quote, 0, len(env.Data.Ticker))
	for _, t := range env.Data.Ticker {
		in, ok := inst[t.Symbol]
		if !ok || !in.Tradable || !t.Buy.IsPositive() || !t.Sell.IsPositive() {
			continue
		}
		out = append(out, screener.Quote{Venue: screener.VenueKuCoin, Base: in.Base, Quote: in.Quote,
			Bid: t.Buy.Decimal, BidQty: t.BestBidSize.Decimal, Ask: t.Sell.Decimal, AskQty: t.BestAskSize.Decimal, At: at})
	}
	return out, nil
}

type kucoinFutTicker struct {
	Symbol       string `json:"symbol"`
	BestBidPrice num    `json:"bestBidPrice"`
	BestBidSize  num    `json:"bestBidSize"`
	BestAskPrice num    `json:"bestAskPrice"`
	BestAskSize  num    `json:"bestAskSize"`
	Ts           int64  `json:"ts"` // nanoseconds
}

// Perps: contracts/active carries mark, index, current + predicted
// funding, the next settlement time and the interval for every
// contract in one call; allTickers adds best bid/ask.
func (c *kucoinCollector) Perps(ctx context.Context) ([]screener.Perp, error) {
	inst, err := c.inst.ensure(ctx, KindPerp)
	if err != nil {
		return nil, err
	}
	contracts, err := c.fetchContracts(ctx)
	if err != nil {
		return nil, err
	}
	var tick kucoinEnvelope[[]kucoinFutTicker]
	if err := c.perp.getJSON(ctx, kucoinWeightFutTick, c.perpBase, "/api/v1/allTickers", nil, &tick); err != nil {
		return nil, err
	}
	if err := kucoinCheck(tick.Code, tick.Msg, "allTickers"); err != nil {
		return nil, err
	}
	tickBy := make(map[string]kucoinFutTicker, len(tick.Data))
	for _, t := range tick.Data {
		tickBy[t.Symbol] = t
	}
	recv := c.now()
	out := make([]screener.Perp, 0, len(contracts))
	for _, ct := range contracts {
		in, ok := inst[ct.Symbol]
		if !ok || !in.Tradable || !ct.MarkPrice.IsPositive() {
			continue
		}
		t := tickBy[ct.Symbol]
		at := recv
		if t.Ts > 0 {
			at = time.Unix(0, t.Ts)
		}
		var next time.Time
		if ct.NextFundingRateDateTime.IsPositive() {
			next = time.UnixMilli(ct.NextFundingRateDateTime.IntPart())
		}
		h := in.IntervalH
		if ct.FundingRateGranularity.IsPositive() {
			h = int(ct.FundingRateGranularity.IntPart() / 3600000)
		}
		out = append(out, screener.Perp{Venue: screener.VenueKuCoin, Base: in.Base, Quote: in.Quote,
			Mark: ct.MarkPrice.Decimal, Index: ct.IndexPrice.Decimal, Bid: t.BestBidPrice.Decimal, Ask: t.BestAskPrice.Decimal,
			BidQty: t.BestBidSize.Decimal, AskQty: t.BestAskSize.Decimal,
			FundingRate: ct.FundingFeeRate.Decimal, PredictedFundingRate: ct.PredictedFundingFeeRate.Decimal,
			IntervalH: h, NextFundingAt: next, At: at})
	}
	return out, nil
}

type kucoinCurrency struct {
	Currency string `json:"currency"`
	Chains   []struct {
		ChainName         string `json:"chainName"`
		IsDepositEnabled  bool   `json:"isDepositEnabled"`
		IsWithdrawEnabled bool   `json:"isWithdrawEnabled"`
	} `json:"chains"`
}

// Networks is public on KuCoin (GET /api/v3/currencies). Same rule as
// Gate: open when at least one chain allows both deposit and withdraw,
// closed otherwise (reason lists the chains), unknown with no chains.
func (c *kucoinCollector) Networks(ctx context.Context) (map[string]screener.NetworkStatus, error) {
	var env kucoinEnvelope[[]kucoinCurrency]
	if err := c.spot.getJSON(ctx, kucoinWeightCurrency, c.spotBase, "/api/v3/currencies", nil, &env); err != nil {
		return nil, err
	}
	if err := kucoinCheck(env.Code, env.Msg, "currencies"); err != nil {
		return nil, err
	}
	out := make(map[string]screener.NetworkStatus, len(env.Data))
	for _, cur := range env.Data {
		if len(cur.Chains) == 0 {
			out[cur.Currency] = screener.UnknownNetworkStatus("no chains published")
			continue
		}
		reason := ""
		open := false
		for _, ch := range cur.Chains {
			if ch.IsDepositEnabled && ch.IsWithdrawEnabled {
				open = true
				break
			}
			switch {
			case !ch.IsDepositEnabled && !ch.IsWithdrawEnabled:
				reason += ch.ChainName + ": deposit+withdraw disabled; "
			case !ch.IsDepositEnabled:
				reason += ch.ChainName + ": deposit disabled; "
			default:
				reason += ch.ChainName + ": withdraw disabled; "
			}
		}
		if open {
			out[cur.Currency] = screener.NetworkStatus{Status: screener.NetworkOpen}
		} else {
			out[cur.Currency] = screener.NetworkStatus{Status: screener.NetworkClosed, Reason: reason}
		}
	}
	return out, nil
}

var _ = url.Values{}
