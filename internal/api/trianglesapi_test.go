package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

type fakeTriangleReader struct {
	view TriangleView
	ok   bool
}

func (f fakeTriangleReader) Triangle(id string) (TriangleView, bool) {
	if !f.ok || f.view.ID != id {
		return TriangleView{}, false
	}
	return f.view, true
}

func TestTriangleDetailRoute(t *testing.T) {
	s, mux := newTestServer(t)
	cookie, _ := login(t, mux, "viewer@example.test", "viewer-pw")

	// Neither engine nor store: honest 404.
	rec := getWith(t, mux, cookie, "/api/v1/triangles/tri-a")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("absent everything = %d: %s", rec.Code, rec.Body.String())
	}

	// Engine present: live legs/fees resolve.
	s.Triangles = fakeTriangleReader{ok: true, view: TriangleView{
		ID: "tri-a", Exchange: "binance", StartingAsset: "USDT",
		Legs: []TriangleLegView{{LegNo: 1, Market: "binance:BTCUSDT", Side: "BUY", From: "USDT", To: "BTC"}},
	}}
	rec = getWith(t, mux, cookie, "/api/v1/triangles/tri-a")
	if rec.Code != http.StatusOK {
		t.Fatalf("with triangle reader = %d: %s", rec.Code, rec.Body.String())
	}
	var env struct {
		Data TriangleDetailResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Data.Triangle == nil || env.Data.Triangle.ID != "tri-a" || len(env.Data.Triangle.Legs) != 1 {
		t.Fatalf("triangle = %+v", env.Data.Triangle)
	}

	// Unknown id still 404s even with a reader present.
	rec = getWith(t, mux, cookie, "/api/v1/triangles/unknown")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown id = %d: %s", rec.Code, rec.Body.String())
	}
}

// TestTriangleRoutePrecedenceOverQuality confirms Go's ServeMux resolves
// the literal "/api/v1/triangles/quality" over the wildcard
// "/api/v1/triangles/{id}" registered later — a regression here would
// route "quality" through the {id} handler and 404 with "not_found"
// instead of reaching the quality handler's own (different) absence
// response.
func TestTriangleRoutePrecedenceOverQuality(t *testing.T) {
	_, mux := newTestServer(t)
	cookie, _ := login(t, mux, "viewer@example.test", "viewer-pw")

	rec := getWith(t, mux, cookie, "/api/v1/triangles/quality")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("quality without store = %d: %s", rec.Code, rec.Body.String())
	}
	var env struct {
		Error *APIError `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Error == nil || env.Error.Code != "storage_absent" {
		t.Fatalf("error = %+v, want storage_absent (the quality handler's own code, proving it wasn't routed through {id})", env.Error)
	}
}
