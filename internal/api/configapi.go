package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/cploutarchou/arb-chain-bot/internal/auth"
	"github.com/cploutarchou/arb-chain-bot/internal/strategy"
)

// extractParentVersion pulls an optional top-level "parent_version" key
// out of a raw JSON object body and returns it alongside the body with
// that key removed — callers that strict-decode the remainder (e.g.
// strategy.Params) then never see an "unknown field" for it. A body with
// no parent_version key returns (0, body unchanged).
func extractParentVersion(body []byte) (int64, []byte, error) {
	var probe struct {
		ParentVersion *int64 `json:"parent_version"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return 0, nil, err
	}
	if probe.ParentVersion == nil {
		return 0, body, nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		return 0, nil, err
	}
	delete(m, "parent_version")
	rest, err := json.Marshal(m)
	if err != nil {
		return 0, nil, err
	}
	return *probe.ParentVersion, rest, nil
}

// configRoutes serve the versioned strategy configuration. Reads need
// PermScannerConfig (audit S5: you read what you can edit — an OPERATOR
// holds it, a VIEWER does not, so the tuning surface is no longer
// enumerable by a read-only seat); writes map each changed top-level
// section to its RBAC permission (risk → PermRiskConfig, everything
// else → PermScannerConfig) so an OPERATOR tunes strategy within bounds
// while risk limits stay ADMIN-only, per the matrix.
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
	mux.HandleFunc("GET /api/v1/config", s.requirePerm(auth.PermScannerConfig, gate(func(w http.ResponseWriter, r *http.Request) {
		WriteData(w, http.StatusOK, s.Strategy.Current())
	})))
	mux.HandleFunc("GET /api/v1/config/version/{n}", s.requirePerm(auth.PermScannerConfig, gate(func(w http.ResponseWriter, r *http.Request) {
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
	mux.HandleFunc("GET /api/v1/config/versions", s.requirePerm(auth.PermScannerConfig, gate(func(w http.ResponseWriter, r *http.Request) {
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
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		WriteError(w, http.StatusBadRequest, "bad_payload", "invalid config payload: "+err.Error(), correlationID(r))
		return
	}
	// parent_version (T-058 optimistic concurrency) is an envelope field,
	// not a strategy.Params field: pull it out before the strict decode
	// below so an unrecognized-field 400 never fires on it.
	parentVersion, rest, err := extractParentVersion(raw)
	if err != nil {
		WriteError(w, http.StatusBadRequest, "bad_payload", "invalid config payload: "+err.Error(), correlationID(r))
		return
	}
	var p strategy.Params
	dec := json.NewDecoder(bytes.NewReader(rest))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		WriteError(w, http.StatusBadRequest, "bad_payload", "invalid config payload: "+err.Error(), correlationID(r))
		return
	}
	// review P3(g): parent_version is optional for Telegram/system/AI
	// callers (a single actor, no concurrent-editor risk), but the web
	// console always has a GET-then-edit-then-POST flow where a SECOND
	// browser tab (or a Telegram operator) can apply a change in between
	// — omitting parent_version there would silently skip the optimistic-
	// concurrency check ApplyAuthorizedExpect exists to provide. Every
	// version the service ever returns is >= 1 (Load seeds version 1), so
	// requiring parentVersion > 0 for source=="web" is exactly "the client
	// must echo back a real version it actually loaded".
	if parentVersion <= 0 {
		WriteError(w, http.StatusBadRequest, "parent_version_required", "parent_version is required for changes made from the console", correlationID(r))
		return
	}
	s.applyParams(w, r, func(actor string, authorize strategy.Authorize) (strategy.Snapshot, error) {
		return s.Strategy.ApplyAuthorizedExpect(r.Context(), actor, "web", p, authorize, parentVersion)
	})
}

func (s *Server) handleConfigRollback(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Version       int64  `json:"version"`
		ParentVersion *int64 `json:"parent_version,omitempty"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body); err != nil || body.Version <= 0 {
		WriteError(w, http.StatusBadRequest, "bad_payload", "body must be {\"version\": n}", correlationID(r))
		return
	}
	var parentVersion int64
	if body.ParentVersion != nil {
		parentVersion = *body.ParentVersion
	}
	// review P3(g): same requirement as handleConfigApply — a web-sourced
	// rollback must echo the version it believes is active.
	if parentVersion <= 0 {
		WriteError(w, http.StatusBadRequest, "parent_version_required", "parent_version is required for changes made from the console", correlationID(r))
		return
	}
	s.applyParams(w, r, func(actor string, authorize strategy.Authorize) (strategy.Snapshot, error) {
		return s.Strategy.RollbackAuthorizedExpect(r.Context(), actor, "web", body.Version, authorize, parentVersion)
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
	var stale *strategy.StaleVersionError
	switch {
	case errors.As(err, &stale):
		WriteErrorData(w, http.StatusConflict, "stale_version",
			"config changed since you loaded it; reload and retry", correlationID(r),
			map[string]any{"current_version": stale.Current})
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
