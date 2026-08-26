package api

import (
	"net/http"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/auth"
	"github.com/cploutarchou/arb-chain-bot/internal/quality"
	"github.com/cploutarchou/arb-chain-bot/internal/storage"
)

// TriangleReader is the live-engine half of the triangle detail route
// (BL-26): legs, current book top, a reference-size VWAP/depth preview,
// and the fee schedule. It is a separate field from ReadModel (like
// RestartController/RecorderController/CampaignService) rather than a
// new ReadModel method — growing ReadModel would force every fake in
// this package's tests to implement an unrelated method.
type TriangleReader interface {
	Triangle(id string) (TriangleView, bool)
}

// TriangleView is one triangle's live shape (BL-26).
type TriangleView struct {
	ID            string            `json:"id"`
	Exchange      string            `json:"exchange"`
	StartingAsset string            `json:"starting_asset"`
	Legs          []TriangleLegView `json:"legs"`
}

// TriangleLegView is one leg's book top, a reference-size VWAP/depth
// preview, and the current effective fee — all live-engine state, so
// this is empty/absent whenever no engine is running in this profile.
type TriangleLegView struct {
	LegNo  int    `json:"leg_no"`
	Market string `json:"market"`
	Side   string `json:"side"`
	From   string `json:"from"`
	To     string `json:"to"`

	BookState string  `json:"book_state,omitempty"`
	BookAgeMs *int64  `json:"book_age_ms,omitempty"`
	TopBid    *string `json:"top_bid,omitempty"`
	TopAsk    *string `json:"top_ask,omitempty"`

	// VWAPPrice/PriceImpactBps/LevelsConsumed/DepthExhausted preview one
	// leg's execution at ReferenceSize (see TriangleView's doc) — NOT a
	// live opportunity's actual sizing, purely a depth/liquidity preview
	// computed by calling pricing.QuoteLeg (never re-derived here).
	VWAPPrice      *string `json:"vwap_price,omitempty"`
	PriceImpactBps *string `json:"price_impact_bps,omitempty"`
	LevelsConsumed int     `json:"levels_consumed,omitempty"`
	DepthExhausted bool    `json:"depth_exhausted,omitempty"`

	FeeRate   *string `json:"fee_rate,omitempty"`
	FeeSource string  `json:"fee_source,omitempty"`
}

// triangleRoutes serve BL-26: triangle detail (graph position, per-leg
// book/VWAP/fee, recent cycles, quality score). PermViewDashboard
// matches the existing /api/v1/triangles/quality route's permission.
func (s *Server) triangleRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/triangles/{id}", s.requirePerm(auth.PermViewDashboard, s.handleTriangleDetail))
}

// TriangleDetailResponse is GET /api/v1/triangles/{id}'s payload. Live
// (Triangle) and historical (RecentCycles/Quality) sections are each
// omitted, not faked, when their source component is absent — the same
// convention system/health uses.
type TriangleDetailResponse struct {
	Triangle     *TriangleView      `json:"triangle,omitempty"`
	RecentCycles []storage.CycleRow `json:"recent_cycles,omitempty"`
	Quality      *quality.Breakdown `json:"quality,omitempty"`
	Notes        []string           `json:"notes,omitempty"`
}

func (s *Server) handleTriangleDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var resp TriangleDetailResponse
	found := false

	if s.Triangles != nil {
		if tv, ok := s.Triangles.Triangle(id); ok {
			resp.Triangle = &tv
			found = true
		}
	} else {
		resp.Notes = append(resp.Notes, "live leg/book/fee data unavailable: no engine running in this profile")
	}

	if s.Store != nil {
		rows, err := s.Store.ListCyclesByTriangle(r.Context(), id, 20)
		if err != nil {
			s.log.Error("triangle recent cycles failed", "id", id, "error", err)
			WriteError(w, http.StatusInternalServerError, "query_failed", "triangle history fetch failed", correlationID(r))
			return
		}
		if len(rows) > 0 {
			resp.RecentCycles = rows
			found = true
		}

		to := time.Now().UTC()
		samples, err := s.Store.QualitySamples(r.Context(), to.Add(-30*24*time.Hour), to)
		if err != nil {
			s.log.Error("triangle quality failed", "id", id, "error", err)
			WriteError(w, http.StatusInternalServerError, "query_failed", "triangle quality fetch failed", correlationID(r))
			return
		}
		for _, sm := range samples {
			if sm.TriangleID != id {
				continue
			}
			b := quality.Score(sm, quality.Config{})
			resp.Quality = &b
			found = true
			break
		}
	}

	if !found {
		WriteError(w, http.StatusNotFound, "not_found", "triangle not found", correlationID(r))
		return
	}
	WriteData(w, http.StatusOK, resp)
}
