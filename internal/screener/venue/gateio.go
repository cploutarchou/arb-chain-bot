package venue

import (
	"context"
	"net/url"
	"time"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/screener"
)

// Gate (APIv4) collector. The docs site answers 403 to non-browser
// fetches; every field below is verified against the official
// auto-generated SDK docs (same OpenAPI source), accessed 2026-08-27:
//   - GET /spot/tickers: currency_pair, last, lowest_ask, highest_bid
//     (no sizes in the batch response, no timestamp) —
//     github.com/gateio/gateapi-python/blob/master/docs/Ticker.md ;
//     spot quotes therefore carry LiquidityUnknown=true and zero sizes.
//   - GET /spot/currency_pairs: id, base, quote, precision,
//     amount_precision, min_quote_amount, trade_status untradable|
//     buyable|sellable|tradable — …/docs/CurrencyPair.md
//   - GET /futures/usdt/tickers: contract, last, mark_price, index_price,
//     funding_rate, funding_rate_indicative (next period, marked
//     deprecated but still served), lowest_ask/lowest_size,
//     highest_bid/highest_size — …/docs/FuturesTicker.md
//   - GET /futures/usdt/contracts: name, quanto_multiplier,
//     funding_interval (s), funding_next_apply (unix s), status
//     prelaunch|trading|delisting|delisted|circuit_breaker, in_delisting,
//     order_price_round — …/docs/Contract.md. Contract has NO base/quote
//     fields: base/quote are resolved by looking the contract name up
//     in the spot currency_pairs list (id == name); contracts without a
//     spot pair are skipped rather than string-split.
//   - GET /spot/currencies: PUBLIC; chains[] {name, deposit_disabled,
//     withdraw_disabled, withdraw_delayed} — …/docs/Currency.md and
//     …/docs/SpotCurrencyChain.md
//   - rate limits: UNVERIFIED (conflicting figures, research §5.5) → a
//     conservative 10 req/s venue gate.
//   - fees: UNVERIFIED from primary (research §5.6: VIP0 spot 0.2 %,
//     futures taker 0.05 %) → Verified=false.
type gateCollector struct {
	base string
	gate *gate
	c    *client
	inst *instrumentCache
	now  func() time.Time
}

const gateBase = "https://api.gateio.ws/api/v4"

func newGateIO(opts Options) *gateCollector {
	now := opts.now()
	c := &gateCollector{base: gateBase, now: now}
	if opts.SpotBase != "" {
		c.base = opts.SpotBase
	}
	c.gate = newGate(10, time.Second, now)
	c.c = newClient(screener.VenueGate, c.gate)
	c.inst = newInstrumentCache(opts, c.fetchInstruments)
	return c
}

func (c *gateCollector) ID() screener.Venue { return screener.VenueGate }
func (c *gateCollector) Fees() Fees {
	return Fees{SpotTakerBps: bps("0.002"), PerpTakerBps: bps("0.0005"), Verified: false}
}
func (c *gateCollector) RateLimited() int { return int(c.gate.limited.Load()) }

type gatePair struct {
	ID              string `json:"id"`
	Base            string `json:"base"`
	Quote           string `json:"quote"`
	Precision       int    `json:"precision"`
	AmountPrecision int    `json:"amount_precision"`
	MinQuoteAmount  string `json:"min_quote_amount"`
	TradeStatus     string `json:"trade_status"`
}

type gateContract struct {
	Name             string `json:"name"`
	QuantoMultiplier string `json:"quanto_multiplier"`
	FundingInterval  int    `json:"funding_interval"`
	FundingNextApply num    `json:"funding_next_apply"`
	Status           string `json:"status"`
	InDelisting      bool   `json:"in_delisting"`
	OrderPriceRound  string `json:"order_price_round"`
}

func (c *gateCollector) fetchInstruments(ctx context.Context) ([]Instrument, error) {
	var pairs []gatePair
	if err := c.c.getJSON(ctx, 1, c.base, "/spot/currency_pairs", nil, &pairs); err != nil {
		return nil, err
	}
	var contracts []gateContract
	if err := c.c.getJSON(ctx, 1, c.base, "/futures/usdt/contracts", nil, &contracts); err != nil {
		return nil, err
	}
	out := make([]Instrument, 0, len(pairs)+len(contracts))
	byID := make(map[string]gatePair, len(pairs))
	for _, p := range pairs {
		byID[p.ID] = p
		out = append(out, Instrument{Venue: screener.VenueGate, Kind: KindSpot, Symbol: p.ID,
			Base: p.Base, Quote: p.Quote, Tradable: p.TradeStatus == "tradable",
			TickSize: decimal.New(1, int32(-p.Precision)).String(), StepSize: decimal.New(1, int32(-p.AmountPrecision)).String(), //nolint:gosec // venue precisions are small non-negative ints
			MinNotional: p.MinQuoteAmount})
	}
	for _, ct := range contracts {
		p, ok := byID[ct.Name]
		if !ok {
			continue // no spot pair to resolve base/quote from
		}
		out = append(out, Instrument{Venue: screener.VenueGate, Kind: KindPerp, Symbol: ct.Name,
			Base: p.Base, Quote: p.Quote, Tradable: ct.Status == "trading" && !ct.InDelisting,
			TickSize: ct.OrderPriceRound, CtVal: ct.QuantoMultiplier, IntervalH: ct.FundingInterval / 3600})
	}
	return out, nil
}

