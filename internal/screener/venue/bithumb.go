package venue

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/screener"
)

// Bithumb collector. Research: docs/research/venues/bithumb.md, every
// endpoint live-verified 2026-09-13; official docs at apidocs.bithumb.com
// (markdown mirrors). api.bithumb.com is the Korean KRW venue: the KRW
// lane (with Upbit) and a small BTC-quoted lane; there is no USDT lane
// (ALL_USDT refuses, status 5500) and no perpetuals on this host.
//   - GET /public/ticker/ALL_KRW and ALL_BTC: {status:"0000", data:
//     {<COIN>:{…24 h stats…}}} — the coin keys are the instrument
//     universe per payment currency; no bid/ask (호가 조회 reference).
//   - GET /public/orderbook/ALL and ALL_BTC: one request per payment
//     currency returns EVERY market's book; data carries a string ms
//     timestamp + payment_currency + one {order_currency, bids[], asks[]}
//     per coin; each level is {price, quantity} (JSON strings) and the
//     arrays are sorted best-first — bids[0]/asks[0] is the top of book
//     WITH sizes, so quotes carry real depth (not LiquidityUnknown).
//   - GET /public/assetsstatus/ALL: per-coin {withdrawal_status,
//     deposit_status} ints — 1 is the only value treated as available;
//     anything else maps to closed (the exact semantics are UNVERIFIED,
//     so the mapping is the conservative one).
//   - rate limits: 150 requests/s per IP per public category
//     (api-요청-수-제한-안내) → a conservative 10 req/s venue gate; the
//     collector spends 2-3 requests per poll.
//   - fees: official fee page (bithumb.com/react/info/fee/trade) lists
//     KRW maker 0.25 %→0.04 % and BTC maker 0.25 %→free via an opt-in
//     program, taker 0.25 % — the STANDARD tier 0.25 %/0.25 % is what
//     the fee table carries (Verified=false: the opt-in state cannot be
//     confirmed remotely, and assuming it repeats the token-discount
//     defect fixed in T-057's review).
//   - symbol → base/quote: built from the payment currency + coin key,
//     never by splitting a symbol string; the venue-native id is the
//     v1 quotation API's own "KRW-BTC" market format (호가-조회:
//     markets=KRW-BTC,BTC-ETH).
type bithumbCollector struct {
	base string
	gate *gate
	c    *client
	inst *instrumentCache
	now  func() time.Time
}

const bithumbBase = "https://api.bithumb.com"

func newBithumb(opts Options) *bithumbCollector {
	now := opts.now()
	c := &bithumbCollector{base: bithumbBase, now: now}
	if opts.SpotBase != "" {
		c.base = opts.SpotBase
	}
	c.gate = newGate(10, time.Second, now)
	c.c = newClient(screener.VenueBithumb, c.gate)
	c.inst = newInstrumentCache(opts, c.fetchInstruments)
	return c
}

func (c *bithumbCollector) ID() screener.Venue { return screener.VenueBithumb }
func (c *bithumbCollector) Fees() Fees {
	return Fees{SpotTakerBps: bps("0.0025"), PerpTakerBps: bps("0.0025"), Verified: false}
}
func (c *bithumbCollector) RateLimited() int { return int(c.gate.limited.Load()) }

// bithumbLane is one payment currency's market set.
type bithumbLane struct {
	quote      string // KRW, BTC
	tickerPath string
	bookPath   string
}

var bithumbLanes = []bithumbLane{
	{quote: "KRW", tickerPath: "/public/ticker/ALL_KRW", bookPath: "/public/orderbook/ALL"},
	{quote: "BTC", tickerPath: "/public/ticker/ALL_BTC", bookPath: "/public/orderbook/ALL_BTC"},
}

type bithumbResp[T any] struct {
	Status string `json:"status"`
	Data   T      `json:"data"`
}

