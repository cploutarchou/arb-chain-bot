package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/cploutarchou/arb-chain-bot/internal/auth"
	"github.com/cploutarchou/arb-chain-bot/internal/platform"
	"github.com/cploutarchou/arb-chain-bot/internal/strategy"
)

// RestartController is the supervised-engine restart control surface
// (T-057). Declared here rather than reusing internal/app.Supervisor's
// types directly: internal/app already imports internal/api to build
// the Server (components.go), so internal/api cannot import internal/app
// without a cycle. app.Supervisor is adapted onto this interface (and
// app.RestartStatus copied into RestartStatus) at the wiring boundary —
// the same pattern RecorderController/PaperController already use.
type RestartController interface {
	// Request classifies the outcome itself (rather than returning an
	// error) so this package never needs to errors.Is against
	// internal/app's guard-rail sentinels.
	Request(actor, reason string, stopRecording bool) RestartRequestResult
	Status() RestartStatus
}

// RestartRequestResult is the classified outcome of a restart request.
type RestartRequestResult struct {
	Accepted bool
	// Code is one of the design §2.5 refusal codes when !Accepted:
	// "campaign_running" | "recording_active" | "restart_in_progress".
	Code    string
	Message string
}

// RestartStatus mirrors app.RestartStatus's wire shape.
type RestartStatus struct {
	State           string     `json:"state"`
	SettingsVersion int64      `json:"settings_version"`
	PendingVersion  int64      `json:"pending_version,omitempty"`
	RequestedBy     string     `json:"requested_by,omitempty"`
	RequestedAt     *time.Time `json:"requested_at,omitempty"`
	ReadyAt         *time.Time `json:"ready_at,omitempty"`
	Restarts        int64      `json:"restarts"`
	PendingReasons  []string   `json:"pending_reasons,omitempty"`
	Error           string     `json:"error,omitempty"`
}

// platformRoutes serve the versioned platform-settings document and the
// supervised restart controls (T-057). Reads need view:system; writes
// map each changed top-level section to its RBAC permission via
// platform.PermissionForSection, evaluated INSIDE the service's writer
// lock via ApplyAuthorized/RollbackAuthorized — the same TOCTOU-safe
// pattern configapi.go uses for strategy config.
//
// Error codes deliberately diverge from configapi.go's: no_change is
// 400 here (not 409) and an absent service is 503 settings_unavailable
// (not 404) — this follows docs/design/platform-settings-and-restart.md
// §3 exactly rather than configapi.go's older convention.
func (s *Server) platformRoutes(mux *http.ServeMux) {
	gate := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if s.Platform == nil {
				WriteError(w, http.StatusServiceUnavailable, "settings_unavailable", "platform settings service not running in this profile", correlationID(r))
				return
			}
			next(w, r)
		}
	}
	mux.HandleFunc("GET /api/v1/platform/settings", s.requirePerm(auth.PermViewSystem, gate(s.handlePlatformGet)))
	mux.HandleFunc("GET /api/v1/platform/settings/versions", s.requirePerm(auth.PermViewSystem, gate(s.handlePlatformVersions)))
	mux.HandleFunc("GET /api/v1/platform/settings/version/{n}", s.requirePerm(auth.PermViewSystem, gate(s.handlePlatformVersion)))
	mux.HandleFunc("POST /api/v1/platform/settings/preview", s.requireAuth(s.requireCSRF(gate(s.handlePlatformPreview))))
	mux.HandleFunc("POST /api/v1/platform/settings", s.requireAuth(s.requireCSRF(gate(s.handlePlatformApply))))
	mux.HandleFunc("POST /api/v1/platform/settings/rollback", s.requireAuth(s.requireCSRF(gate(s.handlePlatformRollback))))

	restartGate := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if s.Restart == nil {
				WriteError(w, http.StatusNotFound, "engine_absent", "engine not running in this profile", correlationID(r))
				return
			}
			next(w, r)
		}
	}
	// Both routes exist per docs/deployment.md and the design's §3
	// table; they serve the identical status payload.
	mux.HandleFunc("GET /api/v1/engine/status", s.requirePerm(auth.PermViewSystem, restartGate(s.handleEngineStatus)))
	mux.HandleFunc("GET /api/v1/engine/restart", s.requirePerm(auth.PermViewSystem, restartGate(s.handleEngineStatus)))
	mux.HandleFunc("POST /api/v1/engine/restart", s.requirePerm(auth.PermSystemConfig, s.requireCSRF(restartGate(s.handleEngineRestartPost))))
}

func (s *Server) handlePlatformGet(w http.ResponseWriter, r *http.Request) {
	WriteData(w, http.StatusOK, s.platformView(r, s.Platform.Current()))
}

