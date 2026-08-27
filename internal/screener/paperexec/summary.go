package paperexec

import (
	"context"
	"sort"
	"time"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/screener"
)

// AutoPaperView implements screener.AutoPaperSource (design §7 GET
// /screener/auto-paper; statistics per strategy-models §7).
func (x *Executor) AutoPaperView(ctx context.Context, now time.Time) (screener.AutoPaperView, error) {
	view := screener.AutoPaperView{
		GeneratedAt: now,
		Model:       "paper: simulated top-of-book fills against public quotes, taker fees, slip allowance, limit-IOC tolerance; no real orders",
		Positions:   []screener.PaperPosition{},
		Summary:     screener.AutoPaperSummary{PerRule: []screener.RuleSummary{}},
		Balances:    []screener.PaperBalance{},
	}
	balances, err := x.ledger.ListBalances(ctx)
	if err != nil {
		return view, err
	}
	view.Balances = balances
	positions, err := x.ledger.ListPositions(ctx, "", "", 500)
	if err != nil {
		return view, err
	}
	rules := map[string]screener.Rule{}
	if x.svc.Rules != nil {
		if list, err := x.svc.Rules.ListRules(ctx); err == nil {
			for _, r := range list {
				rules[r.ID] = r
			}
		}
	}
	ruleIDs := map[string]bool{}
	for _, p := range positions {
		ruleIDs[p.RuleID] = true
		if p.Status == StatusOpen {
			x.markOpen(&p, now)
		}
		view.Positions = append(view.Positions, p)
	}
	for id, r := range rules {
		if r.AutoPaper {
			ruleIDs[id] = true
		}
	}
	ids := make([]string, 0, len(ruleIDs))
	for id := range ruleIDs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		s, err := x.ruleSummary(ctx, id, rules[id], now)
		if err != nil {
			return view, err
		}
		view.Summary.PerRule = append(view.Summary.PerRule, s)
	}
	return view, nil
}

// markOpen fills the unrealised mark of an OPEN perp position: spot
// leg at bid, perp leg at ask, funding to date; ages from the book.
func (x *Executor) markOpen(p *screener.PaperPosition, now time.Time) {
	var po perpOpen
	if err := fromMap(p.OpenPayload, &po); err != nil {
		return
	}
	q, okQ := x.svc.Book.QuotesFor(p.Base, p.Quote)[p.VenueA]
	perp, okP := x.svc.Book.PerpFor(p.VenueA, p.Base)
	if !okQ || !okP || !q.Bid.IsPositive() || !perp.Ask.IsPositive() {
		return
	}
	fS, fP := po.SpotFeeBps.Div(decTenK), po.PerpFeeBps.Div(decTenK)
	spot := p.Qty.Mul(q.Bid.Mul(decOne.Sub(fS)).Sub(po.SpotOpen.Mul(decOne.Add(fS))))
	perpPnL := p.Qty.Mul(po.PerpOpen.Sub(perp.Ask)).Sub(fP.Mul(p.Qty).Mul(po.PerpOpen.Add(perp.Ask)))
	mark := spot.Add(perpPnL).Add(p.FundingQuote)
	age := now.Sub(q.At)
	if pa := now.Sub(perp.At); pa > age {
		age = pa
	}
	ageMs := age.Milliseconds()
	p.MarkPnLQuote = &mark
	p.MarkAgeMs = &ageMs
}

