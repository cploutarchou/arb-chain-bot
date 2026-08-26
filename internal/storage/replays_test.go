package storage

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/replay"
)

func TestReplayRunUpsertGetListRoundTrip(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	started := t0.Add(time.Second)
	queued := replay.Run{
		ID: "rp-1", Recording: "REC1",
		Request:   replay.Request{Recording: "REC1", ConfigVersion: 3, Speed: 2.5},
		Status:    replay.StatusQueued,
		Total:     1,
		CreatedAt: t0, Actor: "u1",
	}
	if err := s.UpsertReplayRun(ctx, queued); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetReplayRun(ctx, "rp-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != replay.StatusQueued || got.Request.ConfigVersion != 3 || got.Request.Speed != 2.5 || got.Request.Recording != "REC1" {
		t.Fatalf("queued round trip = %+v", got)
	}

	done := queued
	done.Status = replay.StatusDone
	done.Done = 1
	done.StartedAt = &started
	fin := started.Add(time.Minute)
	done.FinishedAt = &fin
	done.Evaluations = 42
	done.Qualified = 7
	done.Cycles = 5
	done.Top = []replay.TopOpportunity{
		{OpportunityID: "op-1", TriangleID: "tri-1", Outcome: "ALL_FILLED", NetBps: d("12.5"), At: started},
	}
	if err := s.UpsertReplayRun(ctx, done); err != nil {
		t.Fatal(err)
	}

	got, err = s.GetReplayRun(ctx, "rp-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != replay.StatusDone || got.Evaluations != 42 || got.Qualified != 7 || got.Cycles != 5 {
		t.Fatalf("done round trip = %+v", got)
	}
	if len(got.Top) != 1 || got.Top[0].OpportunityID != "op-1" || !got.Top[0].NetBps.Equal(d("12.5")) {
		t.Fatalf("top round trip = %+v", got.Top)
	}
	if got.StartedAt == nil || !got.StartedAt.Equal(started) || got.FinishedAt == nil || !got.FinishedAt.Equal(fin) {
		t.Fatalf("timestamps = %+v", got)
	}

	// A second, zero-value request row: ConfigVersion/Speed both absent
	// (NULL) round-trip as zero, not an error.
	bare := replay.Run{ID: "rp-2", Recording: "REC2", Request: replay.Request{Recording: "REC2"}, Status: replay.StatusQueued, Total: 1, CreatedAt: t0.Add(time.Minute)}
	if err := s.UpsertReplayRun(ctx, bare); err != nil {
		t.Fatal(err)
	}
	got2, err := s.GetReplayRun(ctx, "rp-2")
	if err != nil {
		t.Fatal(err)
	}
	if got2.Request.ConfigVersion != 0 || got2.Request.Speed != 0 {
		t.Fatalf("bare request = %+v", got2.Request)
	}

	list, err := s.ListReplayRuns(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].ID != "rp-2" || list[1].ID != "rp-1" {
		t.Fatalf("list newest-first = %+v", list)
	}

	if _, err := s.GetReplayRun(ctx, "nope"); !errors.Is(err, replay.ErrNotFound) {
		t.Fatalf("unknown id = %v", err)
	}
}
