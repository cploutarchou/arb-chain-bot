package venue

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/screener"
)

// HTX (Huobi) collector (T-075, docs/research/venues/htx.md). Sources,
// all accessed 2026-08-27:
//   - spot GET /market/tickers (api.huobi.pro): status, ts (ms, top
//     level), data[] {symbol, bid, bidSize, ask, askSize, close} — bare
//     JSON numbers, parsed from their literal text
//     https://huobiapi.github.io/docs/spot/v1/en/#get-latest-tickers-for-all-pairs
//   - spot GET /v2/settings/common/symbols: sc (symbol code), bc (base),
//     qc (quote), state online|offline|suspend|…, te (trade enabled),
//     tpp (price precision), tap (amount precision); minov is not in
//     the live v2 response → MinNotional ""
//     https://huobiapi.github.io/docs/spot/v1/en/#get-all-supported-trading-symbol-v2
//   - spot GET /v2/reference/currencies, PUBLIC: currency, instStatus
//     normal|delisted, chains[] {chain, depositStatus, withdrawStatus
//     allowed|prohibited}
//     https://huobiapi.github.io/docs/spot/v1/en/#apiv2-currency-amp-chains
//   - swap GET /linear-swap-api/v1/swap_contract_info (api.hbdm.com):
//     symbol (= base), contract_code, trade_partition (= quote/settle),
//     contract_size, price_tick, contract_status (1 Ready … 5/8
//     Suspended), business_type swap, settlement_period (hours)
//     https://huobiapi.github.io/docs/usdt_swap/v1/en/#get-swap-info
//   - swap GET /linear-swap-ex/market/detail/batch_merged
//     ?business_type=swap: ticks[] {contract_code, bid [price, size],
//     ask [price, size], ts}
//     https://huobiapi.github.io/docs/usdt_swap/v1/en/#get-a-batch-of-market-data-overview
//   - swap GET /linear-swap-api/v1/swap_batch_funding_rate: data[]
//     {contract_code, funding_rate, estimated_rate (null when not
//     published), funding_time (ms; observed live as the UPCOMING
//     settlement), next_funding_time (null live)}
//     https://huobiapi.github.io/docs/usdt_swap/v1/en/#query-a-batch-of-funding-rate
//   - swap GET /linear-swap-api/v1/swap_index: data[] {contract_code,
//     index_price, index_ts}
//   - mark price: no bulk endpoint (VERIFIED-ABSENT in the endpoint
//     list); GET /index/market/history/linear_swap_mark_price_kline
//     ?contract_code&period=1min&size=1 PER CONTRACT → data[0].close,
//     fetched round-robin (FundingCallsPerPoll per Perps() call, the
//     rest carry their last-known mark; a contract with no mark yet has
//     Mark 0 and basis.go skips it).
//   - funding history (not polled): GET /linear-swap-api/v1/swap_historical_funding_rate
//     ?contract_code → data.data[] {funding_rate, realized_rate, funding_time}
//   - rate limits: UNVERIFIED from a rendered page (research §5): market
//     interfaces "800 times / 1 s per IP", non-market public "240 / 3 s
//     per IP" for derivatives; spot /market/tickers not itemised →
//     conservative gates (spot 2 req/s, swap 10 req/s).
//   - fees: UNVERIFIED (research §6: spot regular 0.2 %; USDT-M taker
//     0.06 % is the "illustration only" figure on
//     https://www.htx.com/support/900000089923) → Verified=false.
//
// HTX's funding_rate is the rate for the current settlement period;
// estimated_rate is the next-period forecast when published (null on
// every row observed live), so PredictedFundingRate falls back to the
// current rate when absent.
type htxCollector struct {
	spotBase string
	perpBase string
	spotGate *gate
	perpGate *gate
	spot     *client
	perp     *client
	inst     *instrumentCache
	rr       *fundingRR // reused for round-robin MARK price fetches
	now      func() time.Time
}

const (
	htxSpotBase = "https://api.huobi.pro"
	htxPerpBase = "https://api.hbdm.com"
)

func newHTX(opts Options) *htxCollector {
	now := opts.now()
	c := &htxCollector{spotBase: htxSpotBase, perpBase: htxPerpBase, now: now, rr: newFundingRR(opts.fundingCalls())}
	if opts.SpotBase != "" {
		c.spotBase = opts.SpotBase
	}
	if opts.PerpBase != "" {
		c.perpBase = opts.PerpBase
	}
	c.spotGate = newGate(2, time.Second, now)
	c.perpGate = newGate(10, time.Second, now)
	c.spot = newClient(screener.VenueHTX, c.spotGate)
	c.perp = newClient(screener.VenueHTX, c.perpGate)
	c.inst = newInstrumentCache(opts, c.fetchInstruments)
	return c
}

