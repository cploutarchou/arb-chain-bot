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

// OKX collector. Sources (https://www.okx.com/docs-v5/en/, accessed
// 2026-08-27):
//   - GET /api/v5/market/tickers?instType=SPOT|SWAP, 20 req/2 s per IP;
//     fields instId, bidPx, bidSz, askPx, askSz, last, ts
//     (#public-data-rest-api-get-tickers). Mark price is NOT in tickers.
//   - GET /api/v5/public/instruments?instType=SPOT|SWAP, 20 req/2 s;
//     baseCcy/quoteCcy (spot), ctVal/ctValCcy/ctType/settleCcy (swap),
//     tickSz, lotSz, minSz, state live|suspend|preopen|test|...
//     (#public-data-rest-api-get-instruments)
//   - GET /api/v5/public/mark-price?instType=SWAP, 10 req/2 s; instId,
//     markPx, ts (#public-data-rest-api-get-mark-price)
//   - GET /api/v5/public/funding-rate?instId=…, 10 req/2 s, PER
//     INSTRUMENT; fundingRate, nextFundingRate, fundingTime,
//     nextFundingTime (#public-data-rest-api-get-funding-rate). The
//     funding interval is derived as nextFundingTime − fundingTime (no
//     dedicated field; OKX runs 8h/4h/2h/1h per instrument).
//   - currency/chain status: /api/v5/asset/currencies requires a key.
//   - fees: UNVERIFIED from the primary fee page (did not render);
//     research doc §2.6 cites Lv1 taker 0.10 % spot / 0.05 % perp via
//     aggregators → Verified=false.
//
// One venue gate at 5 req/s (the strictest documented endpoint is
// 10 req/2 s) covers every path; a poll issues 1 (spot) + 2 (swap
// tickers + mark) + N funding calls.
type okxCollector struct {
	base string
	gate *gate
	c    *client
	inst *instrumentCache
	rr   *fundingRR
	now  func() time.Time
}

const okxBase = "https://www.okx.com"

func newOKX(opts Options) *okxCollector {
	now := opts.now()
	c := &okxCollector{base: okxBase, now: now, rr: newFundingRR(opts.fundingCalls())}
	if opts.SpotBase != "" {
		c.base = opts.SpotBase
	}
	c.gate = newGate(10, 2*time.Second, now)
	c.c = newClient(screener.VenueOKX, c.gate)
	c.inst = newInstrumentCache(opts, c.fetchInstruments)
	return c
}

func (c *okxCollector) ID() screener.Venue { return screener.VenueOKX }
func (c *okxCollector) Fees() Fees {
	return Fees{SpotTakerBps: bps("0.001"), PerpTakerBps: bps("0.0005"), Verified: false}
}
func (c *okxCollector) RateLimited() int { return int(c.gate.limited.Load()) }

type okxEnvelope[T any] struct {
	Code string `json:"code"`
	Msg  string `json:"msg"`
	Data []T    `json:"data"`
}

func okxCheck(code, msg, path string) error {
	if code != "0" {
		return fmt.Errorf("okx: %s: code %s: %s", path, code, msg)
	}
	return nil
}

type okxInstrument struct {
	InstType  string `json:"instType"`
	InstID    string `json:"instId"`
	BaseCcy   string `json:"baseCcy"`
	QuoteCcy  string `json:"quoteCcy"`
	SettleCcy string `json:"settleCcy"`
	CtVal     string `json:"ctVal"`
	CtValCcy  string `json:"ctValCcy"`
	CtType    string `json:"ctType"`
	TickSz    string `json:"tickSz"`
	LotSz     string `json:"lotSz"`
	MinSz     string `json:"minSz"`
	State     string `json:"state"`
}

func (c *okxCollector) fetchInstruments(ctx context.Context) ([]Instrument, error) {
	var out []Instrument
	for _, it := range []string{"SPOT", "SWAP"} {
		var env okxEnvelope[okxInstrument]
		if err := c.c.getJSON(ctx, 1, c.base, "/api/v5/public/instruments", url.Values{"instType": {it}}, &env); err != nil {
			return nil, err
		}
		if err := okxCheck(env.Code, env.Msg, "instruments"); err != nil {
			return nil, err
		}
		for _, in := range env.Data {
			switch it {
			case "SPOT":
				out = append(out, Instrument{Venue: screener.VenueOKX, Kind: KindSpot, Symbol: in.InstID,
					Base: in.BaseCcy, Quote: in.QuoteCcy, Tradable: in.State == "live",
					TickSize: in.TickSz, StepSize: in.LotSz})
			case "SWAP":
				// Linear USDT-settled only: base is the contract-value
				// currency (ctValCcy), quote the settlement currency.
				if in.CtType != "linear" || in.SettleCcy != "USDT" || in.CtValCcy == "" {
					continue
				}
				out = append(out, Instrument{Venue: screener.VenueOKX, Kind: KindPerp, Symbol: in.InstID,
					Base: in.CtValCcy, Quote: in.SettleCcy, Tradable: in.State == "live",
					TickSize: in.TickSz, StepSize: in.LotSz, CtVal: in.CtVal})
			}
		}
	}
	return out, nil
}

func (c *okxCollector) Instruments(ctx context.Context) ([]Instrument, error) {
	return c.inst.refresh(ctx)
}

type okxTicker struct {
	InstID string `json:"instId"`
	Last   num    `json:"last"`
	AskPx  num    `json:"askPx"`
	AskSz  num    `json:"askSz"`
	BidPx  num    `json:"bidPx"`
	BidSz  num    `json:"bidSz"`
	Ts     string `json:"ts"`
}

