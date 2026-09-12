package venue

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/screener"
)

// Phemex collector. Research: docs/research/venues/phemex.md, every
// endpoint live-verified 2026-09-13; docs at phemex-docs.github.io.
// Spot + USDT-M perpetuals; COIN-M perps (BTCUSD and friends, settle
// BTC) are filtered out by settleCurrency.
//   - GET /public/products: currencies[] {currency, valueScale, status},
//     products[] {symbol "sBTCUSDT", type Spot|Perpetual, base/quoteCurrency,
//     status Listed|Delisted, priceScale, quoteTickSizeEv, minOrderValueEv,
//     defaultTakerFee "0.001"} and perpProductsV2[] {symbol "BTCUSDT",
//     settleCurrency, tickSize "0.1", qtyStepSize, minOrderValueRv,
//     fundingInterval (s — 14 400 or 28 800 on the recorded rows)}.
//     The one products[] row with type Perpetual is COIN-M and is not a
//     spot instrument (asserted by the fixture test).
//   - Scaled integers: *Ep price fields divide by 10^priceScale of the
//     product; *Ev value fields by 10^valueScale of the currency
//     (recorded: sBTCUSDT priceScale 8 → bidEp 7722347000000 is
//     77223.47; USDT valueScale 8 → minOrderValueEv 100000000 is
//     1 USDT).
//   - GET /md/spot/ticker/24hr/all: result[] {symbol, bidEp, askEp, …,
//     timestamp (ns)} — every spot ticker in ONE request, no sizes →
//     quotes carry LiquidityUnknown (Gate precedent; the per-symbol
//     /md/spot/orderbook is the depth source when sizes are needed).
//   - GET /md/v3/ticker/24hr/all: result[] {symbol, bidRp/askRp/
//     markRp/indexRp/fundingRateRr/predFundingRateRr (JSON strings),
//     timestamp (ns)} — every USDT-M perp in ONE request. The perp
//     ticker publishes no next-funding time and no sizes: NextFundingAt
//     stays zero and perp BidQty/AskQty stay zero (LIQUIDITY_UNKNOWN,
//     per the Perp policy in types.go).
//   - Funding history (funding-rate-history?symbol=<fundingRate8hSymbol>)
//     is not polled: the current + predicted rates ride on the ticker.
//   - rate limits: 5 000 requests / 5 min per IP, group caps 500/min →
//     a conservative 10 req/s venue gate; the collector spends 2-3
//     requests per poll.
//   - fees: spot VIP0 0.1000 % VERIFIED from the API itself
//     (defaultTakerFee "0.001" on every recorded spot product) and
//     perp VIP0 taker 0.06 % from the official help centre
//     (phemex.com/help-center/phemex-trading-fee-structure).
//   - spot symbols carry a leading "s" (sBTCUSDT); the instrument id
//     strips it. Networks: no public deposit/withdraw status exists →
//     key-gated unknowns.
type phemexCollector struct {
	base string
	gate *gate
	c    *client
	inst *instrumentCache
	now  func() time.Time

	// scalesMu guards the spot symbol → priceScale side table, rebuilt
	// by fetchInstruments (the shared instrumentCache stores no venue
	// scaling metadata).
	scalesMu sync.RWMutex
	scales   map[string]int64
}

const phemexBase = "https://api.phemex.com"

func newPhemex(opts Options) *phemexCollector {
	now := opts.now()
	c := &phemexCollector{base: phemexBase, now: now, scales: map[string]int64{}}
	if opts.SpotBase != "" {
		c.base = opts.SpotBase
	}
	c.gate = newGate(10, time.Second, now)
	c.c = newClient(screener.VenuePhemex, c.gate)
	c.inst = newInstrumentCache(opts, c.fetchInstruments)
	return c
}

func (c *phemexCollector) ID() screener.Venue { return screener.VenuePhemex }
func (c *phemexCollector) Fees() Fees {
	return Fees{SpotTakerBps: bps("0.001"), PerpTakerBps: bps("0.0006"), Verified: true}
}
func (c *phemexCollector) RateLimited() int { return int(c.gate.limited.Load()) }

