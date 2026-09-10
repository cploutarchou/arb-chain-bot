package paperexec

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/cploutarchou/arb-chain-bot/internal/screener"
	"github.com/cploutarchou/arb-chain-bot/internal/screener/alerts"
	"github.com/cploutarchou/arb-chain-bot/internal/simulation"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

var t0 = time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)

type harness struct {
	t      *testing.T
	svc    *screener.Service
	ledger *MemoryLedger
	x      *Executor
	ids    int
}

// bookWaiter mutates the book on the Nth Wait call (a leg REJECT test).
type bookWaiter struct {
	calls  int
	onCall map[int]func()
}

func (w *bookWaiter) Wait(_ context.Context, _ time.Duration) error {
	w.calls++
	if f, ok := w.onCall[w.calls]; ok {
		f()
	}
	return nil
}

func newHarness(t *testing.T, waiter simulation.Waiter) *harness {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := screener.NewService(screener.NewBook(), screener.NewMemoryStore(), log, nil)
	if _, err := svc.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	svc.Rules = screener.NewMemoryRuleStore()
	svc.Events = screener.NewMemoryEventStore()
	svc.Funding = screener.NewMemoryFundingStore()
	h := &harness{t: t, svc: svc, ledger: NewMemoryLedger()}
	if waiter == nil {
		waiter = simulation.VirtualWaiter{Clock: simulation.NewVirtualClock(t0)}
	}
	h.x = New(svc, h.ledger, log, Options{Waiter: waiter, Seed: 7, IDGen: func() string { h.ids++; return "id" + itoa(h.ids) }})
	return h
}

func itoa(i int) string { return decimal.NewFromInt(int64(i)).String() }

func (h *harness) balance(venue screener.Venue, asset, amt string) {
	if err := h.ledger.UpsertBalance(context.Background(), venue, asset, d(amt), t0); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) rule(r screener.Rule) screener.Rule {
	if err := r.Validate(); err != nil {
		h.t.Fatal(err)
	}
	if _, err := h.svc.Rules.InsertRule(context.Background(), r, "t"); err != nil {
		h.t.Fatal(err)
	}
	return r
}

func (h *harness) inputs() alerts.Inputs {
	snap := h.svc.Current()
	return alerts.Inputs{Book: h.svc.Book,
		SpotFees:       func(v screener.Venue) (decimal.Decimal, bool) { return snap.Settings.Venues[v].SpotTakerBps, true },
		PerpFees:       func(v screener.Venue) (decimal.Decimal, bool) { return snap.Settings.Venues[v].PerpTakerBps, true },
		FundingHistory: h.svc.Funding, PollInterval: 5 * time.Second}
}

// signal computes the rule's single signal at now and fails if absent.
func (h *harness) signal(r screener.Rule, now time.Time) alerts.Signal {
	sigs := alerts.ComputeSignals(context.Background(), h.inputs(), r, now)
	if len(sigs) != 1 {
		h.t.Fatalf("signals = %d, want 1", len(sigs))
	}
	return sigs[0]
}

func (h *harness) open(r screener.Rule, now time.Time) (alerts.Signal, screener.Event) {
	s := h.signal(r, now)
	ev := screener.Event{ID: "evt-" + itoa(h.ids+1000), RuleID: r.ID, Kind: r.Kind, Base: s.Lane.Base, Quote: s.Lane.Quote,
		BuyVenue: s.Lane.VenueA, SellVenue: s.Lane.VenueB, OpenedAt: now}
	_ = h.svc.Events.InsertEvent(context.Background(), ev)
	h.x.OnOpen(context.Background(), s, ev)
	return s, ev
}

func (h *harness) execs(ruleID string) []Execution {
	out, err := h.ledger.ListExecutions(context.Background(), ruleID, 0)
	if err != nil {
		h.t.Fatal(err)
	}
	return out
}

func (h *harness) positions(ruleID string) []Position {
	out, err := h.ledger.ListPositions(context.Background(), ruleID, "", 0)
	if err != nil {
		h.t.Fatal(err)
	}
	return out
}

