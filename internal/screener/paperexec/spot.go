package paperexec

import (
	"context"
	"time"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/exchange"
	"github.com/cploutarchou/arb-chain-bot/internal/screener"
	"github.com/cploutarchou/arb-chain-bot/internal/screener/alerts"
)

// executeSpot is strategy-models §2.6 steps 4–8 for one opened event.
// Requires x.mu.
func (x *Executor) executeSpot(ctx context.Context, s alerts.Signal, ev screener.Event, now time.Time) {
	r := s.Rule
	qa, qb := s.SpotA, s.SpotB
	poll := x.pollInterval()
	// §1.1 re-check at decision time (the signal was computed this poll,
	// but the gate is the executor's own responsibility).
	if !ageOK(now.Sub(qa.At), now.Sub(qb.At), poll) {
		x.skip(ctx, s, ev, now, SkipDataAge, errf("ages %dms/%dms vs poll %s", s.AgeAMs, s.AgeBMs, poll))
		return
	}
	// Task 1d: the SAME guard the spreads table and the evaluator apply,
	// re-run here at decision time against the current book (peers for
	// the median test) — the executor never acts on a lane whose two
	// prices cannot be one asset, nor on one with no size to fill.
	if g := screener.GuardLane(qa, qb, x.svc.Book.QuotesFor(s.Lane.Base, s.Lane.Quote), x.maxPlausibleSpreadBps()); g.SkipReason() != "" {
		x.skip(ctx, s, ev, now, g.SkipReason(), guardDetail(g))
		return
	}
	fA, okA := x.spotFee(s.Lane.VenueA)
	fB, okB := x.spotFee(s.Lane.VenueB)
	if !okA || !okB {
		x.skip(ctx, s, ev, now, SkipBalance, "venue disabled in settings")
		return
	}
	h := r.DepthHaircut()
	requested := truncStep(r.PaperSizeQuote.Div(qa.Ask), r.StepSize())
	fillable := truncStep(minDec(requested, minDec(h.Mul(qa.AskQty), h.Mul(qb.BidQty))), r.StepSize())
	if fillable.LessThan(requested) && !r.PartialAllowed() {
		x.skip(ctx, s, ev, now, SkipDepth, errf("requested %s fillable %s (h=%s)", requested, fillable, h))
		return
	}
	size := fillable
	if !size.IsPositive() {
		x.skip(ctx, s, ev, now, SkipDepth, "size truncates to zero")
		return
	}
	if minN := r.MinNotionalQuote(); minN.IsPositive() && size.Mul(qa.Ask).LessThan(minN) {
		x.skip(ctx, s, ev, now, SkipMinNotional, errf("notional %s < %s", size.Mul(qa.Ask), minN))
		return
	}
	// §2.4 (iii) drift cap, measured on executed inventory so far.
	drift, err := x.driftFor(ctx, r.ID, s.Lane)
	if err != nil {
		x.log.Error("paperexec: drift read failed", "error", err)
		return
	}
	if drift.Add(size).Mul(qa.Ask).GreaterThan(r.MaxDriftQuote()) {
		x.skip(ctx, s, ev, now, SkipDriftCap, errf("drift %s + %s %s at %s > cap %s", drift, size, s.Lane.Base, qa.Ask, r.MaxDriftQuote()))
		return
	}
	// Balances (§2.1): ≥ size_quote on A, ≥ size_base on B.
	wa, wb := x.wallet(s.Lane.VenueA), x.wallet(s.Lane.VenueB)
	quoteAsset, baseAsset := exchange.Asset(s.Lane.Quote), exchange.Asset(s.Lane.Base)
	availA, _ := wa.Balance(quoteAsset)
	availB, _ := wb.Balance(baseAsset)
	needQuote := size.Mul(qa.Ask).Mul(decOne.Add(x.tolBps.Div(decTenK))).Mul(decOne.Add(fA.Div(decTenK)))
	if availA.LessThan(needQuote) || availB.LessThan(size) {
		x.skip(ctx, s, ev, now, SkipBalance, errf("%s %s=%s need %s; %s %s=%s need %s",
			s.Lane.VenueA, quoteAsset, availA, needQuote, s.Lane.VenueB, baseAsset, availB, size))
		return
	}
	// §2.6 step 5: reserve with the event id as idempotency key.
	resA, err := wa.Reserve(ev.ID+":A", quoteAsset, needQuote, "screener:"+r.ID, nil)
	if err != nil {
		x.skip(ctx, s, ev, now, SkipBalance, err.Error())
		return
	}
	resB, err := wb.Reserve(ev.ID+":B", baseAsset, size, "screener:"+r.ID, nil)
	if err != nil {
		_ = wa.Release(resA.ID)
		x.skip(ctx, s, ev, now, SkipBalance, err.Error())
		return
	}

	slip := r.SlipBps()
	execID := "exe-" + x.idGen()
	legA := x.simulateLeg(ctx, execID, legReq{Leg: 1, Venue: s.Lane.VenueA, Market: "spot", Side: "BUY",
		Base: s.Lane.Base, Quote: s.Lane.Quote, Qty: size, Price: qa.Ask, FeeBps: fA,
		Reread: func() (decimal.Decimal, bool) {
			return x.sidePrice(s.Lane.VenueA, "spot", "BUY", s.Lane.Base, s.Lane.Quote)
		}}, slip)
	legB := x.simulateLeg(ctx, execID, legReq{Leg: 2, Venue: s.Lane.VenueB, Market: "spot", Side: "SELL",
		Base: s.Lane.Base, Quote: s.Lane.Quote, Qty: size, Price: qb.Bid, FeeBps: fB,
		Reread: func() (decimal.Decimal, bool) {
			return x.sidePrice(s.Lane.VenueB, "spot", "SELL", s.Lane.Base, s.Lane.Quote)
		}}, slip)

	// Settle wallets per leg outcome.
	if legA.Status == "FILLED" {
		cost := legA.FillPrice.Mul(size).Add(legA.FeeQuote)
		_ = wa.Settle(resA.ID, cost)
		_ = wa.Credit(baseAsset, size)
	} else {
		_ = wa.Release(resA.ID)
	}
	if legB.Status == "FILLED" {
		_ = wb.Settle(resB.ID, size)
		_ = wb.Credit(quoteAsset, legB.FillPrice.Mul(size).Sub(legB.FeeQuote))
	} else {
		_ = wb.Release(resB.ID)
	}
	x.persistBalance(ctx, s.Lane.VenueA, string(quoteAsset), now)
	x.persistBalance(ctx, s.Lane.VenueA, string(baseAsset), now)
	x.persistBalance(ctx, s.Lane.VenueB, string(quoteAsset), now)
	x.persistBalance(ctx, s.Lane.VenueB, string(baseAsset), now)

	fills := []Fill{legA, legB}
	fees := legA.FeeQuote.Add(legB.FeeQuote)
	exec := Execution{
		ID: execID, RuleID: r.ID, EventID: ev.ID, Strategy: screener.StrategyCrossVenueSpot,
		Base: s.Lane.Base, Quote: s.Lane.Quote, VenueA: s.Lane.VenueA, VenueB: s.Lane.VenueB,
		Fills: fills, SlipAllowBps: slip, At: now, Payload: map[string]any{},
	}
	pos := Position{
		ID: "pos-" + x.idGen(), RuleID: r.ID, EventID: ev.ID, Strategy: screener.StrategyCrossVenueSpot,
		Base: s.Lane.Base, Quote: s.Lane.Quote, VenueA: s.Lane.VenueA, VenueB: s.Lane.VenueB,
		Qty: size, OpenedAt: now, Status: StatusClosed,
	}
	closedAt := now
	pos.ClosedAt = &closedAt
	exec.PositionID = pos.ID

	switch {
	case legA.Status == "FILLED" && legB.Status == "FILLED":
		// §2.2 pnl_quote = size × [bid_B^fill (1 − f_B) − ask_A^fill (1 + f_A)]
		exec.Kind = KindSpot
		exec.FeesQuote = fees
		exec.PnLQuote = size.Mul(legB.FillPrice.Mul(decOne.Sub(fB.Div(decTenK))).Sub(legA.FillPrice.Mul(decOne.Add(fA.Div(decTenK)))))
		driftNotional := size.Mul(legA.FillPrice)
		rebalance := driftNotional.Mul(fA.Add(fB).Div(decTenK))
		exec.Payload["gross_bps"] = s.GrossBps.String()
		exec.Payload["net_bps"] = s.NetBps.String()
		exec.Payload["exec_bps"] = s.ExecBps.String()
		exec.Payload["drift_notional_quote"] = driftNotional.String()
		exec.Payload["rebalance_charge_quote"] = rebalance.String()
		exec.Payload["pnl_after_rebalance_quote"] = exec.PnLQuote.Sub(rebalance).String()
		exec.Payload["pnl_bps"] = exec.PnLQuote.Div(driftNotional).Mul(decTenK).String()
		pos.PnLQuote = exec.PnLQuote
	case legA.Status != "FILLED" && legB.Status != "FILLED":
		// Nothing deployed: REJECTED, not an execution.
		x.skip(ctx, s, ev, now, SkipRejected, legA.Reason+" | "+legB.Reason)
		return
	default:
		// §2.4 (ii): one leg REJECTED → the other is booked as a
		// one-legged execution with its mark-to-market at that venue's
		// own mid, counted under skipped.partial_leg.
		exec.Kind = KindPartialLeg
		exec.FeesQuote = fees
		var filled Fill
		if legA.Status == "FILLED" {
			filled = legA
		} else {
			filled = legB
		}
		mid, ok := x.midFor(filled.Venue, s.Lane.Base, s.Lane.Quote)
		if !ok {
			mid = filled.FillPrice
		}
		if filled.Side == "BUY" {
			exec.PnLQuote = size.Mul(mid.Sub(filled.FillPrice)).Sub(filled.FeeQuote)
		} else {
			exec.PnLQuote = size.Mul(filled.FillPrice.Sub(mid)).Sub(filled.FeeQuote)
		}
		exec.Payload["mark_mid"] = mid.String()
		pos.PnLQuote = exec.PnLQuote
		pos.Status = StatusSkipped
		pos.SkippedReason = SkipPartialLeg
	}
	pos.OpenPayload = toMap(exec)
	if err := x.ledger.InsertPosition(ctx, pos); err != nil {
		x.log.Error("paperexec: position insert failed", "error", err)
	}
	x.insertExecution(ctx, exec)
	x.queueSlip(execID, fills)
	if closer, ok := x.svc.Events.(screener.EventCloser); ok && x.svc.Events != nil {
		_ = closer.SetEventExecution(ctx, ev.ID, execID)
	}
	x.log.Info("paperexec: spot execution booked", "rule", r.ID, "kind", exec.Kind, "base", s.Lane.Base,
		"venue_a", s.Lane.VenueA, "venue_b", s.Lane.VenueB, "qty", size.String(), "pnl_quote", exec.PnLQuote.StringFixed(4))
}