func (c *htxCollector) ID() screener.Venue { return screener.VenueHTX }
func (c *htxCollector) Fees() Fees {
	return Fees{SpotTakerBps: bps("0.002"), PerpTakerBps: bps("0.0006"), Verified: false}
}
func (c *htxCollector) RateLimited() int {
	return int(c.spotGate.limited.Load() + c.perpGate.limited.Load())
}

// htxStatus is the "status":"ok" envelope used by market endpoints.
type htxStatus struct {
	Status  string `json:"status"`
	ErrCode string `json:"err-code"`
	ErrMsg  string `json:"err-msg"`
	Ts      num    `json:"ts"` // ms; a bare number on market endpoints, a string on /v2/settings
}

// htxRateLimited reports whether an HTX error envelope is a request-limit
// refusal. HTX answers HTTP 200 with status="error" and a message naming
// the limit (observed 2026-08-28 during the fifteen-venue soak:
// err-code "invalid-parameter", err-msg "request limit"); the documented
// codes carry the same wording. Treating it as a plain error would let the
// poller keep hammering a venue that is already refusing, so it is
// classified as an in-band rate limit and backs the gate off.
func htxRateLimited(errCode, errMsg string) bool {
	hay := strings.ToLower(errCode + " " + errMsg)
	for _, needle := range []string{"request limit", "rate limit", "too many requests", "too frequent"} {
		if strings.Contains(hay, needle) {
			return true
		}
	}
	return false
}

// check validates the envelope. g may be nil (tests); when the envelope
// is a request-limit refusal the gate is backed off exactly as it is for
// an HTTP 429, so the next poll waits instead of hammering.
func (s htxStatus) check(path string, g *gate) error {
	if s.Status == "ok" {
		return nil
	}
	if htxRateLimited(s.ErrCode, s.ErrMsg) {
		e := &InBandRateLimit{
			Venue: screener.VenueHTX, Path: path, Code: 0,
			Msg: s.ErrCode + ": " + s.ErrMsg, Pause: htxInBandPause,
		}
		if g != nil {
			g.observeInBand(e)
		}
		return e
	}
	return fmt.Errorf("htx: %s: %s: %s", path, s.ErrCode, s.ErrMsg)
}

// htxInBandPause is our policy, not a documented HTX value: the envelope
// carries no Retry-After.
const htxInBandPause = 10 * time.Second

type htxSymbol struct {
	SC    string `json:"sc"`
	BC    string `json:"bc"`
	QC    string `json:"qc"`
	State string `json:"state"`
	TE    bool   `json:"te"`
	TPP   int    `json:"tpp"`
	TAP   int    `json:"tap"`
}

type htxContract struct {
	Symbol           string `json:"symbol"`
	ContractCode     string `json:"contract_code"`
	TradePartition   string `json:"trade_partition"`
	ContractSize     num    `json:"contract_size"`
	PriceTick        num    `json:"price_tick"`
	ContractStatus   int    `json:"contract_status"`
	BusinessType     string `json:"business_type"`
	SettlementPeriod string `json:"settlement_period"`
}

func (c *htxCollector) fetchInstruments(ctx context.Context) ([]Instrument, error) {
	var spot struct {
		htxStatus
		Data []htxSymbol `json:"data"`
	}
	if err := c.spot.getJSON(ctx, 1, c.spotBase, "/v2/settings/common/symbols", nil, &spot); err != nil {
		return nil, err
	}
	if err := spot.check("settings/common/symbols", c.spotGate); err != nil {
		return nil, err
	}
	var swap struct {
		htxStatus
		Data []htxContract `json:"data"`
	}
	if err := c.perp.getJSON(ctx, 1, c.perpBase, "/linear-swap-api/v1/swap_contract_info", nil, &swap); err != nil {
		return nil, err
	}
	if err := swap.check("swap_contract_info", c.perpGate); err != nil {
		return nil, err
	}
	out := make([]Instrument, 0, len(spot.Data)+len(swap.Data))
	for _, s := range spot.Data {
		// HTX publishes currency codes in lower case ("btc"); the venue
		// list is the source, only the case is normalised.
		out = append(out, Instrument{Venue: screener.VenueHTX, Kind: KindSpot, Symbol: s.SC,
			Base: strings.ToUpper(s.BC), Quote: strings.ToUpper(s.QC), Tradable: s.State == "online" && s.TE,
			TickSize: precisionStep(s.TPP), StepSize: precisionStep(s.TAP)})
	}
	for _, ct := range swap.Data {
		if ct.BusinessType != "swap" || ct.TradePartition != "USDT" {
			continue
		}
		h, _ := strconv.Atoi(ct.SettlementPeriod)
		out = append(out, Instrument{Venue: screener.VenueHTX, Kind: KindPerp, Symbol: ct.ContractCode,
			Base: strings.ToUpper(ct.Symbol), Quote: ct.TradePartition, Tradable: ct.ContractStatus == 1,
			TickSize: ct.PriceTick.String(), CtVal: ct.ContractSize.String(), IntervalH: h})
	}
	return out, nil
}

