package platform

import (
	"context"
	"fmt"
	"sort"

	"github.com/cploutarchou/arb-chain-bot/internal/exchange"
	"github.com/cploutarchou/arb-chain-bot/internal/graph"
)

// Catalog resolves a venue's tradeable market metadata for validation
// that Settings.Validate cannot do purely (design §1.5). engineCatalog
// (internal/app, preferred, uses the engine's retained bootstrap slice)
// and storeCatalog (internal/storage, the API-profile fallback) both
// implement it.
type Catalog interface {
	Markets(ctx context.Context, ex exchange.ExchangeID) ([]exchange.Market, error)
}

// Plan is the topology a settings document would produce on one venue,
// computed with the real graph.Build so the preview's triangle count is
// the number the engine will actually get (design D8).
type Plan struct {
	Venue               string   `json:"venue"`
	Markets             int      `json:"markets"`
	Triangles           int      `json:"triangles"`
	RejectedUntradeable int      `json:"rejected_untradeable"`
	StartingAssets      []string `json:"starting_assets"`
}

// ValidateAgainstCatalog validates the impure parts of a settings
// document (symbol existence/status, starting-asset membership, and a
// topology dry-run per venue) and returns the plan each enabled venue
// would produce. A settings version that cannot build a topology is
// rejected here, at apply time, never discovered at restart (design D8).
func ValidateAgainstCatalog(ctx context.Context, s Settings, c Catalog) (map[string]Plan, error) {
	plans := make(map[string]Plan, len(s.Venues))
	for id, v := range s.Venues {
		if !v.Enabled {
			continue
		}
		markets, err := c.Markets(ctx, exchange.ExchangeID(id))
		if err != nil {
			return nil, fmt.Errorf("platform: %s: fetch markets: %w", id, err)
		}
		byMarket := make(map[string]exchange.Market, len(markets))
		for _, m := range markets {
			byMarket[string(m.ID.Symbol)] = m
		}
		var scoped []exchange.Market
		assetSet := map[exchange.Asset]bool{}
		for _, sym := range v.Symbols {
			m, ok := byMarket[sym]
			if !ok {
				return nil, fmt.Errorf("%w: %s on %s", ErrUnknownSymbol, sym, id)
			}
			if m.Status != exchange.MarketTrading {
				return nil, fmt.Errorf("%w: %s on %s has status %s, not TRADING", ErrUnknownSymbol, sym, id, m.Status)
			}
			scoped = append(scoped, m)
			assetSet[m.Base] = true
			assetSet[m.Quote] = true
		}
		var starts []exchange.Asset
		for _, a := range v.StartingAssets {
			asset := exchange.Asset(a)
			if !assetSet[asset] {
				return nil, fmt.Errorf("%w: starting asset %s on %s is not the base or quote of any selected symbol", ErrNoTriangles, a, id)
			}
			starts = append(starts, asset)
		}
		topo := graph.Build(exchange.ExchangeID(id), scoped, starts)
		if len(topo.Triangles) == 0 {
			return nil, fmt.Errorf("%w: this symbol/asset combination closes no triangle on %s", ErrNoTriangles, id)
		}
		startingOut := append([]string(nil), v.StartingAssets...)
		sort.Strings(startingOut)
		plans[id] = Plan{
			Venue:               id,
			Markets:             len(scoped),
			Triangles:           len(topo.Triangles),
			RejectedUntradeable: topo.Rejected.Untradeable,
			StartingAssets:      startingOut,
		}
	}
	return plans, nil
}
