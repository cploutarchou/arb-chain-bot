package paperexec

import (
	"context"
	"time"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/exchange"
	"github.com/cploutarchou/arb-chain-bot/internal/reservation"
	"github.com/cploutarchou/arb-chain-bot/internal/screener"
	"github.com/cploutarchou/arb-chain-bot/internal/screener/alerts"
)

// perpOpen is the open payload of a carry / funding-harvest position
// (§3.6 step 6). Decimals travel as strings through JSONB.
type perpOpen struct {
	SpotOpen decimal.Decimal `json:"spot_open"`
	PerpOpen decimal.Decimal `json:"perp_open"`
	FeesOpen decimal.Decimal `json:"fees_open"`
	// SpotCostQuote is the quote that actually left the wallet on the
	// entry spot leg (audit T10): the full bought notional plus any
	// quote-side fee — a received-side entry fee was paid in base, so
	// the cost basis is the bare notional while the position holds
	// NetQty. The close measures its spot PnL against this figure.
	SpotCostQuote decimal.Decimal `json:"spot_cost_quote"`
	// PerpOpenFeeQuote is the perp leg's actually-charged open fee (the
	// close's PnL sums it with the close fee instead of approximating —
	// wallet conservation is exact against it).
	PerpOpenFeeQuote decimal.Decimal `json:"perp_open_fee_quote"`
	Collateral       decimal.Decimal `json:"collateral"`
	SpotFeeBps       decimal.Decimal `json:"spot_fee_bps"`
	PerpFeeBps       decimal.Decimal `json:"perp_fee_bps"`
	BasisEntryBps    decimal.Decimal `json:"basis_entry_bps"`
	PredictedBps     decimal.Decimal `json:"predicted_bps_at_open"`
	BreakevenN       int             `json:"breakeven_n,omitempty"`
	IntervalH        int             `json:"interval_h"`
	IntervalHOpen    int             `json:"interval_h_open"`
	NextFundingAt    time.Time       `json:"next_funding_at"`
	Settlements      int             `json:"settlements"`
	FundingMissed    int             `json:"funding_missed"`
	FundingRetry     int             `json:"funding_retry"`
	ExitCount        int             `json:"exit_count"`
	OpenExecID       string          `json:"open_execution_id"`
	// MMR as entered by the operator (§3.4); the liquidation estimate
	// P_liq = P_open × (1 + m)/(1 + MMR), m = 1, is display only.
	MMR       decimal.Decimal `json:"mmr"`
	LiqEst    decimal.Decimal `json:"liquidation_estimate"`
	CloseSlip decimal.Decimal `json:"-"`
}

func perpWallet(quote string) exchange.Asset { return exchange.Asset(quote + PerpWalletSuffix) }

