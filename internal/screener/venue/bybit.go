package venue

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/screener"
)

// Bybit collector. Sources (accessed 2026-08-27):
//   - GET /v5/market/tickers?category=spot: symbol, bid1Price, bid1Size,
//     ask1Price, ask1Size, lastPrice; top-level result.time (ms) — no
//     per-symbol timestamp, so receive time is used.
//     https://bybit-exchange.github.io/docs/v5/market/tickers
//   - GET /v5/market/tickers?category=linear: markPrice, indexPrice,
//     fundingRate, nextFundingTime, bid1Price, ask1Price (same page).
//   - GET /v5/market/instruments-info?category=spot|linear: baseCoin,
//     quoteCoin, status (Trading|PreLaunch|Delivering|…), priceFilter.
//     tickSize, lotSizeFilter.basePrecision/minOrderAmt (spot),
//     lotSizeFilter.qtyStep/minNotionalValue + fundingInterval (minutes)
//     (linear); paginated via limit/cursor.
//     https://bybit-exchange.github.io/docs/v5/market/instrument
//   - rate limit: 600 req / 5 s per IP; excess → HTTP 403 "access too
//     frequent" with a ≥10 min ban.
//     https://bybit-exchange.github.io/docs/v5/rate-limit
//   - currency/chain status: /v5/asset/coin/query-info needs a key.
//   - fees: UNVERIFIED from primary (fee page timed out; research §3.6
//     cites spot 0.1 % / perp taker 0.055 %) → Verified=false.
//
// Bybit's fundingRate on the linear ticker is the rate for the current
// interval; there is no separate predicted field, so it is reported as
// both.
type bybitCollector struct {
	base string
	gate *gate
	c    *client
	inst *instrumentCache
	now  func() time.Time
}

const bybitBase = "https://api.bybit.com"

func newBybit(opts Options) *bybitCollector {
	now := opts.now()
	c := &bybitCollector{base: bybitBase, now: now}
	if opts.SpotBase != "" {
		c.base = opts.SpotBase
	}
	// 600/5 s documented; ceiling 100/5 s leaves the rest to other users
	// of the same egress IP.
	c.gate = newGate(100, 5*time.Second, now)
	c.c = newClient(screener.VenueBybit, c.gate)
	c.inst = newInstrumentCache(opts, c.fetchInstruments)
	return c
}

func (c *bybitCollector) ID() screener.Venue { return screener.VenueBybit }
func (c *bybitCollector) Fees() Fees {
	return Fees{SpotTakerBps: bps("0.001"), PerpTakerBps: bps("0.00055"), Verified: false}
}
func (c *bybitCollector) RateLimited() int { return int(c.gate.limited.Load()) }

type bybitEnvelope[T any] struct {
	RetCode int    `json:"retCode"`
	RetMsg  string `json:"retMsg"`
	Result  struct {
		Category       string `json:"category"`
		NextPageCursor string `json:"nextPageCursor"`
		List           []T    `json:"list"`
	} `json:"result"`
	Time int64 `json:"time"`
}

type bybitInstrument struct {
	Symbol          string `json:"symbol"`
	BaseCoin        string `json:"baseCoin"`
	QuoteCoin       string `json:"quoteCoin"`
	Status          string `json:"status"`
	ContractType    string `json:"contractType"`
	SettleCoin      string `json:"settleCoin"`
	FundingInterval int    `json:"fundingInterval"` // minutes, linear only
	PriceFilter     struct {
		TickSize string `json:"tickSize"`
	} `json:"priceFilter"`
	LotSizeFilter struct {
		BasePrecision    string `json:"basePrecision"`
		MinOrderAmt      string `json:"minOrderAmt"`
		QtyStep          string `json:"qtyStep"`
		MinNotionalValue string `json:"minNotionalValue"`
	} `json:"lotSizeFilter"`
}

func (c *bybitCollector) fetchInstruments(ctx context.Context) ([]Instrument, error) {
	var out []Instrument
	for _, cat := range []string{"spot", "linear"} {
		cursor := ""
		for {
			q := url.Values{"category": {cat}, "limit": {"1000"}}
			if cursor != "" {
				q.Set("cursor", cursor)
			}
			var env bybitEnvelope[bybitInstrument]
			if err := c.c.getJSON(ctx, 1, c.base, "/v5/market/instruments-info", q, &env); err != nil {
				return nil, err
			}
			if env.RetCode != 0 {
				return nil, fmt.Errorf("bybit: instruments-info: retCode %d: %s", env.RetCode, env.RetMsg)
			}
			for _, in := range env.Result.List {
				switch cat {
				case "spot":
					out = append(out, Instrument{Venue: screener.VenueBybit, Kind: KindSpot, Symbol: in.Symbol,
						Base: in.BaseCoin, Quote: in.QuoteCoin, Tradable: in.Status == "Trading",
						TickSize: in.PriceFilter.TickSize, StepSize: in.LotSizeFilter.BasePrecision, MinNotional: in.LotSizeFilter.MinOrderAmt})
				case "linear":
					if in.ContractType != "LinearPerpetual" || in.SettleCoin != "USDT" {
						continue
					}
					out = append(out, Instrument{Venue: screener.VenueBybit, Kind: KindPerp, Symbol: in.Symbol,
						Base: in.BaseCoin, Quote: in.QuoteCoin, Tradable: in.Status == "Trading",
						TickSize: in.PriceFilter.TickSize, StepSize: in.LotSizeFilter.QtyStep, MinNotional: in.LotSizeFilter.MinNotionalValue,
						IntervalH: in.FundingInterval / 60})
				}
			}
			cursor = env.Result.NextPageCursor
			if cursor == "" {
				break
			}
		}
	}
	return out, nil
}

