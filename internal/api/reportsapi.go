package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/cploutarchou/arb-chain-bot/internal/auth"
	"github.com/cploutarchou/arb-chain-bot/internal/reporting"
)

// reportRoutes list persisted reports and generate on demand.
func (s *Server) reportRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/reports", s.requirePerm(auth.PermReportView, func(w http.ResponseWriter, r *http.Request) {
		if s.Store == nil {
			WriteError(w, http.StatusNotFound, "storage_absent", "persistence disabled (ARB_DATABASE_URL unset)", correlationID(r))
			return
		}
		kind := r.URL.Query().Get("kind")
		switch kind {
		case "", "daily", "weekly":
		default:
			WriteError(w, http.StatusBadRequest, "bad_kind", "kind must be daily|weekly", correlationID(r))
			return
		}
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		rows, err := s.Store.Reports().ListReports(r.Context(), kind, limit)
		s.writeListResult(w, r, "reports", rows, err)
	}))
	// Generation runs real aggregate queries — held to OPERATOR+ via its
	// own permission rather than the viewer-held reports:view (audit S-007).
	mux.HandleFunc("POST /api/v1/reports/generate", s.requirePerm(auth.PermReportGenerate, s.requireCSRF(func(w http.ResponseWriter, r *http.Request) {
		if s.Reports == nil {
			WriteError(w, http.StatusNotFound, "reporting_absent", "reporting not running in this profile", correlationID(r))
			return
		}
		var body struct {
			Kind string `json:"kind"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body); err != nil {
			WriteError(w, http.StatusBadRequest, "bad_payload", "body must be {\"kind\": \"daily\"|\"weekly\"}", correlationID(r))
			return
		}
		kind := reporting.KindDaily
		switch body.Kind {
		case "daily":
		case "weekly":
			kind = reporting.KindWeekly
		default:
			WriteError(w, http.StatusBadRequest, "bad_kind", "kind must be daily|weekly", correlationID(r))
			return
		}
		rep, err := s.Reports.Generate(r.Context(), kind)
		if err != nil {
			s.log.Error("on-demand report failed", "error", err)
			WriteError(w, http.StatusInternalServerError, "report_failed", "report generation failed", correlationID(r))
			return
		}
		principal, _ := PrincipalFrom(r.Context())
		s.audit(r, principal.UserID, "report.generate", "report:"+rep.ID)
		WriteData(w, http.StatusOK, rep)
	})))
}
