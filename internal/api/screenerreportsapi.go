package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/screener/report"
)

func nextReportRun() time.Time { return report.NextRun(time.Now().UTC()) }

func (s *Server) reportsGate(w http.ResponseWriter, r *http.Request) bool {
	if s.ScreenerReports == nil || s.ScreenerReports.Store == nil {
		WriteError(w, http.StatusServiceUnavailable, "reports_unavailable", "screener reports are not generated in this profile", correlationID(r))
		return false
	}
	return true
}

func (s *Server) handleScreenerReportsList(w http.ResponseWriter, r *http.Request) {
	if !s.reportsGate(w, r) {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	list, err := s.ScreenerReports.Store.ListReports(r.Context(), limit)
	if err != nil {
		s.log.Error("screener reports list failed", "error", err)
		WriteError(w, http.StatusInternalServerError, "reports_list_failed", "listing reports failed", correlationID(r))
		return
	}
	WriteData(w, http.StatusOK, map[string]any{
		"reports":      list,
		"last_run":     s.ScreenerReports.LastRun(),
		"next_run_utc": nextReportRun(),
		"generated_at": time.Now().UTC(),
	})
}

func (s *Server) handleScreenerReportGet(w http.ResponseWriter, r *http.Request) {
	if !s.reportsGate(w, r) {
		return
	}
	rep, err := s.ScreenerReports.Store.GetReport(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeScreenerError(w, r, err)
		return
	}
	WriteData(w, http.StatusOK, map[string]any{"report": rep})
}

// handleScreenerReportsRun generates the reports for the previous UTC
// day (and cumulative) now. ADMIN + CSRF (route wiring) + audit.
func (s *Server) handleScreenerReportsRun(w http.ResponseWriter, r *http.Request) {
	if !s.reportsGate(w, r) {
		return
	}
	principal, _ := PrincipalFrom(r.Context())
	res, err := s.ScreenerReports.Run(r.Context(), time.Now().UTC())
	if err != nil {
		s.log.Error("screener reports run failed", "error", err, "actor", principal.UserID)
		WriteError(w, http.StatusInternalServerError, "reports_run_failed", "generating reports failed", correlationID(r))
		return
	}
	s.auditWith(r, principal.UserID, "screener.reports.run", "screener_reports:"+res.Day, map[string]any{"day": res.Day, "reports": len(res.Reports), "errors": len(res.Errors)})
	WriteData(w, http.StatusOK, map[string]any{"run": res})
}
