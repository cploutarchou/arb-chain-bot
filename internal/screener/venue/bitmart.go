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

// BitMart collector (T-078, docs/research/venues/bitmart.md). Sources,
// all accessed 2026-08-27 (https://developer-pro.bitmart.com/en/spot/
// and /en/futuresv2/). Hosts: spot https://api-cloud.bitmart.com,
// futures https://api-cloud-v2.bitmart.com.
//   - spot quotes GET /spot/quotation/v3/tickers ("Get all trading
//     pairs with a volume greater than 0 within 24 hours",
//     10 times/2 sec per IP): data[] are POSITIONAL arrays [symbol,
//     last, v_24h, qv_24h, open_24h, high_24h, low_24h, fluctuation,
//     bid_px "top buy price", bid_sz "Size of top buy order", ask_px,
//     ask_sz, ts (ms)]. Only pairs that traded in 24 h appear
//     (documented), so a zero-volume tradable pair has no quote.
//   - spot instruments GET /spot/v1/symbols/details (12 times/2 sec):
//     symbol, base_currency, quote_currency, quote_increment ("The
//     minimum order quantity is also the minimum order quantity
//     increment" → StepSize), base_min_size, price_max_precision
//     ("Maximum price accuracy (decimal places)" → TickSize =
//     10^-p), min_buy_amount ("Minimum order amount" → MinNotional),
//     trade_status ("trading=is trading, pre-trade=pre-open").
//   - perp instruments + index/rates GET /contract/public/details
//     (12 times/2 sec; symbol omitted → all): product_type
//     ("1=perpetual, 2=futures"), base_currency, quote_currency,
//     index_price, contract_size, price_precision (a decimal string,
//     used as TickSize), vol_precision (StepSize), funding_rate
//     ("current funding rate"), expected_funding_rate,
//     funding_interval_hours (Int, "Funding interval"), status
//     ("-Trading -Delisted"). The live funding_time field is NOT in
//     the documented field table → unused; next settlement comes from
//     funding-rate-v2. Only USDT-quoted perpetuals are kept.
//   - bulk funding GET /contract/public/funding-rate-v2 ("current fund
//     rate of all trade pairs", 12 times/2 sec): rate_value ("Funding
//     rate of the previous period"), expected_rate ("Funding rate for
//     the next period"), funding_time ("Next funding settlement time",
//     ms) — live covers a subset of contracts; the rest keep details'
//     rates with NextFundingAt zero.
//   - mark price: no bulk endpoint. GET /contract/public/markprice-kline
//     ?symbol&step=1&start_time&end_time ("querying MarketPrice K-line
//     data", 12 times/2 sec) PER CONTRACT → last row's close_price,
//     fetched round-robin (FundingCallsPerPoll per Perps() call, the
//     rest carry their last-known mark; a contract with no mark yet has
//     Mark 0 + Index>0 — conformance class noBulkMark, like HTX). Perp
//     bid/ask exist only per-symbol (/contract/public/depth) → left 0.
//   - funding history (not polled): GET /contract/public/funding-rate-history
//     ?symbol&limit → funding_rate ("Actual funding rate"), funding_time (ms).
//   - networks GET /spot/v1/currencies (8 times/2 sec), PUBLIC:
//     deposit_enabled ("Whether this currency can be deposited on the
//     platform"), withdraw_enabled — platform-level, no chain detail.
//   - rate limits: "the speed of the public interface is limited
//     according to the IP … the 429 status will be returned"; "HTTP 429
//     … the IP will be blocked. HTTP 418 … the IP has been blocked
//     after error code 429." Gates: 4 req / 2 s per host.
//   - fees: UNVERIFIED (research §6 — official page shows futures "All
//     Users Maker 0.0400% / Taker 0.0600%" but spot classes read
//     "No Data" unless signed in) → placeholders spot taker 25 bps,
//     perp taker 6 bps, Verified=false.
type bitmartCollector struct {
	spotBase string
	perpBase string
	spotGate *gate
	perpGate *gate
	spot     *client
	perp     *client
	inst     *instrumentCache
	rr       *fundingRR // round-robin mark price (Rate = last-known mark)
	now      func() time.Time
}

