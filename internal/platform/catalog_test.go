package platform

import (
	"context"
	"errors"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/exchange"
)

type fakeCatalog struct {
	markets map[exchange.ExchangeID][]exchange.Market
	err     error
}

func (f fakeCatalog) Markets(_ context.Context, ex exchange.ExchangeID) ([]exchange.Market, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.markets[ex], nil
}

func market(ex, symbol, base, quote string, status exchange.MarketStatus) exchange.Market {
	one := decimal.NewFromInt(1)
	return exchange.Market{
		ID:      exchange.MarketID{Exchange: exchange.ExchangeID(ex), Symbol: exchange.Symbol(symbol)},
		Base:    exchange.Asset(base),
		Quote:   exchange.Asset(quote),
		Status:  status,
		Enabled: true,
		Rules: exchange.InstrumentRules{
			QtyMode: exchange.PrecisionStep, QtyStep: one,
			PriceMode: exchange.PrecisionStep, PriceTick: one,
		},
	}
}

func triangleCatalog() fakeCatalog {
	return fakeCatalog{markets: map[exchange.ExchangeID][]exchange.Market{
		"binance": {
			market("binance", "BTCUSDT", "BTC", "USDT", exchange.MarketTrading),
			market("binance", "ETHUSDT", "ETH", "USDT", exchange.MarketTrading),
			market("binance", "ETHBTC", "ETH", "BTC", exchange.MarketTrading),
		},
	}}
}

func TestValidateAgainstCatalogHappyPath(t *testing.T) {
	s := validSettings()
	plans, err := ValidateAgainstCatalog(context.Background(), s, triangleCatalog())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	plan, ok := plans["binance"]
	if !ok {
		t.Fatal("expected a plan for binance")
	}
	if plan.Markets != 3 {
		t.Fatalf("plan.Markets = %d, want 3", plan.Markets)
	}
	if plan.Triangles < 1 {
		t.Fatalf("plan.Triangles = %d, want >= 1", plan.Triangles)
	}
}

func TestValidateAgainstCatalogUnknownSymbol(t *testing.T) {
	s := validSettings()
	v := s.Venues["binance"]
	v.Symbols = []string{"BTCUSDT", "ETHUSDT", "DOGEUSDT"}
	s.Venues["binance"] = v
	_, err := ValidateAgainstCatalog(context.Background(), s, triangleCatalog())
	if !errors.Is(err, ErrUnknownSymbol) {
		t.Fatalf("expected ErrUnknownSymbol, got %v", err)
	}
}

func TestValidateAgainstCatalogStartingAssetNotInSymbols(t *testing.T) {
	s := validSettings()
	v := s.Venues["binance"]
	v.StartingAssets = []string{"DOGE"}
	s.Venues["binance"] = v
	_, err := ValidateAgainstCatalog(context.Background(), s, triangleCatalog())
	if !errors.Is(err, ErrNoTriangles) {
		t.Fatalf("expected ErrNoTriangles, got %v", err)
	}
}

func TestValidateAgainstCatalogNoTriangles(t *testing.T) {
	s := validSettings()
	v := s.Venues["binance"]
	// Two markets that share no starting asset closing a cycle: BTCUSDT
	// and ETHUSDT alone (no ETHBTC leg) never close a triangle.
	v.Symbols = []string{"BTCUSDT", "ETHUSDT", "BNBUSDT"}
	s.Venues["binance"] = v
	cat := fakeCatalog{markets: map[exchange.ExchangeID][]exchange.Market{
		"binance": {
			market("binance", "BTCUSDT", "BTC", "USDT", exchange.MarketTrading),
			market("binance", "ETHUSDT", "ETH", "USDT", exchange.MarketTrading),
			market("binance", "BNBUSDT", "BNB", "USDT", exchange.MarketTrading),
		},
	}}
	_, err := ValidateAgainstCatalog(context.Background(), s, cat)
	if !errors.Is(err, ErrNoTriangles) {
		t.Fatalf("expected ErrNoTriangles, got %v", err)
	}
}
