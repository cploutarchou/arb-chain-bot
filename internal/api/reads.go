package api

import (
	"context"
	"net/http"
	"strconv"

	"github.com/cploutarchou/arb-chain-bot/internal/auth"
)

// ReadModel supplies the live read-side groups (engine-backed). Absent
// members answer honest 404s.
type ReadModel interface {
	RecentOpportunities(limit int) any
	Portfolio() (any, bool)
	PnL() (any, bool)
	Risk() any
	Health() any
}

// readRoutes finish the T-024 route groups: opportunities, paper
// history, portfolio, pnl, risk, audit. Live views come from the
// engine; history comes from PostgreSQL when persistence is enabled.
func (s *Server) readRoutes(mux *http.ServeMux) {
	limitOf := func(r *http.Request) int {
		n, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		return n
	}
	needEngine := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if s.Reads == nil {
				WriteError(w, http.StatusNotFound, "engine_absent", "engine not running in this profile", correlationID(r))
				return
			}
			next(w, r)
		}
	}
	needStore := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if s.Store == nil {
				WriteError(w, http.StatusNotFound, "storage_absent", "persistence disabled (ARB_DATABASE_URL unset)", correlationID(r))
				return
			}
			next(w, r)
		}
	}

	mux.HandleFunc("GET /api/v1/opportunities", s.requirePerm(auth.PermViewOpportunity, needEngine(func(w http.ResponseWriter, r *http.Request) {
		WriteData(w, http.StatusOK, map[string]any{
			"source":        "memory",
			"opportunities": s.Reads.RecentOpportunities(limitOf(r)),
		})
	})))
	mux.HandleFunc("GET /api/v1/opportunities/history", s.requirePerm(auth.PermViewOpportunity, needStore(func(w http.ResponseWriter, r *http.Request) {
		rows, err := s.Store.ListOpportunities(r.Context(), r.URL.Query().Get("status"), limitOf(r))
		s.writeListResult(w, r, "opportunities", rows, err)
	})))
	mux.HandleFunc("GET /api/v1/paper/cycles", s.requirePerm(auth.PermViewPortfolio, needStore(func(w http.ResponseWriter, r *http.Request) {
		rows, err := s.Store.ListCycles(r.Context(), r.URL.Query().Get("session_id"), limitOf(r))
		s.writeListResult(w, r, "cycles", rows, err)
	})))
	mux.HandleFunc("GET /api/v1/paper/cycles/{id}/orders", s.requirePerm(auth.PermViewPortfolio, needStore(func(w http.ResponseWriter, r *http.Request) {
		rows, err := s.Store.ListOrders(r.Context(), r.PathValue("id"))
		s.writeListResult(w, r, "orders", rows, err)
	})))
	mux.HandleFunc("GET /api/v1/portfolio", s.requirePerm(auth.PermViewPortfolio, needEngine(func(w http.ResponseWriter, r *http.Request) {
		data, ok := s.Reads.Portfolio()
		if !ok {
			WriteError(w, http.StatusNotFound, "portfolio_absent", "portfolio not initialized yet", correlationID(r))
			return
		}
		WriteData(w, http.StatusOK, data)
	})))
	mux.HandleFunc("GET /api/v1/pnl", s.requirePerm(auth.PermViewPortfolio, needEngine(func(w http.ResponseWriter, r *http.Request) {
		data, ok := s.Reads.PnL()
		if !ok {
			WriteError(w, http.StatusNotFound, "pnl_absent", "portfolio not initialized yet", correlationID(r))
			return
		}
		WriteData(w, http.StatusOK, data)
	})))
	mux.HandleFunc("GET /api/v1/risk", s.requirePerm(auth.PermViewRisk, needEngine(func(w http.ResponseWriter, r *http.Request) {
		WriteData(w, http.StatusOK, s.Reads.Risk())
	})))
	mux.HandleFunc("GET /api/v1/system/health", s.requirePerm(auth.PermViewSystem, needEngine(func(w http.ResponseWriter, r *http.Request) {
		WriteData(w, http.StatusOK, s.Reads.Health())
	})))
	mux.HandleFunc("GET /api/v1/audit", s.requirePerm(auth.PermViewAudit, needStore(func(w http.ResponseWriter, r *http.Request) {
		rows, err := s.Store.ListAuditEvents(r.Context(), r.URL.Query().Get("entity"), limitOf(r))
		s.writeListResult(w, r, "events", rows, err)
	})))
}

func (s *Server) writeListResult(w http.ResponseWriter, r *http.Request, key string, rows any, err error) {
	if err != nil {
		if r.Context().Err() != nil {
			return // client went away; nothing to write
		}
		s.log.Error("list query failed", "path", r.URL.Path, "error", err)
		WriteError(w, http.StatusInternalServerError, "query_failed", "listing failed", correlationID(r))
		return
	}
	WriteData(w, http.StatusOK, map[string]any{key: rows})
}

// Healthy delegates readiness checks (nil store = always ready).
func (s *Server) storeHealthy(ctx context.Context) bool {
	if s.Store == nil {
		return true
	}
	return s.Store.Healthy(ctx)
}