// driftFor returns the signed base drift for a lane direction from the
// ledger (§2.3: Σ bought on A − Σ sold on B, matched by the reverse
// direction's executions).
func (x *Executor) driftFor(ctx context.Context, ruleID string, lane alerts.Lane) (decimal.Decimal, error) {
	execs, err := x.ledger.ListExecutions(ctx, ruleID, 0)
	if err != nil {
		return decimal.Decimal{}, err
	}
	drift := decimal.Zero
	for _, e := range execs {
		if e.Base != lane.Base || e.Quote != lane.Quote || e.Kind != KindSpot {
			continue
		}
		qty := decimal.Zero
		for _, f := range e.Fills {
			if f.Status == "FILLED" {
				qty = f.Qty
				break
			}
		}
		switch {
		case e.VenueA == lane.VenueA && e.VenueB == lane.VenueB:
			drift = drift.Add(qty)
		case e.VenueA == lane.VenueB && e.VenueB == lane.VenueA:
			drift = drift.Sub(qty)
		}
	}
	return drift, nil
}

func (x *Executor) midFor(v screener.Venue, base, quote string) (decimal.Decimal, bool) {
	q, ok := x.svc.Book.QuotesFor(base, quote)[v]
	if !ok || !q.Bid.IsPositive() || !q.Ask.IsPositive() {
		return decimal.Decimal{}, false
	}
	return q.Bid.Add(q.Ask).Div(decTwo), true
}

func guardDetail(g screener.LaneGuard) string {
	if g.Detail != "" {
		return g.Detail
	}
	return "top-of-book size not published by the venue's bulk ticker"
}

func ageOK(a, b, poll time.Duration) bool {
	if a < 0 || b < 0 || a > poll || b > poll {
		return false
	}
	diff := a - b
	if diff < 0 {
		diff = -diff
	}
	return diff <= poll/2
}

func minDec(a, b decimal.Decimal) decimal.Decimal {
	if b.LessThan(a) {
		return b
	}
	return a
}
