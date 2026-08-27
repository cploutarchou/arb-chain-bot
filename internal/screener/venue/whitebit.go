package venue

import (
	"context"
	"fmt"
	"time"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/screener"
)

// WhiteBIT collector (T-078, docs/research/venues/whitebit.md). Sources,
// all accessed 2026-08-27 (docs.whitebit.com; the site renders
// github.com/whitebit-exchange/api-docs pages/public/http-v{1,4}.mdx).
// REST host https://whitebit.com.
//   - instruments GET /api/v4/public/markets ("all information about
//     available spot and futures markets", 2000 req/10 s): name, stock
//     ("Ticker of stock currency" = base), money ("Ticker of money
//     currency" = quote), stockPrec/moneyPrec (precisions), minTotal
//     ("Minimal amount of money to trade"), tradesEnabled ("Is trading
//     enabled"), type "spot"|"futures" (live also "tradfiFutures",
//     skipped — not in the documented enum). TickSize/StepSize derived
//     as 10^-moneyPrec / 10^-stockPrec; the live tickSize/stepSize
//     fields are NOT documented → unused.
//     https://docs.whitebit.com/public/http-v4/ § Market Info
//   - spot quotes GET /api/v1/public/tickers ("recent trading activity
//     on all markets", 1000 req/10 s): result[market].ticker {bid
//     "Highest bid", ask "Lowest ask"}, at (s). NO size fields
//     (VERIFIED-ABSENT; the per-market orderbook endpoint is one
//     request per market) → quotes carry LiquidityUnknown, sizes zero,
//     like Gate. https://docs.whitebit.com/public/http-v1/ § Market Activity
//   - perps GET /api/v4/public/futures ("list of available futures
//     markets", 2000 req/10 s): ticker_id (= market name, "Identifier
//     of a ticker with delimiter to separate base/target"),
//     stock_currency/money_currency (base/target codes), bid ("Current
//     highest bid price"), ask, product_type ("Futures, Perpetual,
//     Options?" — only Perpetual kept), index_price ("Underlying index
//     price"), funding_rate ("The current funding rate"),
//     next_funding_rate_timestamp (ms), funding_interval_minutes
//     ("Funding interval in minutes") → IntervalH = minutes/60 (0 when
//     not whole). The live mark_price field is NOT in the documented
//     response → unused; Mark stays 0 with Index set (conformance
//     class noBulkMark). No predicted rate (VERIFIED-ABSENT).
//     https://docs.whitebit.com/public/http-v4/ § Available Futures Markets List
//   - funding history (not polled): GET /api/v4/public/funding-history/
//     {market}?limit= → fundingTime (s), fundingRate, settlementPrice.
//   - networks GET /api/v4/public/assets ("assets status",
//     2000 req/10 s): per asset can_deposit ("Identifies whether
//     deposits are enabled or disabled"), can_withdraw — PUBLIC.
//     https://docs.whitebit.com/public/http-v4/ § Asset status list
//   - rate limits: per-endpoint numbers above; FAQ: "If the rate limit
//     for an endpoint is exceeded, you will receive a 429 error."
//     (https://docs.whitebit.com/faq). Gate: 100 req / 10 s.
//   - fees: UNVERIFIED (research §6 — the markets doc example "0.001
//     ratio" contradicts the assets doc "in percentage" and the live
//     "0.1"/"0.055" values; the fee-schedule web page answers 403) →
//     placeholders spot taker 10 bps, perp taker 5.5 bps,
//     Verified=false.
type whitebitCollector struct {
	base  string
	gate_ *gate
	cl    *client
	inst  *instrumentCache
	now   func() time.Time
}

const whitebitBase = "https://whitebit.com"

func newWhiteBIT(opts Options) *whitebitCollector {
	now := opts.now()
	c := &whitebitCollector{base: whitebitBase, now: now}
	if opts.SpotBase != "" {
		c.base = opts.SpotBase
	}
	c.gate_ = newGate(100, 10*time.Second, now)
	c.cl = newClient(screener.VenueWhiteBIT, c.gate_)
	c.inst = newInstrumentCache(opts, c.fetchInstruments)
	return c
}

func (c *whitebitCollector) ID() screener.Venue { return screener.VenueWhiteBIT }
func (c *whitebitCollector) Fees() Fees {
	// UNVERIFIED placeholders — docs/research/venues/whitebit.md §6.
	return Fees{SpotTakerBps: bps("0.001"), PerpTakerBps: bps("0.00055"), Verified: false}
}
func (c *whitebitCollector) RateLimited() int { return int(c.gate_.limited.Load()) }

type whitebitMarket struct {
	Name          string `json:"name"`
	Stock         string `json:"stock"`
	Money         string `json:"money"`
	StockPrec     num    `json:"stockPrec"`
	MoneyPrec     num    `json:"moneyPrec"`
	MinTotal      string `json:"minTotal"`
	TradesEnabled bool   `json:"tradesEnabled"`
	Type          string `json:"type"`
}

func (c *whitebitCollector) fetchInstruments(ctx context.Context) ([]Instrument, error) {
	var markets []whitebitMarket
	if err := c.cl.getJSON(ctx, 1, c.base, "/api/v4/public/markets", nil, &markets); err != nil {
		return nil, err
	}
	out := make([]Instrument, 0, len(markets))
	for _, m := range markets {
		var kind InstrumentKind
		switch m.Type {
		case "spot":
			kind = KindSpot
		case "futures":
			kind = KindPerp
		default: // "tradfiFutures" etc — not in the documented enum
			continue
		}
		out = append(out, Instrument{Venue: screener.VenueWhiteBIT, Kind: kind, Symbol: m.Name,
			Base: m.Stock, Quote: m.Money, Tradable: m.TradesEnabled,
			TickSize:    decimal.New(1, int32(-m.MoneyPrec.IntPart())).String(), //nolint:gosec // venue precisions are small non-negative ints
			StepSize:    decimal.New(1, int32(-m.StockPrec.IntPart())).String(), //nolint:gosec
			MinNotional: m.MinTotal})
	}
	return out, nil
}

