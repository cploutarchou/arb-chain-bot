package paperexec

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/screener"
	"github.com/cploutarchou/arb-chain-bot/internal/screener/alerts"
)

func skippedReason(t *testing.T, h *harness, ruleID string) string {
	t.Helper()
	ps := h.positions(ruleID)
	if len(ps) != 1 {
		t.Fatalf("positions = %d, want 1: %+v", len(ps), ps)
	}
	if ps[0].Status != StatusSkipped {
		t.Fatalf("status = %s, want SKIPPED (%+v)", ps[0].Status, ps[0])
	}
	return ps[0].SkippedReason
}

func TestSkipDataAge(t *testing.T) {
	h := newHarness(t, nil)
	h.balance(screener.VenueBinance, "USDT", "100000")
	h.balance(screener.VenueOKX, "BTC", "1")
	r := h.rule(spreadRule("5000"))
	setSpot(h.svc.Book, screener.VenueBinance, "49999", "1", "50000", "0.35", t0)
	setSpot(h.svc.Book, screener.VenueOKX, "50250", "0.20", "50255", "1", t0)
	s := h.signal(r, t0)
	// The evaluator opened at t0 but the executor is handed the signal
	// with quotes now 6 s old (poll 5 s): DATA_AGE, nothing traded.
	s.At = t0.Add(6 * time.Second)
	h.x.OnOpen(context.Background(), s, screener.Event{ID: "e1", RuleID: r.ID})
	if got := skippedReason(t, h, r.ID); got != SkipDataAge {
		t.Fatalf("reason = %s, want DATA_AGE", got)
	}
	if len(h.execs(r.ID)) != 0 {
		t.Fatal("execution booked despite DATA_AGE")
	}
	// screener_paper_executions_total{strategy,outcome} source.
	oc := h.x.Outcomes()
	if len(oc) != 1 || oc[0].Strategy != screener.StrategyCrossVenueSpot || oc[0].Outcome != OutcomeSkipped || oc[0].Count != 1 {
		t.Fatalf("Outcomes() = %+v", oc)
	}
}

func TestSkipDepth(t *testing.T) {
	h := newHarness(t, nil)
	h.balance(screener.VenueBinance, "USDT", "100000")
	h.balance(screener.VenueOKX, "BTC", "1")
	r := h.rule(spreadRule("5000"))
	// bidQty 0.05 < requested 0.1, partial_allowed false.
	setSpot(h.svc.Book, screener.VenueBinance, "49999", "1", "50000", "0.35", t0)
	setSpot(h.svc.Book, screener.VenueOKX, "50250", "0.05", "50255", "1", t0)
	h.open(r, t0)
	if got := skippedReason(t, h, r.ID); got != SkipDepth {
		t.Fatalf("reason = %s, want DEPTH", got)
	}
	// With partial_allowed the smaller fill proceeds at 0.05.
	h2 := newHarness(t, nil)
	h2.balance(screener.VenueBinance, "USDT", "100000")
	h2.balance(screener.VenueOKX, "BTC", "1")
	rp := spreadRule("5000")
	rp.Params = &screener.RuleParams{PartialAllowed: true}
	rp = h2.rule(rp)
	setSpot(h2.svc.Book, screener.VenueBinance, "49999", "1", "50000", "0.35", t0)
	setSpot(h2.svc.Book, screener.VenueOKX, "50250", "0.05", "50255", "1", t0)
	h2.open(rp, t0)
	execs := h2.execs(rp.ID)
	if len(execs) != 1 || !execs[0].Fills[0].Qty.Equal(d("0.05")) {
		t.Fatalf("partial execution = %+v", execs)
	}
}

func TestSkipBalance(t *testing.T) {
	h := newHarness(t, nil)
	h.balance(screener.VenueBinance, "USDT", "1000") // < 5 000
	h.balance(screener.VenueOKX, "BTC", "1")
	r := h.rule(spreadRule("5000"))
	setSpot(h.svc.Book, screener.VenueBinance, "49999", "1", "50000", "0.35", t0)
	setSpot(h.svc.Book, screener.VenueOKX, "50250", "0.20", "50255", "1", t0)
	h.open(r, t0)
	if got := skippedReason(t, h, r.ID); got != SkipBalance {
		t.Fatalf("reason = %s, want BALANCE", got)
	}
}

