package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/cploutarchou/arb-chain-bot/internal/auth"
	"github.com/cploutarchou/arb-chain-bot/internal/strategy"
)

// configRoutes serve the versioned strategy configuration. Reads need
// PermViewSystem; writes map each changed top-level section to its RBAC
// permission (risk → PermRiskConfig, everything else → PermScannerConfig)
// so an OPERATOR tunes strategy within bounds while risk limits stay
// ADMIN-only, per the matrix.
func (s *Server) configRoutes(mux *http.ServeMux) {
	gate := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if s.Strategy == nil {
				WriteError(w, http.StatusNotFound, "config_absent", "config service not running in this profile", correlationID(r))
				return
			}
			next(w, r)
		}
	}
	mux.HandleFunc("GET /api/v1/config", s.requirePerm(auth.PermViewSystem, gate(func(w http.ResponseWriter, r *http.Request) {
		WriteData(w, http.StatusOK, s.Strategy.Current())
	})))
	mux.HandleFunc("GET /api/v1/config/versions", s.requirePerm(auth.PermViewSystem, gate(func(w http.ResponseWriter, r *http.Request) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		list, err := s.Strategy.List(r.Context(), limit)
		if err != nil {
			s.log.Error("config list failed", "error", err)
			WriteError(w, http.StatusInternalServerError, "config_list_failed", "listing versions failed", correlationID(r))
			return
		}
		WriteData(w, http.StatusOK, list)
	})))
	mux.HandleFunc("POST /api/v1/config", s.requireAuth(s.requireCSRF(gate(s.handleConfigApply))))
	mux.HandleFunc("POST /api/v1/config/rollback", s.requireAuth(s.requireCSRF(gate(s.handleConfigRollback))))
}

func (s *Server) handleConfigApply(w http.ResponseWriter, r *http.Request) {
	var p strategy.Params
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		WriteError(w, http.StatusBadRequest, "bad_payload", "invalid config payload: "+err.Error(), correlationID(r))
		return
	}
	s.applyParams(w, r, p, func(actor string) (strategy.Snapshot, error) {
		return s.Strategy.Apply(r.Context(), actor, "web", p)
	})
}

func (s *Server) handleConfigRollback(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Version int64 `json:"version"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body); err != nil || body.Version <= 0 {
		WriteError(w, http.StatusBadRequest, "bad_payload", "body must be {\"version\": n}", correlationID(r))
		return
	}
	target, err := s.Strategy.Get(r.Context(), body.Version)
	if err != nil {
		s.writeConfigError(w, r, err)
		return
	}
	s.applyParams(w, r, target.Params, func(actor string) (strategy.Snapshot, error) {
		return s.Strategy.Rollback(r.Context(), actor, "web", body.Version)
	})
}

// applyParams enforces per-section permissions on the diff the change
// would produce, then runs do and writes the outcome.
func (s *Server) applyParams(w http.ResponseWriter, r *http.Request, p strategy.Params, do func(actor string) (strategy.Snapshot, error)) {
	principal, _ := PrincipalFrom(r.Context())
	diff, err := s.Strategy.PlanDiff(p)
	if err != nil {
		WriteError(w, http.StatusBadRequest, "bad_payload", "diff computation failed", correlationID(r))
		return
	}
	for _, section := range strategy.TopLevelSections(diff) {
		need := auth.PermScannerConfig
		if section == "risk" {
			need = auth.PermRiskConfig
		}
		if !auth.Can(principal.Role, need) {
			WriteError(w, http.StatusForbidden, "forbidden",
				"changing "+section+" requires "+string(need), correlationID(r))
			return
		}
	}
	snap, err := do(principal.UserID)
	if err != nil {
		s.writeConfigError(w, r, err)
		return
	}
	s.log.Info("strategy config change applied",
		"version", snap.Version, "actor", principal.UserID, "changes", len(diff))
	WriteData(w, http.StatusOK, snap)
}

func (s *Server) writeConfigError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, strategy.ErrNoChange):
		WriteError(w, http.StatusConflict, "no_change", "payload equals the current version", correlationID(r))
	case errors.Is(err, strategy.ErrNotFound):
		WriteError(w, http.StatusNotFound, "version_not_found", "no such config version", correlationID(r))
	case errors.Is(err, strategy.ErrInvalid):
		WriteError(w, http.StatusBadRequest, "invalid_config", err.Error(), correlationID(r))
	default:
		s.log.Error("config change failed", "error", err)
		WriteError(w, http.StatusInternalServerError, "config_failed", "config change failed", correlationID(r))
	}
}
