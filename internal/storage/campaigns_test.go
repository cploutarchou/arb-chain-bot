package storage

import (
	"context"
	"testing"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/campaign"
)

// Acceptance (BL-05b): verdicts are written on both the initial INSERT
// (queue time, no verdicts yet) and the completion UPDATE — a run row is
// always inserted before it completes (runner.go's Start persists a
// StatusQueued snapshot immediately), so if `verdicts` were only in the
// INSERT column list the completion update would silently never persist
// it. This round-trips both writes through a real UPSERT to catch that
// class of bug directly, rather than trusting the SQL text.
func TestCampaignRunVerdictsRoundTrip(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	run := campaign.Run{
		ID: "run-verdicts-1", Recording: "REC1",
		Request: campaign.Request{Recording: "REC1"},
		Status:  campaign.StatusQueued, CreatedAt: t0,
	}
	if err := s.UpsertCampaignRun(ctx, run); err != nil {
		t.Fatalf("insert (queued, no verdicts): %v", err)
	}

	fin := t0.Add(time.Minute)
	run.Status = campaign.StatusDone
	run.FinishedAt = &fin
	run.Flags = map[string][]string{"USDT": {"BASELINE UNPROFITABLE: net PnL -5 USDT."}}
	run.Verdicts = map[string][]campaign.Verdict{
		"USDT": {{Text: "BASELINE UNPROFITABLE: net PnL -5 USDT.", Severity: "bad"}},
	}
	if err := s.UpsertCampaignRun(ctx, run); err != nil {
		t.Fatalf("update (done, with verdicts): %v", err)
	}

	got, err := s.GetCampaignRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got.Verdicts["USDT"]) != 1 || got.Verdicts["USDT"][0].Severity != "bad" {
		t.Fatalf("verdicts did not round-trip through the completion UPDATE: %+v", got.Verdicts)
	}

	list, err := s.ListCampaignRuns(ctx, 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	found := false
	for _, r := range list {
		if r.ID != run.ID {
			continue
		}
		found = true
		if len(r.Verdicts["USDT"]) != 1 {
			t.Fatalf("list view dropped verdicts: %+v", r.Verdicts)
		}
	}
	if !found {
		t.Fatal("run missing from list")
	}
}