func TestSkipDriftCap(t *testing.T) {
	h := newHarness(t, nil)
	h.balance(screener.VenueBinance, "USDT", "100000")
	h.balance(screener.VenueOKX, "BTC", "1")
	cap := d("8000") // room for one 5 000 execution, not two
	r := spreadRule("5000")
	r.Params = &screener.RuleParams{MaxDriftQuote: &cap}
	r = h.rule(r)
	setSpot(h.svc.Book, screener.VenueBinance, "49999", "1", "50000", "0.35", t0)
	setSpot(h.svc.Book, screener.VenueOKX, "50250", "0.20", "50255", "1", t0)
	h.open(r, t0)
	h.open(r, t0.Add(5*time.Second))
	ps := h.positions(r.ID)
	if len(ps) != 2 {
		t.Fatalf("positions = %d", len(ps))
	}
	var reasons []string
	for _, p := range ps {
		if p.Status == StatusSkipped {
			reasons = append(reasons, p.SkippedReason)
		}
	}
	if len(reasons) != 1 || reasons[0] != SkipDriftCap {
		t.Fatalf("skips = %v, want [DRIFT_CAP]", reasons)
	}
	// The reverse direction is not capped: OKX→Binance reduces drift.
	rev := spreadRule("5000")
	rev.ID, rev.BuyVenues, rev.SellVenues = "spot-rev", []screener.Venue{screener.VenueOKX}, []screener.Venue{screener.VenueBinance}
	rev.Params = &screener.RuleParams{MaxDriftQuote: &cap}
	rev = h.rule(rev)
	h.balance(screener.VenueOKX, "USDT", "100000")
	setSpot(h.svc.Book, screener.VenueOKX, "49999", "1", "50000", "0.35", t0.Add(10*time.Second))
	setSpot(h.svc.Book, screener.VenueBinance, "50250", "0.20", "50255", "1", t0.Add(10*time.Second))
	h.x.loaded = false // reload wallets with the new OKX USDT row
	h.open(rev, t0.Add(10*time.Second))
	if got := h.execs(rev.ID); len(got) != 1 || got[0].Kind != KindSpot {
		t.Fatalf("reverse execution = %+v (positions %+v)", got, h.positions(rev.ID))
	}
	view, _ := h.x.AutoPaperView(context.Background(), t0.Add(15*time.Second))
	for _, rs := range view.Summary.PerRule {
		if rs.RuleID == r.ID && rs.Skipped[SkipDriftCap] != 1 {
			t.Errorf("summary skipped = %v", rs.Skipped)
		}
	}
}

func TestSkipMMRUnknown(t *testing.T) {
	h := newHarness(t, nil)
	h.balance(screener.VenueBinance, "USDT", "20000")
	h.balance(screener.VenueBinance, "USDT:perp", "20000")
	r := h.rule(carryRule(screener.RuleKindCarry, nil)) // no params.mmr
	for i := 1; i <= 30; i++ {
		_ = h.svc.Funding.UpsertFunding(context.Background(), screener.VenueBinance, "BTC", t0.Add(-time.Duration(i*8)*time.Hour), "0.00012")
	}
	setCarryBook(h.svc.Book, t0, "49998", "50000", "50100", "50102", "50050", "0.0001", t0.Add(4*time.Hour))
	h.open(r, t0)
	if got := skippedReason(t, h, r.ID); got != SkipMMRUnknown {
		t.Fatalf("reason = %s, want MMR_UNKNOWN", got)
	}
}

