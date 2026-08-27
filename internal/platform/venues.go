package platform

import (
	"github.com/cploutarchou/arb-chain-bot/internal/exchange"
	"github.com/cploutarchou/arb-chain-bot/internal/fees"
)

// VenueProfile is one row of VenueTable (T-061, settings-expansion §5):
// the venue display table, a superset of the CompiledVenues enforcement
// gate (D12). Unavailable venues carry the task that blocks them.
type VenueProfile struct {
	ID        string                 `json:"id"`
	Name      string                 `json:"name"`
	Available bool                   `json:"available"`
	Reason    string                 `json:"reason,omitempty"`
	Discount  *CompiledVenueDiscount `json:"discount,omitempty"`
}

// venueRows is the static registry: id, display name, blocking task
// (empty = built). Availability is derived from CompiledVenues so the
// two can never disagree.
var venueRows = []struct{ id, name, blockedBy string }{
	{"binance", "Binance", ""},
	{"okx", "OKX", "connector not built (T-050)"},
	{"bybit", "Bybit", "connector not built (T-051)"},
	{"bitget", "Bitget", "connector not built (T-051)"},
	{"gate", "Gate", "connector not built (T-051)"},
	{"mexc", "MEXC", "connector not built (T-051; researched T-056)"},
}

// VenueTable returns every venue the console may list, sorted as
// registered (binance first, then the unbuilt ones), with the compiled-in
// fee-discount profile where one exists. Every venue is public market
// data only — no API keys are used anywhere (credentials stored under
// Security are never read; secrets/registry.go).
func VenueTable() []VenueProfile {
	out := make([]VenueProfile, 0, len(venueRows))
	for _, r := range venueRows {
		v := VenueProfile{ID: r.id, Name: r.name, Available: CompiledVenues[r.id]}
		if !v.Available {
			v.Reason = r.blockedBy
			if v.Reason == "" {
				v.Reason = "connector not compiled into this build"
			}
		}
		if d, ok := fees.VenueDiscount(exchange.ExchangeID(r.id)); ok {
			v.Discount = &CompiledVenueDiscount{
				PayAsset:     string(d.PayAsset),
				Rate:         d.Rate.String(),
				AppliesToAPI: d.AppliesToAPI,
				Modeled:      false,
				Reason:       "token-paid discounts are not modeled: no pay-asset ledger",
			}
		}
		out = append(out, v)
	}
	return out
}

func venueProfile(id string) (VenueProfile, bool) {
	for _, v := range VenueTable() {
		if v.ID == id {
			return v, true
		}
	}
	return VenueProfile{}, false
}
