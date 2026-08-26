// Package graph builds the directed conversion graph from tradeable
// markets and enumerates canonical three-leg cycles (triangles). Topology
// is computed once per instrument/config change and cached; per-tick work
// is only the ByMarket lookup (SKILL.md §13–§14).
package graph

import (
	"fmt"
	"strings"

	"github.com/cploutarchou/arb-chain-bot/internal/exchange"
)

// Leg is one directed conversion: spend From, receive To, by taking the
// stated side on Market. SideBuy consumes asks (quote→base); SideSell
// consumes bids (base→quote). Direction and side are fixed at enumeration
// time — pricing never re-derives them (resources/triangular-math.md).
type Leg struct {
	Market exchange.MarketID
	Base   exchange.Asset
	Quote  exchange.Asset
	From   exchange.Asset
	To     exchange.Asset
	Side   exchange.Side
}

// Triangle is a directed three-leg cycle starting and ending in Start.
type Triangle struct {
	ID       string // canonical: exchange|start|sym1>sym2>sym3
	Exchange exchange.ExchangeID
	Start    exchange.Asset
	Legs     [3]Leg
}

// Topology is the cached triangle universe with the market→triangles
// index that drives dirty re-pricing.
type Topology struct {
	Triangles []Triangle
	ByMarket  map[exchange.MarketID][]int // indices into Triangles
	Rejected  RejectionStats
}

// RejectionStats records why markets were excluded (observability; a
// silent shrink of the universe is a debugging nightmare).
type RejectionStats struct {
	SelfLoop    int // base == quote
	Untradeable int // disabled or not TRADING
	BadRules    int // unusable instrument rules
}

// AffectedBy returns the indices of triangles that must be re-priced when
// a market's book changes.
func (t *Topology) AffectedBy(id exchange.MarketID) []int { return t.ByMarket[id] }

type edge struct {
	market exchange.Market
	from   exchange.Asset
	to     exchange.Asset
	side   exchange.Side
}

// Build enumerates every valid directed triangle for the configured
// starting assets. Multiple markets over the same asset pair enumerate
// separately (they have distinct books). The two directions of a cycle are
// distinct triangles: their legs consume different book sides.
func Build(ex exchange.ExchangeID, markets []exchange.Market, starts []exchange.Asset) *Topology {
	topo := &Topology{ByMarket: make(map[exchange.MarketID][]int)}

	// Directed adjacency: outgoing[asset] = edges spending that asset.
	outgoing := make(map[exchange.Asset][]edge)
	for _, m := range markets {
		if m.ID.Exchange != ex {
			continue
		}
		if m.Base == m.Quote {
			topo.Rejected.SelfLoop++
			continue
		}
		if !m.Tradeable() {
			topo.Rejected.Untradeable++
			continue
		}
		if !m.Rules.Usable() {
			topo.Rejected.BadRules++
			continue
		}
		outgoing[m.Quote] = append(outgoing[m.Quote], edge{market: m, from: m.Quote, to: m.Base, side: exchange.SideBuy})
		outgoing[m.Base] = append(outgoing[m.Base], edge{market: m, from: m.Base, to: m.Quote, side: exchange.SideSell})
	}

	seen := make(map[string]struct{})
	for _, start := range starts {
		for _, e1 := range outgoing[start] {
			b := e1.to
			for _, e2 := range outgoing[b] {
				c := e2.to
				if c == start || c == b {
					continue // two-cycles and self-steps are not triangles
				}
				if e2.market.ID == e1.market.ID {
					continue
				}
				for _, e3 := range outgoing[c] {
					if e3.to != start {
						continue
					}
					if e3.market.ID == e1.market.ID || e3.market.ID == e2.market.ID {
						continue
					}
					tri := Triangle{
						Exchange: ex,
						Start:    start,
						Legs: [3]Leg{
							legFrom(e1), legFrom(e2), legFrom(e3),
						},
					}
					tri.ID = canonicalID(tri)
					if _, dup := seen[tri.ID]; dup {
						continue
					}
					seen[tri.ID] = struct{}{}
					idx := len(topo.Triangles)
					topo.Triangles = append(topo.Triangles, tri)
					for _, l := range tri.Legs {
						topo.ByMarket[l.Market] = append(topo.ByMarket[l.Market], idx)
					}
				}
			}
		}
	}
	return topo
}

func legFrom(e edge) Leg {
	return Leg{
		Market: e.market.ID,
		Base:   e.market.Base,
		Quote:  e.market.Quote,
		From:   e.from,
		To:     e.to,
		Side:   e.side,
	}
}

// canonicalID pins one directed cycle with a fixed start to one string.
// Ordered leg symbols preserve direction: the reverse cycle produces a
// different ID by construction, and rotations cannot occur because the
// start asset is part of the identity.
func canonicalID(t Triangle) string {
	syms := make([]string, 0, 3)
	for _, l := range t.Legs {
		syms = append(syms, string(l.Market.Symbol))
	}
	return fmt.Sprintf("%s|%s|%s", t.Exchange, t.Start, strings.Join(syms, ">"))
}

// Validate checks a triangle's structural invariants; it exists for tests
// and defensive assertions, not the hot path.
func (t Triangle) Validate() error {
	if t.Legs[0].From != t.Start {
		return fmt.Errorf("leg1 starts in %s, triangle starts in %s", t.Legs[0].From, t.Start)
	}
	if t.Legs[2].To != t.Start {
		return fmt.Errorf("leg3 ends in %s, triangle starts in %s", t.Legs[2].To, t.Start)
	}
	for i := 0; i < 2; i++ {
		if t.Legs[i].To != t.Legs[i+1].From {
			return fmt.Errorf("leg%d output %s != leg%d input %s", i+1, t.Legs[i].To, i+2, t.Legs[i+1].From)
		}
	}
	if t.Legs[0].Market == t.Legs[1].Market || t.Legs[1].Market == t.Legs[2].Market || t.Legs[0].Market == t.Legs[2].Market {
		return fmt.Errorf("duplicate market in cycle")
	}
	return nil
}