// openPerp is §3.6 steps 4–6 (and §5.1 for funding harvest). Requires
// x.mu.
func (x *Executor) openPerp(ctx context.Context, s alerts.Signal, ev screener.Event, now time.Time) {
	r := s.Rule
	venue := s.Lane.VenueA
	q, p := s.SpotA, s.Perp
	poll := x.pollInterval()
	if !ageOK(now.Sub(q.At), now.Sub(p.At), poll) || !p.NextFundingAt.After(now) || now.Sub(p.At) > 2*poll {
		x.skip(ctx, s, ev, now, SkipDataAge, errf("spot %dms perp %dms poll %s", s.AgeAMs, s.AgeBMs, poll))
		return
	}
	if p.NextFundingAt.Sub(now) < 2*poll {
		x.skip(ctx, s, ev, now, SkipSettlementNear, errf("next settlement %s away", p.NextFundingAt.Sub(now)))
		return
	}
	// Task 1d: shared guard, re-run at decision time.
	if g := screener.GuardSpotPerp(q, p, x.maxPlausibleSpreadBps()); g.SkipReason() != "" {
		x.skip(ctx, s, ev, now, g.SkipReason(), guardDetail(g))
		return
	}
	mmr, ok := r.MMR()
	if !ok {
		x.skip(ctx, s, ev, now, SkipMMRUnknown, "rule params.mmr not set")
		return
	}
	open, err := x.ledger.ListPositions(ctx, r.ID, StatusOpen, 0)
	if err != nil {
		x.log.Error("paperexec: open positions read failed", "error", err)
		return
	}
	for _, o := range open {
		if o.Base == s.Lane.Base {
			x.skip(ctx, s, ev, now, SkipOpenPosition, "one open position per (rule, base)")
			return
		}
	}
	fSpot, okS := x.spotFee(venue)
	fPerp, okP := x.perpFee(venue)
	if !okS || !okP {
		x.skip(ctx, s, ev, now, SkipBalance, "venue or perps disabled in settings")
		return
	}
	h := r.DepthHaircut()
	requested := truncStep(r.PaperSizeQuote.Div(q.Ask), r.StepSize())
	// X8: the §1.3 haircut applies to BOTH legs — the spot ask the lane
	// buys and the perp bid it shorts (a zero perp size means the venue
	// published none; the guard has already refused such lanes, this is
	// the second line of defense).
	fillable := truncStep(minDec(minDec(requested, h.Mul(q.AskQty)), h.Mul(p.BidQty)), r.StepSize())
	if fillable.LessThan(requested) && !r.PartialAllowed() {
		x.skip(ctx, s, ev, now, SkipDepth, errf("requested %s fillable %s (h=%s)", requested, fillable, h))
		return
	}
	qty := fillable
	if !qty.IsPositive() {
		x.skip(ctx, s, ev, now, SkipDepth, "qty truncates to zero")
		return
	}
	// The exit compares perp ask against spot bid; if that is already at
	// or below close_bps the position would close on the next poll with
	// nothing collected (T-097). The signal checked this a tick ago —
	// re-check against the book we are about to open on.
	// Carry only: funding harvest (§5) never uses the converged exit, so
	// a negative basis there is the premise, not a defect.
	if s.Strategy != screener.StrategyFundingHarvest && q.Bid.IsPositive() {
		basisExit := p.Ask.Sub(q.Bid).Div(q.Bid).Mul(decTenK)
		if basisExit.LessThanOrEqual(r.CloseBps()) {
			x.skip(ctx, s, ev, now, SkipClosesImmediately,
				errf("exit-side basis %s bps <= close_bps %s", basisExit.StringFixed(2), r.CloseBps()))
			return
		}
	}
	w := x.wallet(venue)
	quoteAsset, baseAsset, perpAsset := exchange.Asset(s.Lane.Quote), exchange.Asset(s.Lane.Base), perpWallet(s.Lane.Quote)
	needSpot := qty.Mul(q.Ask).Mul(decOne.Add(x.tolBps.Div(decTenK))).Mul(decOne.Add(fSpot.Div(decTenK)))
	// §1.6: collateral posted = full notional (1×) plus the open fee.
	needPerp := qty.Mul(p.Bid).Mul(decOne.Add(fPerp.Div(decTenK)))
	availS, _ := w.Balance(quoteAsset)
	availP, _ := w.Balance(perpAsset)
	if availS.LessThan(needSpot) || availP.LessThan(needPerp) {
		x.skip(ctx, s, ev, now, SkipBalance, errf("%s=%s need %s; %s=%s need %s", quoteAsset, availS, needSpot, perpAsset, availP, needPerp))
		return
	}
	resS, err := w.Reserve(ev.ID+":spot", quoteAsset, needSpot, "screener:"+r.ID, nil)
	if err != nil {
		x.skip(ctx, s, ev, now, SkipBalance, err.Error())
		return
	}
	resP, err := w.Reserve(ev.ID+":perp", perpAsset, needPerp, "screener:"+r.ID, nil)
	if err != nil {
		_ = w.Release(resS.ID)
		x.skip(ctx, s, ev, now, SkipBalance, err.Error())
		return
	}
	slip := r.SlipBps()
	execID := "exe-" + x.idGen()
	spotConv := screener.VenueFeeConvention(venue)
	spotLeg := x.simulateLeg(ctx, execID, legReq{Leg: 1, Venue: venue, Market: "spot", Side: "BUY",
		Base: s.Lane.Base, Quote: s.Lane.Quote, Qty: qty, Price: q.Ask, FeeBps: fSpot,
		Convention: spotConv,
		Reread:     func() (decimal.Decimal, bool) { return x.sidePrice(venue, "spot", "BUY", s.Lane.Base, s.Lane.Quote) }}, slip)
	if spotLeg.Status != "FILLED" {
		_ = w.Release(resS.ID)
		_ = w.Release(resP.ID)
		x.skip(ctx, s, ev, now, SkipRejected, "spot leg: "+spotLeg.Reason)
		return
	}
	_ = w.Settle(resS.ID, spotLeg.FillPrice.Mul(qty).Add(spotLeg.FeeQuote))
	_ = w.Credit(baseAsset, spotLeg.NetQty)

	// The perp leg is quote-side BY CONSTRUCTION (audit T10): linear
	// USDT-margined perpetuals settle taker fees in the margin asset
	// regardless of the venue's spot convention. It is sized to the
	// POST-FEE spot holding — a received-side entry fee leaves less
	// base than was bought, and a hedged carry hedges what it holds,
	// so funding, collateral and the close all run on one size.
	perpLeg := x.simulateLeg(ctx, execID, legReq{Leg: 2, Venue: venue, Market: "perp", Side: "SELL",
		Base: s.Lane.Base, Quote: s.Lane.Quote, Qty: spotLeg.NetQty, Price: p.Bid, FeeBps: fPerp,
		Convention: exchange.FeeInQuote,
		Reread:     func() (decimal.Decimal, bool) { return x.sidePrice(venue, "perp", "SELL", s.Lane.Base, s.Lane.Quote) }}, slip)
	if perpLeg.Status != "FILLED" {
		// §3.6 step 5: unwind the first leg immediately; the cost is
		// booked under skipped.UNWIND.
		_ = w.Release(resP.ID)
		unwind := x.simulateLeg(ctx, execID+":unwind", legReq{Leg: 3, Venue: venue, Market: "spot", Side: "SELL",
			Base: s.Lane.Base, Quote: s.Lane.Quote, Qty: spotLeg.NetQty, Price: q.Bid, FeeBps: fSpot,
			Convention: spotConv,
			Reread:     func() (decimal.Decimal, bool) { return x.sidePrice(venue, "spot", "SELL", s.Lane.Base, s.Lane.Quote) }}, slip)
		pos := Position{ID: "pos-" + x.idGen(), RuleID: r.ID, EventID: ev.ID, Strategy: s.Strategy,
			Base: s.Lane.Base, Quote: s.Lane.Quote, VenueA: venue, VenueB: venue, Qty: spotLeg.NetQty, OpenedAt: now,
			Status: StatusSkipped, SkippedReason: SkipUnwind}
		t := now
		pos.ClosedAt = &t
		exec := Execution{ID: execID, PositionID: pos.ID, RuleID: r.ID, EventID: ev.ID, Strategy: s.Strategy, Kind: KindUnwind,
			Base: s.Lane.Base, Quote: s.Lane.Quote, VenueA: venue, VenueB: venue, Fills: []Fill{spotLeg, perpLeg, unwind},
			SlipAllowBps: slip, At: now, Payload: map[string]any{"reason": "perp leg rejected: " + perpLeg.Reason}}
		if unwind.Status == "FILLED" {
			res, _ := w.Reserve(ev.ID+":unwind", baseAsset, spotLeg.NetQty, "screener:"+r.ID, nil)
			_ = w.Settle(res.ID, unwind.NetQty)
			_ = w.Credit(quoteAsset, unwind.FillPrice.Mul(unwind.NetQty).Sub(unwind.FeeQuote))
			exec.FeesQuote = spotLeg.FeeQuote.Add(spotLeg.FeeQuoteEquiv).Add(unwind.FeeQuote).Add(unwind.FeeQuoteEquiv)
			exec.PnLQuote = unwind.FillPrice.Mul(unwind.NetQty).Sub(unwind.FeeQuote).
				Sub(spotLeg.FillPrice.Mul(qty).Add(spotLeg.FeeQuote))
		} else {
			// Unwind itself rejected: the base stays in the wallet,
			// marked at the venue mid as open exposure.
			mid, ok := x.midFor(venue, s.Lane.Base, s.Lane.Quote)
			if !ok {
				mid = spotLeg.FillPrice
			}
			exec.FeesQuote = spotLeg.FeeQuote.Add(spotLeg.FeeQuoteEquiv)
			exec.PnLQuote = spotLeg.NetQty.Mul(mid.Sub(spotLeg.FillPrice)).Sub(spotLeg.FeeQuote)
			exec.Payload["unwind_rejected"] = unwind.Reason
			exec.Payload["mark_mid"] = mid.String()
		}
		pos.PnLQuote = exec.PnLQuote
		pos.OpenPayload = toMap(exec)
		x.countOutcome(pos)
		x.invalidateView()
		if err := x.ledger.InsertPosition(ctx, pos); err != nil {
			x.log.Error("paperexec: unwind position insert failed", "error", err)
		}
		x.insertExecution(ctx, exec)
		x.persistBalance(ctx, venue, string(quoteAsset), now)
		x.persistBalance(ctx, venue, string(baseAsset), now)
		x.persistBalance(ctx, venue, string(perpAsset), now)
		x.log.Info("paperexec: unwind booked", "rule", r.ID, "base", s.Lane.Base, "cost_quote", exec.PnLQuote.StringFixed(4))
		return
	}
	collateral := qty.Mul(perpLeg.FillPrice)
	_ = w.Settle(resP.ID, collateral.Add(perpLeg.FeeQuote))
	x.persistBalance(ctx, venue, string(quoteAsset), now)
	x.persistBalance(ctx, venue, string(baseAsset), now)
	x.persistBalance(ctx, venue, string(perpAsset), now)

	intervalH := p.IntervalH
	if intervalH <= 0 {
		intervalH = 8
	}
	po := perpOpen{
		SpotOpen: spotLeg.FillPrice, PerpOpen: perpLeg.FillPrice,
		FeesOpen:      spotLeg.FeeQuote.Add(spotLeg.FeeQuoteEquiv).Add(perpLeg.FeeQuote).Add(perpLeg.FeeQuoteEquiv),
		SpotCostQuote: spotLeg.FillPrice.Mul(qty).Add(spotLeg.FeeQuote), PerpOpenFeeQuote: perpLeg.FeeQuote, Collateral: collateral,
		SpotFeeBps: fSpot, PerpFeeBps: fPerp,
		BasisEntryBps: s.BasisEntryBps, PredictedBps: s.PredictedBps, BreakevenN: s.BreakevenN,
		IntervalH: intervalH, IntervalHOpen: intervalH, NextFundingAt: p.NextFundingAt,
		OpenExecID: execID, MMR: mmr,
		LiqEst: perpLeg.FillPrice.Mul(decTwo).Div(decOne.Add(mmr)),
	}
	pos := Position{ID: "pos-" + x.idGen(), RuleID: r.ID, EventID: ev.ID, Strategy: s.Strategy,
		Base: s.Lane.Base, Quote: s.Lane.Quote, VenueA: venue, VenueB: venue, Qty: spotLeg.NetQty, OpenedAt: now,
		Status: StatusOpen, OpenPayload: toMap(po)}
	exec := Execution{ID: execID, PositionID: pos.ID, RuleID: r.ID, EventID: ev.ID, Strategy: s.Strategy, Kind: KindOpen,
		Base: s.Lane.Base, Quote: s.Lane.Quote, VenueA: venue, VenueB: venue, Fills: []Fill{spotLeg, perpLeg},
		FeesQuote: po.FeesOpen, SlipAllowBps: slip, At: now,
		Payload: map[string]any{"basis_entry_bps": s.BasisEntryBps.String(), "edge_bps": s.EdgeBps.String(),
			"predicted_bps": s.PredictedBps.String(), "collateral": collateral.String()}}
	x.countOutcome(pos)
	x.invalidateView()
	if err := x.ledger.InsertPosition(ctx, pos); err != nil {
		x.log.Error("paperexec: position insert failed", "error", err)
	}
	x.insertExecution(ctx, exec)
	x.queueSlip(execID, exec.Fills)
	if closer, ok := x.svc.Events.(screener.EventCloser); ok && x.svc.Events != nil {
		_ = closer.SetEventExecution(ctx, ev.ID, execID)
	}
	x.log.Info("paperexec: perp position opened", "rule", r.ID, "strategy", s.Strategy, "base", s.Lane.Base,
		"venue", venue, "qty", qty.String(), "spot_open", spotLeg.FillPrice.String(), "perp_open", perpLeg.FillPrice.String())
}

