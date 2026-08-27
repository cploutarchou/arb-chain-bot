package venue

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/screener"
)

// Kraken collector (T-075, docs/research/venues/kraken.md). Sources, all
// accessed 2026-08-27:
//   - spot GET /0/public/Ticker?assetVersion=1 (api.kraken.com), pair
//     omitted → all pairs; result keyed by pair; a = ask [price, whole
//     lot volume, lot volume], b = bid [same]; no timestamp
//     (VERIFIED-ABSENT). assetVersion=1 "controls whether response keys
//     and asset identifier fields use Kraken's internal names or display
//     names" — display names ("BTC/USD", base "BTC") are used so no
//     X/Z-prefix or XBT alias table is needed.
//     https://docs.kraken.com/api/docs/rest-api/get-ticker-information
//   - spot GET /0/public/AssetPairs?assetVersion=1: base, quote, status
//     online|cancel_only|post_only|limit_only|reduce_only, tick_size,
//     lot_decimals, costmin
//     https://docs.kraken.com/api/docs/rest-api/get-tradable-asset-pairs
//   - spot public rate limit: "1 per second (or less)" per IP, excess
//     restricted for a few seconds
//     https://support.kraken.com/hc/en-us/articles/206548367-What-are-the-API-rate-limits
//   - futures GET /derivatives/api/v3/instruments (futures.kraken.com):
//     symbol, type flexible_futures|futures_inverse|…, tradeable,
//     tickSize, contractSize, contractValueTradePrecision, base, quote,
//     pair; public
//     https://docs.kraken.com/api/docs/futures-api/trading/get-instruments
//   - futures GET /derivatives/api/v3/tickers: symbol, tag perpetual|
//     month|quarter|…, bid, bidSize, ask, askSize, markPrice, indexPrice,
//     fundingRate ("current absolute funding rate"), fundingRatePrediction,
//     suspended; serverTime; bare JSON numbers parsed from their text
//     https://docs.kraken.com/api/docs/futures-api/trading/get-tickers
//   - futures GET /derivatives/api/v4/historicalfundingrates?symbol=…
//     PER SYMBOL: rates[] {timestamp, fundingRate, relativeFundingRate}
//     (live: one row per hour; the last row is the current hour).
//   - funding period: "settlement every 1 hour"; absolute rate = funding
//     per 1 contract unit for 1 hour; relative rate = absolute "relative
//     to the spot price at the time of funding rate calculation"
//     https://support.kraken.com/articles/4844359082772-linear-multi-collateral-derivatives-contract-specifications
//   - futures public endpoints "do not have a cost and therefore do not
//     count against any rate limiting budget"
//     https://docs.kraken.com/api/docs/guides/futures-rate-limits
//   - currency/chain status: no public endpoint (DepositMethods /
//     DepositStatus are private) → "unknown (venue requires API key)".
//   - fees, VERIFIED (https://www.kraken.com/features/fee-schedule):
//     Kraken Pro spot Tier 1 "$0+, 0.40 %, 0.80 %"; futures Tier 1
//     "< $5M, 0.02 %, 0.05 %" → Verified=true.
//
// Kraken Futures perpetuals (PF_*) are USD-quoted multi-collateral
// contracts — there is no USDT-margined perp; Quote is "USD" as the
// instrument list says. FundingRate is the venue's relative rate: the
// exact relativeFundingRate from the history endpoint when the
// round-robin has fetched it, else absolute ÷ index (the venue's own
// definition, evaluated with the current index); PredictedFundingRate
// is fundingRatePrediction ÷ index; IntervalH is 1.
type krakenCollector struct {
	spotBase string
	perpBase string
	spotGate *gate
	perpGate *gate
	spot     *client
	perp     *client
	inst     *instrumentCache
	rr       *fundingRR
	now      func() time.Time
}

const (
	krakenSpotBase = "https://api.kraken.com"
	krakenPerpBase = "https://futures.kraken.com"
)

func newKraken(opts Options) *krakenCollector {
	now := opts.now()
	c := &krakenCollector{spotBase: krakenSpotBase, perpBase: krakenPerpBase, now: now, rr: newFundingRR(opts.fundingCalls())}
	if opts.SpotBase != "" {
		c.spotBase = opts.SpotBase
	}
	if opts.PerpBase != "" {
		c.perpBase = opts.PerpBase
	}
	c.spotGate = newGate(1, time.Second, now)
	c.perpGate = newGate(10, time.Second, now)
	c.spot = newClient(screener.VenueKraken, c.spotGate)
	c.perp = newClient(screener.VenueKraken, c.perpGate)
	c.inst = newInstrumentCache(opts, c.fetchInstruments)
	return c
}

