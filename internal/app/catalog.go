package app

import (
	"context"

	"github.com/cploutarchou/arb-chain-bot/internal/exchange"
	"github.com/cploutarchou/arb-chain-bot/internal/platform"
)

// engineCatalog adapts Engine.Catalog() to platform.Catalog — the
// preferred implementation (design §1.5): it reads the full retained
// bootstrap metadata slice (E2) rather than round-tripping through
// storage. An empty result is ambiguous between "not bootstrapped yet"
// and "this venue genuinely has no markets"; this always means the
// former for a fresh process (a venue with zero markets could never
// have produced a non-empty Settings.Venues[id].Symbols list to begin
// with), so it is reported distinctly as platform.ErrCatalogNotReady —
// never as platform.ErrUnknownSymbol, which would make every symbol in
// a perfectly valid document look wrong during the bootstrap window.
type engineCatalog struct{ e *Engine }

func (c engineCatalog) Markets(_ context.Context, ex exchange.ExchangeID) ([]exchange.Market, error) {
	all := c.e.Catalog()
	if len(all) == 0 {
		return nil, platform.ErrCatalogNotReady
	}
	var out []exchange.Market
	for _, m := range all {
		if m.ID.Exchange == ex {
			out = append(out, m)
		}
	}
	return out, nil
}
