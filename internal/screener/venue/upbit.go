package venue

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/screener"
)

// Upbit collector (T-075 Tier-4, docs/research/venues/upbit.md): the
// Korean spot venue, three quote lanes (KRW, BTC, USDT — quote-first
// market ids, live 853 markets: 288 KRW / 328 BTC / 237 USDT), no
// perpetuals and no public deposit/withdraw status. Every endpoint
// fact live-verified 2026-09-13; the official docs host
// (api-docs.upbit.com) is egress-blocked from this environment, so
// nothing doc-only below is claimed VERIFIED.
//   - GET /v1/market/all: [{market "KRW-BTC", korean_name,
//     english_name}] — the instrument list; the market id IS
//     <quote>-<base> in the venue's own list (the only place base and
//     quote exist — the id is the instrument list's documented format,
//     not a ticker symbol being split); KRW-USDT exists (the venue's
//     own FX pair, context for the KRW lane, never a conversion).
//   - GET /v1/ticker?markets=… carries no bid/ask (research §1) and
//     GET /v1/ticker/all refuses with 400 — the orderbook is the only
//     top-of-book source:
//   - GET /v1/orderbook?markets=KRW-BTC,KRW-ETH (comma list, VERIFIED
//     live): [{market, timestamp (ms), total_ask_size, total_bid_size,
//     orderbook_units: [{bid_price, bid_size, ask_price, ask_size}…]}]
//     — units[0] is the best level WITH sizes (bare JSON numbers,
//     parsed from literal text); each row carries its own timestamp.
//     The batch cap is UNVERIFIED (docs host blocked) → a conservative
//     40 markets per request, one sweep per poll covers every market
//     in ceil(n/40) requests.
//   - rate limits: read live from the remaining-req response header —
//     600 requests/min AND 10 requests/s per group ("orderbook",
//     "market") → one 10 req/s venue gate satisfies both windows.
//   - fees: UNVERIFIED (third-party corroboration only: KRW market
//     0.05 %, BTC/USDT markets 0.25 %) → the conservative 0.25 %
//     (25 bps) covers every lane; Verified=false and the settings UI
//     shows Upbit as unverified.
//   - Networks(): no public status endpoint (/v1/market/warnings 404
//     live; deposit/withdraw status is authenticated) → key-gated
//     unknowns.
type upbitCollector struct {
	base string
	gate *gate
	c    *client
	inst *instrumentCache
	now  func() time.Time
}

const (
	upbitBase      = "https://api.upbit.com"
	upbitBatchSize = 40 // markets per /v1/orderbook request (cap UNVERIFIED — conservative)
)

func newUpbit(opts Options) *upbitCollector {
	now := opts.now()
	c := &upbitCollector{base: upbitBase, now: now}
	if opts.SpotBase != "" {
		c.base = opts.SpotBase
	}
	c.gate = newGate(10, time.Second, now)
	c.c = newClient(screener.VenueUpbit, c.gate)
	c.inst = newInstrumentCache(opts, c.fetchInstruments)
	return c
}

func (c *upbitCollector) ID() screener.Venue { return screener.VenueUpbit }
func (c *upbitCollector) Fees() Fees {
	return Fees{SpotTakerBps: bps("0.0025"), PerpTakerBps: bps("0.0025"), Verified: false}
}
func (c *upbitCollector) RateLimited() int { return int(c.gate.limited.Load()) }

type upbitMarket struct {
	Market string `json:"market"` // <quote>-<base>, e.g. KRW-BTC
}

func (c *upbitCollector) fetchInstruments(ctx context.Context) ([]Instrument, error) {
	var rows []upbitMarket
	if err := c.c.getJSON(ctx, 1, c.base, "/v1/market/all", nil, &rows); err != nil {
		return nil, err
	}
	out := make([]Instrument, 0, len(rows))
	for _, m := range rows {
		quote, base, ok := strings.Cut(m.Market, "-")
		if !ok || quote == "" || base == "" || strings.Contains(base, "-") {
			continue // not the documented <quote>-<base> shape
		}
		out = append(out, Instrument{
			Venue: screener.VenueUpbit, Kind: KindSpot, Symbol: m.Market,
			Base: base, Quote: quote,
			// Every id in market/all is a listed, trading market.
			Tradable: true,
		})
	}
	return out, nil
}

func (c *upbitCollector) Instruments(ctx context.Context) ([]Instrument, error) {
	return c.inst.refresh(ctx)
}

type upbitBookRow struct {
	Market         string `json:"market"`
	Timestamp      num    `json:"timestamp"` // ms, bare JSON number
	OrderbookUnits []struct {
		BidPrice num `json:"bid_price"`
		BidSize  num `json:"bid_size"`
		AskPrice num `json:"ask_price"`
		AskSize  num `json:"ask_size"`
	} `json:"orderbook_units"`
}

// Spot sweeps every market's top of book in comma batches of
// upbitBatchSize — one full sweep per poll, all of it fresh (no
// carry-over needed: the sweep always covers the whole list).
func (c *upbitCollector) Spot(ctx context.Context) ([]screener.Quote, error) {
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
	if len(symbols) == 0 {
		return nil, nil
	}
	out := make([]screener.Quote, 0, len(symbols))
	for start := 0; start < len(symbols); start += upbitBatchSize {
		end := min(start+upbitBatchSize, len(symbols))
		q := url.Values{"markets": {strings.Join(symbols[start:end], ",")}}
		var rows []upbitBookRow
		if err := c.c.getJSON(ctx, 1, c.base, "/v1/orderbook", q, &rows); err != nil {
			return nil, err
		}
		for _, r := range rows {
			in, ok := inst[r.Market]
			if !ok || !in.Tradable || len(r.OrderbookUnits) == 0 {
				continue
			}
			top := r.OrderbookUnits[0]
			if !top.BidPrice.IsPositive() || !top.AskPrice.IsPositive() {
				continue
			}
			at := c.now()
			if r.Timestamp.IsPositive() {
				at = time.UnixMilli(r.Timestamp.IntPart())
			}
			out = append(out, screener.Quote{
				Venue: screener.VenueUpbit, Base: in.Base, Quote: in.Quote,
				Bid: top.BidPrice.Decimal, BidQty: top.BidSize.Decimal,
				Ask: top.AskPrice.Decimal, AskQty: top.AskSize.Decimal,
				At: at,
			})
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("upbit: orderbook sweep returned no rows for %d markets", len(symbols))
	}
	return out, nil
}

func (c *upbitCollector) Perps(ctx context.Context) ([]screener.Perp, error) {
	// Upbit operates spot markets only (research §3, VERIFIED-ABSENT
	// from the live market list).
	return []screener.Perp{}, nil
}

func (c *upbitCollector) Networks(ctx context.Context) (map[string]screener.NetworkStatus, error) {
	return c.inst.keyGatedNetworks(ctx)
}
