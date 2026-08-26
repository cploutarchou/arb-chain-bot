package binance

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/exchange"
	"github.com/cploutarchou/arb-chain-bot/internal/orderbook"
)

// Binance sends prices/quantities as JSON strings — decoded straight into
// decimals, never through float64 (resources/order-book.md).

type wsEnvelope struct {
	Stream string          `json:"stream"`
	Data   json.RawMessage `json:"data"`
}

type depthUpdate struct {
	EventType string      `json:"e"`
	EventTime int64       `json:"E"` // ms epoch
	Symbol    string      `json:"s"`
	FirstID   int64       `json:"U"`
	FinalID   int64       `json:"u"`
	Bids      [][2]string `json:"b"`
	Asks      [][2]string `json:"a"`
}

type restDepth struct {
	LastUpdateID int64       `json:"lastUpdateId"`
	Bids         [][2]string `json:"bids"`
	Asks         [][2]string `json:"asks"`
}

// ErrNotDepthEvent marks frames of other stream types (trades, klines);
// callers route or skip them.
var ErrNotDepthEvent = fmt.Errorf("binance: not a depth event")

// DecodeWSFrame decodes a raw WS frame (combined-stream envelope or bare
// event) into a normalized DepthEvent.
func DecodeWSFrame(frame []byte, recv time.Time) (orderbook.DepthEvent, error) {
	body := frame
	var env wsEnvelope
	if err := json.Unmarshal(frame, &env); err == nil && len(env.Data) > 0 {
		body = env.Data
	}
	var du depthUpdate
	if err := json.Unmarshal(body, &du); err != nil {
		return orderbook.DepthEvent{}, fmt.Errorf("binance: decode frame: %w", err)
	}
	if du.EventType != "depthUpdate" {
		return orderbook.DepthEvent{}, fmt.Errorf("%w: %q", ErrNotDepthEvent, du.EventType)
	}
	if du.Symbol == "" || du.FinalID == 0 {
		return orderbook.DepthEvent{}, fmt.Errorf("binance: malformed depthUpdate (symbol=%q u=%d)", du.Symbol, du.FinalID)
	}
	bids, err := parseLevels(du.Bids)
	if err != nil {
		return orderbook.DepthEvent{}, fmt.Errorf("binance: bids: %w", err)
	}
	asks, err := parseLevels(du.Asks)
	if err != nil {
		return orderbook.DepthEvent{}, fmt.Errorf("binance: asks: %w", err)
	}
	return orderbook.DepthEvent{
		Market:        exchange.MarketID{Exchange: ID, Symbol: exchange.Symbol(du.Symbol)},
		FirstUpdateID: du.FirstID,
		FinalUpdateID: du.FinalID,
		Bids:          bids,
		Asks:          asks,
		EventTime:     time.UnixMilli(du.EventTime),
		ReceiveTime:   recv,
	}, nil
}

// DecodeRESTSnapshot decodes /api/v3/depth output. The snapshot carries no
// event time; ReceiveTime anchors age.
func DecodeRESTSnapshot(symbol exchange.Symbol, body []byte, recv time.Time) (orderbook.DepthEvent, error) {
	var rd restDepth
	if err := json.Unmarshal(body, &rd); err != nil {
		return orderbook.DepthEvent{}, fmt.Errorf("binance: decode snapshot: %w", err)
	}
	if rd.LastUpdateID == 0 {
		return orderbook.DepthEvent{}, fmt.Errorf("binance: snapshot missing lastUpdateId")
	}
	bids, err := parseLevels(rd.Bids)
	if err != nil {
		return orderbook.DepthEvent{}, fmt.Errorf("binance: snapshot bids: %w", err)
	}
	asks, err := parseLevels(rd.Asks)
	if err != nil {
		return orderbook.DepthEvent{}, fmt.Errorf("binance: snapshot asks: %w", err)
	}
	return orderbook.DepthEvent{
		Market:        exchange.MarketID{Exchange: ID, Symbol: symbol},
		IsSnapshot:    true,
		FinalUpdateID: rd.LastUpdateID,
		Bids:          bids,
		Asks:          asks,
		ReceiveTime:   recv,
	}, nil
}

func parseLevels(raw [][2]string) ([]orderbook.Level, error) {
	out := make([]orderbook.Level, 0, len(raw))
	for _, pq := range raw {
		price, err := decimal.NewFromString(pq[0])
		if err != nil {
			return nil, fmt.Errorf("price %q: %w", pq[0], err)
		}
		qty, err := decimal.NewFromString(pq[1])
		if err != nil {
			return nil, fmt.Errorf("qty %q: %w", pq[1], err)
		}
		out = append(out, orderbook.Level{Price: price, Qty: qty})
	}
	return out, nil
}