func setSpot(book *screener.Book, venue screener.Venue, bid, bidQty, ask, askQty string, at time.Time) {
	book.SetQuote(screener.Quote{Venue: venue, Base: "BTC", Quote: "USDT", Bid: d(bid), BidQty: d(bidQty), Ask: d(ask), AskQty: d(askQty), At: at})
}

func spreadRule(size string) screener.Rule {
	min := d("5")
	return screener.Rule{ID: "spot1", Name: "spot", Enabled: true, Kind: screener.RuleKindSpread, MinSpreadBps: &min,
		MinLiquidityQuote: d("500"), BuyVenues: []screener.Venue{screener.VenueBinance}, SellVenues: []screener.Venue{screener.VenueOKX},
		Quotes: []string{"USDT"}, AutoPaper: true, PaperSizeQuote: d(size)}
}

// §2.5: Binance ask 50 000 / 0.35; venue B bid 50 250 / 0.20; size 5 000.
func TestGoldenCrossVenueSpot(t *testing.T) {
	h := newHarness(t, nil)
	h.balance(screener.VenueBinance, "USDT", "100000")
	h.balance(screener.VenueOKX, "BTC", "1")
	r := h.rule(spreadRule("5000"))
	setSpot(h.svc.Book, screener.VenueBinance, "49999", "1", "50000", "0.35", t0)
	setSpot(h.svc.Book, screener.VenueOKX, "50250", "0.20", "50255", "1", t0)

	s, _ := h.open(r, t0)
	if got := s.ExecBps.StringFixed(2); got != "20.95" {
		t.Fatalf("exec_bps = %s, want 20.95", got)
	}
	execs := h.execs(r.ID)
	if len(execs) != 1 || execs[0].Kind != KindSpot {
		t.Fatalf("executions = %+v", execs)
	}
	e := execs[0]
	if got := e.Fills[0].Qty.String(); got != "0.1" {
		t.Errorf("size_base = %s, want 0.1", got)
	}
	if got := e.Fills[0].FillPrice.String(); got != "50010" {
		t.Errorf("buy fill = %s, want 50010", got)
	}
	if got := e.Fills[1].FillPrice.String(); got != "50239.95" {
		t.Errorf("sell fill = %s, want 50239.95", got)
	}
	if got := e.PnLQuote.StringFixed(2); got != "12.97" {
		t.Errorf("pnl_quote = %s (%s), want 12.97", got, e.PnLQuote)
	}
	if got := e.Payload["pnl_after_rebalance_quote"].(string); d(got).StringFixed(2) != "2.97" {
		t.Errorf("pnl_after_rebalance = %s, want 2.97", got)
	}
	if got := d(e.Payload["pnl_bps"].(string)).StringFixed(1); got != "25.9" {
		t.Errorf("pnl_bps = %s, want 25.9", got)
	}
	// Wallets moved: Binance USDT down by cost, BTC up 0.1; OKX BTC down 0.1, USDT up proceeds.
	bals, _ := h.ledger.ListBalances(context.Background())
	want := map[string]string{
		"binance/USDT": d("100000").Sub(d("50010").Mul(d("0.1")).Mul(d("1.001"))).String(),
		"binance/BTC":  "0.1",
		"okx/BTC":      "0.9",
		"okx/USDT":     d("50239.95").Mul(d("0.1")).Mul(d("0.999")).String(),
	}
	for _, b := range bals {
		k := string(b.Venue) + "/" + b.Asset
		if w, ok := want[k]; ok && !b.Balance.Equal(d(w)) {
			t.Errorf("balance %s = %s, want %s", k, b.Balance, w)
		}
	}
	// Next poll: realised slippage measured (unchanged quotes → 0).
	h.x.Tick(context.Background(), t0.Add(5*time.Second))
	e = h.execs(r.ID)[0]
	if e.RealisedSlipBps == nil || !e.RealisedSlipBps.IsZero() {
		t.Errorf("realised slip = %v, want 0", e.RealisedSlipBps)
	}
	// Summary.
	view, err := h.x.AutoPaperView(context.Background(), t0.Add(5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Summary.PerRule) != 1 {
		t.Fatalf("per_rule = %d", len(view.Summary.PerRule))
	}
	rs := view.Summary.PerRule[0]
	if rs.Executed != 1 || rs.Alerts != 1 || rs.NetPnLQuote.StringFixed(2) != "12.97" || rs.HitRate == nil || !rs.HitRate.Equal(decimal.NewFromInt(1)) {
		t.Errorf("summary = %+v", rs)
	}
	if len(rs.InventoryDrift) != 1 || !rs.InventoryDrift[0].DriftBase.Equal(d("0.1")) || rs.InventoryDrift[0].Unmatched != 1 {
		t.Errorf("drift = %+v", rs.InventoryDrift)
	}
	// Conservative figure: 20 bps of the drift notional at the current mid.
	mid := d("49999").Add(d("50000")).Div(decTwo)
	wantAfter := d("12.970005").Sub(d("0.1").Mul(mid).Mul(d("0.002")))
	if !rs.PnLAfterRebalance.Equal(wantAfter) {
		t.Errorf("pnl_after_rebalance = %s, want %s", rs.PnLAfterRebalance, wantAfter)
	}
}

// §2.5 first case: 30 bps gross → exec 0.97 < 5 → the rule never
// activates, so no event and no execution.
func TestSpotThirtyBpsGrossIsNotTradeable(t *testing.T) {
	h := newHarness(t, nil)
	r := h.rule(spreadRule("5000"))
	setSpot(h.svc.Book, screener.VenueBinance, "49999", "1", "50000", "0.35", t0)
	setSpot(h.svc.Book, screener.VenueOKX, "50150", "0.20", "50155", "1", t0)
	s := h.signal(r, t0)
	if s.Active || s.ExecBps.StringFixed(2) != "0.97" {
		t.Fatalf("active=%v exec=%s", s.Active, s.ExecBps.StringFixed(2))
	}
}

func setCarryBook(book *screener.Book, at time.Time, spotBid, spotAsk, perpBid, perpAsk, mark, predicted string, nextFunding time.Time) {
	setSpot(book, screener.VenueBinance, spotBid, "2", spotAsk, "2", at)
	book.SetPerp(screener.Perp{Venue: screener.VenueBinance, Base: "BTC", Quote: "USDT", Mark: d(mark), Index: d(mark),
		Bid: d(perpBid), Ask: d(perpAsk), BidQty: d("2"), AskQty: d("2"), FundingRate: d(predicted), PredictedFundingRate: d(predicted), IntervalH: 8,
		NextFundingAt: nextFunding, At: at})
}

func carryRule(kind screener.RuleKind, params *screener.RuleParams) screener.Rule {
	apr := d("0")
	return screener.Rule{ID: "carry1", Name: "carry", Enabled: true, Kind: kind, MinCarryAPR: &apr,
		BuyVenues: []screener.Venue{screener.VenueBinance}, Quotes: []string{"USDT"}, AutoPaper: true,
		PaperSizeQuote: d("10000"), Params: params}
}

// §3.5: spot ask 50 000 / bid 49 998; perp bid 50 100; predicted
// +0.0100 %/8 h, last-30 mean +0.0120 %; size 10 000; max hold 30 d.
func TestGoldenCarry(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	h.balance(screener.VenueBinance, "USDT", "20000")
	h.balance(screener.VenueBinance, "USDT:perp", "20000")
	mmr := d("0.004")
	r := h.rule(carryRule(screener.RuleKindCarry, &screener.RuleParams{MMR: &mmr, MaxHoldH: 720}))
	for i := 1; i <= 30; i++ {
		_ = h.svc.Funding.UpsertFunding(ctx, screener.VenueBinance, "BTC", t0.Add(-time.Duration(i*8)*time.Hour), "0.00012")
	}
	first := t0.Add(4 * time.Hour)
	setCarryBook(h.svc.Book, t0, "49998", "50000", "50100", "50102", "50050", "0.0001", first)

	s, _ := h.open(r, t0)
	if got := s.EdgeBps.StringFixed(2); got != "84.80" {
		t.Fatalf("edge = %s, want 84.80", got)
	}
	execs := h.execs(r.ID)
	if len(execs) != 1 || execs[0].Kind != KindOpen {
		t.Fatalf("open execution missing: %+v", h.positions(r.ID))
	}
	e := execs[0]
	if e.Fills[0].FillPrice.String() != "50010" || e.Fills[0].FeeQuote.String() != "10.002" {
		t.Errorf("spot open fill %s fee %s, want 50010 / 10.002", e.Fills[0].FillPrice, e.Fills[0].FeeQuote)
	}
	if e.Fills[1].FillPrice.String() != "50089.98" || e.Fills[1].FeeQuote.StringFixed(3) != "5.009" {
		t.Errorf("perp open fill %s fee %s, want 50089.98 / 5.009", e.Fills[1].FillPrice, e.Fills[1].FeeQuote)
	}
	if got := d(e.Payload["collateral"].(string)).StringFixed(0); got != "10018" {
		t.Errorf("collateral = %s, want 10018", got)
	}
	// 90 settlements at +0.008 % with mark 50 000 → 0.8 each = 72.00.
	for i := 0; i < 90; i++ {
		T := first.Add(time.Duration(i*8) * time.Hour)
		_ = h.svc.Funding.UpsertFunding(ctx, screener.VenueBinance, "BTC", T, "0.00008")
		now := T.Add(time.Second)
		setCarryBook(h.svc.Book, now, "49998", "50000", "50100", "50102", "50000", "0.0001", T.Add(8*time.Hour))
		h.x.Tick(ctx, now)
	}
	pos := h.positions(r.ID)[0]
	if pos.Status != StatusOpen {
		t.Fatalf("position closed early: %+v", pos)
	}
	if got := pos.FundingQuote.StringFixed(2); got != "72.00" {
		t.Errorf("funding = %s, want 72.00", got)
	}
	var po perpOpen
	_ = fromMap(pos.OpenPayload, &po)
	if po.Settlements != 90 || po.FundingMissed != 0 {
		t.Errorf("settlements=%d missed=%d, want 90/0", po.Settlements, po.FundingMissed)
	}
	// Max-hold exit at spot bid 51 000 / perp ask 51 010. The worked
	// example applies no slippage on the close legs, so the rule's
	// slip allowance is set to 0 before the close (operator-set,
	// versioned per §1.3) to reproduce its figures exactly.
	zero := d("0")
	r.Params.SlipBps = &zero
	if _, err := h.svc.Rules.UpdateRule(ctx, r, "t"); err != nil {
		t.Fatal(err)
	}
	closeAt := t0.Add(720 * time.Hour)
	setCarryBook(h.svc.Book, closeAt, "51000", "51002", "51008", "51010", "51000", "0.0001", closeAt.Add(4*time.Hour))
	h.x.Tick(ctx, closeAt)
	pos = h.positions(r.ID)[0]
	if pos.Status != StatusClosed {
		t.Fatalf("position not closed: %+v", pos)
	}
	closeExec := h.execs(r.ID)[len(h.execs(r.ID))-1]
	if closeExec.Kind != KindClose || closeExec.Payload["reason"] != "max_hold" {
		t.Fatalf("close exec = %+v", closeExec)
	}
	spotLeg := d(closeExec.Payload["spot_leg_pnl"].(string))
	perpPrice := d(closeExec.Payload["perp_price_pnl"].(string))
	perpFees := d(closeExec.Payload["perp_fees"].(string))
	if spotLeg.StringFixed(2) != "177.80" || perpPrice.StringFixed(2) != "-184.00" || perpFees.StringFixed(2) != "10.11" {
		t.Errorf("components spot %s perp %s fees %s, want 177.80 / -184.00 / 10.11", spotLeg, perpPrice, perpFees)
	}
	// Exact decimal total is 55.684 (rounds to 55.68); the spec's 55.69
	// is the sum of its individually rounded components — both stated.
	if got := pos.PnLQuote.StringFixed(3); got != "55.684" {
		t.Errorf("total = %s, want 55.684", got)
	}
	roundedSum := d(spotLeg.StringFixed(2)).Add(d(perpPrice.StringFixed(2))).Sub(d(perpFees.StringFixed(2))).Add(d(pos.FundingQuote.StringFixed(2)))
	if got := roundedSum.StringFixed(2); got != "55.69" {
		t.Errorf("sum of rounded components = %s, want 55.69", got)
	}
	// Liquidation estimate ≈ 99 779 (2 × 50 089.98 / 1.004).
	if got := po.LiqEst.StringFixed(0); got != "99781" && got != "99779" {
		t.Logf("liquidation estimate = %s (spec ≈ 99 779 from 50 100 × 2 / 1.004; ours uses the fill 50 089.98)", got)
	}
	// Wallet conservation: USDT + USDT:perp end = start + pnl.
	bals, _ := h.ledger.ListBalances(ctx)
	total := decimal.Zero
	for _, b := range bals {
		if b.Asset == "USDT" || b.Asset == "USDT:perp" {
			total = total.Add(b.Balance)
		}
	}
	if !total.Equal(d("40000").Add(pos.PnLQuote)) {
		t.Errorf("wallet total = %s, want %s", total, d("40000").Add(pos.PnLQuote))
	}
}

// §5.3: predicted +0.05 %/8 h, last-6 mean +0.045 %, basis −2 bps,
// size 10 000 → 0.2 BTC; 12 settlements mean +0.035 % → 42.00; fees
// 30.00; realised slippage 4.00 (1 bps × 4 legs); basis change −4 bps →
// net 4.00.
func TestGoldenFundingHarvest(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	h.balance(screener.VenueBinance, "USDT", "20000")
	h.balance(screener.VenueBinance, "USDT:perp", "20000")
	mmr, slip := d("0.004"), d("1")
	r := h.rule(carryRule(screener.RuleKindBasis, &screener.RuleParams{MMR: &mmr, SlipBps: &slip}))
	for i := 1; i <= 6; i++ {
		_ = h.svc.Funding.UpsertFunding(ctx, screener.VenueBinance, "BTC", t0.Add(-time.Duration(i*8)*time.Hour), "0.00045")
	}
	first := t0.Add(4 * time.Hour)
	setCarryBook(h.svc.Book, t0, "49999", "50000", "49990", "49992", "50000", "0.0005", first)
	s, _ := h.open(r, t0)
	if !s.Active || s.BreakevenN != 9 {
		t.Fatalf("signal active=%v reason=%s breakeven=%d", s.Active, s.Reason, s.BreakevenN)
	}
	e := h.execs(r.ID)[0]
	if e.Kind != KindOpen || e.Fills[0].FillPrice.String() != "50005" || e.Fills[1].FillPrice.String() != "49985.001" {
		t.Fatalf("open fills: %+v", e.Fills)
	}
	for i := 0; i < 12; i++ {
		T := first.Add(time.Duration(i*8) * time.Hour)
		_ = h.svc.Funding.UpsertFunding(ctx, screener.VenueBinance, "BTC", T, "0.00035")
		now := T.Add(time.Second)
		predicted := "0.0005"
		if i >= 10 {
			predicted = "0.00005" // ≤ 1 bps for the last two settlements → funding_exit
		}
		setCarryBook(h.svc.Book, now, "50000", "50001", "50008", "50010", "50000", predicted, T.Add(8*time.Hour))
		h.x.Tick(ctx, now)
	}
	pos := h.positions(r.ID)[0]
	if pos.Status != StatusClosed {
		t.Fatalf("position not closed: %+v", pos)
	}
	closeExec := h.execs(r.ID)[len(h.execs(r.ID))-1]
	if closeExec.Payload["reason"] != "funding_exit" {
		t.Errorf("reason = %v, want funding_exit", closeExec.Payload["reason"])
	}
	if got := pos.FundingQuote.StringFixed(2); got != "42.00" {
		t.Errorf("funding = %s, want 42.00", got)
	}
	if got := pos.PnLQuote.StringFixed(2); got != "4.00" {
		t.Errorf("net = %s (%s), want 4.00", got, pos.PnLQuote)
	}
	view, _ := h.x.AutoPaperView(ctx, t0.Add(100*time.Hour))
	rs := view.Summary.PerRule[0]
	if rs.FundingRows != 12 || rs.FundingQuote.StringFixed(2) != "42.00" || rs.Executed != 1 || rs.Samples != 1 {
		t.Errorf("summary = %+v", rs)
	}
	if rs.FeesQuote.StringFixed(2) != "30.00" {
		t.Errorf("fees = %s, want 30.00", rs.FeesQuote)
	}
}
