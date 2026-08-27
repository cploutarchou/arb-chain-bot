package venue

import (
	"context"
	"testing"

	"github.com/cploutarchou/arb-chain-bot/internal/screener"
)

// Per-venue conformance classes (each cited in the collector header):
//   - verifiedFees: fee numbers read from the venue's official page/API.
//   - publicNetworks: a public currency/chain status endpoint exists, so
//     Networks may answer open/closed rather than the key-gated unknown.
//   - noPerps: the venue lists no perpetuals on its public market data.
//   - noBulkMark: mark price is per-contract (round-robin), so a perp may
//     carry Mark 0 (basis.go skips it) while Index is set.
var (
	verifiedFees   = map[screener.Venue]bool{screener.VenueBinance: true, screener.VenueKuCoin: true, screener.VenueKraken: true}
	publicNetworks = map[screener.Venue]bool{screener.VenueGate: true, screener.VenueKuCoin: true, screener.VenueHTX: true, screener.VenueCoinbase: true}
	noPerps        = map[screener.Venue]bool{screener.VenueCoinbase: true}
	noBulkMark     = map[screener.Venue]bool{screener.VenueHTX: true}
)

// TestConformance is the shared gate every collector must pass before a
// venue is enabled by default (SKILL.md step 3): over the recorded
// fixtures, ≥ 90 % of tradable spot instruments produce a Quote, ≥ 90 %
// of tradable perps produce a Perp, no zero/negative prices, base/quote
// non-empty, ages set, Networks answers for every base, Fees non-zero.
func TestConformance(t *testing.T) {
	for _, id := range screener.OrderedVenues {
		t.Run(string(id), func(t *testing.T) {
			c, _ := fixtureCollector(t, id)
			ctx := context.Background()
			if c.ID() != id {
				t.Fatalf("ID() = %s", c.ID())
			}
			inst, err := c.Instruments(ctx)
			if err != nil {
				t.Fatalf("Instruments: %v", err)
			}
			tradSpot, tradPerp := 0, 0
			for _, in := range inst {
				if in.Symbol == "" || in.Base == "" || in.Quote == "" {
					t.Fatalf("instrument with empty symbol/base/quote: %+v", in)
				}
				if !in.Tradable {
					continue
				}
				switch in.Kind {
				case KindSpot:
					tradSpot++
				case KindPerp:
					tradPerp++
				}
			}
			if tradSpot == 0 || (tradPerp == 0) != noPerps[id] {
				t.Fatalf("fixture has %d tradable spot / %d tradable perp instruments", tradSpot, tradPerp)
			}

			quotes, err := c.Spot(ctx)
			if err != nil {
				t.Fatalf("Spot: %v", err)
			}
			for _, q := range quotes {
				if q.Venue != id || q.Base == "" || q.Quote == "" {
					t.Fatalf("bad quote identity: %+v", q)
				}
				if !q.Bid.IsPositive() || !q.Ask.IsPositive() {
					t.Fatalf("non-positive price: %+v", q)
				}
				if q.BidQty.IsNegative() || q.AskQty.IsNegative() {
					t.Fatalf("negative size: %+v", q)
				}
				if q.At.IsZero() {
					t.Fatalf("age not set: %+v", q)
				}
				if id == screener.VenueGate && !q.LiquidityUnknown {
					t.Fatalf("gate quote must carry LiquidityUnknown: %+v", q)
				}
				if id != screener.VenueGate && q.LiquidityUnknown {
					t.Fatalf("%s quote wrongly flagged LiquidityUnknown", id)
				}
			}
			if pct := 100 * len(quotes) / tradSpot; pct < 90 {
				t.Fatalf("spot coverage %d%% (%d quotes / %d tradable) < 90%%", pct, len(quotes), tradSpot)
			}

			perps, err := c.Perps(ctx)
			if err != nil {
				t.Fatalf("Perps: %v", err)
			}
			for _, p := range perps {
				if p.Venue != id || p.Base == "" || p.Quote == "" {
					t.Fatalf("bad perp identity: %+v", p)
				}
				if !p.Mark.IsPositive() || p.Bid.IsNegative() || p.Ask.IsNegative() || p.Index.IsNegative() {
					if !noBulkMark[id] || !p.Mark.IsZero() || !p.Index.IsPositive() {
						t.Fatalf("bad perp price: %+v", p)
					}
				}
				if p.At.IsZero() {
					t.Fatalf("perp age not set: %+v", p)
				}
				if p.IntervalH < 0 || p.IntervalH > 24 {
					t.Fatalf("perp interval out of range: %+v", p)
				}
			}
			if noPerps[id] {
				if len(perps) != 0 {
					t.Fatalf("%s must return no perps, got %d", id, len(perps))
				}
			} else if pct := 100 * len(perps) / tradPerp; pct < 90 {
				t.Fatalf("perp coverage %d%% (%d / %d tradable) < 90%%", pct, len(perps), tradPerp)
			}

			nets, err := c.Networks(ctx)
			if err != nil {
				t.Fatalf("Networks: %v", err)
			}
			if len(nets) == 0 {
				t.Fatal("Networks returned nothing")
			}
			for asset, st := range nets {
				if !publicNetworks[id] && (st.Status != screener.NetworkUnknown || st.Reason != KeyGatedReason) {
					t.Fatalf("%s: key-gated venue must answer unknown/%q, got %+v for %s", id, KeyGatedReason, st, asset)
				}
			}

			f := c.Fees()
			if !f.SpotTakerBps.IsPositive() || !f.PerpTakerBps.IsPositive() {
				t.Fatalf("fees must be positive: %+v", f)
			}
			if f.Verified != verifiedFees[id] {
				t.Fatalf("%s Fees().Verified = %v; want %v (see verifiedFees)", id, f.Verified, verifiedFees[id])
			}
			t.Logf("%s: instruments=%d (spot %d / perp %d tradable) quotes=%d perps=%d networks=%d rate_limited=%d",
				id, len(inst), tradSpot, tradPerp, len(quotes), len(perps), len(nets), c.RateLimited())
		})
	}
}

// TestRegistry pins the registry to the ten venues (six Tier-1 + four
// Tier-2, T-075) and their verification flags.
func TestRegistry(t *testing.T) {
	reg := Registry()
	if len(reg) != len(screener.OrderedVenues) {
		t.Fatalf("registry has %d venues, want %d", len(reg), len(screener.OrderedVenues))
	}
	for i, e := range reg {
		if e.ID != screener.OrderedVenues[i] {
			t.Fatalf("registry order: %s at %d", e.ID, i)
		}
		if e.Verified != verifiedFees[e.ID] {
			t.Fatalf("%s Verified=%v", e.ID, e.Verified)
		}
	}
}