func okxTime(ms string, fallback time.Time) time.Time {
	if n, err := strconv.ParseInt(ms, 10, 64); err == nil && n > 0 {
		return time.UnixMilli(n)
	}
	return fallback
}

func (c *okxCollector) Spot(ctx context.Context) ([]screener.Quote, error) {
	inst, err := c.inst.ensure(ctx, KindSpot)
	if err != nil {
		return nil, err
	}
	var env okxEnvelope[okxTicker]
	if err := c.c.getJSON(ctx, 1, c.base, "/api/v5/market/tickers", url.Values{"instType": {"SPOT"}}, &env); err != nil {
		return nil, err
	}
	if err := okxCheck(env.Code, env.Msg, "tickers"); err != nil {
		return nil, err
	}
	recv := c.now()
	out := make([]screener.Quote, 0, len(env.Data))
	for _, t := range env.Data {
		in, ok := inst[t.InstID]
		if !ok || !in.Tradable || !t.BidPx.IsPositive() || !t.AskPx.IsPositive() {
			continue
		}
		out = append(out, screener.Quote{Venue: screener.VenueOKX, Base: in.Base, Quote: in.Quote,
			Bid: t.BidPx.Decimal, BidQty: t.BidSz.Decimal, Ask: t.AskPx.Decimal, AskQty: t.AskSz.Decimal, At: okxTime(t.Ts, recv)})
	}
	return out, nil
}

type okxMark struct {
	InstID string `json:"instId"`
	MarkPx num    `json:"markPx"`
	Ts     string `json:"ts"`
}

type okxFunding struct {
	InstID          string `json:"instId"`
	FundingRate     num    `json:"fundingRate"`
	NextFundingRate string `json:"nextFundingRate"` // "" when not published
	FundingTime     string `json:"fundingTime"`
	NextFundingTime string `json:"nextFundingTime"`
}

func (c *okxCollector) Perps(ctx context.Context) ([]screener.Perp, error) {
	inst, err := c.inst.ensure(ctx, KindPerp)
	if err != nil {
		return nil, err
	}
	var tick okxEnvelope[okxTicker]
	if err := c.c.getJSON(ctx, 1, c.base, "/api/v5/market/tickers", url.Values{"instType": {"SWAP"}}, &tick); err != nil {
		return nil, err
	}
	if err := okxCheck(tick.Code, tick.Msg, "tickers"); err != nil {
		return nil, err
	}
	var marks okxEnvelope[okxMark]
	if err := c.c.getJSON(ctx, 1, c.base, "/api/v5/public/mark-price", url.Values{"instType": {"SWAP"}}, &marks); err != nil {
		return nil, err
	}
	if err := okxCheck(marks.Code, marks.Msg, "mark-price"); err != nil {
		return nil, err
	}
	markBy := make(map[string]okxMark, len(marks.Data))
	for _, m := range marks.Data {
		markBy[m.InstID] = m
	}
	// Round-robin funding: N instruments per poll, last-known for the rest.
	symbols := make([]string, 0, len(inst))
	for s, in := range inst {
		if in.Tradable {
			symbols = append(symbols, s)
		}
	}
	for _, s := range c.rr.pick(symbols) {
		var f okxEnvelope[okxFunding]
		if err := c.c.getJSON(ctx, 1, c.base, "/api/v5/public/funding-rate", url.Values{"instId": {s}}, &f); err != nil {
			return nil, err
		}
		if err := okxCheck(f.Code, f.Msg, "funding-rate"); err != nil {
			return nil, err
		}
		for _, r := range f.Data {
			fi := fundingInfo{Rate: r.FundingRate.Decimal, At: c.now(),
				NextAt: okxTime(r.FundingTime, time.Time{})}
			fi.IntervalH = hoursBetween(fi.NextAt, okxTime(r.NextFundingTime, time.Time{}))
			if r.NextFundingRate != "" {
				if d, err := decimal.NewFromString(r.NextFundingRate); err == nil {
					fi.Predicted = d
				}
			}
			c.rr.set(r.InstID, fi)
		}
	}
	recv := c.now()
	out := make([]screener.Perp, 0, len(tick.Data))
	for _, t := range tick.Data {
		in, ok := inst[t.InstID]
		if !ok || !in.Tradable {
			continue
		}
		m, ok := markBy[t.InstID]
		if !ok || !m.MarkPx.IsPositive() {
			continue
		}
		p := screener.Perp{Venue: screener.VenueOKX, Base: in.Base, Quote: in.Quote,
			Mark: m.MarkPx.Decimal, Bid: t.BidPx.Decimal, Ask: t.AskPx.Decimal, At: okxTime(t.Ts, recv)}
		// VERIFIED-ABSENT: OKX publishes no index price on tickers or
		// mark-price; Index stays zero.
		if fi, ok := c.rr.get(t.InstID); ok {
			p.FundingRate, p.PredictedFundingRate, p.IntervalH, p.NextFundingAt = fi.Rate, fi.Predicted, fi.IntervalH, fi.NextAt
		}
		out = append(out, p)
	}
	return out, nil
}

// Networks: GET /api/v5/asset/currencies requires an API key
// (docs-v5 Funding Account, 2026-08-27).
func (c *okxCollector) Networks(ctx context.Context) (map[string]screener.NetworkStatus, error) {
	return c.inst.keyGatedNetworks(ctx)
}
