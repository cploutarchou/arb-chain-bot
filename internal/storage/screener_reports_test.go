package storage

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/screener"
	"github.com/cploutarchou/arb-chain-bot/internal/screener/report"
)

func TestScreenerReportsRoundTrip(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	st := s.ScreenerReports()

	p := report.Payload{Window: report.Window{Label: report.PeriodDay, Start: t0, End: t0.Add(24 * time.Hour)},
		Strategy: screener.StrategyCrossVenueSpot, GeneratedAt: t0.Add(24 * time.Hour), Model: report.Model, Notes: []string{"n"}}
	p.Stats.N = 3
	p.Stats.NetPnLQuote = d("12.5")
	p.Stats.Skipped = map[string]int64{"DEPTH": 2}
	p.Gate = report.Checklist(screener.StrategyCrossVenueSpot, p.Stats)
	p.GateTotal = len(p.Gate)
	r := report.Report{ID: "rep-1", PeriodStart: p.Window.Start, PeriodEnd: p.Window.End, Strategy: screener.StrategyCrossVenueSpot,
		RuleID: "", Payload: p, Markdown: report.Markdown(p), CreatedAt: t0.Add(25 * time.Hour)}
	if err := st.InsertReport(ctx, r); err != nil {
		t.Fatal(err)
	}
	r2 := r
	r2.ID, r2.RuleID, r2.CreatedAt = "rep-2", "rule-x", t0.Add(26*time.Hour)
	if err := st.InsertReport(ctx, r2); err != nil {
		t.Fatal(err)
	}
	list, err := st.ListReports(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].ID != "rep-2" || list[1].N != 3 || !list[1].NetPnLQuote.Equal(d("12.5")) || list[1].GateTotal != 8 {
		t.Fatalf("list = %+v", list)
	}
	got, err := st.GetReport(ctx, "rep-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Markdown != r.Markdown || got.Payload.Stats.Skipped["DEPTH"] != 2 || len(got.Payload.Gate) != 8 || got.Strategy != screener.StrategyCrossVenueSpot {
		t.Fatalf("get = %+v", got)
	}
	if _, err := st.GetReport(ctx, "nope"); !errors.Is(err, screener.ErrNotFound) {
		t.Fatalf("missing report err = %v", err)
	}
}