type phemexProducts struct {
	Currencies []struct {
		Currency   string `json:"currency"`
		ValueScale int64  `json:"valueScale"`
	} `json:"currencies"`
	Products []struct {
		Symbol          string `json:"symbol"` // sBTCUSDT
		Type            string `json:"type"`   // Spot | Perpetual (COIN-M)
		Base            string `json:"baseCurrency"`
		Quote           string `json:"quoteCurrency"`
		Status          string `json:"status"` // Listed | Delisted
		PriceScale      int64  `json:"priceScale"`
		QuoteTickSizeEv int64  `json:"quoteTickSizeEv"`
		MinOrderValueEv int64  `json:"minOrderValueEv"`
	} `json:"products"`
	PerpV2 []struct {
		Symbol           string `json:"symbol"` // BTCUSDT
		Base             string `json:"baseCurrency"`
		Quote            string `json:"quoteCurrency"`
		Settle           string `json:"settleCurrency"`
		Status           string `json:"status"`
		TickSize         num    `json:"tickSize"`        // "0.1"
		QtyStepSize      num    `json:"qtyStepSize"`     // "0.001"
		MinOrderValueRv  num    `json:"minOrderValueRv"` // "1"
		FundingIntervalS num    `json:"fundingInterval"` // 14400 | 28800
	} `json:"perpProductsV2"`
}

// pow10 returns 10^exp for a venue scale exponent, refusing anything
// outside -18..18 (decimal.New's int32 exponent bound, and sanity: no
// recorded venue scale exceeds single digits).
func pow10(exp int64) (decimal.Decimal, bool) {
	if exp < -18 || exp > 18 {
		return decimal.Decimal{}, false
	}
	return decimal.New(1, int32(exp)), true
}

func (c *phemexCollector) fetchInstruments(ctx context.Context) ([]Instrument, error) {
	var resp struct {
		Data phemexProducts `json:"data"`
	}
	if err := c.c.getJSON(ctx, 1, c.base, "/public/products", nil, &resp); err != nil {
		return nil, err
	}
	valueScale := make(map[string]int64, len(resp.Data.Currencies))
	for _, cur := range resp.Data.Currencies {
		valueScale[cur.Currency] = cur.ValueScale
	}
	scales := make(map[string]int64, len(resp.Data.Products))
	out := make([]Instrument, 0, len(resp.Data.Products)+len(resp.Data.PerpV2))
	for _, p := range resp.Data.Products {
		if p.Type != "Spot" {
			continue // the COIN-M perp products[] row is not a spot instrument
		}
		id := strings.TrimPrefix(p.Symbol, "s")
		in := Instrument{
			Venue: screener.VenuePhemex, Kind: KindSpot, Symbol: id,
			Base: p.Base, Quote: p.Quote, Tradable: p.Status == "Listed",
		}
		if ps, ok := pow10(p.PriceScale); ok {
			in.TickSize = decimal.NewFromInt(p.QuoteTickSizeEv).Div(ps).String()
		}
		if vs, ok := pow10(valueScale[p.Quote]); ok {
			in.MinNotional = decimal.NewFromInt(p.MinOrderValueEv).Div(vs).String()
		}
		out = append(out, in)
		if p.PriceScale > 0 && p.PriceScale <= 18 {
			scales[id] = p.PriceScale
		}
	}
	for _, p := range resp.Data.PerpV2 {
		if p.Settle != "USDT" {
			continue // COIN-M (BTCUSD settles BTC) — the screener lanes are USDT-settled
		}
		out = append(out, Instrument{
			Venue: screener.VenuePhemex, Kind: KindPerp, Symbol: p.Symbol,
			Base: p.Base, Quote: p.Quote, Tradable: p.Status == "Listed",
			TickSize: p.TickSize.String(), StepSize: p.QtyStepSize.String(),
			MinNotional: p.MinOrderValueRv.String(),
			IntervalH:   int(p.FundingIntervalS.IntPart() / 3600),
		})
	}
	c.scalesMu.Lock()
	c.scales = scales
	c.scalesMu.Unlock()
	return out, nil
}

