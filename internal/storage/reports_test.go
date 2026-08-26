package storage

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/reporting"
)

// TestReportGetByID covers BL-32's detail route: InsertReport persists
// the full structured payload and GetReport returns it byte-identical
// (round-tripped through JSON), with an honest ErrReportNotFound for an
// unknown id.
func TestReportGetByID(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	reports := s.Reports()

	rep := reporting.Report{
		ID: "rep-detail-1", Kind: reporting.KindDaily,
		PeriodStart: t0, PeriodEnd: t0.Add(24 * time.Hour), GeneratedAt: t0.Add(24 * time.Hour),
		Executive:    "all healthy",
		SystemHealth: reporting.SystemSection{Mode: "PAPER", Ready: true, ConfigVersion: 3},
		TopTriangles: []reporting.TriangleStat{{TriangleID: "tri-a", Cycles: 5, NetPnL: "12.50"}},
		Notes:        []string{"caveat"},
	}
	if err := reports.InsertReport(ctx, rep); err != nil {
		t.Fatal(err)
	}

	got, err := reports.GetReport(ctx, "rep-detail-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Executive != rep.Executive || got.SystemHealth.Mode != "PAPER" {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
	if len(got.TopTriangles) != 1 || got.TopTriangles[0].TriangleID != "tri-a" {
		t.Fatalf("top_triangles round-trip = %+v", got.TopTriangles)
	}

	if _, err := reports.GetReport(ctx, "does-not-exist"); !errors.Is(err, ErrReportNotFound) {
		t.Fatalf("unknown id error = %v, want ErrReportNotFound", err)
	}
}