func (c *htxCollector) Instruments(ctx context.Context) ([]Instrument, error) {
	return c.inst.refresh(ctx)
}

type htxSpotTicker struct {
	Symbol  string `json:"symbol"`
	Bid     num    `json:"bid"`
	BidSize num    `json:"bidSize"`
	Ask     num    `json:"ask"`
	AskSize num    `json:"askSize"`
}

func (c *htxCollector) Spot(ctx context.Context) ([]screener.Quote, error) {
	inst, err := c.inst.ensure(ctx, KindSpot)
	if err != nil {
		return nil, err
	}
	var env struct {
		htxStatus
		Data []htxSpotTicker `json:"data"`
	}
	if err := c.spot.getJSON(ctx, 1, c.spotBase, "/market/tickers", nil, &env); err != nil {
		return nil, err
	}
	if err := env.check("market/tickers", c.spotGate); err != nil {
		return nil, err
	}
	at := c.now()
	if env.Ts.IsPositive() {
		at = time.UnixMilli(env.Ts.IntPart())
	}
	out := make([]screener.Quote, 0, len(env.Data))
	for _, t := range env.Data {
		in, ok := inst[t.Symbol]
		if !ok || !in.Tradable || !t.Bid.IsPositive() || !t.Ask.IsPositive() {
			continue
		}
		out = append(out, screener.Quote{Venue: screener.VenueHTX, Base: in.Base, Quote: in.Quote,
			Bid: t.Bid.Decimal, BidQty: t.BidSize.Decimal, Ask: t.Ask.Decimal, AskQty: t.AskSize.Decimal, At: at})
	}
	return out, nil
}

type htxBatchTick struct {
	ContractCode string `json:"contract_code"`
	Bid          []num  `json:"bid"` // [price, size]
	Ask          []num  `json:"ask"`
	Ts           int64  `json:"ts"`
}

type htxFunding struct {
	ContractCode  string `json:"contract_code"`
	FundingRate   num    `json:"funding_rate"`
	EstimatedRate *num   `json:"estimated_rate"`
	FundingTime   num    `json:"funding_time"`
}

type htxIndex struct {
	ContractCode string `json:"contract_code"`
	IndexPrice   num    `json:"index_price"`
}

type htxMarkKline struct {
	htxStatus
	Data []struct {
		Close num `json:"close"`
	} `json:"data"`
}