func (c *phemexCollector) Instruments(ctx context.Context) ([]Instrument, error) {
	return c.inst.refresh(ctx)
}

type phemexSpotTicker struct {
	Symbol    string `json:"symbol"` // sBTCUSDT
	BidEp     int64  `json:"bidEp"`
	AskEp     int64  `json:"askEp"`
	Timestamp int64  `json:"timestamp"` // ns
}

func (c *phemexCollector) Spot(ctx context.Context) ([]screener.Quote, error) {
	inst, err := c.inst.ensure(ctx, KindSpot)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Result []phemexSpotTicker `json:"result"`
	}
	if err := c.c.getJSON(ctx, 1, c.base, "/md/spot/ticker/24hr/all", nil, &resp); err != nil {
		return nil, err
	}
	c.scalesMu.RLock()
	defer c.scalesMu.RUnlock()
	out := make([]screener.Quote, 0, len(resp.Result))
	for _, t := range resp.Result {
		id := strings.TrimPrefix(t.Symbol, "s")
		in, ok := inst[id]
		if !ok || !in.Tradable {
			continue
		}
		scale, ok := c.scales[id]
		if !ok {
			continue // no recorded price scale — cannot decode honestly
		}
		ps, ok := pow10(scale)
		if !ok {
			continue
		}
		bid := decimal.NewFromInt(t.BidEp).Div(ps)
		ask := decimal.NewFromInt(t.AskEp).Div(ps)
		if !bid.IsPositive() || !ask.IsPositive() {
			continue
		}
		at := c.now()
		if t.Timestamp > 0 {
			at = time.Unix(0, t.Timestamp)
		}
		out = append(out, screener.Quote{
			Venue: screener.VenuePhemex, Base: in.Base, Quote: in.Quote,
			Bid: bid, Ask: ask, At: at,
			// No sizes on the bulk spot ticker (research §3).
			LiquidityUnknown: true,
		})
	}
	return out, nil
}

type phemexPerpTicker struct {
	Symbol          string `json:"symbol"`
	BidRp           num    `json:"bidRp"`
	AskRp           num    `json:"askRp"`
	MarkRp          num    `json:"markRp"`
	IndexRp         num    `json:"indexRp"`
	FundingRateRr   num    `json:"fundingRateRr"`
	PredFundingRate num    `json:"predFundingRateRr"`
	Timestamp       int64  `json:"timestamp"` // ns
}

func (c *phemexCollector) Perps(ctx context.Context) ([]screener.Perp, error) {
	inst, err := c.inst.ensure(ctx, KindPerp)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Result []phemexPerpTicker `json:"result"`
	}
	if err := c.c.getJSON(ctx, 1, c.base, "/md/v3/ticker/24hr/all", nil, &resp); err != nil {
		return nil, err
	}
	out := make([]screener.Perp, 0, len(resp.Result))
	for _, t := range resp.Result {
		in, ok := inst[t.Symbol]
		if !ok || !in.Tradable || !t.MarkRp.IsPositive() {
			continue
		}
		at := c.now()
		if t.Timestamp > 0 {
			at = time.Unix(0, t.Timestamp)
		}
		out = append(out, screener.Perp{
			Venue: screener.VenuePhemex, Base: in.Base, Quote: in.Quote,
			Mark: t.MarkRp.Decimal, Index: t.IndexRp.Decimal,
			Bid: t.BidRp.Decimal, Ask: t.AskRp.Decimal,
			FundingRate: t.FundingRateRr.Decimal, PredictedFundingRate: t.PredFundingRate.Decimal,
			IntervalH: in.IntervalH,
			// The bulk perp ticker publishes no next-funding time (research
			// §4) — NextFundingAt stays zero rather than being synthesised.
			At: at,
		})
	}
	return out, nil
}

func (c *phemexCollector) Networks(ctx context.Context) (map[string]screener.NetworkStatus, error) {
	// No public deposit/withdraw status (research §4): the currencies
	// list carries listing status, which is not a network status.
	return c.inst.keyGatedNetworks(ctx)
}
