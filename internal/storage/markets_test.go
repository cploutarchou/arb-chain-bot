package storage

import (
	"context"
	"errors"
	"testing"

	"github.com/cploutarchou/arb-chain-bot/internal/config"
	"github.com/cploutarchou/arb-chain-bot/internal/exchange"
	"github.com/cploutarchou/arb-chain-bot/internal/platform"
)

// TestCatalogPersistedUnscopedValidatesUnconfiguredSymbol is the P2-1
// regression test: engine.go now calls UpsertMarkets with the FULL
// bootstrap catalog (every market the exchange returned), not just the
// currently configured `scoped` subset. Before that fix, a symbol the
// operator had never selected could never be added through the console
// — platform.ValidateAgainstCatalog would report unknown_symbol even
// though the venue genuinely lists it, because storage.Catalog (the
// API-profile fallback platform.Catalog) only ever saw what was already
// in scope. This test writes the full catalog (mirroring engine.go's
// UpsertMarkets(ctx, markets) call) and confirms a symbol OUTSIDE any
// "currently configured" subset still validates.
func TestCatalogPersistedUnscopedValidatesUnconfiguredSymbol(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	step := d("0.001")
	rules := exchange.InstrumentRules{
		QtyMode: exchange.PrecisionStep, QtyStep: step,
		PriceMode: exchange.PrecisionStep, PriceTick: step,
		MinNotional: d("5"),
	}
	// The FULL bootstrap catalog: BTCUSDT/ETHUSDT/ETHBTC are the
	// "currently configured" scope; BNBUSDT is NOT configured by any
	// venue settings document below, but exists on the exchange.
	full := []exchange.Market{
		{ID: exchange.MarketID{Exchange: "binance", Symbol: "BTCUSDT"}, Base: "BTC", Quote: "USDT", Enabled: true, Status: exchange.MarketTrading, Rules: rules},
		{ID: exchange.MarketID{Exchange: "binance", Symbol: "ETHUSDT"}, Base: "ETH", Quote: "USDT", Enabled: true, Status: exchange.MarketTrading, Rules: rules},
		{ID: exchange.MarketID{Exchange: "binance", Symbol: "ETHBTC"}, Base: "ETH", Quote: "BTC", Enabled: true, Status: exchange.MarketTrading, Rules: rules},
		{ID: exchange.MarketID{Exchange: "binance", Symbol: "BNBUSDT"}, Base: "BNB", Quote: "USDT", Enabled: true, Status: exchange.MarketTrading, Rules: rules},
	}
	if err := s.UpsertMarkets(ctx, full); err != nil {
		t.Fatal(err)
	}

	catalog := Catalog{S: s}

	// A document that adds BNBUSDT (never "currently configured" above)
	// must validate: the full catalog, not a scoped one, backs this
	// Catalog implementation.
	doc := platform.Settings{
		Venues: map[string]platform.VenueSettings{
			"binance": {
				Enabled: true, PaperEnabled: true,
				Symbols:        []string{"BTCUSDT", "ETHUSDT", "ETHBTC", "BNBUSDT"},
				StartingAssets: []string{"USDT"},
				Fees: platform.FeeSettings{
					MakerBps: d("10"), TakerBps: d("10"),
				},
			},
		},
		Paper: platform.PaperSettings{Balances: map[string]string{"USDT": "10000"}},
	}.WithDefaults(config.Bootstrap{Mode: config.ModePaper, AIModel: "claude-sonnet-5"})
	if err := doc.Validate(); err != nil {
		t.Fatalf("doc.Validate: %v", err)
	}
	if _, err := platform.ValidateAgainstCatalog(ctx, doc, catalog); err != nil {
		t.Fatalf("ValidateAgainstCatalog rejected an unconfigured-but-listed symbol: %v", err)
	}

	// Sanity: a symbol genuinely absent from the exchange still fails.
	bad := doc
	bad.Venues = map[string]platform.VenueSettings{"binance": func() platform.VenueSettings {
		v := doc.Venues["binance"]
		v.Symbols = append([]string(nil), v.Symbols...)
		v.Symbols = append(v.Symbols, "DOGEUSDT")
		return v
	}()}
	if err := bad.Validate(); err != nil {
		t.Fatalf("bad.Validate: %v", err)
	}
	if _, err := platform.ValidateAgainstCatalog(ctx, bad, catalog); !errors.Is(err, platform.ErrUnknownSymbol) {
		t.Fatalf("expected ErrUnknownSymbol for a genuinely absent symbol, got %v", err)
	}
}