func (c *krakenCollector) ID() screener.Venue { return screener.VenueKraken }
func (c *krakenCollector) Fees() Fees {
	return Fees{SpotTakerBps: bps("0.008"), PerpTakerBps: bps("0.0005"), Verified: true}
}
func (c *krakenCollector) RateLimited() int {
	return int(c.spotGate.limited.Load() + c.perpGate.limited.Load())
}

type krakenEnvelope[T any] struct {
	Error  []string `json:"error"`
	Result T        `json:"result"`
}

func krakenCheck(errs []string, path string) error {
	if len(errs) > 0 {
		return fmt.Errorf("kraken: %s: %v", path, errs)
	}
	return nil
}

type krakenPair struct {
	Altname     string `json:"altname"`
	Base        string `json:"base"`
	Quote       string `json:"quote"`
	Status      string `json:"status"`
	TickSize    string `json:"tick_size"`
	LotDecimals int    `json:"lot_decimals"`
	CostMin     string `json:"costmin"`
}

type krakenFutEnvelope struct {
	Result      string `json:"result"`
	Error       string `json:"error"`
	ServerTime  string `json:"serverTime"`
	Instruments []struct {
		Symbol       string `json:"symbol"`
		Type         string `json:"type"`
		Tradeable    bool   `json:"tradeable"`
		TickSize     num    `json:"tickSize"`
		ContractSize num    `json:"contractSize"`
		Base         string `json:"base"`
		Quote        string `json:"quote"`
	} `json:"instruments"`
	Tickers []krakenFutTicker `json:"tickers"`
	Rates   []struct {
		Timestamp           string `json:"timestamp"`
		FundingRate         num    `json:"fundingRate"`
		RelativeFundingRate num    `json:"relativeFundingRate"`
	} `json:"rates"`
}

type krakenFutTicker struct {
	Symbol                string `json:"symbol"`
	Tag                   string `json:"tag"`
	Bid                   num    `json:"bid"`
	BidSize               num    `json:"bidSize"`
	Ask                   num    `json:"ask"`
	AskSize               num    `json:"askSize"`
	MarkPrice             num    `json:"markPrice"`
	IndexPrice            num    `json:"indexPrice"`
	FundingRate           num    `json:"fundingRate"`
	FundingRatePrediction num    `json:"fundingRatePrediction"`
	Suspended             bool   `json:"suspended"`
}

func (e krakenFutEnvelope) check(path string) error {
	if e.Result != "success" {
		return fmt.Errorf("kraken futures: %s: %s", path, e.Error)
	}
	return nil
}

func (c *krakenCollector) fetchInstruments(ctx context.Context) ([]Instrument, error) {
	var pairs krakenEnvelope[map[string]krakenPair]
	if err := c.spot.getJSON(ctx, 1, c.spotBase, "/0/public/AssetPairs", url.Values{"assetVersion": {"1"}}, &pairs); err != nil {
		return nil, err
	}
	if err := krakenCheck(pairs.Error, "AssetPairs"); err != nil {
		return nil, err
	}
	var ins krakenFutEnvelope
	if err := c.perp.getJSON(ctx, 1, c.perpBase, "/derivatives/api/v3/instruments", nil, &ins); err != nil {
		return nil, err
	}
	if err := ins.check("instruments"); err != nil {
		return nil, err
	}
	// The perpetual/dated distinction is the tickers' tag field, not on
	// the instrument list.
	var tk krakenFutEnvelope
	if err := c.perp.getJSON(ctx, 1, c.perpBase, "/derivatives/api/v3/tickers", nil, &tk); err != nil {
		return nil, err
	}
	if err := tk.check("tickers"); err != nil {
		return nil, err
	}
	perpetual := make(map[string]bool, len(tk.Tickers))
	for _, t := range tk.Tickers {
		perpetual[t.Symbol] = t.Tag == "perpetual"
	}
	out := make([]Instrument, 0, len(pairs.Result)+len(ins.Instruments))
	for key, p := range pairs.Result {
		out = append(out, Instrument{Venue: screener.VenueKraken, Kind: KindSpot, Symbol: key,
			Base: p.Base, Quote: p.Quote, Tradable: p.Status == "online",
			TickSize: p.TickSize, StepSize: decimal.New(1, int32(-p.LotDecimals)).String(), MinNotional: p.CostMin})
	}
	for _, in := range ins.Instruments {
		if in.Type != "flexible_futures" || !perpetual[in.Symbol] {
			continue
		}
		out = append(out, Instrument{Venue: screener.VenueKraken, Kind: KindPerp, Symbol: in.Symbol,
			Base: in.Base, Quote: in.Quote, Tradable: in.Tradeable,
			TickSize: in.TickSize.String(), CtVal: in.ContractSize.String(), IntervalH: 1})
	}
	return out, nil
}

