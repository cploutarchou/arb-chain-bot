package api

import (
	"encoding/json"
	"net/http"

	"github.com/cploutarchou/arb-chain-bot/internal/auth"
	"github.com/cploutarchou/arb-chain-bot/internal/risk"
)

// BreakerController is the operator-facing breaker control surface the
// Risk Center closes breakers through. Closing a breaker is a deliberate
// human decision (docs/risk.md §5): the daily-loss, drawdown, slippage
// and simulation-inconsistency policies never close their own breakers,
// so this is the only path that resumes qualification after one opens
// short of a restart.
type BreakerController interface {
	// CloseBreaker closes the named breaker and returns its resulting
	// state; found=false when no breaker is registered under (name,
	// scope) in this run.
	CloseBreaker(name, scope string) (state risk.BreakerState, found bool)
}

// riskRoutes: the operator's breaker acknowledgement. ADMIN-only
// (PermRiskConfig — the same permission that may change risk limits),
// CSRF-protected like every control route, audited with the resulting
// state, and gated on a real type-to-confirm: the caller must type the
// breaker's own name, so a stray POST or a misfired script can never
// silence a breaker that is open for a reason the operator has not read.
func (s *Server) riskRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/risk/breakers/close", s.requirePerm(auth.PermRiskConfig, s.requireCSRF(func(w http.ResponseWriter, r *http.Request) {
		if s.Breakers == nil {
			WriteError(w, http.StatusNotFound, "breakers_absent", "breaker registry not running in this profile", correlationID(r))
			return
		}
		var req struct {
			Name    string `json:"name"`
			Scope   string `json:"scope"`
			Confirm string `json:"confirm"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid_body", "malformed breaker close request", correlationID(r))
			return
		}
		if req.Name == "" {
			WriteError(w, http.StatusBadRequest, "breaker_name_required", "name is required", correlationID(r))
			return
		}
		if req.Confirm == "" || req.Confirm != req.Name {
			WriteError(w, http.StatusBadRequest, "confirmation_required", "confirm must repeat the breaker's name verbatim", correlationID(r))
			return
		}
		state, found := s.Breakers.CloseBreaker(req.Name, req.Scope)
		if !found {
			WriteError(w, http.StatusNotFound, "breaker_not_found", "no breaker registered under that name and scope", correlationID(r))
			return
		}
		p, _ := PrincipalFrom(r.Context())
		s.auditWith(r, p.UserID, "risk.breaker_close", "breaker:"+req.Name+"|"+req.Scope, map[string]string{"state": state.String()})
		s.log.Info("circuit breaker closed by operator",
			"actor", p.UserID, "breaker", req.Name, "scope", req.Scope, "state", state.String())
		WriteData(w, http.StatusOK, map[string]any{
			"name": req.Name, "scope": req.Scope, "state": state.String(),
		})
	})))
}
