package api

import (
	"encoding/json"
	"errors"
	"fmt"
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
	mux.HandleFunc("GET /api/v1/config/version/{n}", s.requirePerm(auth.PermViewSystem, gate(func(w http.ResponseWriter, r *http.Request) {
		n, err := strconv.ParseInt(r.PathValue("n"), 10, 64)
		if err != nil || n <= 0 {
			WriteError(w, http.StatusBadRequest, "bad_version", "version must be a positive integer", correlationID(r))
			return
		}
		snap, err := s.Strategy.Get(r.Context(), n)
		if err != nil {
			s.writeConfigError(w, r, err)
			return
		}
		WriteData(w, http.StatusOK, snap)
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
	s.applyParams(w, r, func(actor string, authorize strategy.Authorize) (strategy.Snapshot, error) {
		return s.Strategy.ApplyAuthorized(r.Context(), actor, "web", p, authorize)
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
	s.applyParams(w, r, func(actor string, authorize strategy.Authorize) (strategy.Snapshot, error) {
		return s.Strategy.RollbackAuthorized(r.Context(), actor, "web", body.Version, authorize)
	})
}

// applyParams runs the change with per-section authorization evaluated
// INSIDE the strategy service's writer lock — against the diff that is
// actually written, so a concurrent apply cannot invalidate the check.
func (s *Server) applyParams(w http.ResponseWriter, r *http.Request, do func(actor string, authorize strategy.Authorize) (strategy.Snapshot, error)) {
	principal, _ := PrincipalFrom(r.Context())
	authorize := SectionAuthorizer(principal.Role)
	snap, err := do(principal.UserID, authorize)
	if err != nil {
		s.writeConfigError(w, r, err)
		return
	}
	s.log.Info("strategy config change applied",
		"version", snap.Version, "actor", principal.UserID)
	WriteData(w, http.StatusOK, snap)
}

// SectionAuthorizer builds the in-lock gate mapping changed config
// sections to RBAC permissions for one principal role. Shared by the
// config routes and the AI-approval path (audit S-002/P0-2: approvals
// previously bypassed the risk→ADMIN mapping entirely).
func SectionAuthorizer(role auth.Role) strategy.Authorize {
	return func(diff map[string]strategy.Change) error {
		for _, section := range strategy.TopLevelSections(diff) {
			need := auth.PermissionForConfigSection(section)
			if !auth.Can(role, need) {
				return fmt.Errorf("%w: changing %s requires %s", strategy.ErrForbidden, section, need)
			}
		}
		return nil
	}
}

func (s *Server) writeConfigError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, strategy.ErrNoChange):
		WriteError(w, http.StatusConflict, "no_change", "payload equals the current version", correlationID(r))
	case errors.Is(err, strategy.ErrNotFound):
		WriteError(w, http.StatusNotFound, "version_not_found", "no such config version", correlationID(r))
	case errors.Is(err, strategy.ErrForbidden):
		WriteError(w, http.StatusForbidden, "forbidden", err.Error(), correlationID(r))
	case errors.Is(err, strategy.ErrInvalid):
		WriteError(w, http.StatusBadRequest, "invalid_config", err.Error(), correlationID(r))
	default:
		s.log.Error("config change failed", "error", err)
		WriteError(w, http.StatusInternalServerError, "config_failed", "config change failed", correlationID(r))
	}
}
