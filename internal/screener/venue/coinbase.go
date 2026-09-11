package venue

import (
	"context"
	"errors"
	"net/url"
	"sync"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/screener"
)

// Coinbase collector (T-075, docs/research/venues/coinbase.md): Advanced
// Trade PUBLIC market data. Sources, all accessed 2026-08-27:
//   - GET /api/v3/brokerage/market/products (api.coinbase.com), public
//     ("security: []"): product_id, base_currency_id, quote_currency_id,
//     status, trading_disabled, is_disabled, view_only, cancel_only,
//     auction_mode, price_increment, base_increment, quote_min_size,
//     product_type, best_bid_price / best_ask_price — the last two are
//     present in the schema but EMPTY STRINGS on the public list (live,
//     929 products), so there is no bulk bid/ask on this venue.
//     https://docs.cdp.coinbase.com/api-reference/advanced-trade-api/rest-api/public/list-public-products
//   - GET /api/v3/brokerage/market/product_book?product_id&limit=1:
//     pricebook {product_id, bids[] {price, size}, asks[], time (RFC
//     3339)} PER PRODUCT; listed under the public endpoints of
//     https://docs.cdp.coinbase.com/coinbase-app/advanced-trade-apis/rest-api.md
//     and answered without a key live. Spot() fetches BooksPerPoll
//     products round-robin and carries every other product's last-known
//     quote with ITS OWN timestamp, so ages stay honest.
//   - perpetuals: product_type on the public list is SPOT for all 929
//     products; Coinbase's perpetual futures are an international
//     (INTX → Deribit-powered gateway from 2026-09-09) offering, not in
//     Advanced Trade public market data →
//     Perps() returns an empty slice (VERIFIED-ABSENT).
//     https://docs.cdp.coinbase.com/coinbase-app/advanced-trade-apis/guides/derivatives/overview.md
//   - currency/network status: Advanced Trade has none; the sibling
//     Coinbase Exchange public GET /currencies (api.exchange.coinbase.com)
//     publishes status online|delisted and supported_networks[]
//     {id, status} — network status only, no deposit/withdraw flags,
//     which is what Networks() reports.
//     https://docs.cdp.coinbase.com/exchange/rest-api/api-reference.md
//   - rate limits: Advanced Trade public "10 requests per second per IP"
//     UNVERIFIED (rendered page unavailable; figure from the official
//     page's search excerpt, research §5); Exchange public 10 req/s per
//     IP, bursts to 15, VERIFIED
//     https://docs.cdp.coinbase.com/exchange/introduction/rate-limits-overview.md
//     → one 8 req/s gate for both hosts.
//   - fees: UNVERIFIED (fee pages answer 403 to fetches; research §6:
//     Advanced "Intro" tier 0.60 % maker / 1.20 % taker) → Verified=false;
//     PerpTakerBps is a placeholder (no perps).
type coinbaseCollector struct {
	base     string
	exchange string
	gate     *gate
	c        *client
	inst     *instrumentCache
	rr       *fundingRR // round-robin over products for product_book
	perPoll  int
	mu       sync.Mutex
	known    map[string]screener.Quote
	now      func() time.Time

	// Burst adaptation (audit X10): Coinbase shares one IP-wide request
	// budget, so a 429 halves the per-poll book count and ten clean polls
	// earn one back, up to the configured BooksPerPoll. burst/cleanPolls
	// live under mu; maxBurst is immutable after construction.
	burst      int
	maxBurst   int
	cleanPolls int
}

// currentBurst returns the effective per-poll book count.
func (c *coinbaseCollector) currentBurst() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.burst
}

// noteBurst records a poll outcome: a rate-limited poll halves the
// burst (floor 1) and resets the recovery streak; a clean poll counts
// toward earning one book back every ten polls.
func (c *coinbaseCollector) noteBurst(rateLimited bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if rateLimited {
		c.burst = max(1, c.burst/2)
		c.cleanPolls = 0
		return
	}
	if c.burst < c.maxBurst {
		c.cleanPolls++
		if c.cleanPolls >= 10 {
			c.cleanPolls = 0
			c.burst++
		}
	}
}

