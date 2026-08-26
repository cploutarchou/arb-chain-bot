package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/cploutarchou/arb-chain-bot/internal/auth"
)

// paperResetRoute: POST /api/v1/paper/reset (BL-10). ADMIN-only
// (PermPaperReset) and CSRF-protected like every other paper control
// route; additionally requires an explicit type-to-confirm body — SKILL
// §37: "reset ONLY with strong confirmation" — so a stray POST (e.g. a
// replayed request, a misconfigured script) can never wipe the session.
func (s *Server) paperResetRoute(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/paper/reset", s.requirePerm(auth.PermPaperReset, s.requireCSRF(func(w http.ResponseWriter, r *http.Request) {
		if s.Paper == nil {
			WriteError(w, http.StatusNotFound, "paper_absent", "paper engine not running (mode is not PAPER)", correlationID(r))
			return
		}
		var req struct {
			Confirm string `json:"confirm"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256)).Decode(&req); err != nil || req.Confirm != "RESET" {
			WriteError(w, http.StatusBadRequest, "confirmation_required", `body must be {"confirm":"RESET"}`, correlationID(r))
			return
		}
		p, _ := PrincipalFrom(r.Context())
		if err := s.Paper.Reset(r.Context()); err != nil {
			if errors.Is(err, ErrPaperNotIdle) {
				WriteError(w, http.StatusConflict, "paper_running", err.Error(), correlationID(r))
				return
			}
			s.log.Error("paper reset failed", "error", err)
			WriteError(w, http.StatusInternalServerError, "paper_reset_failed", "paper reset failed", correlationID(r))
			return
		}
		s.audit(r, p.UserID, "paper.reset", "paper_engine")
		s.log.Info("paper engine reset", "actor", p.UserID)
		WriteData(w, http.StatusOK, map[string]any{"running": s.Paper.Running()})
	})))
}