func TestSkipSettlementNear(t *testing.T) {
	h := newHarness(t, nil)
	h.balance(screener.VenueBinance, "USDT", "20000")
	h.balance(screener.VenueBinance, "USDT:perp", "20000")
	mmr := d("0.004")
	r := h.rule(carryRule(screener.RuleKindCarry, &screener.RuleParams{MMR: &mmr}))
	setCarryBook(h.svc.Book, t0, "49998", "50000", "50100", "50102", "50050", "0.0001", t0.Add(8*time.Second))
	h.open(r, t0)
	if got := skippedReason(t, h, r.ID); got != SkipSettlementNear {
		t.Fatalf("reason = %s, want SETTLEMENT_NEAR", got)
	}
}

// Perp leg rejected by the limit-IOC re-read → the spot leg is unwound
// at once and the cost lands under skipped.UNWIND (§3.6 step 5).
func TestUnwindOnRejectedSecondLeg(t *testing.T) {
	var h *harness
	w := &bookWaiter{onCall: map[int]func(){
		// Wait calls: 1 = spot submit, 2 = spot fill, 3 = perp submit.
		// Before the perp re-read, move the perp bid 30 bps lower.
		3: func() {
			p, _ := h.svc.Book.PerpFor(screener.VenueBinance, "BTC", "USDT")
			p.Bid = d("49949")
			h.svc.Book.SetPerp(p)
		},
	}}
	h = newHarness(t, w)
	h.balance(screener.VenueBinance, "USDT", "20000")
	h.balance(screener.VenueBinance, "USDT:perp", "20000")
	mmr := d("0.004")
	r := h.rule(carryRule(screener.RuleKindCarry, &screener.RuleParams{MMR: &mmr}))
	setCarryBook(h.svc.Book, t0, "49998", "50000", "50100", "50102", "50050", "0.0001", t0.Add(4*time.Hour))
	h.open(r, t0)
	if got := skippedReason(t, h, r.ID); got != SkipUnwind {
		t.Fatalf("reason = %s, want UNWIND", got)
	}
	execs := h.execs(r.ID)
	if len(execs) != 1 || execs[0].Kind != KindUnwind {
		t.Fatalf("execs = %+v", execs)
	}
	e := execs[0]
	if e.Fills[1].Status != "REJECTED" || e.Fills[2].Status != "FILLED" {
		t.Fatalf("fills = %+v", e.Fills)
	}
	// Cost: bought 0.2 at 50 010 (fee 10.002), sold at 49 998 × 0.9998 =
	// 49 988.0004 (fee 9.99760008) → 0.2 × (49 988.0004 − 50 010) − 19.99960008.
	want := d("0.2").Mul(d("49988.0004").Sub(d("50010"))).Sub(d("10.002")).Sub(d("9.99760008"))
	if !e.PnLQuote.Equal(want) {
		t.Errorf("unwind cost = %s, want %s", e.PnLQuote, want)
	}
	if !e.PnLQuote.IsNegative() {
		t.Errorf("unwind must cost money: %s", e.PnLQuote)
	}
	// Wallet: no base left, USDT down by the cost, perp wallet untouched.
	bals, _ := h.ledger.ListBalances(context.Background())
	for _, b := range bals {
		switch b.Asset {
		case "BTC":
			if !b.Balance.IsZero() {
				t.Errorf("BTC left after unwind: %s", b.Balance)
			}
		case "USDT":
			if !b.Balance.Equal(d("20000").Add(want)) {
				t.Errorf("USDT = %s, want %s", b.Balance, d("20000").Add(want))
			}
		case "USDT:perp":
			if !b.Balance.Equal(d("20000")) {
				t.Errorf("perp wallet = %s, want 20000", b.Balance)
			}
		}
	}
	view, _ := h.x.AutoPaperView(context.Background(), t0)
	if rs := view.Summary.PerRule[0]; rs.Skipped[SkipUnwind] != 1 || !rs.UnwindCostQuote.Equal(want) {
		t.Errorf("summary = %+v", rs)
	}
}