// managePositions is §3.6 step 2 for every OPEN position. Requires x.mu.
func (x *Executor) managePositions(ctx context.Context, now time.Time) {
	open, err := x.ledger.ListPositions(ctx, "", StatusOpen, 0)
	if err != nil {
		x.log.Error("paperexec: open positions read failed", "error", err)
		return
	}
	if len(open) == 0 {
		return
	}
	rules := map[string]screener.Rule{}
	if x.svc.Rules != nil {
		if list, err := x.svc.Rules.ListRules(ctx); err == nil {
			for _, r := range list {
				rules[r.ID] = r
			}
		}
	}
	for _, pos := range open {
		r, ok := rules[pos.RuleID]
		if !ok {
			// Rule deleted: keep accruing funding, exit on max hold only.
			r = screener.Rule{ID: pos.RuleID, Kind: screener.RuleKindCarry}
		}
		x.managePosition(ctx, r, pos, now)
	}
}

func (x *Executor) managePosition(ctx context.Context, r screener.Rule, pos Position, now time.Time) {
	var po perpOpen
	if err := fromMap(pos.OpenPayload, &po); err != nil {
		x.log.Error("paperexec: position payload unreadable", "id", pos.ID, "error", err)
		return
	}
	venue := pos.VenueA
	// The position's quote is the contract's margin asset: a USDT
	// position is marked and closed against the USDT contract even when
	// the venue also lists a USDC one on the same base.
	p, okP := x.svc.Book.PerpFor(venue, pos.Base, pos.Quote)
	q, okQ := x.svc.Book.QuotesFor(pos.Base, pos.Quote)[venue]
	changed := false

	// Funding accrual (§1.4): settled rate from funding history after
	// T_i; retry ≤ 3 polls; mark = the perp mark seen at booking time
	// (the closest observation to T_i the screener has).
	for !po.NextFundingAt.IsZero() && !now.Before(po.NextFundingAt) {
		T := po.NextFundingAt
		rate, found := x.settledRate(ctx, venue, pos.Base, T)
		if !found {
			po.FundingRetry++
			changed = true
			if po.FundingRetry <= 3 {
				break // try again next poll
			}
			po.FundingMissed++
			x.log.Warn("paperexec: settled funding rate missing after 3 polls", "position", pos.ID, "settlement", T)
		} else {
			mark := po.PerpOpen
			if okP && p.Mark.IsPositive() {
				mark = p.Mark
			}
			// short receives funding when F > 0 (pays when F < 0)
			amount := pos.Qty.Mul(mark).Mul(rate)
			pos.FundingQuote = pos.FundingQuote.Add(amount)
			x.insertExecution(ctx, Execution{ID: "exe-" + x.idGen(), PositionID: pos.ID, RuleID: pos.RuleID, EventID: pos.EventID,
				Strategy: pos.Strategy, Kind: KindFunding, Base: pos.Base, Quote: pos.Quote, VenueA: venue, VenueB: venue,
				Fills: []Fill{}, PnLQuote: amount, At: now,
				Payload: map[string]any{"settled_at": T, "rate": rate.String(), "mark": mark.String(), "amount_quote": amount.String()}})
			po.Settlements++
			po.FundingRetry = 0
			// Exit counters evaluated at each settlement.
			predictedBps := decimal.Zero
			if okP {
				pr := p.PredictedFundingRate
				if pr.IsZero() {
					pr = p.FundingRate
				}
				predictedBps = pr.Mul(decTenK)
			}
			var exitThresholdBps decimal.Decimal
			if pos.Strategy == screener.StrategyFundingHarvest {
				exitThresholdBps = r.ExitFundingBps()
			} else {
				exitThresholdBps = r.ExitFunding().Mul(decTenK)
			}
			if !predictedBps.GreaterThan(exitThresholdBps) {
				po.ExitCount++
			} else {
				po.ExitCount = 0
			}
		}
		h := po.IntervalH
		if okP && p.IntervalH > 0 {
			h = p.IntervalH
			po.IntervalH = h
		}
		po.NextFundingAt = T.Add(time.Duration(h) * time.Hour)
		changed = true
	}
	if okP && p.IntervalH > 0 && p.IntervalH != po.IntervalH {
		po.IntervalH = p.IntervalH
		changed = true
	}
	if changed {
		pos.OpenPayload = toMap(po)
		if err := x.ledger.UpdatePosition(ctx, pos); err != nil {
			x.log.Error("paperexec: position update failed", "id", pos.ID, "error", err)
		}
	}

	// §3.4: a stale leg pauses exits.
	if !okP || !okQ || !ageOK(now.Sub(q.At), now.Sub(p.At), x.pollInterval()) {
		return
	}
	reason := x.exitReason(r, pos, po, p, q, now)
	if reason == "" {
		return
	}
	x.closePerp(ctx, r, pos, po, p, q, now, reason)
}

