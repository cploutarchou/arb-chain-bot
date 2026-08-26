package app

import (
	"time"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/api"
	"github.com/cploutarchou/arb-chain-bot/internal/orderbook"
	"github.com/cploutarchou/arb-chain-bot/internal/pricing"
)

// triangleReader adapts the live engine to api.TriangleReader (BL-26).
// It is a value type over *Engine, constructed once at wiring time; every
// call resolves the CURRENT run through e.currentScanner() (the same
// accessor pattern this file's siblings use), so it keeps reporting the
// live run's data across a supervised restart instead of a dead one.
type triangleReader struct{ e *Engine }

// NewTriangleReader wires the adapter (exported for component assembly).
func NewTriangleReader(e *Engine) triangleReader { return triangleReader{e: e} }

// triangleLegReferenceSize is the notional (in the leg's From asset) the
// VWAP/depth preview quotes at. It is informative context for the
// triangle-detail page — NOT a live opportunity's actual sizing, which
// the scanner computes per-opportunity via its own size search.
var triangleLegReferenceSize = decimal.NewFromInt(100)

func (t triangleReader) Triangle(id string) (api.TriangleView, bool) {
	scn := t.e.currentScanner()
	if scn == nil || scn.Topo == nil {
		return api.TriangleView{}, false
	}
	var found bool
	var idx int
	for i, tri := range scn.Topo.Triangles {
		if tri.ID == id {
			idx, found = i, true
			break
		}
	}
	if !found {
		return api.TriangleView{}, false
	}
	tri := scn.Topo.Triangles[idx]

	view := api.TriangleView{ID: tri.ID, Exchange: string(tri.Exchange), StartingAsset: string(tri.Start)}
	for i, leg := range tri.Legs {
		lv := api.TriangleLegView{
			LegNo: i + 1, Market: leg.Market.String(), Side: leg.Side.String(),
			From: string(leg.From), To: string(leg.To),
		}
		var bv orderbook.View
		var haveBook bool
		if scn.Books != nil {
			bv, haveBook = scn.Books.View(leg.Market, 50)
		}
		if haveBook {
			lv.BookState = bv.State.String()
			ageMs := bv.Age(time.Now()).Milliseconds()
			lv.BookAgeMs = &ageMs
			if b, ok := bv.BestBid(); ok {
				s := b.Price.String()
				lv.TopBid = &s
			}
			if a, ok := bv.BestAsk(); ok {
				s := a.Price.String()
				lv.TopAsk = &s
			}
		}
		if scn.Fees != nil {
			eff := scn.Fees.Taker(leg.Market)
			rate := eff.Rate.String()
			lv.FeeRate, lv.FeeSource = &rate, eff.Source
		}
		if haveBook && scn.Fees != nil {
			if rules, ok := scn.Rules[leg.Market]; ok {
				md := pricing.MarketData{View: bv, Rules: rules}
				if q, err := pricing.QuoteLeg(leg, md, scn.Fees, triangleLegReferenceSize); err == nil {
					vwap := q.AvgPrice.String()
					lv.VWAPPrice = &vwap
					impact := q.PriceImpactBps.String()
					lv.PriceImpactBps = &impact
					lv.LevelsConsumed = q.LevelsConsumed
					lv.DepthExhausted = q.DepthExhausted
				}
			}
		}
		view.Legs = append(view.Legs, lv)
	}
	return view, true
}