// Spot: leg B rejected → one-legged execution, mark-to-market, counted
// under skipped.partial_leg (§2.4 ii).
func TestSpotPartialLeg(t *testing.T) {
	var h *harness
	w := &bookWaiter{onCall: map[int]func(){
		3: func() { setSpot(h.svc.Book, screener.VenueOKX, "50100", "0.20", "50105", "1", t0) }, // bid −30 bps
	}}
	h = newHarness(t, w)
	h.balance(screener.VenueBinance, "USDT", "100000")
	h.balance(screener.VenueOKX, "BTC", "1")
	r := h.rule(spreadRule("5000"))
	setSpot(h.svc.Book, screener.VenueBinance, "49999", "1", "50000", "0.35", t0)
	setSpot(h.svc.Book, screener.VenueOKX, "50250", "0.20", "50255", "1", t0)
	h.open(r, t0)
	if got := skippedReason(t, h, r.ID); got != SkipPartialLeg {
		t.Fatalf("reason = %s, want partial_leg", got)
	}
	e := h.execs(r.ID)[0]
	if e.Kind != KindPartialLeg || e.Fills[0].Status != "FILLED" || e.Fills[1].Status != "REJECTED" {
		t.Fatalf("exec = %+v", e)
	}
	// Mark: bought 0.1 at 50 010, Binance mid 49 999.5 → 0.1 × (−10.5) − 5.001.
	want := d("0.1").Mul(d("49999.5").Sub(d("50010"))).Sub(d("5.001"))
	if !e.PnLQuote.Equal(want) {
		t.Errorf("partial-leg mark = %s, want %s", e.PnLQuote, want)
	}
}

// Funding accrual (§1.4): settled rate only; a missing settlement is
// retried for 3 polls then counted as missed, never estimated.
func TestFundingAccrualSettledOnlyAndRetry(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	h.balance(screener.VenueBinance, "USDT", "20000")
	h.balance(screener.VenueBinance, "USDT:perp", "20000")
	mmr := d("0.004")
	r := h.rule(carryRule(screener.RuleKindCarry, &screener.RuleParams{MMR: &mmr}))
	for i := 1; i <= 30; i++ {
		_ = h.svc.Funding.UpsertFunding(ctx, screener.VenueBinance, "BTC", t0.Add(-time.Duration(i*8)*time.Hour), "0.00012")
	}
	first := t0.Add(4 * time.Hour)
	setCarryBook(h.svc.Book, t0, "49998", "50000", "50100", "50102", "50050", "0.0001", first)
	h.open(r, t0)
	if len(h.execs(r.ID)) != 1 {
		t.Fatalf("open failed: %+v", h.positions(r.ID))
	}
	// Settlement 1 passes with NO history row: 3 retries, then missed.
	for i := 0; i < 5; i++ {
		now := first.Add(time.Duration(i+1) * 5 * time.Second)
		setCarryBook(h.svc.Book, now, "49998", "50000", "50100", "50102", "50000", "0.0001", first.Add(8*time.Hour))
		h.x.Tick(ctx, now)
	}
	pos := h.positions(r.ID)[0]
	var po perpOpen
	_ = fromMap(pos.OpenPayload, &po)
	if po.FundingMissed != 1 || po.Settlements != 0 || !pos.FundingQuote.IsZero() {
		t.Fatalf("after missing rate: missed=%d settlements=%d funding=%s", po.FundingMissed, po.Settlements, pos.FundingQuote)
	}
	if !po.NextFundingAt.Equal(first.Add(8 * time.Hour)) {
		t.Fatalf("next_funding_at = %s, want %s", po.NextFundingAt, first.Add(8*time.Hour))
	}
	// Settlement 2 with a NEGATIVE settled rate: the short PAYS.
	T2 := first.Add(8 * time.Hour)
	_ = h.svc.Funding.UpsertFunding(ctx, screener.VenueBinance, "BTC", T2, "-0.0002")
	now := T2.Add(time.Second)
	setCarryBook(h.svc.Book, now, "49998", "50000", "50100", "50102", "50000", "0.0001", T2.Add(8*time.Hour))
	h.x.Tick(ctx, now)
	pos = h.positions(r.ID)[0]
	want := d("0.2").Mul(d("50000")).Mul(d("-0.0002"))
	if !pos.FundingQuote.Equal(want) {
		t.Fatalf("funding = %s, want %s", pos.FundingQuote, want)
	}
	execs := h.execs(r.ID)
	last := execs[len(execs)-1]
	if last.Kind != KindFunding || last.Payload["rate"] != "-0.0002" || last.Payload["mark"] != "50000" {
		t.Fatalf("funding row = %+v", last)
	}
	// The predicted rate is never booked: no row for the interval that
	// only has a prediction.
	if n := len(execs); n != 2 {
		t.Fatalf("executions = %d (open + 1 funding), got %+v", n, execs)
	}
}