// exitReason evaluates §3.2 exits and §3.4 stops (and §5.2 for harvest).
func (x *Executor) exitReason(r screener.Rule, pos Position, po perpOpen, p screener.Perp, q screener.Quote, now time.Time) string {
	notional := pos.Qty.Mul(po.PerpOpen)
	basisNow := p.Ask.Sub(q.Bid).Div(q.Bid).Mul(decTenK)
	held := now.Sub(pos.OpenedAt)
	// Stops first (§3.4).
	if po.PerpOpen.IsPositive() && p.Mark.Sub(po.PerpOpen).Div(po.PerpOpen).GreaterThanOrEqual(r.MarginStopFrac()) {
		return "margin_stop"
	}
	if basisNow.Sub(po.BasisEntryBps).GreaterThanOrEqual(r.BasisStopBps(pos.Base)) {
		return "basis_stop"
	}
	// §3.4: funding has eaten the basis the position captured. Only a
	// POSITIVE entry basis was captured — with a negative one the naive
	// form degenerates to "0 < a positive number" and fires on the first
	// poll, which closed 97 of 97 positions on 2026-08-28 (T-097).
	captured := decimal.Max(po.BasisEntryBps, decimal.Zero)
	if pos.FundingQuote.IsNegative() && pos.FundingQuote.LessThan(captured.Div(decTenK).Mul(notional).Neg()) {
		return "funding_reversal"
	}
	if held >= time.Duration(r.MaxHoldH())*time.Hour {
		return "max_hold"
	}
	switch pos.Strategy {
	case screener.StrategyFundingHarvest:
		if po.ExitCount >= 2 {
			return "funding_exit"
		}
		if po.IntervalHOpen > 1 && po.IntervalH == 1 {
			return "interval_switch"
		}
		if po.BreakevenN > 0 && po.Settlements >= po.BreakevenN {
			basisChange := po.BasisEntryBps.Sub(basisNow).Div(decTenK).Mul(notional)
			if !pos.FundingQuote.Add(basisChange).IsPositive() {
				return "post_breakeven_nonpositive"
			}
		}
	default:
		// A carry's thesis is funding, so a "converged" close before the
		// first settlement collects nothing and simply realises the
		// round-trip spread plus four taker fees — the shape that lost
		// 412 of 412 closes on 2026-08-27 (T-097). The stops above are
		// deliberately checked first and stay immediate: a position that
		// is genuinely going wrong still exits at once.
		// T-100: `>= 1` was nowhere near enough. Measured on the guarded
		// build, 13 of 13 carry closes were `converged` with exactly one
		// settlement and every one was negative: fees_open ~15 bps means
		// a ~30 bps round trip, while one settlement paid ~0.5 bps —
		// about a sixtieth of what the position owed.
		//
		// The number the entry gate already computed is the right one.
		// carryActive admits a position only when BreakevenN settlements
		// (the settlements needed to pay the round trip at the
		// conservative rate) fit inside max_hold_h. The converged exit
		// now waits for exactly that many, so the take-profit cannot
		// fire before the thesis it was admitted on has been tested.
		//
		// max(BreakevenN, 1) covers positions opened before the entry
		// gate guaranteed BreakevenN >= 1: they keep the old behaviour
		// rather than becoming unclosable by this branch. Every stop
		// above — margin, basis blow-out, funding reversal, max hold —
		// is checked first and stays immediate, so a position going
		// wrong still exits at once; only the take-profit waits.
		needed := po.BreakevenN
		if needed < 1 {
			needed = 1
		}
		if basisNow.LessThanOrEqual(r.CloseBps()) && po.Settlements >= needed {
			return "converged"
		}
		if po.ExitCount >= r.ExitK() {
			return "funding_exit"
		}
	}
	return ""
}

