package storage

import (
	"context"
	"testing"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/screener"
	"github.com/cploutarchou/arb-chain-bot/internal/screener/paperexec"
)

func TestScreenerPaperLedgerRoundTrip(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	l := s.ScreenerPaper()

	if err := l.UpsertBalance(ctx, screener.VenueBinance, "USDT", d("100000"), t0); err != nil {
		t.Fatal(err)
	}
	if err := l.UpsertBalance(ctx, screener.VenueBinance, "USDT", d("99949.99"), t0.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	bals, err := l.ListBalances(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(bals) != 1 || !bals[0].Balance.Equal(d("99949.99")) || bals[0].Venue != screener.VenueBinance {
		t.Fatalf("balances = %+v", bals)
	}

	closed := t0
	pos := paperexec.Position{ID: "pos-1", RuleID: "r1", EventID: "evt-1", Strategy: screener.StrategyCrossVenueSpot,
		Base: "BTC", Quote: "USDT", VenueA: screener.VenueBinance, VenueB: screener.VenueOKX, Qty: d("0.1"),
		OpenPayload: map[string]any{"exec_bps": "20.95"}, OpenedAt: t0, ClosedAt: &closed, PnLQuote: d("12.970005"), Status: paperexec.StatusClosed}
	if err := l.InsertPosition(ctx, pos); err != nil {
		t.Fatal(err)
	}
	open := paperexec.Position{ID: "pos-2", RuleID: "r2", Strategy: screener.StrategyCarry, Base: "BTC", Quote: "USDT",
		VenueA: screener.VenueBinance, VenueB: screener.VenueBinance, Qty: d("0.2"), OpenPayload: map[string]any{"spot_open": "50010"},
		OpenedAt: t0.Add(time.Minute), Status: paperexec.StatusOpen}
	if err := l.InsertPosition(ctx, open); err != nil {
		t.Fatal(err)
	}
	skipped := paperexec.Position{ID: "pos-3", RuleID: "r1", Strategy: screener.StrategyCrossVenueSpot, Base: "ETH", Quote: "USDT",
		VenueA: screener.VenueBinance, VenueB: screener.VenueOKX, OpenedAt: t0.Add(2 * time.Minute), Status: paperexec.StatusSkipped,
		SkippedReason: paperexec.SkipDepth, ClosedAt: &closed}
	if err := l.InsertPosition(ctx, skipped); err != nil {
		t.Fatal(err)
	}
	got, err := l.ListPositions(ctx, "", paperexec.StatusOpen, 0)
	if err != nil || len(got) != 1 || got[0].ID != "pos-2" || got[0].OpenPayload["spot_open"] != "50010" {
		t.Fatalf("open positions = %+v err=%v", got, err)
	}
	open.FundingQuote = d("0.8")
	open.OpenPayload["settlements"] = 1
	if err := l.UpdatePosition(ctx, open); err != nil {
		t.Fatal(err)
	}
	got, _ = l.ListPositions(ctx, "r2", "", 0)
	if len(got) != 1 || !got[0].FundingQuote.Equal(d("0.8")) || got[0].OpenPayload["settlements"] != float64(1) {
		t.Fatalf("updated = %+v", got)
	}
	got, _ = l.ListPositions(ctx, "r1", "", 0)
	if len(got) != 2 || got[0].ID != "pos-3" || got[0].SkippedReason != paperexec.SkipDepth || got[1].ClosedAt == nil {
		t.Fatalf("r1 positions = %+v", got)
	}
	if err := l.UpdatePosition(ctx, paperexec.Position{ID: "missing"}); err != screener.ErrNotFound {
		t.Fatalf("update missing = %v", err)
	}

	e1 := paperexec.Execution{ID: "exe-1", PositionID: "pos-1", RuleID: "r1", EventID: "evt-1", Strategy: screener.StrategyCrossVenueSpot,
		Kind: paperexec.KindSpot, Base: "BTC", Quote: "USDT", VenueA: screener.VenueBinance, VenueB: screener.VenueOKX,
		Fills:     []paperexec.Fill{{Leg: 1, Venue: screener.VenueBinance, Market: "spot", Side: "BUY", Qty: d("0.1"), FillPrice: d("50010"), FeeQuote: d("5.001"), Status: "FILLED"}},
		FeesQuote: d("10.024"), SlipAllowBps: d("2"), PnLQuote: d("12.970005"), Payload: map[string]any{"net_bps": "29.95"}, At: t0}
	e2 := paperexec.Execution{ID: "exe-2", PositionID: "pos-2", RuleID: "r2", Strategy: screener.StrategyCarry, Kind: paperexec.KindFunding,
		Base: "BTC", Quote: "USDT", VenueA: screener.VenueBinance, VenueB: screener.VenueBinance, PnLQuote: d("0.8"), At: t0.Add(time.Hour)}
	for _, e := range []paperexec.Execution{e1, e2, e1} { // duplicate insert is idempotent
		if err := l.InsertExecution(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.SetExecutionSlip(ctx, "exe-1", d("0.5")); err != nil {
		t.Fatal(err)
	}
	execs, err := l.ListExecutions(ctx, "", 0)
	if err != nil || len(execs) != 2 || execs[0].ID != "exe-1" || execs[1].ID != "exe-2" {
		t.Fatalf("executions = %+v err=%v", execs, err)
	}
	if execs[0].RealisedSlipBps == nil || !execs[0].RealisedSlipBps.Equal(d("0.5")) || !execs[0].PnLQuote.Equal(d("12.970005")) ||
		len(execs[0].Fills) != 1 || !execs[0].Fills[0].FillPrice.Equal(d("50010")) || execs[0].Payload["net_bps"] != "29.95" {
		t.Fatalf("exe-1 = %+v", execs[0])
	}
	if execs[1].Fills == nil || len(execs[1].Fills) != 0 || execs[1].RealisedSlipBps != nil {
		t.Fatalf("exe-2 = %+v", execs[1])
	}
	r1, _ := l.ListExecutions(ctx, "r1", 0)
	if len(r1) != 1 {
		t.Fatalf("r1 executions = %d", len(r1))
	}

	// Event close / execution link / count (T-070 extensions).
	ev := s.ScreenerEvents()
	if err := ev.InsertEvent(ctx, screener.Event{ID: "evt-1", RuleID: "r1", Kind: screener.RuleKindSpread, Base: "BTC", Quote: "USDT", OpenedAt: t0, PeakNetBps: "20.95"}); err != nil {
		t.Fatal(err)
	}
	if err := ev.SetEventExecution(ctx, "evt-1", "exe-1"); err != nil {
		t.Fatal(err)
	}
	if err := ev.CloseEvent(ctx, "evt-1", t0.Add(20*time.Second), 20, "22.95"); err != nil {
		t.Fatal(err)
	}
	if err := ev.CloseEvent(ctx, "nope", t0, 0, "0"); err != screener.ErrNotFound {
		t.Fatalf("close missing = %v", err)
	}
	events, _ := ev.ListEvents(ctx, "r1", 10)
	if len(events) != 1 || events[0].ClosedAt == nil || events[0].LifetimeS != 20 || events[0].PeakNetBps != "22.95" ||
		events[0].PaperExecutionID == nil || *events[0].PaperExecutionID != "exe-1" {
		t.Fatalf("event = %+v", events)
	}
	n, err := ev.CountEvents(ctx, "r1")
	if err != nil || n != 1 {
		t.Fatalf("count = %d err=%v", n, err)
	}

	// Close reason (audit X3): stored with the close and read back; an
	// event closed through the plain path has none.
	if err := ev.InsertEvent(ctx, screener.Event{ID: "evt-2", RuleID: "r1", Kind: screener.RuleKindSpread, Base: "ETH", Quote: "USDT", OpenedAt: t0.Add(time.Minute), PeakNetBps: "15"}); err != nil {
		t.Fatal(err)
	}
	if err := ev.CloseEventWithReason(ctx, "evt-2", t0.Add(2*time.Minute), 60, "16", "HOLD_TIMEOUT"); err != nil {
		t.Fatal(err)
	}
	if err := ev.CloseEventWithReason(ctx, "nope", t0, 0, "0", "x"); err != screener.ErrNotFound {
		t.Fatalf("close missing with reason = %v", err)
	}
	events, _ = ev.ListEvents(ctx, "r1", 10)
	reasons := map[string]string{}
	for _, e := range events {
		reasons[e.ID] = e.CloseReason
	}
	if reasons["evt-2"] != "HOLD_TIMEOUT" || reasons["evt-1"] != "" {
		t.Fatalf("close reasons = %v", reasons)
	}
}
