package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/cploutarchou/arb-chain-bot/internal/auth"
	"github.com/cploutarchou/arb-chain-bot/internal/notification"
)

// alertRoutes expose the alert center: list for anyone with dashboard
// view, ack/resolve behind PermAlertAck + CSRF, audited by the caller-
// supplied hook. State is the same center Telegram acts on.
func (s *Server) alertRoutes(mux *http.ServeMux) {
	gate := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if s.Alerts == nil {
				WriteError(w, http.StatusNotFound, "alerts_absent", "alert center not running in this profile", correlationID(r))
				return
			}
			next(w, r)
		}
	}
	mux.HandleFunc("GET /api/v1/alerts", s.requirePerm(auth.PermViewDashboard, gate(func(w http.ResponseWriter, r *http.Request) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		state := notification.AlertState(r.URL.Query().Get("state"))
		switch state {
		case "", notification.AlertActive, notification.AlertAcked, notification.AlertResolved:
		default:
			WriteError(w, http.StatusBadRequest, "bad_state", "state must be active|acked|resolved", correlationID(r))
			return
		}
		WriteData(w, http.StatusOK, map[string]any{
			"alerts": s.Alerts.List(state, limit),
			"active": s.Alerts.ActiveCount(),
		})
	})))
	act := func(do func(id, actor string) (notification.Alert, error), action string) http.HandlerFunc {
		return s.requirePerm(auth.PermAlertAck, s.requireCSRF(gate(func(w http.ResponseWriter, r *http.Request) {
			id := r.PathValue("id")
			principal, _ := PrincipalFrom(r.Context())
			alert, err := do(id, principal.UserID)
			switch {
			case errors.Is(err, notification.ErrAlertNotFound):
				WriteError(w, http.StatusNotFound, "alert_not_found", "no such alert", correlationID(r))
				return
			case errors.Is(err, notification.ErrBadTransition):
				WriteError(w, http.StatusConflict, "bad_transition", "alert is not in a state that allows this", correlationID(r))
				return
			case err != nil:
				s.log.Error("alert action failed", "error", err)
				WriteError(w, http.StatusInternalServerError, "alert_action_failed", "alert action failed", correlationID(r))
				return
			}
			if s.AuditAction != nil {
				s.AuditAction(principal.UserID, action, "alert:"+id)
			}
			WriteData(w, http.StatusOK, alert)
		})))
	}
	mux.HandleFunc("POST /api/v1/alerts/{id}/ack", act(s.Alerts0Ack, "alert.ack"))
	mux.HandleFunc("POST /api/v1/alerts/{id}/resolve", act(s.Alerts0Resolve, "alert.resolve"))
}

// Alerts0Ack / Alerts0Resolve defer to the center at request time (the
// center is set once at wiring; the indirection keeps the route table
// construction independent of it).
func (s *Server) Alerts0Ack(id, actor string) (notification.Alert, error) {
	return s.Alerts.Ack(id, actor)
}

func (s *Server) Alerts0Resolve(id, actor string) (notification.Alert, error) {
	return s.Alerts.Resolve(id, actor)
}