func (c *bithumbCollector) fetchInstruments(ctx context.Context) ([]Instrument, error) {
	var out []Instrument
	for _, lane := range bithumbLanes {
		var resp bithumbResp[map[string]json.RawMessage]
		if err := c.c.getJSON(ctx, 1, c.base, lane.tickerPath, nil, &resp); err != nil {
			return nil, err
		}
		if resp.Status != "0000" {
			return nil, fmt.Errorf("bithumb: %s: status %s", lane.tickerPath, resp.Status)
		}
		for coin := range resp.Data {
			out = append(out, Instrument{
				Venue: screener.VenueBithumb, Kind: KindSpot,
				Symbol: lane.quote + "-" + coin, Base: coin, Quote: lane.quote,
				// Every key in the ALL ticker is a listed, trading market;
				// suspensions surface per-asset through assetsstatus, not here.
				Tradable: true,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Symbol < out[j].Symbol })
	return out, nil
}

func (c *bithumbCollector) Instruments(ctx context.Context) ([]Instrument, error) {
	return c.inst.refresh(ctx)
}

type bithumbLevel struct {
	Price    num `json:"price"`
	Quantity num `json:"quantity"`
}

type bithumbBook struct {
	OrderCurrency string         `json:"order_currency"`
	Bids          []bithumbLevel `json:"bids"`
	Asks          []bithumbLevel `json:"asks"`
}

// bithumbBooks splits the orderbook payload's data object into its
// envelope keys (timestamp, payment_currency) and the per-coin rows.
type bithumbBooks struct {
	Timestamp       num                    `json:"timestamp"` // ms, JSON string
	PaymentCurrency string                 `json:"payment_currency"`
	Coins           map[string]bithumbBook `json:"-"`
}

func (b *bithumbBooks) UnmarshalJSON(raw []byte) error {
	var mixed map[string]json.RawMessage
	if err := json.Unmarshal(raw, &mixed); err != nil {
		return err
	}
	*b = bithumbBooks{Coins: make(map[string]bithumbBook, len(mixed))}
	for k, v := range mixed {
		switch k {
		case "timestamp":
			if err := json.Unmarshal(v, &b.Timestamp); err != nil {
				return err
			}
		case "payment_currency":
			if err := json.Unmarshal(v, &b.PaymentCurrency); err != nil {
				return err
			}
		default:
			var bk bithumbBook
			if err := json.Unmarshal(v, &bk); err != nil {
				return err
			}
			b.Coins[k] = bk
		}
	}
	return nil
}

func (c *bithumbCollector) Spot(ctx context.Context) ([]screener.Quote, error) {
	inst, err := c.inst.ensure(ctx, KindSpot)
	if err != nil {
		return nil, err
	}
	var out []screener.Quote
	for _, lane := range bithumbLanes {
		var resp bithumbResp[bithumbBooks]
		if err := c.c.getJSON(ctx, 1, c.base, lane.bookPath, nil, &resp); err != nil {
			return nil, err
		}
		if resp.Status != "0000" {
			return nil, fmt.Errorf("bithumb: %s: status %s", lane.bookPath, resp.Status)
		}
		if resp.Data.PaymentCurrency != lane.quote {
			return nil, fmt.Errorf("bithumb: %s: payment_currency %q, want %q", lane.bookPath, resp.Data.PaymentCurrency, lane.quote)
		}
		at := c.now()
		if resp.Data.Timestamp.IsPositive() {
			at = time.UnixMilli(resp.Data.Timestamp.IntPart())
		}
		for coin, book := range resp.Data.Coins {
			in, ok := inst[lane.quote+"-"+coin]
			if !ok || !in.Tradable || len(book.Bids) == 0 || len(book.Asks) == 0 {
				continue
			}
			bid, ask := book.Bids[0], book.Asks[0]
			if !bid.Price.IsPositive() || !ask.Price.IsPositive() {
				continue
			}
			out = append(out, screener.Quote{
				Venue: screener.VenueBithumb, Base: coin, Quote: lane.quote,
				Bid: bid.Price.Decimal, BidQty: bid.Quantity.Decimal,
				Ask: ask.Price.Decimal, AskQty: ask.Quantity.Decimal,
				At: at,
			})
		}
	}
	return out, nil
}

func (c *bithumbCollector) Perps(ctx context.Context) ([]screener.Perp, error) {
	// No perpetuals on api.bithumb.com (research §3, VERIFIED-ABSENT).
	return []screener.Perp{}, nil
}

func (c *bithumbCollector) Networks(ctx context.Context) (map[string]screener.NetworkStatus, error) {
	var resp bithumbResp[map[string]struct {
		WithdrawalStatus int `json:"withdrawal_status"`
		DepositStatus    int `json:"deposit_status"`
	}]
	if err := c.c.getJSON(ctx, 1, c.base, "/public/assetsstatus/ALL", nil, &resp); err != nil {
		return nil, err
	}
	if resp.Status != "0000" {
		return nil, fmt.Errorf("bithumb: assetsstatus: status %s", resp.Status)
	}
	out := make(map[string]screener.NetworkStatus, len(resp.Data))
	for coin, st := range resp.Data {
		// Only 1 counts as available (conservative; semantics UNVERIFIED).
		switch {
		case st.WithdrawalStatus == 1 && st.DepositStatus == 1:
			out[coin] = screener.NetworkStatus{Status: screener.NetworkOpen}
		default:
			out[coin] = screener.NetworkStatus{Status: screener.NetworkClosed,
				Reason: strings.TrimSpace(fmt.Sprintf("withdrawal_status=%d deposit_status=%d", st.WithdrawalStatus, st.DepositStatus))}
		}
	}
	return out, nil
}