const (
	bitmartSpotBase = "https://api-cloud.bitmart.com"
	bitmartPerpBase = "https://api-cloud-v2.bitmart.com"
)

func newBitMart(opts Options) *bitmartCollector {
	now := opts.now()
	c := &bitmartCollector{spotBase: bitmartSpotBase, perpBase: bitmartPerpBase, now: now,
		rr: newFundingRR(opts.fundingCalls())}
	if opts.SpotBase != "" {
		c.spotBase = opts.SpotBase
	}
	if opts.PerpBase != "" {
		c.perpBase = opts.PerpBase
	}
	c.spotGate = newGate(4, 2*time.Second, now)
	c.perpGate = newGate(4, 2*time.Second, now)
	c.spot = newClient(screener.VenueBitMart, c.spotGate)
	c.perp = newClient(screener.VenueBitMart, c.perpGate)
	c.inst = newInstrumentCache(opts, c.fetchInstruments)
	return c
}

func (c *bitmartCollector) ID() screener.Venue { return screener.VenueBitMart }
func (c *bitmartCollector) Fees() Fees {
	// UNVERIFIED placeholders — docs/research/venues/bitmart.md §6.
	return Fees{SpotTakerBps: bps("0.0025"), PerpTakerBps: bps("0.0006"), Verified: false}
}
func (c *bitmartCollector) RateLimited() int {
	return int(c.spotGate.limited.Load() + c.perpGate.limited.Load())
}

// bitmartEnvelope is {code: 1000, message, data, trace}; 1000 = success.
type bitmartEnvelope[T any] struct {
	Code    int64  `json:"code"`
	Message string `json:"message"`
	Data    T      `json:"data"`
}

func bitmartCheck(code int64, msg, path string) error {
	if code != 1000 {
		return fmt.Errorf("bitmart: %s: code %d: %s", path, code, msg)
	}
	return nil
}

type bitmartSpotSymbol struct {
	Symbol            string `json:"symbol"`
	BaseCurrency      string `json:"base_currency"`
	QuoteCurrency     string `json:"quote_currency"`
	QuoteIncrement    string `json:"quote_increment"`
	PriceMaxPrecision int32  `json:"price_max_precision"`
	MinBuyAmount      string `json:"min_buy_amount"`
	TradeStatus       string `json:"trade_status"`
}

type bitmartContract struct {
	Symbol              string `json:"symbol"`
	ProductType         int    `json:"product_type"`
	BaseCurrency        string `json:"base_currency"`
	QuoteCurrency       string `json:"quote_currency"`
	IndexPrice          num    `json:"index_price"`
	ContractSize        string `json:"contract_size"`
	PricePrecision      string `json:"price_precision"`
	VolPrecision        string `json:"vol_precision"`
	FundingRate         num    `json:"funding_rate"`
	ExpectedFundingRate num    `json:"expected_funding_rate"`
	FundingIntervalH    int    `json:"funding_interval_hours"`
	Status              string `json:"status"`
}

func (c *bitmartCollector) fetchContracts(ctx context.Context) ([]bitmartContract, error) {
	var env bitmartEnvelope[struct {
		Symbols []bitmartContract `json:"symbols"`
	}]
	if err := c.perp.getJSON(ctx, 1, c.perpBase, "/contract/public/details", nil, &env); err != nil {
		return nil, err
	}
	if err := bitmartCheck(env.Code, env.Message, "contract/details"); err != nil {
		return nil, err
	}
	return env.Data.Symbols, nil
}

