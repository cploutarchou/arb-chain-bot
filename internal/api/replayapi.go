package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/cploutarchou/arb-chain-bot/internal/auth"
	"github.com/cploutarchou/arb-chain-bot/internal/jobrun"
	"github.com/cploutarchou/arb-chain-bot/internal/replay"
)

// ReplayService launches and lists console-driven replay runs (BL-17).
type ReplayService interface {
	Start(req replay.Request, actor string) (replay.Run, error)
	List(ctx context.Context, limit int) ([]replay.Run, error)
	Get(ctx context.Context, id string) (replay.Run, error)
}

// speedNoOpNote surfaces replay.Request.Speed's documented limitation
// (review P3(j)) in the API response itself, not only in the Go doc
// comment a console developer would never read: backtest.Run steps to
// the next recorded frame/latency deadline, not to wall-clock time, so
// there is no "pace" for Speed to scale — a value is accepted, stored
// for the audit trail, and otherwise has no effect on execution.
const speedNoOpNote = "speed is accepted and stored for the audit trail but has no effect on execution: a replay is a deterministic discrete-event step-through, not real-time-paced"

// replayNotes returns speedNoOpNote when the request actually asked for
// a non-default speed — no note (and no "notes" key at all) for the
// common case of an unset speed, so the payload stays honest emptiness
// rather than a note nobody asked about on every single response.
func replayNotes(req replay.Request) []string {
	if req.Speed == 0 {
		return nil
	}
	return []string{speedNoOpNote}
}

// replayRoutes: console-driven replays through the real scanner
// (BL-17) — reads need view:system (same bar as campaigns, which this
// mirrors exactly); starting a run needs PermCampaignRun plus CSRF and
// is audited.
func (s *Server) replayRoutes(mux *http.ServeMux) {
	needReplays := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if s.Replays == nil {
				WriteError(w, http.StatusNotFound, "replays_absent", "replay runner not available in this profile", correlationID(r))
				return
			}
			next(w, r)
		}
	}
	mux.HandleFunc("GET /api/v1/replays", s.requirePerm(auth.PermViewSystem, needReplays(func(w http.ResponseWriter, r *http.Request) {
		runs, err := s.Replays.List(r.Context(), limitParam(r))
		s.writeListResult(w, r, "runs", runs, err)
	})))
	mux.HandleFunc("GET /api/v1/replays/{id}", s.requirePerm(auth.PermViewSystem, needReplays(func(w http.ResponseWriter, r *http.Request) {
		run, err := s.Replays.Get(r.Context(), r.PathValue("id"))
		switch {
		case errors.Is(err, replay.ErrNotFound):
			WriteError(w, http.StatusNotFound, "not_found", "replay run not found", correlationID(r))
		case err != nil:
			s.writeListResult(w, r, "run", nil, err)
		default:
			resp := map[string]any{"run": run}
			if notes := replayNotes(run.Request); len(notes) > 0 {
				resp["notes"] = notes
			}
			WriteData(w, http.StatusOK, resp)
		}
	})))
	mux.HandleFunc("POST /api/v1/replays", s.requirePerm(auth.PermCampaignRun, s.requireCSRF(needReplays(func(w http.ResponseWriter, r *http.Request) {
		var req replay.Request
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid_body", "malformed replay request", correlationID(r))
			return
		}
		p, _ := PrincipalFrom(r.Context())
		run, err := s.Replays.Start(req, p.UserID)
		switch {
		case errors.Is(err, replay.ErrBusy):
			WriteError(w, http.StatusConflict, "replay_busy", err.Error(), correlationID(r))
			return
		case errors.Is(err, jobrun.ErrNotStarted):
			// review P3(h): the runner has not pinned its lifetime context
			// yet (a startup race, not a bad request) -- 503, not 400, with
			// its own code so the console can distinguish "try again in a
			// moment" from "fix your request".
			WriteError(w, http.StatusServiceUnavailable, "not_ready", "replay runner is starting up; try again shortly", correlationID(r))
			return
		case err != nil:
			WriteError(w, http.StatusBadRequest, "invalid_request", err.Error(), correlationID(r))
			return
		}
		s.audit(r, p.UserID, "replay.start", "replay:"+run.ID)
		s.log.Info("replay started", "actor", p.UserID, "run", run.ID, "recording", run.Recording)
		resp := map[string]any{"run": run}
		if notes := replayNotes(run.Request); len(notes) > 0 {
			resp["notes"] = notes
		}
		WriteData(w, http.StatusAccepted, resp)
	}))))
}