func (c *htxCollector) Perps(ctx context.Context) ([]screener.Perp, error) {
	inst, err := c.inst.ensure(ctx, KindPerp)
	if err != nil {
		return nil, err
	}
	var batch struct {
		htxStatus
		Ticks []htxBatchTick `json:"ticks"`
	}
	if err := c.perp.getJSON(ctx, 1, c.perpBase, "/linear-swap-ex/market/detail/batch_merged", url.Values{"business_type": {"swap"}}, &batch); err != nil {
		return nil, err
	}
	if err := batch.check("batch_merged", c.perpGate); err != nil {
		return nil, err
	}
	var fund struct {
		htxStatus
		Data []htxFunding `json:"data"`
	}
	if err := c.perp.getJSON(ctx, 1, c.perpBase, "/linear-swap-api/v1/swap_batch_funding_rate", nil, &fund); err != nil {
		return nil, err
	}
	if err := fund.check("swap_batch_funding_rate", c.perpGate); err != nil {
		return nil, err
	}
	var idx struct {
		htxStatus
		Data []htxIndex `json:"data"`
	}
	if err := c.perp.getJSON(ctx, 1, c.perpBase, "/linear-swap-api/v1/swap_index", nil, &idx); err != nil {
		return nil, err
	}
	if err := idx.check("swap_index", c.perpGate); err != nil {
		return nil, err
	}
	fundBy := make(map[string]htxFunding, len(fund.Data))
	for _, f := range fund.Data {
		fundBy[f.ContractCode] = f
	}
	idxBy := make(map[string]htxIndex, len(idx.Data))
	for _, i := range idx.Data {
		idxBy[i.ContractCode] = i
	}
	// Round-robin mark price: N contracts per poll, last-known for the rest.
	symbols := make([]string, 0, len(inst))
	for s, in := range inst {
		if in.Tradable {
			symbols = append(symbols, s)
		}
	}
	for _, s := range c.rr.pick(symbols) {
		var k htxMarkKline
		q := url.Values{"contract_code": {s}, "period": {"1min"}, "size": {"1"}}
		if err := c.perp.getJSON(ctx, 1, c.perpBase, "/index/market/history/linear_swap_mark_price_kline", q, &k); err != nil {
			return nil, err
		}
		if err := k.check("linear_swap_mark_price_kline", c.perpGate); err != nil {
			return nil, err
		}
		if len(k.Data) > 0 && k.Data[0].Close.IsPositive() {
			c.rr.set(s, fundingInfo{Rate: k.Data[0].Close.Decimal, At: c.now()})
		}
	}
	recv := c.now()
	out := make([]screener.Perp, 0, len(batch.Ticks))
	for _, t := range batch.Ticks {
		in, ok := inst[t.ContractCode]
		if !ok || !in.Tradable {
			continue
		}
		at := recv
		if t.Ts > 0 {
			at = time.UnixMilli(t.Ts)
		}
		p := screener.Perp{Venue: screener.VenueHTX, Base: in.Base, Quote: in.Quote, IntervalH: in.IntervalH, At: at}
		if len(t.Bid) > 0 {
			p.Bid = t.Bid[0].Decimal
		}
		if len(t.Ask) > 0 {
			p.Ask = t.Ask[0].Decimal
		}
		p.Index = idxBy[t.ContractCode].IndexPrice.Decimal
		if mk, ok := c.rr.get(t.ContractCode); ok {
			p.Mark = mk.Rate // last-known mark price (see package comment)
		}
		if f, ok := fundBy[t.ContractCode]; ok {
			p.FundingRate = f.FundingRate.Decimal
			p.PredictedFundingRate = f.FundingRate.Decimal
			if f.EstimatedRate != nil {
				p.PredictedFundingRate = f.EstimatedRate.Decimal
			}
			if f.FundingTime.IsPositive() {
				p.NextFundingAt = time.UnixMilli(f.FundingTime.IntPart())
			}
		}
		if !p.Mark.IsPositive() && !p.Index.IsPositive() {
			continue // nothing priced yet
		}
		out = append(out, p)
	}
	return out, nil
}

type htxCurrency struct {
	Currency   string `json:"currency"`
	InstStatus string `json:"instStatus"`
	Chains     []struct {
		Chain          string `json:"chain"`
		DepositStatus  string `json:"depositStatus"`
		WithdrawStatus string `json:"withdrawStatus"`
	} `json:"chains"`
}

// Networks is public on HTX (GET /v2/reference/currencies): open when at
// least one chain has depositStatus and withdrawStatus "allowed", closed
// otherwise (reason lists the chains), delisted → closed.
func (c *htxCollector) Networks(ctx context.Context) (map[string]screener.NetworkStatus, error) {
	var env struct {
		Code int           `json:"code"`
		Data []htxCurrency `json:"data"`
	}
	if err := c.spot.getJSON(ctx, 1, c.spotBase, "/v2/reference/currencies", nil, &env); err != nil {
		return nil, err
	}
	if env.Code != 200 {
		return nil, fmt.Errorf("htx: reference/currencies: code %d", env.Code)
	}
	out := make(map[string]screener.NetworkStatus, len(env.Data))
	for _, cur := range env.Data {
		asset := strings.ToUpper(cur.Currency)
		if cur.InstStatus == "delisted" {
			out[asset] = screener.NetworkStatus{Status: screener.NetworkClosed, Reason: "delisted"}
			continue
		}
		if len(cur.Chains) == 0 {
			out[asset] = screener.UnknownNetworkStatus("no chains published")
			continue
		}
		reason := ""
		open := false
		for _, ch := range cur.Chains {
			dep, wd := ch.DepositStatus == "allowed", ch.WithdrawStatus == "allowed"
			if dep && wd {
				open = true
				break
			}
			switch {
			case !dep && !wd:
				reason += ch.Chain + ": deposit+withdraw prohibited; "
			case !dep:
				reason += ch.Chain + ": deposit prohibited; "
			default:
				reason += ch.Chain + ": withdraw prohibited; "
			}
		}
		if open {
			out[asset] = screener.NetworkStatus{Status: screener.NetworkOpen}
		} else {
			out[asset] = screener.NetworkStatus{Status: screener.NetworkClosed, Reason: reason}
		}
	}
	return out, nil
}

// precisionStep renders a decimal-places count as a step string
// ("0.01" for 2), "" for a negative count.
func precisionStep(places int) string {
	if places < 0 {
		return ""
	}
	if places == 0 {
		return "1"
	}
	return "0." + strings.Repeat("0", places-1) + "1"
}