func (c *bitmartCollector) fetchInstruments(ctx context.Context) ([]Instrument, error) {
	var spot bitmartEnvelope[struct {
		Symbols []bitmartSpotSymbol `json:"symbols"`
	}]
	if err := c.spot.getJSON(ctx, 1, c.spotBase, "/spot/v1/symbols/details", nil, &spot); err != nil {
		return nil, err
	}
	if err := bitmartCheck(spot.Code, spot.Message, "symbols/details"); err != nil {
		return nil, err
	}
	contracts, err := c.fetchContracts(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Instrument, 0, len(spot.Data.Symbols)+len(contracts))
	for _, s := range spot.Data.Symbols {
		out = append(out, Instrument{Venue: screener.VenueBitMart, Kind: KindSpot, Symbol: s.Symbol,
			Base: s.BaseCurrency, Quote: s.QuoteCurrency, Tradable: s.TradeStatus == "trading",
			TickSize: decimal.New(1, -s.PriceMaxPrecision).String(),
			StepSize: s.QuoteIncrement, MinNotional: s.MinBuyAmount})
	}
	for _, ct := range contracts {
		// USDT-quoted perpetuals only (product_type 1 = perpetual).
		if ct.ProductType != 1 || ct.QuoteCurrency != "USDT" {
			continue
		}
		out = append(out, Instrument{Venue: screener.VenueBitMart, Kind: KindPerp, Symbol: ct.Symbol,
			Base: ct.BaseCurrency, Quote: ct.QuoteCurrency, Tradable: ct.Status == "Trading",
			TickSize: ct.PricePrecision, StepSize: ct.VolPrecision, CtVal: ct.ContractSize,
			IntervalH: ct.FundingIntervalH})
	}
	return out, nil
}

func (c *bitmartCollector) Instruments(ctx context.Context) ([]Instrument, error) {
	return c.inst.refresh(ctx)
}

func (c *bitmartCollector) Spot(ctx context.Context) ([]screener.Quote, error) {
	inst, err := c.inst.ensure(ctx, KindSpot)
	if err != nil {
		return nil, err
	}
	// Positional rows (see package comment): [symbol, last, v_24h,
	// qv_24h, open_24h, high_24h, low_24h, fluctuation, bid_px, bid_sz,
	// ask_px, ask_sz, ts].
	var env bitmartEnvelope[[][]string]
	if err := c.spot.getJSON(ctx, 1, c.spotBase, "/spot/quotation/v3/tickers", nil, &env); err != nil {
		return nil, err
	}
	if err := bitmartCheck(env.Code, env.Message, "v3/tickers"); err != nil {
		return nil, err
	}
	recv := c.now()
	out := make([]screener.Quote, 0, len(env.Data))
	for _, row := range env.Data {
		if len(row) < 13 {
			continue
		}
		in, ok := inst[row[0]]
		if !ok || !in.Tradable {
			continue
		}
		bid, err1 := decimal.NewFromString(row[8])
		bidSz, err2 := decimal.NewFromString(row[9])
		ask, err3 := decimal.NewFromString(row[10])
		askSz, err4 := decimal.NewFromString(row[11])
		if err1 != nil || err2 != nil || err3 != nil || err4 != nil || !bid.IsPositive() || !ask.IsPositive() {
			continue
		}
		at := recv
		if ms, err := strconv.ParseInt(row[12], 10, 64); err == nil && ms > 0 {
			at = time.UnixMilli(ms)
		}
		out = append(out, screener.Quote{Venue: screener.VenueBitMart, Base: in.Base, Quote: in.Quote,
			Bid: bid, BidQty: bidSz, Ask: ask, AskQty: askSz, At: at})
	}
	return out, nil
}

type bitmartFundingRow struct {
	Symbol       string `json:"symbol"`
	RateValue    num    `json:"rate_value"`
	ExpectedRate num    `json:"expected_rate"`
	FundingTime  int64  `json:"funding_time"`
}

