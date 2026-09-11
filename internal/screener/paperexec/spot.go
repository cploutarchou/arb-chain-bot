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
		Convention: screener.VenueFeeConvention(s.Lane.VenueA),
		Reread: func() (decimal.Decimal, bool) {
			return x.sidePrice(s.Lane.VenueA, "spot", "BUY", s.Lane.Base, s.Lane.Quote)
		}}, slip)
	// The second leg sells what actually survived the first leg's fee
	// (audit T10): a received-side buy fee means less base arrives on
	// venue B than was bought on venue A.
	sellQty := size
	if legA.Status == "FILLED" {
		sellQty = legA.NetQty
	}
	legB := x.simulateLeg(ctx, execID, legReq{Leg: 2, Venue: s.Lane.VenueB, Market: "spot", Side: "SELL",
		Base: s.Lane.Base, Quote: s.Lane.Quote, Qty: sellQty, Price: qb.Bid, FeeBps: fB,
		Convention: screener.VenueFeeConvention(s.Lane.VenueB),
		Reread: func() (decimal.Decimal, bool) {
			return x.sidePrice(s.Lane.VenueB, "spot", "SELL", s.Lane.Base, s.Lane.Quote)
		}}, slip)

	// Settle wallets per leg outcome. A sell that only walked NetQty of
	// the reserved base (SPENT-side fee) returns the difference.
	if legA.Status == "FILLED" {
		cost := legA.FillPrice.Mul(size).Add(legA.FeeQuote)
		_ = wa.Settle(resA.ID, cost)
		_ = wa.Credit(baseAsset, legA.NetQty)
	} else {
		_ = wa.Release(resA.ID)
	}
	if legB.Status == "FILLED" {
		_ = wb.Settle(resB.ID, legB.NetQty)
		_ = wb.Credit(quoteAsset, legB.FillPrice.Mul(legB.NetQty).Sub(legB.FeeQuote))
	} else {
		_ = wb.Release(resB.ID)
	}
	x.persistBalance(ctx, s.Lane.VenueA, string(quoteAsset), now)
	x.persistBalance(ctx, s.Lane.VenueA, string(baseAsset), now)
	x.persistBalance(ctx, s.Lane.VenueB, string(quoteAsset), now)
	x.persistBalance(ctx, s.Lane.VenueB, string(baseAsset), now)

	fills := []Fill{legA, legB}
	// The ledger bills in quote: quote-side fees plus the fill-price
	// value of base-side fees (the per-asset truth rides the fills).
	fees := legA.FeeQuote.Add(legA.FeeQuoteEquiv).Add(legB.FeeQuote).Add(legB.FeeQuoteEquiv)
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
		// §2.2, per-venue fee placement (audit T10): what the second leg
		// realises over what the first leg cost, each leg's fee charged
		// on the side its venue takes it —
		//   pnl = bid_B^fill × netQty_B − feeQuote_B
		//       − (ask_A^fill × size + feeQuote_A)
		// with base-side fees already inside netQty_B (a received-side
		// buy fee shrinks what arrives on B; a spent-side sell fee
		// shrinks what B can sell).
		exec.Kind = KindSpot
		exec.FeesQuote = fees
		exec.PnLQuote = legB.FillPrice.Mul(legB.NetQty).Sub(legB.FeeQuote).
			Sub(legA.FillPrice.Mul(size).Add(legA.FeeQuote))
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
			exec.PnLQuote = filled.NetQty.Mul(mid.Sub(filled.FillPrice)).Sub(filled.FeeQuote)
		} else {
			exec.PnLQuote = filled.NetQty.Mul(filled.FillPrice.Sub(mid)).Sub(filled.FeeQuote)
		}
		exec.Payload["mark_mid"] = mid.String()
		pos.PnLQuote = exec.PnLQuote
		pos.Status = StatusSkipped
		pos.SkippedReason = SkipPartialLeg
	}
	pos.OpenPayload = toMap(exec)
	x.countOutcome(pos)
	x.invalidateView()
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

// driftKey canonicalises a lane into the incremental drift map's key
// (audit X9): the venue pair is sorted, so a lane and its reverse share
// one key and the SIGN expresses direction (positive when the lane runs
// in the key's canonical order).
func driftKey(ruleID, base, quote string, a, b screener.Venue) (string, screener.Venue) {
	if string(b) < string(a) {
		a, b = b, a
	}
	return ruleID + "|" + base + "|" + quote + "|" + string(a) + ">" + string(b), a
}

// execDrift is one spot execution's delta against the canonical
// direction: the first FILLED fill's quantity, + when the execution ran
// in canonical order, − when reversed, 0 when nothing filled.
func execDrift(e Execution, canonicalFirst screener.Venue) decimal.Decimal {
	qty := decimal.Zero
	for _, f := range e.Fills {
		if f.Status == "FILLED" {
			qty = f.Qty
			break
		}
	}
	if qty.IsZero() {
		return decimal.Zero
	}
	if e.VenueA != canonicalFirst {
		return qty.Neg()
	}
	return qty
}

// driftFor returns the signed base drift for a lane direction (§2.3:
// Σ bought on A − Σ sold on B, matched by the reverse direction's
// executions). Incremental (audit X9): the FIRST call for a rule seeds
// every lane of that rule from one ledger scan; subsequent calls and
// every booked execution read/write the in-memory map, so the hot path
// stops re-reading the rule's whole execution list (up to 20 000 JSONB
// rows) per opened event. Requires x.mu.
func (x *Executor) driftFor(ctx context.Context, ruleID string, lane alerts.Lane) (decimal.Decimal, error) {
	if !x.driftSeeded[ruleID] {
		execs, err := x.ledger.ListExecutions(ctx, ruleID, 0)
		if err != nil {
			return decimal.Decimal{}, err
		}
		for _, e := range execs {
			if e.RuleID != ruleID || e.Kind != KindSpot {
				continue
			}
			k, first := driftKey(e.RuleID, e.Base, e.Quote, e.VenueA, e.VenueB)
			x.drift[k] = x.drift[k].Add(execDrift(e, first))
		}
		x.driftSeeded[ruleID] = true
	}
	k, first := driftKey(ruleID, lane.Base, lane.Quote, lane.VenueA, lane.VenueB)
	d := x.drift[k]
	if lane.VenueA != first {
		return d.Neg(), nil
	}
	return d, nil
}

// noteDrift applies one booked execution's signed drift delta to the
// incremental map. Requires x.mu.
func (x *Executor) noteDrift(e Execution) {
	if e.Kind != KindSpot {
		return
	}
	k, first := driftKey(e.RuleID, e.Base, e.Quote, e.VenueA, e.VenueB)
	x.drift[k] = x.drift[k].Add(execDrift(e, first))
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