// closePerp simulates both close legs and books §3.2's pnl.
func (x *Executor) closePerp(ctx context.Context, r screener.Rule, pos Position, po perpOpen, p screener.Perp, q screener.Quote, now time.Time, reason string) {
	venue := pos.VenueA
	slip := r.SlipBps()
	execID := "exe-" + x.idGen()
	spotConv := screener.VenueFeeConvention(venue)
	spotLeg := x.simulateLeg(ctx, execID, legReq{Leg: 1, Venue: venue, Market: "spot", Side: "SELL",
		Base: pos.Base, Quote: pos.Quote, Qty: pos.Qty, Price: q.Bid, FeeBps: po.SpotFeeBps,
		Convention: spotConv,
		Reread:     func() (decimal.Decimal, bool) { return x.sidePrice(venue, "spot", "SELL", pos.Base, pos.Quote) }}, slip)
	perpLeg := x.simulateLeg(ctx, execID, legReq{Leg: 2, Venue: venue, Market: "perp", Side: "BUY",
		Base: pos.Base, Quote: pos.Quote, Qty: pos.Qty, Price: p.Ask, FeeBps: po.PerpFeeBps,
		Convention: exchange.FeeInQuote, // margin-asset settlement by construction
		Reread:     func() (decimal.Decimal, bool) { return x.sidePrice(venue, "perp", "BUY", pos.Base, pos.Quote) }}, slip)
	if spotLeg.Status != "FILLED" || perpLeg.Status != "FILLED" {
		// Close legs are retried next poll (a hedged position stays
		// hedged); nothing is booked.
		x.log.Warn("paperexec: close leg rejected, retrying next poll", "position", pos.ID, "spot", spotLeg.Reason, "perp", perpLeg.Reason)
		return
	}
	// Spot leg per placement (audit T10): the close sells NetQty (a
	// spent-side fee means only that much is sellable) and its fee
	// comes off the side the venue takes it, measured against the quote
	// that actually left the wallet at entry (full notional + entry
	// quote fees — a received-side entry fee was paid in base).
	spotPnL := spotLeg.FillPrice.Mul(spotLeg.NetQty).Sub(spotLeg.FeeQuote).
		Sub(po.SpotCostQuote)
	perpPrice := pos.Qty.Mul(po.PerpOpen.Sub(perpLeg.FillPrice))
	// Actual charged fees, not the (open+close) average approximation:
	// the wallet debited exactly these, so conservation is exact.
	perpFees := po.PerpOpenFeeQuote.Add(perpLeg.FeeQuote)
	perpPnL := perpPrice.Sub(perpFees)
	total := spotPnL.Add(perpPnL).Add(pos.FundingQuote)

	w := x.wallet(venue)
	quoteAsset, baseAsset, perpAsset := exchange.Asset(pos.Quote), exchange.Asset(pos.Base), perpWallet(pos.Quote)
	if res, err := w.Reserve(execID+":base", baseAsset, pos.Qty, "screener:"+pos.RuleID, nil); err == nil {
		_ = w.Settle(res.ID, spotLeg.NetQty)
	} else {
		x.log.Warn("paperexec: base wallet short at close", "position", pos.ID, "error", err)
		x.debit(w, baseAsset, pos.Qty)
	}
	_ = w.Credit(quoteAsset, spotLeg.FillPrice.Mul(spotLeg.NetQty).Sub(spotLeg.FeeQuote))
	// Futures wallet: collateral back, plus the perp leg's price pnl net
	// of the close fee (the open fee left the wallet at open), plus
	// settled funding.
	perpReturn := po.Collateral.Add(perpPrice).Sub(perpLeg.FeeQuote).Add(pos.FundingQuote)
	if perpReturn.IsNegative() {
		x.log.Warn("paperexec: perp wallet return negative (stop should have fired earlier)", "position", pos.ID, "return", perpReturn.String())
		perpReturn = decimal.Zero
	}
	_ = w.Credit(perpAsset, perpReturn)
	x.persistBalance(ctx, venue, string(quoteAsset), now)
	x.persistBalance(ctx, venue, string(baseAsset), now)
	x.persistBalance(ctx, venue, string(perpAsset), now)

	closedAt := now
	pos.ClosedAt = &closedAt
	pos.Status = StatusClosed
	pos.PnLQuote = total
	if err := x.ledger.UpdatePosition(ctx, pos); err != nil {
		x.log.Error("paperexec: position close update failed", "id", pos.ID, "error", err)
	}
	exec := Execution{ID: execID, PositionID: pos.ID, RuleID: pos.RuleID, EventID: pos.EventID, Strategy: pos.Strategy, Kind: KindClose,
		Base: pos.Base, Quote: pos.Quote, VenueA: venue, VenueB: venue, Fills: []Fill{spotLeg, perpLeg},
		FeesQuote: spotLeg.FeeQuote.Add(spotLeg.FeeQuoteEquiv).Add(perpLeg.FeeQuote).Add(perpLeg.FeeQuoteEquiv), SlipAllowBps: slip, PnLQuote: total, At: now,
		Payload: map[string]any{
			"reason": reason, "spot_leg_pnl": spotPnL.String(), "perp_leg_pnl": perpPnL.String(),
			"perp_price_pnl": perpPrice.String(), "perp_fees": perpFees.String(),
			"funding_quote": pos.FundingQuote.String(), "settlements": po.Settlements, "funding_missed": po.FundingMissed,
			"held_s":          int64(now.Sub(pos.OpenedAt) / time.Second),
			"basis_close_bps": p.Ask.Sub(q.Bid).Div(q.Bid).Mul(decTenK).String(),
		}}
	x.insertExecution(ctx, exec)
	x.queueSlip(execID, exec.Fills)
	x.log.Info("paperexec: perp position closed", "rule", pos.RuleID, "position", pos.ID, "reason", reason,
		"pnl_quote", total.StringFixed(4), "funding_quote", pos.FundingQuote.StringFixed(4))
}