func (c *whitebitCollector) Instruments(ctx context.Context) ([]Instrument, error) {
	return c.inst.refresh(ctx)
}

type whitebitV1Envelope[T any] struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Result  T      `json:"result"`
}

func (c *whitebitCollector) Spot(ctx context.Context) ([]screener.Quote, error) {
	inst, err := c.inst.ensure(ctx, KindSpot)
	if err != nil {
		return nil, err
	}
	var env whitebitV1Envelope[map[string]struct {
		Ticker struct {
			Bid num `json:"bid"`
			Ask num `json:"ask"`
		} `json:"ticker"`
		At int64 `json:"at"`
	}]
	if err := c.cl.getJSON(ctx, 1, c.base, "/api/v1/public/tickers", nil, &env); err != nil {
		return nil, err
	}
	if !env.Success {
		return nil, fmt.Errorf("whitebit: v1 tickers: %s", env.Message)
	}
	recv := c.now()
	out := make([]screener.Quote, 0, len(env.Result))
	for market, t := range env.Result {
		in, ok := inst[market]
		if !ok || !in.Tradable || !t.Ticker.Bid.IsPositive() || !t.Ticker.Ask.IsPositive() {
			continue
		}
		at := recv
		if t.At > 0 {
			at = time.Unix(t.At, 0)
		}
		out = append(out, screener.Quote{Venue: screener.VenueWhiteBIT, Base: in.Base, Quote: in.Quote,
			Bid: t.Ticker.Bid.Decimal, Ask: t.Ticker.Ask.Decimal, At: at,
			// v1 tickers publish no bid/ask sizes (VERIFIED-ABSENT).
			LiquidityUnknown: true})
	}
	return out, nil
}

func (c *whitebitCollector) Perps(ctx context.Context) ([]screener.Perp, error) {
	inst, err := c.inst.ensure(ctx, KindPerp)
	if err != nil {
		return nil, err
	}
	var env whitebitV1Envelope[[]struct {
		TickerID           string `json:"ticker_id"`
		MoneyCurrency      string `json:"money_currency"`
		Bid                num    `json:"bid"`
		Ask                num    `json:"ask"`
		ProductType        string `json:"product_type"`
		IndexPrice         num    `json:"index_price"`
		FundingRate        num    `json:"funding_rate"`
		NextFundingRateTs  num    `json:"next_funding_rate_timestamp"`
		FundingIntervalMin num    `json:"funding_interval_minutes"`
	}]
	if err := c.cl.getJSON(ctx, 1, c.base, "/api/v4/public/futures", nil, &env); err != nil {
		return nil, err
	}
	if !env.Success {
		return nil, fmt.Errorf("whitebit: futures: %s", env.Message)
	}
	at := c.now() // no snapshot timestamp on the futures list (VERIFIED-ABSENT)
	out := make([]screener.Perp, 0, len(env.Result))
	for _, f := range env.Result {
		in, ok := inst[f.TickerID]
		// USDT-margined perpetuals only; Index must be positive because
		// Mark is unavailable (see package comment — noBulkMark).
		if !ok || !in.Tradable || f.ProductType != "Perpetual" || f.MoneyCurrency != "USDT" || !f.IndexPrice.IsPositive() {
			continue
		}
		var next time.Time
		if f.NextFundingRateTs.IsPositive() {
			next = time.UnixMilli(f.NextFundingRateTs.IntPart())
		}
		h := 0
		if mins := f.FundingIntervalMin.IntPart(); mins > 0 && mins%60 == 0 {
			h = int(mins / 60)
		}
		out = append(out, screener.Perp{Venue: screener.VenueWhiteBIT, Base: in.Base, Quote: in.Quote,
			Index: f.IndexPrice.Decimal, Bid: f.Bid.Decimal, Ask: f.Ask.Decimal,
			FundingRate: f.FundingRate.Decimal,
			IntervalH:   h, NextFundingAt: next, At: at})
	}
	return out, nil
}

// Networks: public per-asset status (research §4). Open when deposits
// AND withdrawals are enabled; closed otherwise, naming the side.
func (c *whitebitCollector) Networks(ctx context.Context) (map[string]screener.NetworkStatus, error) {
	var assets map[string]struct {
		CanWithdraw bool `json:"can_withdraw"`
		CanDeposit  bool `json:"can_deposit"`
	}
	if err := c.cl.getJSON(ctx, 1, c.base, "/api/v4/public/assets", nil, &assets); err != nil {
		return nil, err
	}
	out := make(map[string]screener.NetworkStatus, len(assets))
	for asset, a := range assets {
		switch {
		case a.CanDeposit && a.CanWithdraw:
			out[asset] = screener.NetworkStatus{Status: screener.NetworkOpen}
		case !a.CanDeposit && !a.CanWithdraw:
			out[asset] = screener.NetworkStatus{Status: screener.NetworkClosed, Reason: "deposit+withdraw disabled"}
		case !a.CanDeposit:
			out[asset] = screener.NetworkStatus{Status: screener.NetworkClosed, Reason: "deposit disabled"}
		default:
			out[asset] = screener.NetworkStatus{Status: screener.NetworkClosed, Reason: "withdraw disabled"}
		}
	}
	return out, nil
}
