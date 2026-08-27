package api

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/auth"
	"github.com/cploutarchou/arb-chain-bot/internal/storage"
)

// windowHours parses ?hours=, clamping to [1, 720] (30 days) with a
// 24h default — the same convention /api/v1/triangles/quality already
// uses.
func windowHours(r *http.Request) int {
	hours, _ := strconv.Atoi(r.URL.Query().Get("hours"))
	if hours < 1 || hours > 24*30 {
		hours = 24
	}
	return hours
}

// pnlRoutes serve BL-19's PnL & analytics group: breakdowns by
// dimension, a cumulative P&L/drawdown series, and edge/slippage/
// latency distributions. All are store-backed history (PermViewPortfolio,
// matching the existing /api/v1/pnl live route).
func (s *Server) pnlRoutes(mux *http.ServeMux) {
	needStore := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if s.Store == nil {
				WriteError(w, http.StatusNotFound, "storage_absent", "persistence disabled (ARB_DATABASE_URL unset)", correlationID(r))
				return
			}
			next(w, r)
		}
	}
	mux.HandleFunc("GET /api/v1/pnl/breakdown", s.requirePerm(auth.PermViewPortfolio, needStore(func(w http.ResponseWriter, r *http.Request) {
		hours := windowHours(r)
		to := time.Now().UTC()
		by := r.URL.Query().Get("by")
		res, err := s.Store.PnLBreakdown(r.Context(), by, to.Add(-time.Duration(hours)*time.Hour), to)
		if err != nil {
			if errors.Is(err, storage.ErrBadBreakdown) {
				WriteError(w, http.StatusBadRequest, "bad_by", err.Error(), correlationID(r))
				return
			}
			s.log.Error("pnl breakdown failed", "by", by, "error", err)
			WriteError(w, http.StatusInternalServerError, "query_failed", "breakdown query failed", correlationID(r))
			return
		}
		res.WindowHours = hours
		WriteData(w, http.StatusOK, res)
	})))
	mux.HandleFunc("GET /api/v1/pnl/series", s.requirePerm(auth.PermViewPortfolio, needStore(func(w http.ResponseWriter, r *http.Request) {
		hours := windowHours(r)
		to := time.Now().UTC()
		res, err := s.Store.PnLSeries(r.Context(), to.Add(-time.Duration(hours)*time.Hour), to)
		if err != nil {
			s.log.Error("pnl series failed", "error", err)
			WriteError(w, http.StatusInternalServerError, "query_failed", "series query failed", correlationID(r))
			return
		}
		res.WindowHours = hours
		WriteData(w, http.StatusOK, res)
	})))
	mux.HandleFunc("GET /api/v1/analytics/distributions", s.requirePerm(auth.PermViewPortfolio, needStore(func(w http.ResponseWriter, r *http.Request) {
		hours := windowHours(r)
		to := time.Now().UTC()
		res, err := s.Store.Distributions(r.Context(), to.Add(-time.Duration(hours)*time.Hour), to)
		if err != nil {
			s.log.Error("analytics distributions failed", "error", err)
			WriteError(w, http.StatusInternalServerError, "query_failed", "distribution query failed", correlationID(r))
			return
		}
		res.WindowHours = hours
		WriteData(w, http.StatusOK, res)
	})))
}