// debit lowers a wallet balance when no reservation could be made
// (base wallet drained by another rule); done via a reserve of what is
// available so the reservation ledger's invariants hold.
func (x *Executor) debit(w *reservation.Manager, asset exchange.Asset, amount decimal.Decimal) {
	avail, _ := w.Balance(asset)
	if !avail.IsPositive() {
		return
	}
	if amount.GreaterThan(avail) {
		amount = avail
	}
	if res, err := w.Reserve("debit-"+x.idGen(), asset, amount, "screener", nil); err == nil {
		_ = w.Settle(res.ID, amount)
	}
}

// settledRate finds the settled funding rate at T (± 1 minute) in the
// funding history store.
func (x *Executor) settledRate(ctx context.Context, venue screener.Venue, base string, T time.Time) (decimal.Decimal, bool) {
	if x.svc.Funding == nil {
		return decimal.Decimal{}, false
	}
	series, err := x.svc.Funding.ListFunding(ctx, base, []screener.Venue{venue}, T.Add(-time.Minute))
	if err != nil {
		return decimal.Decimal{}, false
	}
	for _, s := range series {
		if s.Venue != venue || s.Base != base {
			continue
		}
		for _, pt := range s.Points {
			d := pt.At.Sub(T)
			if d < 0 {
				d = -d
			}
			if d <= time.Minute {
				v, err := decimal.NewFromString(pt.Rate)
				if err == nil {
					return v, true
				}
			}
		}
	}
	return decimal.Decimal{}, false
}
