package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/cploutarchou/arb-chain-bot/internal/ai"
	"github.com/cploutarchou/arb-chain-bot/internal/auth"
	"github.com/cploutarchou/arb-chain-bot/internal/strategy"
)

// aiRoutes expose the advisor: analyses and recommendations to viewers,
// approve/reject behind PermAIApprove + CSRF (audited). Approval runs
// through the strategy service — same validation, versioning, and audit
// as a human config edit.
func (s *Server) aiRoutes(mux *http.ServeMux) {
	gate := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if s.AI == nil {
				WriteError(w, http.StatusNotFound, "ai_absent", "AI advisor not configured in this profile", correlationID(r))
				return
			}
			next(w, r)
		}
	}
	mux.HandleFunc("GET /api/v1/ai/analyses", s.requirePerm(auth.PermViewDashboard, gate(func(w http.ResponseWriter, r *http.Request) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		WriteData(w, http.StatusOK, s.AI.Analyses(limit))
	})))
	mux.HandleFunc("GET /api/v1/ai/recommendations", s.requirePerm(auth.PermViewDashboard, gate(func(w http.ResponseWriter, r *http.Request) {
		status := r.URL.Query().Get("status")
		switch status {
		case "", "proposed", "approved", "rejected", "deferred", "expired":
		default:
			WriteError(w, http.StatusBadRequest, "bad_status", "unknown status filter", correlationID(r))
			return
		}
		WriteData(w, http.StatusOK, s.AI.Recommendations(status))
	})))
	mux.HandleFunc("POST /api/v1/ai/recommendations/{id}/approve", s.requirePerm(auth.PermAIApprove, s.requireCSRF(gate(func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		principal, _ := PrincipalFrom(r.Context())
		// The approver's own per-section RBAC applies to the change the
		// recommendation makes — an AI proposal never widens privileges.
		snap, err := s.AI.Approve(r.Context(), id, principal.UserID, "web", SectionAuthorizer(principal.Role))
		if err != nil {
			s.writeAIError(w, r, err)
			return
		}
		s.audit(r, principal.UserID, "ai_recommendation.approve", "ai_recommendation:"+id)
		WriteData(w, http.StatusOK, map[string]any{"config_version": snap.Version})
	}))))
	mux.HandleFunc("POST /api/v1/ai/recommendations/{id}/reject", s.requirePerm(auth.PermAIApprove, s.requireCSRF(gate(func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		principal, _ := PrincipalFrom(r.Context())
		if err := s.AI.Reject(r.Context(), id, principal.UserID); err != nil {
			s.writeAIError(w, r, err)
			return
		}
		s.audit(r, principal.UserID, "ai_recommendation.reject", "ai_recommendation:"+id)
		WriteData(w, http.StatusOK, map[string]any{"status": "rejected"})
	}))))
}

func (s *Server) writeAIError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ai.ErrRecommendationNotFound):
		WriteError(w, http.StatusNotFound, "recommendation_not_found", "no such recommendation", correlationID(r))
	case errors.Is(err, ai.ErrRecommendationDecided):
		WriteError(w, http.StatusConflict, "already_decided", "recommendation already decided or expired", correlationID(r))
	case errors.Is(err, strategy.ErrForbidden):
		WriteError(w, http.StatusForbidden, "forbidden", err.Error(), correlationID(r))
	default:
		s.log.Error("ai action failed", "error", err)
		WriteError(w, http.StatusInternalServerError, "ai_action_failed", "action failed", correlationID(r))
	}
}