const (
	coinbaseBase         = "https://api.coinbase.com"
	coinbaseExchangeBase = "https://api.exchange.coinbase.com"
)

func newCoinbase(opts Options) *coinbaseCollector {
	now := opts.now()
	maxBurst := opts.booksPerPoll()
	c := &coinbaseCollector{base: coinbaseBase, exchange: coinbaseExchangeBase, now: now,
		perPoll: maxBurst, maxBurst: maxBurst, burst: maxBurst, known: map[string]screener.Quote{}}
	c.rr = newFundingRR(c.perPoll)
	if opts.SpotBase != "" {
		c.base = opts.SpotBase
	}
	if opts.PerpBase != "" {
		c.exchange = opts.PerpBase // Exchange host (Networks only)
	}
	c.gate = newGate(8, time.Second, now)
	c.c = newClient(screener.VenueCoinbase, c.gate)
	c.inst = newInstrumentCache(opts, c.fetchInstruments)
	return c
}

func (c *coinbaseCollector) ID() screener.Venue { return screener.VenueCoinbase }
func (c *coinbaseCollector) Fees() Fees {
	return Fees{SpotTakerBps: bps("0.012"), PerpTakerBps: bps("0.0005"), Verified: false}
}
func (c *coinbaseCollector) RateLimited() int { return int(c.gate.limited.Load()) }

type coinbaseProduct struct {
	ProductID       string `json:"product_id"`
	BaseCurrencyID  string `json:"base_currency_id"`
	QuoteCurrencyID string `json:"quote_currency_id"`
	Status          string `json:"status"`
	TradingDisabled bool   `json:"trading_disabled"`
	IsDisabled      bool   `json:"is_disabled"`
	ViewOnly        bool   `json:"view_only"`
	CancelOnly      bool   `json:"cancel_only"`
	AuctionMode     bool   `json:"auction_mode"`
	ProductType     string `json:"product_type"`
	PriceIncrement  string `json:"price_increment"`
	BaseIncrement   string `json:"base_increment"`
	QuoteMinSize    string `json:"quote_min_size"`
}

func (c *coinbaseCollector) fetchInstruments(ctx context.Context) ([]Instrument, error) {
	var env struct {
		Products []coinbaseProduct `json:"products"`
	}
	if err := c.c.getJSON(ctx, 1, c.base, "/api/v3/brokerage/market/products", url.Values{"product_type": {"SPOT"}}, &env); err != nil {
		return nil, err
	}
	out := make([]Instrument, 0, len(env.Products))
	for _, p := range env.Products {
		if p.ProductType != "SPOT" {
			continue
		}
		tradable := p.Status == "online" && !p.TradingDisabled && !p.IsDisabled && !p.ViewOnly && !p.CancelOnly && !p.AuctionMode
		out = append(out, Instrument{Venue: screener.VenueCoinbase, Kind: KindSpot, Symbol: p.ProductID,
			Base: p.BaseCurrencyID, Quote: p.QuoteCurrencyID, Tradable: tradable,
			TickSize: p.PriceIncrement, StepSize: p.BaseIncrement, MinNotional: p.QuoteMinSize})
	}
	return out, nil
}

func (c *coinbaseCollector) Instruments(ctx context.Context) ([]Instrument, error) {
	return c.inst.refresh(ctx)
}

type coinbaseBook struct {
	Pricebook struct {
		ProductID string `json:"product_id"`
		Bids      []struct {
			Price num `json:"price"`
			Size  num `json:"size"`
		} `json:"bids"`
		Asks []struct {
			Price num `json:"price"`
			Size  num `json:"size"`
		} `json:"asks"`
		Time string `json:"time"`
	} `json:"pricebook"`
}