// platformView assembles the GET/apply response shape: version,
// created_by, created_at, settings, plan, restart, field_timing — the
// backend serves field_timing so the frontend never hardcodes which
// fields are hot (design §3).
func (s *Server) platformView(r *http.Request, snap platform.Snapshot) map[string]any {
	view := map[string]any{
		"version":      snap.Version,
		"created_by":   snap.CreatedBy,
		"created_at":   snap.CreatedAt,
		"settings":     snap.Settings,
		"field_timing": platform.FieldTiming(snap.Settings, s.botRunning()),
	}
	if s.PlatformCatalog != nil {
		if plans, err := platform.ValidateAgainstCatalog(r.Context(), snap.Settings, s.PlatformCatalog); err == nil {
			view["plan"] = plans
		}
	}
	if s.Restart != nil {
		view["restart"] = s.Restart.Status()
	}
	return view
}

func (s *Server) botRunning() bool {
	if s.BotRunning == nil {
		return false
	}
	return s.BotRunning()
}

func (s *Server) handlePlatformVersions(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	list, err := s.Platform.List(r.Context(), limit)
	if err != nil {
		s.log.Error("platform settings list failed", "error", err)
		WriteError(w, http.StatusInternalServerError, "settings_list_failed", "listing versions failed", correlationID(r))
		return
	}
	WriteData(w, http.StatusOK, list)
}

func (s *Server) handlePlatformVersion(w http.ResponseWriter, r *http.Request) {
	n, err := strconv.ParseInt(r.PathValue("n"), 10, 64)
	if err != nil || n <= 0 {
		WriteError(w, http.StatusBadRequest, "bad_version", "version must be a positive integer", correlationID(r))
		return
	}
	snap, err := s.Platform.Get(r.Context(), n)
	if err != nil {
		s.writePlatformError(w, r, err)
		return
	}
	WriteData(w, http.StatusOK, snap)
}

// decodePlatformSettings reads {"settings": {...}, "parent_version": n};
// on error it has already written the response.
func decodePlatformSettings(w http.ResponseWriter, r *http.Request) (platform.Settings, int64, bool) {
	var body struct {
		Settings      platform.Settings `json:"settings"`
		ParentVersion *int64            `json:"parent_version,omitempty"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		WriteError(w, http.StatusBadRequest, "bad_payload", "invalid settings payload: "+err.Error(), correlationID(r))
		return platform.Settings{}, 0, false
	}
	var parentVersion int64
	if body.ParentVersion != nil {
		parentVersion = *body.ParentVersion
	}
	return body.Settings, parentVersion, true
}

// handlePlatformPreview computes the diff/plan WITHOUT writing a new
// version — identical body to the apply route (design §3).
func (s *Server) handlePlatformPreview(w http.ResponseWriter, r *http.Request) {
	doc, _, ok := decodePlatformSettings(w, r)
	if !ok {
		return
	}
	diff, err := s.Platform.PlanDiff(doc)
	if err != nil {
		s.writePlatformError(w, r, err)
		return
	}
	if len(diff) == 0 {
		WriteError(w, http.StatusBadRequest, "no_change", "payload equals the current version", correlationID(r))
		return
	}
	principal, _ := PrincipalFrom(r.Context())
	sections := strategy.TopLevelSections(diff)
	for _, section := range sections {
		need := platform.PermissionForSection(section)
		if !auth.Can(principal.Role, need) {
			WriteError(w, http.StatusForbidden, "forbidden", fmt.Sprintf("changing %s requires %s", section, need), correlationID(r))
			return
		}
	}
	resp := map[string]any{
		"diff":             diff,
		"sections":         sections,
		"requires_restart": s.Platform.Current().Settings.RestartScoped(diff),
	}
	if s.PlatformCatalog != nil {
		plans, err := platform.ValidateAgainstCatalog(r.Context(), doc, s.PlatformCatalog)
		if err != nil {
			s.writePlatformError(w, r, err)
			return
		}
		resp["plan"] = plans
	}
	WriteData(w, http.StatusOK, resp)
}

func (s *Server) handlePlatformApply(w http.ResponseWriter, r *http.Request) {
	doc, parentVersion, ok := decodePlatformSettings(w, r)
	if !ok {
		return
	}
	s.applyPlatform(w, r, func(actor string, authorize platform.Authorize) (platform.Snapshot, error) {
		return s.Platform.ApplyAuthorizedExpect(r.Context(), actor, "web", doc, authorize, parentVersion)
	})
}

func (s *Server) handlePlatformRollback(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Version       int64  `json:"version"`
		ParentVersion *int64 `json:"parent_version,omitempty"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body); err != nil || body.Version <= 0 {
		WriteError(w, http.StatusBadRequest, "bad_payload", `body must be {"version": n}`, correlationID(r))
		return
	}
	var parentVersion int64
	if body.ParentVersion != nil {
		parentVersion = *body.ParentVersion
	}
	s.applyPlatform(w, r, func(actor string, authorize platform.Authorize) (platform.Snapshot, error) {
		return s.Platform.RollbackAuthorizedExpect(r.Context(), actor, "web", body.Version, authorize, parentVersion)
	})
}

