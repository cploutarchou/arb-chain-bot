package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/auth"
	"github.com/cploutarchou/arb-chain-bot/internal/quality"
	"github.com/cploutarchou/arb-chain-bot/internal/storage"
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
func limitParam(r *http.Request) int {
	n, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	return n
}

func (s *Server) readRoutes(mux *http.ServeMux) {
	limitOf := limitParam
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
	// BL-27: why detected/qualified/rejected, book versions used, and the
	// simulation result when one was recorded.
	mux.HandleFunc("GET /api/v1/opportunities/{id}", s.requirePerm(auth.PermViewOpportunity, needStore(func(w http.ResponseWriter, r *http.Request) {
		detail, err := s.Store.GetOpportunity(r.Context(), r.PathValue("id"))
		switch {
		case errors.Is(err, storage.ErrOpportunityNotFound):
			WriteError(w, http.StatusNotFound, "not_found", "opportunity not found", correlationID(r))
		case err != nil:
			s.log.Error("opportunity detail failed", "error", err)
			WriteError(w, http.StatusInternalServerError, "query_failed", "opportunity fetch failed", correlationID(r))
		default:
			WriteData(w, http.StatusOK, detail)
		}
	})))
	mux.HandleFunc("GET /api/v1/paper/cycles", s.requirePerm(auth.PermViewPortfolio, needStore(func(w http.ResponseWriter, r *http.Request) {
		rows, err := s.Store.ListCycles(r.Context(), r.URL.Query().Get("session_id"), limitOf(r))
		s.writeListResult(w, r, "cycles", rows, err)
	})))
	mux.HandleFunc("GET /api/v1/paper/cycles/{id}/orders", s.requirePerm(auth.PermViewPortfolio, needStore(func(w http.ResponseWriter, r *http.Request) {
		rows, err := s.Store.ListOrders(r.Context(), r.PathValue("id"))
		s.writeListResult(w, r, "orders", rows, err)
	})))
	// BL-20: global, filterable, cursor-paginated Orders and Fills pages
	// (cross-linked fill→order→cycle→triangle→opportunity per SKILL
	// §38-39); unlike /paper/cycles/{id}/orders these are not scoped to
	// one cycle.
	mux.HandleFunc("GET /api/v1/orders", s.requirePerm(auth.PermViewPortfolio, needStore(func(w http.ResponseWriter, r *http.Request) {
		f, err := parseListFilter(r)
		if err != nil {
			WriteError(w, http.StatusBadRequest, "bad_filter", err.Error(), correlationID(r))
			return
		}
		page, err := s.Store.ListOrdersGlobal(r.Context(), f)
		if err != nil {
			// review P3(b): a malformed cursor is the CALLER's fault (a
			// stale/tampered next_cursor), not a server fault — 400, not
			// the generic 500 every other storage error gets.
			if errors.Is(err, storage.ErrInvalidCursor) {
				WriteError(w, http.StatusBadRequest, "invalid_cursor", "cursor is malformed or expired", correlationID(r))
				return
			}
			s.writeListResult(w, r, "orders", nil, err)
			return
		}
		WriteData(w, http.StatusOK, page)
	})))
	mux.HandleFunc("GET /api/v1/fills", s.requirePerm(auth.PermViewPortfolio, needStore(func(w http.ResponseWriter, r *http.Request) {
		f, err := parseListFilter(r)
		if err != nil {
			WriteError(w, http.StatusBadRequest, "bad_filter", err.Error(), correlationID(r))
			return
		}
		page, err := s.Store.ListFillsGlobal(r.Context(), f)
		if err != nil {
			if errors.Is(err, storage.ErrInvalidCursor) {
				WriteError(w, http.StatusBadRequest, "invalid_cursor", "cursor is malformed or expired", correlationID(r))
				return
			}
			s.writeListResult(w, r, "fills", nil, err)
			return
		}
		WriteData(w, http.StatusOK, page)
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
	// BL-31: persisted risk-event timeline (breaker transitions + risk
	// rejections), unlike /api/v1/risk's in-memory reject_reason_counts
	// which resets on restart.
	mux.HandleFunc("GET /api/v1/risk/events", s.requirePerm(auth.PermViewRisk, needStore(func(w http.ResponseWriter, r *http.Request) {
		hours, _ := strconv.Atoi(r.URL.Query().Get("hours"))
		if hours < 1 || hours > 24*30 {
			hours = 24
		}
		to := time.Now().UTC()
		rows, err := s.Store.ListRiskEvents(r.Context(), to.Add(-time.Duration(hours)*time.Hour), to, limitOf(r))
		if err != nil {
			s.writeListResult(w, r, "events", nil, err)
			return
		}
		WriteData(w, http.StatusOK, map[string]any{"window_hours": hours, "events": rows, "n": len(rows)})
	})))
	// BL-18: full health payload. Unlike the other read groups this does
	// NOT gate on needEngine — process stats, DB pool stats, queue
	// depths, and supervisor state are all available in the API profile
	// with no engine at all, and hiding them behind engine_absent would
	// make the API-profile console blind to exactly the things it most
	// needs when the engine itself is what's down.
	mux.HandleFunc("GET /api/v1/system/health", s.requirePerm(auth.PermViewSystem, s.handleSystemHealth))
	mux.HandleFunc("GET /api/v1/audit", s.requirePerm(auth.PermViewAudit, needStore(func(w http.ResponseWriter, r *http.Request) {
		rows, err := s.Store.ListAuditEvents(r.Context(), r.URL.Query().Get("entity"), limitOf(r))
		s.writeListResult(w, r, "events", rows, err)
	})))
	// Recordings: persisted sessions plus the live recorder state. Without
	// persistence the list is empty but the recorder is still reported,
	// so the console can show what is (not) being captured.
	mux.HandleFunc("GET /api/v1/recordings", s.requirePerm(auth.PermViewSystem, func(w http.ResponseWriter, r *http.Request) {
		out := map[string]any{"recordings": []storage.RecordingRow{}, "persistence": s.Store != nil}
		if s.Store != nil {
			rows, err := s.Store.ListRecordings(r.Context(), limitOf(r))
			if err != nil {
				s.writeListResult(w, r, "recordings", nil, err)
				return
			}
			if rows != nil {
				out["recordings"] = rows
			}
		}
		if s.Recorder != nil {
			out["recorder"] = s.Recorder.Status()
		}
		WriteData(w, http.StatusOK, out)
	}))
	mux.HandleFunc("GET /api/v1/triangles/quality", s.requirePerm(auth.PermViewDashboard, needStore(func(w http.ResponseWriter, r *http.Request) {
		hours, _ := strconv.Atoi(r.URL.Query().Get("hours"))
		if hours < 1 || hours > 24*30 {
			hours = 24
		}
		to := time.Now().UTC()
		samples, err := s.Store.QualitySamples(r.Context(), to.Add(-time.Duration(hours)*time.Hour), to)
		if err != nil {
			s.writeListResult(w, r, "scores", nil, err)
			return
		}
		cfg := quality.Config{}
		if s.Strategy != nil {
			// The configured minimum expected profit is the natural
			// per-cycle reference for full profitability credit.
			if ref := s.Strategy.Current().Params.Risk.MinExpectedProfit; ref.IsPositive() {
				cfg.ReferenceProfitPerCycle = ref
			}
		}
		WriteData(w, http.StatusOK, map[string]any{
			"window_hours": hours,
			"scores":       quality.Rank(samples, cfg),
			"notes": []string{
				"drawdown component reflects portfolio-level tracking; per-triangle drawdown is not recorded yet",
			},
		})
	})))
}

// parseListFilter reads the Orders/Fills/analytics query params shared
// by BL-19/BL-20: symbol, triangle, cycle, status, from/to (RFC3339),
// limit, cursor.
func parseListFilter(r *http.Request) (storage.ListFilter, error) {
	q := r.URL.Query()
	f := storage.ListFilter{
		Symbol: q.Get("symbol"), Triangle: q.Get("triangle"),
		Cycle: q.Get("cycle"), Status: q.Get("status"),
		Limit: limitParam(r), Cursor: q.Get("cursor"),
	}
	if v := q.Get("from"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return f, fmt.Errorf("bad from: %w", err)
		}
		f.From = t
	}
	if v := q.Get("to"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return f, fmt.Errorf("bad to: %w", err)
		}
		f.To = t
	}
	return f, nil
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
