package screener

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
)

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestServiceLoadSeedsDefaults(t *testing.T) {
	svc := NewService(NewBook(), NewMemoryStore(), discardLogger(), nil)
	snap, err := svc.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snap.Version != 1 {
		t.Fatalf("version = %d, want 1", snap.Version)
	}
	if snap.CreatedBy != "system" {
		t.Fatalf("created_by = %q, want system", snap.CreatedBy)
	}
	if len(snap.Settings.Venues) != len(KnownVenues) {
		t.Fatalf("seeded venues = %d, want %d", len(snap.Settings.Venues), len(KnownVenues))
	}
}

func TestServiceApplyExpectStaleVersion(t *testing.T) {
	svc := NewService(NewBook(), NewMemoryStore(), discardLogger(), nil)
	ctx := context.Background()
	if _, err := svc.Load(ctx); err != nil {
		t.Fatal(err)
	}

	next := svc.Current().Settings.Clone()
	v := next.Venues[VenueBinance]
	v.SpotTakerBps = d("7")
	next.Venues[VenueBinance] = v

	if _, err := svc.ApplyExpect(ctx, "admin", "web", next, 1); err != nil {
		t.Fatalf("apply with correct parent_version failed: %v", err)
	}
	if got := svc.Current().Version; got != 2 {
		t.Fatalf("version = %d, want 2", got)
	}

	// Stale parent_version (still 1) is refused.
	_, err := svc.ApplyExpect(ctx, "admin", "web", next, 1)
	var stale *StaleVersionError
	if !errors.As(err, &stale) {
		t.Fatalf("err = %v, want *StaleVersionError", err)
	}
	if stale.Current != 2 {
		t.Fatalf("stale.Current = %d, want 2", stale.Current)
	}
	if got := svc.Current().Version; got != 2 {
		t.Fatalf("version must not advance on a stale write, got %d", got)
	}
}

func TestServiceApplyExpectNoChange(t *testing.T) {
	svc := NewService(NewBook(), NewMemoryStore(), discardLogger(), nil)
	ctx := context.Background()
	if _, err := svc.Load(ctx); err != nil {
		t.Fatal(err)
	}
	same := svc.Current().Settings.Clone()
	if _, err := svc.ApplyExpect(ctx, "admin", "web", same, 1); !errors.Is(err, ErrNoChange) {
		t.Fatalf("err = %v, want ErrNoChange", err)
	}
}

func TestServiceApplyExpectInvalidRejected(t *testing.T) {
	svc := NewService(NewBook(), NewMemoryStore(), discardLogger(), nil)
	ctx := context.Background()
	if _, err := svc.Load(ctx); err != nil {
		t.Fatal(err)
	}
	bad := svc.Current().Settings.Clone()
	bad.PollIntervalS = 999
	if _, err := svc.ApplyExpect(ctx, "admin", "web", bad, 1); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
	if got := svc.Current().Version; got != 1 {
		t.Fatalf("version must not advance on an invalid write, got %d", got)
	}
}
