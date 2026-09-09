package binance

import (
	"encoding/json"
	"fmt"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/exchange"
)

// exchangeInfo mapping: /api/v3/exchangeInfo → normalized markets with
// InstrumentRules from PRICE_FILTER / LOT_SIZE / NOTIONAL filters
// (docs/research/fees.md §Binance). Unknown filters are ignored; missing
// required filters leave rules unusable, which excludes the market from
// triangles (graph.Build rejects BadRules) instead of guessing.

type exchangeInfoDoc struct {
	Symbols []symbolInfo `json:"symbols"`
}

type symbolInfo struct {
	Symbol     string            `json:"symbol"`
	Status     string            `json:"status"`
	BaseAsset  string            `json:"baseAsset"`
	QuoteAsset string            `json:"quoteAsset"`
	Filters    []json.RawMessage `json:"filters"`
}

type filterHeader struct {
	Type string `json:"filterType"`
}

type priceFilter struct {
	TickSize string `json:"tickSize"`
}

type lotSizeFilter struct {
	MinQty   string `json:"minQty"`
	MaxQty   string `json:"maxQty"`
	StepSize string `json:"stepSize"`
}

type notionalFilter struct {
	MinNotional string `json:"minNotional"`
	MaxNotional string `json:"maxNotional"`
}

// ParseExchangeInfo maps the metadata document to normalized markets.
func ParseExchangeInfo(body []byte) ([]exchange.Market, error) {
	var doc exchangeInfoDoc
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("binance: exchangeInfo: %w", err)
	}
	out := make([]exchange.Market, 0, len(doc.Symbols))
	for _, s := range doc.Symbols {
		m := exchange.Market{
			ID:      exchange.MarketID{Exchange: ID, Symbol: exchange.Symbol(s.Symbol)},
			Base:    exchange.Asset(s.BaseAsset),
			Quote:   exchange.Asset(s.QuoteAsset),
			Status:  mapStatus(s.Status),
			Enabled: true, // platform-level enablement; config narrows later
		}
		rules, err := parseFilters(s.Filters)
		if err != nil {
			return nil, fmt.Errorf("binance: %s: %w", s.Symbol, err)
		}
		m.Rules = rules
		out = append(out, m)
	}
	return out, nil
}

func mapStatus(s string) exchange.MarketStatus {
	switch s {
	case "TRADING":
		return exchange.MarketTrading
	case "HALT", "BREAK":
		return exchange.MarketHalted
	case "DELISTED":
		return exchange.MarketDelisted
	default:
		return exchange.MarketUnknown
	}
}

func parseFilters(filters []json.RawMessage) (exchange.InstrumentRules, error) {
	var r exchange.InstrumentRules
	for _, raw := range filters {
		var h filterHeader
		if err := json.Unmarshal(raw, &h); err != nil {
			return r, err
		}
		switch h.Type {
		case "PRICE_FILTER":
			var f priceFilter
			if err := json.Unmarshal(raw, &f); err != nil {
				return r, err
			}
			tick, err := decimal.NewFromString(f.TickSize)
			if err != nil {
				return r, fmt.Errorf("tickSize: %w", err)
			}
			r.PriceMode = exchange.PrecisionStep
			r.PriceTick = tick
		case "LOT_SIZE":
			var f lotSizeFilter
			if err := json.Unmarshal(raw, &f); err != nil {
				return r, err
			}
			step, err := decimal.NewFromString(f.StepSize)
			if err != nil {
				return r, fmt.Errorf("stepSize: %w", err)
			}
			r.QtyMode = exchange.PrecisionStep
			r.QtyStep = step
			if f.MinQty != "" {
				if r.MinQty, err = decimal.NewFromString(f.MinQty); err != nil {
					return r, fmt.Errorf("minQty: %w", err)
				}
			}
			if f.MaxQty != "" {
				if r.MaxQty, err = decimal.NewFromString(f.MaxQty); err != nil {
					return r, fmt.Errorf("maxQty: %w", err)
				}
			}
		case "MARKET_LOT_SIZE":
			var f lotSizeFilter
			if err := json.Unmarshal(raw, &f); err != nil {
				return r, err
			}
			var err error
			if f.StepSize != "" {
				if r.MarketQtyStep, err = decimal.NewFromString(f.StepSize); err != nil {
					return r, fmt.Errorf("market stepSize: %w", err)
				}
			}
			if f.MinQty != "" {
				if r.MarketMinQty, err = decimal.NewFromString(f.MinQty); err != nil {
					return r, fmt.Errorf("market minQty: %w", err)
				}
			}
			if f.MaxQty != "" {
				if r.MarketMaxQty, err = decimal.NewFromString(f.MaxQty); err != nil {
					return r, fmt.Errorf("market maxQty: %w", err)
				}
			}
		case "NOTIONAL", "MIN_NOTIONAL": // MIN_NOTIONAL is the legacy shape
			var f notionalFilter
			if err := json.Unmarshal(raw, &f); err != nil {
				return r, err
			}
			if f.MinNotional != "" {
				v, err := decimal.NewFromString(f.MinNotional)
				if err != nil {
					return r, fmt.Errorf("minNotional: %w", err)
				}
				r.MinNotional = v
			}
			if f.MaxNotional != "" {
				v, err := decimal.NewFromString(f.MaxNotional)
				if err != nil {
					return r, fmt.Errorf("maxNotional: %w", err)
				}
				r.MaxNotional = v
			}
		}
	}
	return r, nil
}