func (c *gateCollector) Instruments(ctx context.Context) ([]Instrument, error) {
	return c.inst.refresh(ctx)
}

type gateSpotTicker struct {
	CurrencyPair string `json:"currency_pair"`
	Last         num    `json:"last"`
	LowestAsk    num    `json:"lowest_ask"`
	HighestBid   num    `json:"highest_bid"`
}

func (c *gateCollector) Spot(ctx context.Context) ([]screener.Quote, error) {
	inst, err := c.inst.ensure(ctx, KindSpot)
	if err != nil {
		return nil, err
	}
	var rows []gateSpotTicker
	if err := c.c.getJSON(ctx, 1, c.base, "/spot/tickers", nil, &rows); err != nil {
		return nil, err
	}
	at := c.now() // no timestamp in the batch ticker
	out := make([]screener.Quote, 0, len(rows))
	for _, t := range rows {
		in, ok := inst[t.CurrencyPair]
		if !ok || !in.Tradable || !t.HighestBid.IsPositive() || !t.LowestAsk.IsPositive() {
			continue
		}
		out = append(out, screener.Quote{Venue: screener.VenueGate, Base: in.Base, Quote: in.Quote,
			Bid: t.HighestBid.Decimal, Ask: t.LowestAsk.Decimal, At: at, LiquidityUnknown: true})
	}
	return out, nil
}

type gateFuturesTicker struct {
	Contract              string `json:"contract"`
	Last                  num    `json:"last"`
	MarkPrice             num    `json:"mark_price"`
	IndexPrice            num    `json:"index_price"`
	FundingRate           num    `json:"funding_rate"`
	FundingRateIndicative num    `json:"funding_rate_indicative"`
	LowestAsk             num    `json:"lowest_ask"`
	HighestBid            num    `json:"highest_bid"`
}

func (c *gateCollector) Perps(ctx context.Context) ([]screener.Perp, error) {
	inst, err := c.inst.ensure(ctx, KindPerp)
	if err != nil {
		return nil, err
	}
	var rows []gateFuturesTicker
	if err := c.c.getJSON(ctx, 1, c.base, "/futures/usdt/tickers", nil, &rows); err != nil {
		return nil, err
	}
	// funding_next_apply lives on the contract list (refreshed with the
	// instrument cache), not the ticker.
	var contracts []gateContract
	if err := c.c.getJSON(ctx, 1, c.base, "/futures/usdt/contracts", nil, &contracts); err != nil {
		return nil, err
	}
	nextBy := make(map[string]time.Time, len(contracts))
	for _, ct := range contracts {
		if ct.FundingNextApply.IsPositive() {
			nextBy[ct.Name] = time.Unix(ct.FundingNextApply.IntPart(), 0)
		}
	}
	at := c.now()
	out := make([]screener.Perp, 0, len(rows))
	for _, t := range rows {
		in, ok := inst[t.Contract]
		if !ok || !in.Tradable || !t.MarkPrice.IsPositive() {
			continue
		}
		out = append(out, screener.Perp{Venue: screener.VenueGate, Base: in.Base, Quote: in.Quote,
			Mark: t.MarkPrice.Decimal, Index: t.IndexPrice.Decimal, Bid: t.HighestBid.Decimal, Ask: t.LowestAsk.Decimal,
			FundingRate: t.FundingRate.Decimal, PredictedFundingRate: t.FundingRateIndicative.Decimal,
			IntervalH: in.IntervalH, NextFundingAt: nextBy[t.Contract], At: at})
	}
	return out, nil
}

type gateCurrency struct {
	Currency string `json:"currency"`
	Delisted bool   `json:"delisted"`
	Chains   []struct {
		Name             string `json:"name"`
		DepositDisabled  bool   `json:"deposit_disabled"`
		WithdrawDisabled bool   `json:"withdraw_disabled"`
		WithdrawDelayed  bool   `json:"withdraw_delayed"`
	} `json:"chains"`
}

// Networks is the one public chain-status source among the six venues.
// An asset is "open" when at least one chain has both deposit and
// withdraw enabled, "closed" when every chain has either side disabled
// (reason lists which), "unknown" when the currency publishes no chains.
func (c *gateCollector) Networks(ctx context.Context) (map[string]screener.NetworkStatus, error) {
	var rows []gateCurrency
	if err := c.c.getJSON(ctx, 1, c.base, "/spot/currencies", nil, &rows); err != nil {
		return nil, err
	}
	out := make(map[string]screener.NetworkStatus, len(rows))
	for _, cur := range rows {
		if cur.Delisted {
			out[cur.Currency] = screener.NetworkStatus{Status: screener.NetworkClosed, Reason: "delisted"}
			continue
		}
		if len(cur.Chains) == 0 {
			out[cur.Currency] = screener.UnknownNetworkStatus("no chains published")
			continue
		}
		reason := ""
		open := false
		for _, ch := range cur.Chains {
			if !ch.DepositDisabled && !ch.WithdrawDisabled {
				open = true
				break
			}
			switch {
			case ch.DepositDisabled && ch.WithdrawDisabled:
				reason += ch.Name + ": deposit+withdraw disabled; "
			case ch.DepositDisabled:
				reason += ch.Name + ": deposit disabled; "
			default:
				reason += ch.Name + ": withdraw disabled; "
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