// applyPlatform runs the change with per-section authorization evaluated
// INSIDE the platform service's writer lock via ApplyAuthorized/
// RollbackAuthorized — against the diff that is actually written, so a
// concurrent apply cannot invalidate the check (mirrors configapi.go's
// applyParams).
func (s *Server) applyPlatform(w http.ResponseWriter, r *http.Request, do func(actor string, authorize platform.Authorize) (platform.Snapshot, error)) {
	principal, _ := PrincipalFrom(r.Context())
	authorize := platformSectionAuthorizer(principal.Role)
	snap, err := do(principal.UserID, authorize)
	if err != nil {
		s.writePlatformError(w, r, err)
		return
	}
	s.log.Info("platform settings change applied", "version", snap.Version, "actor", principal.UserID)
	WriteData(w, http.StatusOK, s.platformView(r, snap))
}

// platformSectionAuthorizer builds the in-lock gate mapping changed
// settings sections to RBAC permissions for one principal role.
func platformSectionAuthorizer(role auth.Role) platform.Authorize {
	return func(diff map[string]strategy.Change) error {
		for _, section := range strategy.TopLevelSections(diff) {
			need := platform.PermissionForSection(section)
			if !auth.Can(role, need) {
				return fmt.Errorf("%w: changing %s requires %s", platform.ErrForbidden, section, need)
			}
		}
		return nil
	}
}

func (s *Server) writePlatformError(w http.ResponseWriter, r *http.Request, err error) {
	var stale *platform.StaleVersionError
	switch {
	case errors.As(err, &stale):
		WriteErrorData(w, http.StatusConflict, "stale_version",
			"settings changed since you loaded them; reload and retry", correlationID(r),
			map[string]any{"current_version": stale.Current})
	case errors.Is(err, platform.ErrNoChange):
		WriteError(w, http.StatusBadRequest, "no_change", "payload equals the current version", correlationID(r))
	case errors.Is(err, platform.ErrNotFound):
		WriteError(w, http.StatusNotFound, "version_not_found", "no such settings version", correlationID(r))
	case errors.Is(err, platform.ErrForbidden):
		WriteError(w, http.StatusForbidden, "forbidden", err.Error(), correlationID(r))
	case errors.Is(err, platform.ErrUnknownSymbol):
		WriteError(w, http.StatusBadRequest, "unknown_symbol", err.Error(), correlationID(r))
	case errors.Is(err, platform.ErrNoTriangles):
		WriteError(w, http.StatusBadRequest, "no_triangles", err.Error(), correlationID(r))
	case errors.Is(err, platform.ErrCatalogNotReady):
		WriteError(w, http.StatusServiceUnavailable, "settings_unavailable", err.Error(), correlationID(r))
	case errors.Is(err, platform.ErrInvalid):
		WriteError(w, http.StatusBadRequest, "invalid_settings", err.Error(), correlationID(r))
	default:
		s.log.Error("platform settings change failed", "error", err)
		WriteError(w, http.StatusInternalServerError, "settings_failed", "settings change failed", correlationID(r))
	}
}

func (s *Server) handleEngineStatus(w http.ResponseWriter, r *http.Request) {
	WriteData(w, http.StatusOK, map[string]any{"restart": s.Restart.Status()})
}

func (s *Server) handleEngineRestartPost(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Confirm       string `json:"confirm"`
		Reason        string `json:"reason"`
		StopRecording bool   `json:"stop_recording"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body); err != nil {
		WriteError(w, http.StatusBadRequest, "bad_payload", "invalid restart payload", correlationID(r))
		return
	}
	if body.Confirm != "RESTART" {
		WriteError(w, http.StatusBadRequest, "confirm_required", "type RESTART to confirm", correlationID(r))
		return
	}
	principal, _ := PrincipalFrom(r.Context())
	result := s.Restart.Request(principal.UserID, body.Reason, body.StopRecording)
	if !result.Accepted {
		s.audit(r, principal.UserID, "engine.restart.refused", "engine:"+result.Code)
		WriteError(w, http.StatusConflict, result.Code, result.Message, correlationID(r))
		return
	}
	s.audit(r, principal.UserID, "engine.restart", "engine")
	s.log.Info("engine restart requested", "actor", principal.UserID, "stop_recording", body.StopRecording)
	WriteData(w, http.StatusAccepted, map[string]any{"restart": s.Restart.Status()})
}