func TestStopsFire(t *testing.T) {
	cases := []struct {
		name          string
		spotBid, mark string
		perpAsk       string
		rate          string // settled + predicted funding rate per 8 h
		settle        bool   // advance past the first settlement first
		want          string // "" = the position must stay OPEN
	}{
		{"margin_stop", "75200", "75135", "75200", "0.0001", false, "margin_stop"},
		{"basis_stop", "50000", "50050", "50600", "0.0001", false, "basis_stop"}, // basis_now 120 bps − 20 ≥ 100
		// T-100. At 1 bps per 8 h the round trip needs 43 settlements, so
		// one settlement pays about a sixtieth of what the position owes
		// and the take-profit must NOT fire. This case previously
		// asserted the opposite, which is precisely the configuration
		// that closed 13 of 13 carries at a loss, every one `converged`
		// with `settlements = 1`.
		{"converged_waits_for_breakeven", "50000", "50050", "49990", "0.0001", true, ""},
		// Same book at 50 bps per 8 h: breakeven is 1 settlement, so the
		// one collected settlement does pay the round trip and the
		// position converges.
		{"converged", "50000", "50050", "49990", "0.005", true, "converged"}, // basis_now −2 ≤ 0
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, nil)
			ctx := context.Background()
			h.balance(screener.VenueBinance, "USDT", "20000")
			h.balance(screener.VenueBinance, "USDT:perp", "20000")
			mmr := d("0.004")
			r := h.rule(carryRule(screener.RuleKindCarry, &screener.RuleParams{MMR: &mmr}))
			// Settled history at the same rate: the entry gate refuses a
			// carry whose funding is unconfirmed by settled points
			// (T-097), so the look-back has to be populated for the
			// position to open at all.
			for i := 1; i <= 30; i++ {
				_ = h.svc.Funding.UpsertFunding(ctx, screener.VenueBinance, "BTC", t0.Add(-time.Duration(i*8)*time.Hour), tc.rate)
			}
			setCarryBook(h.svc.Book, t0, "49998", "50000", "50100", "50102", "50050", tc.rate, t0.Add(4*time.Hour))
			h.open(r, t0)
			now := t0.Add(time.Hour)
			if tc.settle {
				// Past the first settlement, with its settled rate
				// published, so funding actually accrues; the stop cases
				// stay at one hour to prove stops never wait (T-097).
				_ = h.svc.Funding.UpsertFunding(ctx, screener.VenueBinance, "BTC", t0.Add(4*time.Hour), tc.rate)
				now = t0.Add(5 * time.Hour)
			}
			setCarryBook(h.svc.Book, now, tc.spotBid, d(tc.spotBid).Add(decimal.NewFromInt(2)).String(), d(tc.perpAsk).Sub(decimal.NewFromInt(2)).String(), tc.perpAsk, tc.mark, tc.rate, t0.Add(4*time.Hour))
			h.x.Tick(ctx, now)
			pos := h.positions(r.ID)[0]
			if tc.want == "" {
				if pos.Status == StatusClosed {
					t.Fatalf("closed before breakeven, which is the T-100 defect: %+v", pos)
				}
				return
			}
			if pos.Status != StatusClosed {
				t.Fatalf("not closed: %+v", pos)
			}
			execs := h.execs(r.ID)
			if got := execs[len(execs)-1].Payload["reason"]; got != tc.want {
				t.Fatalf("reason = %v, want %s", got, tc.want)
			}
		})
	}
}

var _ = alerts.Lane{}