func (x *Executor) ruleSummary(ctx context.Context, ruleID string, r screener.Rule, now time.Time) (screener.RuleSummary, error) {
	s := screener.RuleSummary{RuleID: ruleID, Strategy: r.EffectiveStrategy(), Skipped: map[string]int64{},
		InventoryDrift: []screener.DriftRow{}, SlipAllowanceBps: r.SlipBps()}
	if r.ID == "" {
		s.Strategy = ""
	}
	if counter, ok := x.svc.Events.(screener.EventCounter); ok && x.svc.Events != nil {
		n, err := counter.CountEvents(ctx, ruleID)
		if err != nil {
			return s, err
		}
		s.Alerts = n
	}
	positions, err := x.ledger.ListPositions(ctx, ruleID, "", 0)
	if err != nil {
		return s, err
	}
	execs, err := x.ledger.ListExecutions(ctx, ruleID, 0)
	if err != nil {
		return s, err
	}
	var (
		samples, wins int64
		lifetimeSum   decimal.Decimal
		lifetimeN     int64
		slips         []decimal.Decimal
	)
	for _, p := range positions {
		if s.Strategy == "" {
			s.Strategy = p.Strategy
		}
		switch p.Status {
		case StatusSkipped:
			s.Skipped[p.SkippedReason]++
			if p.SkippedReason == SkipUnwind {
				s.UnwindCostQuote = s.UnwindCostQuote.Add(p.PnLQuote)
				s.NetPnLQuote = s.NetPnLQuote.Add(p.PnLQuote)
			}
			if p.SkippedReason == SkipPartialLeg {
				s.PartialLegPnLQuote = s.PartialLegPnLQuote.Add(p.PnLQuote)
				s.NetPnLQuote = s.NetPnLQuote.Add(p.PnLQuote)
			}
		case StatusOpen:
			s.OpenPositions++
			s.Executed++
			age := int64(now.Sub(p.OpenedAt) / time.Second)
			if s.OldestOpenAgeS == nil || age > *s.OldestOpenAgeS {
				a := age
				s.OldestOpenAgeS = &a
			}
		case StatusClosed:
			s.Executed++
			samples++
			if p.PnLQuote.IsPositive() {
				wins++
			}
			s.NetPnLQuote = s.NetPnLQuote.Add(p.PnLQuote)
			if p.ClosedAt != nil && p.Strategy != screener.StrategyCrossVenueSpot {
				lifetimeSum = lifetimeSum.Add(decimal.NewFromInt(int64(p.ClosedAt.Sub(p.OpenedAt) / time.Second)))
				lifetimeN++
			}
		}
	}
	// Spot alert lifetime from closed events (ListEvents is capped at
	// 500 by the store; the mean is over that window).
	if s.Strategy == screener.StrategyCrossVenueSpot && x.svc.Events != nil {
		events, err := x.svc.Events.ListEvents(ctx, ruleID, 500)
		if err == nil {
			for _, e := range events {
				if e.ClosedAt != nil {
					lifetimeSum = lifetimeSum.Add(decimal.NewFromInt(e.LifetimeS))
					lifetimeN++
				}
			}
		}
	}
	type laneKey struct {
		base   string
		venueA screener.Venue
		venueB screener.Venue
	}
	drift := map[laneKey]decimal.Decimal{}
	unmatched := map[laneKey]int{}
	queues := map[laneKey][]decimal.Decimal{}
	var lanes []laneKey
	for _, e := range execs {
		s.FeesQuote = s.FeesQuote.Add(e.FeesQuote)
		if e.RealisedSlipBps != nil {
			slips = append(slips, *e.RealisedSlipBps)
		}
		if t := e.At; s.LastExecutionAt == nil || t.After(*s.LastExecutionAt) {
			tt := t
			s.LastExecutionAt = &tt
		}
		switch e.Kind {
		case KindFunding:
			s.FundingRows++
			s.FundingQuote = s.FundingQuote.Add(e.PnLQuote)
		case KindSpot:
			qty := decimal.Zero
			for _, f := range e.Fills {
				if f.Status == "FILLED" {
					qty = f.Qty
					break
				}
			}
			k := laneKey{e.Base, e.VenueA, e.VenueB}
			rev := laneKey{e.Base, e.VenueB, e.VenueA}
			if _, seen := drift[k]; !seen {
				if _, seenRev := drift[rev]; !seenRev {
					lanes = append(lanes, k)
				}
			}
			drift[k] = drift[k].Add(qty)
			drift[rev] = drift[rev].Sub(qty)
			// FIFO matching: an A→B execution pairs with a later B→A one.
			if q := queues[rev]; len(q) > 0 {
				s.MatchedPairs++
				s.MatchedPairNet = s.MatchedPairNet.Add(q[0]).Add(e.PnLQuote)
				queues[rev] = q[1:]
				unmatched[rev]--
			} else {
				queues[k] = append(queues[k], e.PnLQuote)
				unmatched[k]++
			}
		}
	}
	rebalance := decimal.Zero
	for _, k := range lanes {
		d := drift[k]
		row := screener.DriftRow{Base: k.base, VenueA: k.venueA, VenueB: k.venueB, DriftBase: d, Unmatched: unmatched[k] + unmatched[laneKey{k.base, k.venueB, k.venueA}]}
		if mid, ok := x.midFor(k.venueA, k.base, quoteFor(execs, k.base)); ok {
			row.DriftNotional = d.Abs().Mul(mid)
			if q, ok := x.svc.Book.QuotesFor(k.base, quoteFor(execs, k.base))[k.venueA]; ok {
				age := now.Sub(q.At).Milliseconds()
				row.MarkAgeMs = &age
			}
			fA, _ := x.spotFee(k.venueA)
			fB, _ := x.spotFee(k.venueB)
			rebalance = rebalance.Add(row.DriftNotional.Mul(fA.Add(fB).Div(decTenK)))
		}
		s.InventoryDrift = append(s.InventoryDrift, row)
	}
	s.PnLAfterRebalance = s.NetPnLQuote.Sub(rebalance)
	s.Samples = samples
	if samples > 0 {
		hr := decimal.NewFromInt(wins).Div(decimal.NewFromInt(samples))
		s.HitRate = &hr
	}
	if lifetimeN > 0 {
		m := lifetimeSum.Div(decimal.NewFromInt(lifetimeN))
		s.MeanLifetimeS = &m
	}
	if len(slips) > 0 {
		sort.Slice(slips, func(i, j int) bool { return slips[i].LessThan(slips[j]) })
		sum := decimal.Zero
		for _, v := range slips {
			sum = sum.Add(v)
		}
		mean := sum.Div(decimal.NewFromInt(int64(len(slips))))
		idx := (len(slips)*95 + 99) / 100
		if idx > 0 {
			idx--
		}
		p95 := slips[idx]
		s.RealisedSlipMean, s.RealisedSlipP95 = &mean, &p95
	}
	return s, nil
}

// quoteFor finds the quote asset used with base in this rule's
// executions ("USDT" when none).
func quoteFor(execs []Execution, base string) string {
	for _, e := range execs {
		if e.Base == base {
			return e.Quote
		}
	}
	return "USDT"
}
