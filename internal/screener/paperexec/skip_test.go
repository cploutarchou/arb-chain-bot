package paperexec

import (
	"github.com/cploutarchou/arb-chain-bot/internal/exchange"

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
	// The forward buy's received-side fee left 0.0999 BTC on binance;
	// the reverse sells 0.1 of it — seed the difference like a funded
	// wallet would be.
	h.balance(screener.VenueBinance, "BTC", "0.1")
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
	// Cost under per-venue placement (audit T10): bought 0.2 at 50 010
	// with the fee taken in base (0.1998 held, zero quote fee), unwound
	// 0.1998 at 49 988.0004 with the sell fee in quote — the entry cost
	// basis is the bare 0.2 notional.
	want := d("0.1998").Mul(d("49988.0004")).Mul(d("0.999")).Sub(d("0.2").Mul(d("50010")))
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
	// Mark under placement (audit T10): bought 0.1 at 50 010 with the
	// fee taken in base (0.0999 held, no quote fee), Binance mid
	// 49 999.5 → 0.0999 × (−10.5).
	want := d("0.0999").Mul(d("49999.5").Sub(d("50010")))
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
	want := d("0.1998").Mul(d("50000")).Mul(d("-0.0002")) // held size after the entry fee (audit T10)
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

// TestPerpLegDepthBoundsTheFill (audit X8): the §1.3 haircut applies to
// both legs. With deep spot depth and a thin perp bid, the fillable
// quantity is the perp side's bound, not the spot side's — before X8
// the executor filled at any size the spot book supported.
func TestPerpLegDepthBoundsTheFill(t *testing.T) {
	h := newHarness(t, nil)
	h.balance(screener.VenueBinance, "USDT", "100000")
	h.balance(screener.VenueBinance, "USDT:perp", "100000")
	partial, mmr := true, d("0.005")
	r := h.rule(carryRule(screener.RuleKindCarry, &screener.RuleParams{PartialAllowed: partial, MMR: &mmr}))
	for i := 1; i <= 30; i++ {
		_ = h.svc.Funding.UpsertFunding(context.Background(), screener.VenueBinance, "BTC", t0.Add(-time.Duration(i*8)*time.Hour), "0.00012")
	}
	// Spot ask 50 000 × 2 BTC (deep: 100 000 quote of depth); perp bid
	// 50 100 × 0.05 BTC (2 505 quote). Size 10 000 quote must come down
	// to the perp's 2 505 (haircut 1.0), not the spot's 100 000.
	setSpot(h.svc.Book, screener.VenueBinance, "49998", "2", "50000", "2", t0)
	h.svc.Book.SetPerp(screener.Perp{Venue: screener.VenueBinance, Base: "BTC", Quote: "USDT",
		Mark: d("50050"), Index: d("49999"), Bid: d("50100"), Ask: d("50102"), BidQty: d("0.05"), AskQty: d("0.05"),
		FundingRate: d("0.0001"), PredictedFundingRate: d("0.0001"), IntervalH: 8,
		NextFundingAt: t0.Add(4 * time.Hour), At: t0})
	h.open(r, t0)

	pos := h.positions(r.ID)
	if len(pos) != 1 || pos[0].Status != StatusOpen {
		t.Fatalf("positions = %+v", pos)
	}
	// 2 505 quote / 50 000 per perp bid = 0.05; step-truncated to the
	// instrument step. Before X8 the fill was the spot-bound quantity.
	if !pos[0].Qty.LessThanOrEqual(d("0.05")) {
		t.Fatalf("qty = %s, want bounded by the perp bid depth 0.05", pos[0].Qty)
	}
	if ex := h.execs(r.ID); len(ex) == 0 {
		t.Fatal("no executions recorded")
	}
}

// TestTruncStepMatchesCanonicalQuantize (audit T10): the screener's
// quantity quantization IS the engine's — exchange.QuantizeQty's exact
// QuoRem, not a 28-digit Div-Floor-Mul that can drift at precision
// edges. The differential grid must hold for every (q, step) the
// executor can produce; any divergence would mean the two paper stacks
// fill different sizes for the same rule.
func TestTruncStepMatchesCanonicalQuantize(t *testing.T) {
	qs := []string{"0", "0.0000001", "0.3", "1", "1.7", "2.9999999", "12345.67890123456789012345678",
		"0.9999999999999999999999999999", "3.0000000000000000000000000001"}
	steps := []string{"0.00000001", "0.001", "0.01", "1", "5", "0.3000000000000000000000000001"}
	for _, qs_ := range qs {
		q := dec(qs_)
		for _, ss := range steps {
			step := dec(ss)
			want, err := exchange.InstrumentRules{QtyMode: exchange.PrecisionStep, QtyStep: step}.QuantizeQty(q)
			if err != nil {
				t.Fatal(err)
			}
			if got := truncStep(q, step); !got.Equal(want) {
				t.Fatalf("truncStep(%s, %s) = %s, canonical = %s", q, step, got, want)
			}
		}
	}
	// A non-positive step leaves the quantity untouched.
	if got := truncStep(dec("1.2345"), dec("0")); !got.Equal(dec("1.2345")) {
		t.Fatalf("zero step: %s", got)
	}
}

// TestDriftIncrementalMatchesLedgerScan (audit X9): the incremental
// drift map must equal what a fresh executor would compute by scanning
// the ledger — forward executions add, reverse subtract, and the two
// directions share one canonical key.
func TestDriftIncrementalMatchesLedgerScan(t *testing.T) {
	h := newHarness(t, nil)
	h.balance(screener.VenueBinance, "USDT", "1000000")
	h.balance(screener.VenueOKX, "BTC", "10")
	cap := d("1000000")
	r := spreadRule("5000")
	r.Params = &screener.RuleParams{MaxDriftQuote: &cap}
	r = h.rule(r)
	rev := spreadRule("5000")
	rev.ID, rev.BuyVenues, rev.SellVenues = r.ID+"-rev", []screener.Venue{screener.VenueOKX}, []screener.Venue{screener.VenueBinance}
	rev.Params = &screener.RuleParams{MaxDriftQuote: &cap}
	rev = h.rule(rev)
	setSpot(h.svc.Book, screener.VenueBinance, "49999", "1", "50000", "0.35", t0)
	setSpot(h.svc.Book, screener.VenueOKX, "50250", "0.20", "50255", "1", t0)
	h.x.loaded = false
	// Two forward executions, one reverse.
	h.open(r, t0)
	h.open(r, t0.Add(5*time.Second))
	h.open(rev, t0.Add(10*time.Second))

	ctx := context.Background()
	lane := alerts.Lane{Base: "BTC", Quote: "USDT", VenueA: screener.VenueBinance, VenueB: screener.VenueOKX}
	h.x.mu.Lock()
	got, err := h.x.driftFor(ctx, r.ID, lane)
	h.x.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	// A fresh executor over the same ledger seeds from the scan alone.
	fresh := &Executor{svc: h.svc, ledger: h.ledger, drift: map[string]decimal.Decimal{}, driftSeeded: map[string]bool{}}
	fresh.mu.Lock()
	want, err := fresh.driftFor(ctx, r.ID, lane)
	fresh.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(want) {
		t.Fatalf("incremental drift %s != scan %s", got, want)
	}
	// The reverse lane reads the negation of the forward lane.
	h.x.mu.Lock()
	revDrift, _ := h.x.driftFor(ctx, r.ID, alerts.Lane{Base: "BTC", Quote: "USDT", VenueA: screener.VenueOKX, VenueB: screener.VenueBinance})
	h.x.mu.Unlock()
	if !revDrift.Equal(got.Neg()) {
		t.Fatalf("reverse lane drift %s != -%s", revDrift, got)
	}
}

// TestAutoPaperViewCachedUntilWrite (audit X9): within one poll
// interval the view is served from the cache, and an execution landing
// invalidates it — the next request reflects the write immediately.
func TestAutoPaperViewCachedUntilWrite(t *testing.T) {
	h := newHarness(t, nil)
	h.balance(screener.VenueBinance, "USDT", "1000000")
	r := h.rule(spreadRule("5000"))
	setSpot(h.svc.Book, screener.VenueBinance, "49999", "1", "50000", "0.35", t0)
	setSpot(h.svc.Book, screener.VenueOKX, "50250", "0.20", "50255", "1", t0)
	ctx := context.Background()

	v1, err := h.x.AutoPaperView(ctx, t0)
	if err != nil {
		t.Fatal(err)
	}
	if len(v1.Positions) != 0 {
		t.Fatalf("cold view positions = %d", len(v1.Positions))
	}
	h.open(r, t0.Add(time.Second))
	v2, err := h.x.AutoPaperView(ctx, t0.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(v2.Positions) == 0 {
		t.Fatal("write did not invalidate the cached view")
	}
}