func (c *bitmartCollector) Perps(ctx context.Context) ([]screener.Perp, error) {
	inst, err := c.inst.ensure(ctx, KindPerp)
	if err != nil {
		return nil, err
	}
	contracts, err := c.fetchContracts(ctx)
	if err != nil {
		return nil, err
	}
	var fund bitmartEnvelope[struct {
		List []bitmartFundingRow `json:"list"`
	}]
	if err := c.perp.getJSON(ctx, 1, c.perpBase, "/contract/public/funding-rate-v2", nil, &fund); err != nil {
		return nil, err
	}
	if err := bitmartCheck(fund.Code, fund.Message, "funding-rate-v2"); err != nil {
		return nil, err
	}
	fundBy := make(map[string]bitmartFundingRow, len(fund.Data.List))
	for _, f := range fund.Data.List {
		fundBy[f.Symbol] = f
	}
	// Round-robin mark price: FundingCallsPerPoll contracts per poll via
	// markprice-kline; the rest carry their last-known mark.
	symbols := make([]string, 0, len(inst))
	for s, in := range inst {
		if in.Tradable {
			symbols = append(symbols, s)
		}
	}
	nowT := c.now()
	for _, s := range c.rr.pick(symbols) {
		var k bitmartEnvelope[[]struct {
			ClosePrice num   `json:"close_price"`
			Timestamp  int64 `json:"timestamp"`
		}]
		q := url.Values{"symbol": {s}, "step": {"1"},
			"start_time": {strconv.FormatInt(nowT.Add(-3*time.Minute).Unix(), 10)},
			"end_time":   {strconv.FormatInt(nowT.Unix(), 10)}}
		if err := c.perp.getJSON(ctx, 1, c.perpBase, "/contract/public/markprice-kline", q, &k); err != nil {
			return nil, err
		}
		if err := bitmartCheck(k.Code, k.Message, "markprice-kline"); err != nil {
			return nil, err
		}
		if n := len(k.Data); n > 0 && k.Data[n-1].ClosePrice.IsPositive() {
			c.rr.set(s, fundingInfo{Rate: k.Data[n-1].ClosePrice.Decimal, At: nowT})
		}
	}
	out := make([]screener.Perp, 0, len(contracts))
	for _, ct := range contracts {
		in, ok := inst[ct.Symbol]
		if !ok || !in.Tradable || !ct.IndexPrice.IsPositive() {
			continue
		}
		p := screener.Perp{Venue: screener.VenueBitMart, Base: in.Base, Quote: in.Quote,
			Index:       ct.IndexPrice.Decimal,
			FundingRate: ct.FundingRate.Decimal, PredictedFundingRate: ct.ExpectedFundingRate.Decimal,
			IntervalH: ct.FundingIntervalH, At: nowT}
		if f, ok := fundBy[ct.Symbol]; ok {
			p.FundingRate = f.RateValue.Decimal
			p.PredictedFundingRate = f.ExpectedRate.Decimal
			if f.FundingTime > 0 {
				p.NextFundingAt = time.UnixMilli(f.FundingTime)
			}
		}
		if mk, ok := c.rr.get(ct.Symbol); ok {
			p.Mark = mk.Rate // last-known mark (see package comment)
		}
		out = append(out, p)
	}
	return out, nil
}

// Networks: public platform-level deposit/withdraw flags per currency
// (research §4) — no chain granularity is published.
func (c *bitmartCollector) Networks(ctx context.Context) (map[string]screener.NetworkStatus, error) {
	var env bitmartEnvelope[struct {
		Currencies []struct {
			ID              string `json:"id"`
			WithdrawEnabled bool   `json:"withdraw_enabled"`
			DepositEnabled  bool   `json:"deposit_enabled"`
		} `json:"currencies"`
	}]
	if err := c.spot.getJSON(ctx, 1, c.spotBase, "/spot/v1/currencies", nil, &env); err != nil {
		return nil, err
	}
	if err := bitmartCheck(env.Code, env.Message, "currencies"); err != nil {
		return nil, err
	}
	out := make(map[string]screener.NetworkStatus, len(env.Data.Currencies))
	for _, cur := range env.Data.Currencies {
		switch {
		case cur.DepositEnabled && cur.WithdrawEnabled:
			out[cur.ID] = screener.NetworkStatus{Status: screener.NetworkOpen}
		case !cur.DepositEnabled && !cur.WithdrawEnabled:
			out[cur.ID] = screener.NetworkStatus{Status: screener.NetworkClosed, Reason: "deposit+withdraw disabled"}
		case !cur.DepositEnabled:
			out[cur.ID] = screener.NetworkStatus{Status: screener.NetworkClosed, Reason: "deposit disabled"}
		default:
			out[cur.ID] = screener.NetworkStatus{Status: screener.NetworkClosed, Reason: "withdraw disabled"}
		}
	}
	return out, nil
}