func (c *bybitCollector) Instruments(ctx context.Context) ([]Instrument, error) {
	return c.inst.refresh(ctx)
}

type bybitTicker struct {
	Symbol          string `json:"symbol"`
	Bid1Price       num    `json:"bid1Price"`
	Bid1Size        num    `json:"bid1Size"`
	Ask1Price       num    `json:"ask1Price"`
	Ask1Size        num    `json:"ask1Size"`
	MarkPrice       num    `json:"markPrice"`
	IndexPrice      num    `json:"indexPrice"`
	FundingRate     num    `json:"fundingRate"`
	NextFundingTime string `json:"nextFundingTime"`
}

func (c *bybitCollector) Spot(ctx context.Context) ([]screener.Quote, error) {
	inst, err := c.inst.ensure(ctx, KindSpot)
	if err != nil {
		return nil, err
	}
	var env bybitEnvelope[bybitTicker]
	if err := c.c.getJSON(ctx, 1, c.base, "/v5/market/tickers", url.Values{"category": {"spot"}}, &env); err != nil {
		return nil, err
	}
	if env.RetCode != 0 {
		return nil, fmt.Errorf("bybit: tickers: retCode %d: %s", env.RetCode, env.RetMsg)
	}
	at := c.now() // per-symbol timestamp absent in the bulk spot ticker
	out := make([]screener.Quote, 0, len(env.Result.List))
	for _, t := range env.Result.List {
		in, ok := inst[t.Symbol]
		if !ok || !in.Tradable || !t.Bid1Price.IsPositive() || !t.Ask1Price.IsPositive() {
			continue
		}
		out = append(out, screener.Quote{Venue: screener.VenueBybit, Base: in.Base, Quote: in.Quote,
			Bid: t.Bid1Price.Decimal, BidQty: t.Bid1Size.Decimal, Ask: t.Ask1Price.Decimal, AskQty: t.Ask1Size.Decimal, At: at})
	}
	return out, nil
}

func (c *bybitCollector) Perps(ctx context.Context) ([]screener.Perp, error) {
	inst, err := c.inst.ensure(ctx, KindPerp)
	if err != nil {
		return nil, err
	}
	var env bybitEnvelope[bybitTicker]
	if err := c.c.getJSON(ctx, 1, c.base, "/v5/market/tickers", url.Values{"category": {"linear"}}, &env); err != nil {
		return nil, err
	}
	if env.RetCode != 0 {
		return nil, fmt.Errorf("bybit: tickers: retCode %d: %s", env.RetCode, env.RetMsg)
	}
	at := c.now()
	if env.Time > 0 {
		at = time.UnixMilli(env.Time)
	}
	out := make([]screener.Perp, 0, len(env.Result.List))
	for _, t := range env.Result.List {
		in, ok := inst[t.Symbol]
		if !ok || !in.Tradable || !t.MarkPrice.IsPositive() {
			continue
		}
		var next time.Time
		if n, err := strconv.ParseInt(t.NextFundingTime, 10, 64); err == nil && n > 0 {
			next = time.UnixMilli(n)
		}
		out = append(out, screener.Perp{Venue: screener.VenueBybit, Base: in.Base, Quote: in.Quote,
			Mark: t.MarkPrice.Decimal, Index: t.IndexPrice.Decimal, Bid: t.Bid1Price.Decimal, Ask: t.Ask1Price.Decimal,
			FundingRate: t.FundingRate.Decimal, PredictedFundingRate: t.FundingRate.Decimal,
			IntervalH: in.IntervalH, NextFundingAt: next, At: at})
	}
	return out, nil
}

// Networks: GET /v5/asset/coin/query-info requires an API key
// (docs/v5/asset/coin-info, 2026-08-27).
func (c *bybitCollector) Networks(ctx context.Context) (map[string]screener.NetworkStatus, error) {
	return c.inst.keyGatedNetworks(ctx)
}