func (c *krakenCollector) Instruments(ctx context.Context) ([]Instrument, error) {
	return c.inst.refresh(ctx)
}

type krakenTicker struct {
	A []num `json:"a"` // [price, whole lot volume, lot volume]
	B []num `json:"b"`
}

func (c *krakenCollector) Spot(ctx context.Context) ([]screener.Quote, error) {
	inst, err := c.inst.ensure(ctx, KindSpot)
	if err != nil {
		return nil, err
	}
	var env krakenEnvelope[map[string]krakenTicker]
	if err := c.spot.getJSON(ctx, 1, c.spotBase, "/0/public/Ticker", url.Values{"assetVersion": {"1"}}, &env); err != nil {
		return nil, err
	}
	if err := krakenCheck(env.Error, "Ticker"); err != nil {
		return nil, err
	}
	at := c.now() // VERIFIED-ABSENT: no timestamp in Ticker
	out := make([]screener.Quote, 0, len(env.Result))
	for key, t := range env.Result {
		in, ok := inst[key]
		if !ok || !in.Tradable || len(t.A) < 3 || len(t.B) < 3 || !t.B[0].IsPositive() || !t.A[0].IsPositive() {
			continue
		}
		out = append(out, screener.Quote{Venue: screener.VenueKraken, Base: in.Base, Quote: in.Quote,
			Bid: t.B[0].Decimal, BidQty: t.B[2].Decimal, Ask: t.A[0].Decimal, AskQty: t.A[2].Decimal, At: at})
	}
	return out, nil
}

func (c *krakenCollector) Perps(ctx context.Context) ([]screener.Perp, error) {
	inst, err := c.inst.ensure(ctx, KindPerp)
	if err != nil {
		return nil, err
	}
	var tk krakenFutEnvelope
	if err := c.perp.getJSON(ctx, 1, c.perpBase, "/derivatives/api/v3/tickers", nil, &tk); err != nil {
		return nil, err
	}
	if err := tk.check("tickers"); err != nil {
		return nil, err
	}
	at := c.now()
	if t, err := time.Parse(time.RFC3339Nano, tk.ServerTime); err == nil {
		at = t
	}
	next := at.Truncate(time.Hour).Add(time.Hour) // hourly settlement
	// Round-robin exact relative funding from the history endpoint.
	symbols := make([]string, 0, len(inst))
	for s, in := range inst {
		if in.Tradable {
			symbols = append(symbols, s)
		}
	}
	for _, s := range c.rr.pick(symbols) {
		var h krakenFutEnvelope
		if err := c.perp.getJSON(ctx, 1, c.perpBase, "/derivatives/api/v4/historicalfundingrates", url.Values{"symbol": {s}}, &h); err != nil {
			return nil, err
		}
		if err := h.check("historicalfundingrates"); err != nil {
			return nil, err
		}
		if n := len(h.Rates); n > 0 {
			last := h.Rates[n-1]
			fi := fundingInfo{Rate: last.RelativeFundingRate.Decimal, IntervalH: 1, At: c.now()}
			if ts, err := time.Parse(time.RFC3339Nano, last.Timestamp); err == nil {
				fi.NextAt = ts
			}
			c.rr.set(s, fi)
		}
	}
	out := make([]screener.Perp, 0, len(tk.Tickers))
	for _, t := range tk.Tickers {
		in, ok := inst[t.Symbol]
		if !ok || !in.Tradable || t.Suspended || !t.MarkPrice.IsPositive() {
			continue
		}
		p := screener.Perp{Venue: screener.VenueKraken, Base: in.Base, Quote: in.Quote,
			Mark: t.MarkPrice.Decimal, Index: t.IndexPrice.Decimal, Bid: t.Bid.Decimal, Ask: t.Ask.Decimal,
			IntervalH: 1, NextFundingAt: next, At: at}
		if t.IndexPrice.IsPositive() {
			p.FundingRate = t.FundingRate.Div(t.IndexPrice.Decimal)
			p.PredictedFundingRate = t.FundingRatePrediction.Div(t.IndexPrice.Decimal)
		}
		if fi, ok := c.rr.get(t.Symbol); ok && !fi.NextAt.IsZero() && at.Sub(fi.NextAt) < time.Hour {
			p.FundingRate = fi.Rate // exact relative rate for the current hour
		}
		out = append(out, p)
	}
	return out, nil
}

// Networks: Kraken's deposit-method/status endpoints are private
// (docs.kraken.com/api/docs/rest-api/get-deposit-methods, 2026-08-27).
func (c *krakenCollector) Networks(ctx context.Context) (map[string]screener.NetworkStatus, error) {
	return c.inst.keyGatedNetworks(ctx)
}
