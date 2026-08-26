package app

import (
	"context"
	"errors"
	"testing"
)

// TestTriangleReaderReturnsLegsAndFees covers BL-26's live half: legs
// with market/side/from/to and the current effective fee resolve for a
// real triangle from the harness's topology; an unknown id and an
// engine with no run yet both report absence honestly.
func TestTriangleReaderReturnsLegsAndFees(t *testing.T) {
	e, _ := newTestEngine(t)
	tr := NewTriangleReader(e)

	if _, ok := tr.Triangle("anything"); ok {
		t.Fatal("must report absence before any Run has started")
	}

	if err := runOnce(t, e); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}

	scn := e.currentScanner()
	if scn == nil || len(scn.Topo.Triangles) == 0 {
		t.Fatal("harness produced no triangles to test against")
	}
	id := scn.Topo.Triangles[0].ID

	view, ok := tr.Triangle(id)
	if !ok {
		t.Fatalf("Triangle(%q) not found after Run", id)
	}
	if view.ID != id || view.StartingAsset != "USDT" {
		t.Fatalf("view = %+v", view)
	}
	if len(view.Legs) != 3 {
		t.Fatalf("legs = %d, want 3", len(view.Legs))
	}
	for _, leg := range view.Legs {
		if leg.Market == "" || leg.Side == "" || leg.From == "" || leg.To == "" {
			t.Fatalf("leg missing basic fields: %+v", leg)
		}
		if leg.FeeRate == nil || leg.FeeSource == "" {
			t.Fatalf("leg missing fee data: %+v", leg)
		}
	}

	if _, ok := tr.Triangle("does-not-exist"); ok {
		t.Fatal("unknown triangle id must report absence")
	}
}