// Spot refreshes BooksPerPoll products' top of book and returns every
// product with a known quote (each at its own observation time).
func (c *coinbaseCollector) Spot(ctx context.Context) ([]screener.Quote, error) {
	inst, err := c.inst.ensure(ctx, KindSpot)
	if err != nil {
		return nil, err
	}
	symbols := make([]string, 0, len(inst))
	for s, in := range inst {
		if in.Tradable {
			symbols = append(symbols, s)
		}
	}
	rateLimited := false
	for _, s := range c.rr.pickN(symbols, c.currentBurst()) {
		var b coinbaseBook
		q := url.Values{"product_id": {s}, "limit": {"1"}}
		if err := c.c.getJSON(ctx, 1, c.base, "/api/v3/brokerage/market/product_book", q, &b); err != nil {
			var he *HTTPError
			if errors.As(err, &he) && he.RateLimit() {
				// X10: a 429 mid-poll does not discard the books already
				// refreshed — they are real observations. Publish the
				// partial set (a degraded poll: the untouched symbols
				// keep their previous quote and age out through the
				// normal data-age gates), halve the burst, and let the
				// gate's Retry-After backoff pace the next attempt.
				rateLimited = true
				break
			}
			return nil, err
		}
		pb := b.Pricebook
		in := inst[s]
		if len(pb.Bids) == 0 || len(pb.Asks) == 0 || !pb.Bids[0].Price.IsPositive() || !pb.Asks[0].Price.IsPositive() {
			continue // one-sided book: keep the previous quote if any
		}
		at := c.now()
		if t, err := time.Parse(time.RFC3339Nano, pb.Time); err == nil {
			at = t
		}
		q2 := screener.Quote{Venue: screener.VenueCoinbase, Base: in.Base, Quote: in.Quote,
			Bid: pb.Bids[0].Price.Decimal, BidQty: pb.Bids[0].Size.Decimal,
			Ask: pb.Asks[0].Price.Decimal, AskQty: pb.Asks[0].Size.Decimal, At: at}
		c.mu.Lock()
		c.known[s] = q2
		c.mu.Unlock()
	}
	c.noteBurst(rateLimited)
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]screener.Quote, 0, len(c.known))
	for s, q := range c.known {
		if in, ok := inst[s]; ok && in.Tradable {
			out = append(out, q)
		}
	}
	return out, nil
}

// Perps: none on Advanced Trade public market data (see package comment).
func (c *coinbaseCollector) Perps(ctx context.Context) ([]screener.Perp, error) {
	return []screener.Perp{}, nil
}

type coinbaseCurrency struct {
	ID                string `json:"id"`
	Status            string `json:"status"`
	SupportedNetworks []struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	} `json:"supported_networks"`
}

// Networks reads the Exchange public /currencies: open when the
// currency is online and at least one supported network is online,
// closed otherwise (reason lists networks / delisting). This is network
// status, not deposit/withdraw enablement (the venue publishes none).
func (c *coinbaseCollector) Networks(ctx context.Context) (map[string]screener.NetworkStatus, error) {
	var rows []coinbaseCurrency
	if err := c.c.getJSON(ctx, 1, c.exchange, "/currencies", nil, &rows); err != nil {
		return nil, err
	}
	out := make(map[string]screener.NetworkStatus, len(rows))
	for _, cur := range rows {
		if cur.Status != "online" {
			out[cur.ID] = screener.NetworkStatus{Status: screener.NetworkClosed, Reason: "currency " + cur.Status}
			continue
		}
		if len(cur.SupportedNetworks) == 0 {
			out[cur.ID] = screener.UnknownNetworkStatus("no networks published")
			continue
		}
		reason := ""
		open := false
		for _, n := range cur.SupportedNetworks {
			if n.Status == "online" {
				open = true
				break
			}
			reason += n.ID + ": " + n.Status + "; "
		}
		if open {
			out[cur.ID] = screener.NetworkStatus{Status: screener.NetworkOpen}
		} else {
			out[cur.ID] = screener.NetworkStatus{Status: screener.NetworkClosed, Reason: reason}
		}
	}
	return out, nil
}
